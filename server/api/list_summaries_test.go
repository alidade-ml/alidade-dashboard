package api

// Tests for ListSummaries — the narrow read behind the experiments list.
//
// The whole claim of this slice is "same output, fewer queries", so most of
// what follows is equivalence against ListAll plus the two things a
// per-submit-query-to-grouped-query rewrite can silently break: ordering
// within a submit, and attribution between submits.

import (
	"database/sql"
	"testing"
)

func addTransition(t *testing.T, db *sql.DB, submitID, state, at string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO state_transitions (submit_id, state, at) VALUES (?, ?, ?)`,
		submitID, state, at); err != nil {
		t.Fatal(err)
	}
}

func summariesFor(t *testing.T, path string) []ExperimentState {
	t.Helper()
	r, err := NewStateReader(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ListSummaries()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// --- Unhappy paths ---

// TestListSummariesKeepsSubmitsWithNoTransitions is the row a join drops.
//
// The engine inserts a submit and its first transition in one transaction, but
// legacy imports have submits with no transitions at all (see cost_test's
// "Deliberately NOT inserting state_transitions rows"). Those rows must still
// appear on the home page.
func TestListSummariesKeepsSubmitsWithNoTransitions(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "quiet", "version": "v1", "submit_id": "quiet-v1",
			"submitted_by": "nathan", "backend": "local",
			"started_at": "2026-08-01T00:00:00+00:00", "current_state": "COMPLETED",
		})
	})
	got := summariesFor(t, path)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 — a submit with no transitions was dropped", len(got))
	}
	if len(got[0].StateHistory) != 0 {
		t.Errorf("StateHistory = %v, want empty", got[0].StateHistory)
	}
}

// TestListSummariesOrdersTransitionsByTimeNotID protects the fix that
// listTransitions carries a comment about.
//
// The state-file importer backfills a synthetic current-state row: low id,
// recent timestamp. Ordering by id puts it first, which broke the FSM history
// strip and produced a 33-day billing window on legacy rows. A grouped query
// preserves scan order, so the ORDER BY is the only thing preventing it here.
func TestListSummariesOrdersTransitionsByTimeNotID(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "legacy", "version": "v1", "submit_id": "legacy-v1",
			"submitted_by": "nathan", "backend": "local",
			"started_at": "2026-08-01T00:00:00+00:00", "current_state": "COMPLETED",
		})
		// Inserted in id order 1,2,3 but NOT in time order: the first row is
		// the synthetic backfill, carrying the newest timestamp.
		addTransition(t, db, "legacy-v1", "COMPLETED", "2026-08-01T05:00:00+00:00")
		addTransition(t, db, "legacy-v1", "PENDING", "2026-08-01T00:00:00+00:00")
		addTransition(t, db, "legacy-v1", "RUNNING", "2026-08-01T01:00:00+00:00")
	})
	got := summariesFor(t, path)
	want := []string{"PENDING", "RUNNING", "COMPLETED"}
	if len(got[0].StateHistory) != len(want) {
		t.Fatalf("got %d transitions, want %d", len(got[0].StateHistory), len(want))
	}
	for i, w := range want {
		if got[0].StateHistory[i].State != w {
			t.Fatalf("transition %d = %q, want %q — ordered by id instead of at, "+
				"which is the bug that put a synthetic backfill row first",
				i, got[0].StateHistory[i].State, w)
		}
	}
}

// TestListSummariesDoesNotMixTransitionsBetweenSubmits makes the grouping key
// do visible work. With one submit in the DB, any grouping passes.
func TestListSummariesDoesNotMixTransitionsBetweenSubmits(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		for _, n := range []string{"a", "b"} {
			insertSubmit(t, db, map[string]any{
				"experiment_name": n, "version": "v1", "submit_id": n + "-v1",
				"submitted_by": "nathan", "backend": "local",
				"started_at":    "2026-08-0" + map[string]string{"a": "1", "b": "2"}[n] + "T00:00:00+00:00",
				"current_state": "COMPLETED",
			})
		}
		addTransition(t, db, "a-v1", "PENDING", "2026-08-01T00:00:00+00:00")
		addTransition(t, db, "a-v1", "RUNNING", "2026-08-01T01:00:00+00:00")
		addTransition(t, db, "b-v1", "FAILED", "2026-08-02T00:00:00+00:00")
	})
	byName := map[string][]StateTransition{}
	for _, s := range summariesFor(t, path) {
		byName[s.Name] = s.StateHistory
	}
	if len(byName["a"]) != 2 {
		t.Errorf("a has %d transitions, want 2", len(byName["a"]))
	}
	if len(byName["b"]) != 1 {
		t.Errorf("b has %d transitions, want 1", len(byName["b"]))
	}
	if len(byName["b"]) == 1 && byName["b"][0].State != "FAILED" {
		t.Errorf("b's transition = %q, want FAILED — submits borrowed each "+
			"other's history", byName["b"][0].State)
	}
}

func TestListSummariesOnEmptyDB(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {})
	if got := summariesFor(t, path); len(got) != 0 {
		t.Errorf("got %d rows from an empty DB, want 0", len(got))
	}
}

// --- The narrowing, asserted so it cannot be undone by accident ---

// TestListSummariesDoesNotHydrateIncludesOrTags is the point of the slice.
//
// Nothing renders these on the experiments list, and fetching them is two of
// the three per-submit queries this replaced. If someone "fixes" a nil by
// re-adding hydration, the cost comes back silently — so it fails here.
func TestListSummariesDoesNotHydrateIncludesOrTags(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "exp", "version": "v1", "submit_id": "exp-v1",
			"submitted_by": "nathan", "backend": "local",
			"started_at": "2026-08-01T00:00:00+00:00", "current_state": "COMPLETED",
		})
		if _, err := db.Exec(
			`INSERT INTO includes (submit_id, spec) VALUES (?, ?)`, "exp-v1", "other"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO git_tags (submit_id, tag) VALUES (?, ?)`, "exp-v1", "completed/x"); err != nil {
			t.Fatal(err)
		}
	})
	got := summariesFor(t, path)
	if len(got[0].IncludeRuns) != 0 {
		t.Errorf("IncludeRuns = %v, want none — the experiments list does not "+
			"render them and fetching them is the cost this slice removed",
			got[0].IncludeRuns)
	}
	if len(got[0].GitTags) != 0 {
		t.Errorf("GitTags = %v, want none", got[0].GitTags)
	}
}

// TestListSummariesMatchesListAllOnRenderedFields is the equivalence that
// makes the swap in HandleExperiments safe.
//
// It is also why one reversion is NOT guarded, which is worth stating rather
// than leaving to be discovered: putting `ListAll` back in HandleExperiments
// passes every test in this package. The two are output-equivalent by design —
// that is exactly what this test asserts — and the difference is only how many
// queries it took. There is no fake to count SQL the way fakeAimCountingLists
// counts HTTP, and adding a query counter to StateReader to catch a one-line
// deliberate edit is not worth the production surface.
//
// What IS guarded is the accident: TestListSummariesDoesNotHydrateIncludesOrTags
// fails if the per-submit hydration comes back here, which is the realistic way
// the cost returns.
func TestListSummariesMatchesListAllOnRenderedFields(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "muon", "version": "v1", "submit_id": "muon-v1",
			"submitted_by": "nathan", "backend": "lambda", "gpu_type": "gpu_1x_a100",
			"started_at":  "2026-08-01T00:00:00+00:00",
			"finished_at": "2026-08-01T02:00:00+00:00",
			"outcome":     "success", "current_state": "COMPLETED", "repo": "ProjectOrion",
		})
		insertSubmit(t, db, map[string]any{
			"experiment_name": "muon", "version": "v2", "submit_id": "muon-v2",
			"submitted_by": "alice", "backend": "lambda",
			"started_at": "2026-08-03T00:00:00+00:00", "current_state": "RUNNING",
		})
		addTransition(t, db, "muon-v1", "PENDING", "2026-08-01T00:00:00+00:00")
		addTransition(t, db, "muon-v1", "COMPLETED", "2026-08-01T02:00:00+00:00")
		addTransition(t, db, "muon-v2", "RUNNING", "2026-08-03T00:10:00+00:00")
	})
	r, err := NewStateReader(path)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := r.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := r.ListSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) != len(narrow) {
		t.Fatalf("ListAll returned %d rows, ListSummaries %d", len(wide), len(narrow))
	}
	for i := range wide {
		w, n := wide[i], narrow[i]
		if w.Name != n.Name || w.State != n.State || w.GPUType != n.GPUType ||
			w.StartedAt != n.StartedAt || w.FinishedAt != n.FinishedAt ||
			w.Outcome != n.Outcome || w.Repo != n.Repo ||
			w.LinearDocURL != n.LinearDocURL || w.SubmittedBy != n.SubmittedBy ||
			w.Version != n.Version {
			t.Errorf("row %d differs:\n ListAll       %+v\n ListSummaries %+v", i, w, n)
		}
		if len(w.StateHistory) != len(n.StateHistory) {
			t.Fatalf("row %d: history lengths %d vs %d", i, len(w.StateHistory), len(n.StateHistory))
		}
		for j := range w.StateHistory {
			if w.StateHistory[j] != n.StateHistory[j] {
				t.Errorf("row %d transition %d: %+v vs %+v", i, j,
					w.StateHistory[j], n.StateHistory[j])
			}
		}
	}
}
