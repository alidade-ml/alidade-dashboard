package api

// Tests for GET /api/experiments/{name}.
//
// Contract, from what the page needs rather than from the handler:
//
//   * version_count spans EVERY submit for the name, not just the newest one
//     the header is built from. The schema enforces UNIQUE(experiment_name,
//     version), so versions and submits are one-to-one and "distinct" is
//     belt-and-braces; the real risk is returning 1 because GetState returns
//     one row.
//   * The response comes from the state DB alone, so the header survives Aim
//     being unreachable.
//   * An unknown name is 404. A blank header and a slow load look identical.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func detailHandler(t *testing.T, seed func(*sql.DB)) *Handler {
	t.Helper()
	sr, err := NewStateReader(makeStateDBWith(t, seed))
	if err != nil {
		t.Fatal(err)
	}
	// Aim pointed at a server that refuses every request: nothing in this
	// endpoint may reach for it.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "aim is down", http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)
	return NewHandler(NewAimClient(dead.URL), sr, nil)
}

func getDetail(t *testing.T, h *Handler, name string) (*httptest.ResponseRecorder, ExperimentDetail) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/experiments/"+name, nil)
	rr := httptest.NewRecorder()
	h.HandleExperimentDetail(rr, req)
	var out ExperimentDetail
	if rr.Code == 200 {
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rr, out
}

func submit(t *testing.T, db *sql.DB, name, version, submitID, startedAt string, extra map[string]any) {
	t.Helper()
	f := map[string]any{
		"experiment_name": name, "version": version, "submit_id": submitID,
		"submitted_by": "nathan", "backend": "lambda", "started_at": startedAt,
		"current_state": "COMPLETED",
	}
	for k, v := range extra {
		f[k] = v
	}
	insertSubmit(t, db, f)
}

// --- Unhappy paths ---

func TestExperimentDetailUnknownNameIs404(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "real", "v1", "real-v1", "2026-08-01T00:00:00+00:00", nil)
	})
	rr, _ := getDetail(t, h, "not-a-thing")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — an empty 200 renders as a blank "+
			"header, which a reader cannot tell from a slow load", rr.Code)
	}
}

func TestExperimentDetailEmptyNameIs400(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {})
	req := httptest.NewRequest("GET", "/api/experiments/", nil)
	rr := httptest.NewRecorder()
	h.HandleExperimentDetail(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestExperimentDetailDoesNotNeedAim is the property that makes this endpoint
// worth its own type. Every handler here is built against a dead Aim.
func TestExperimentDetailDoesNotNeedAim(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "exp", "v1", "exp-v1", "2026-08-01T00:00:00+00:00",
			map[string]any{"gpu_type": "gpu_1x_a100"})
	})
	rr, got := getDetail(t, h, "exp")
	if rr.Code != 200 {
		t.Fatalf("status = %d with Aim unreachable; the header is state-DB "+
			"data and must not depend on Aim", rr.Code)
	}
	if got.Name != "exp" || got.GPUType != "gpu_1x_a100" {
		t.Errorf("got %+v, want the header populated from SQLite", got)
	}
}

// TestExperimentDetailCountsEveryVersion guards the plausible wrong answer:
// building the count from the one submit the header came from.
//
// GetState returns the newest submit. A count derived from it reads 1 for every
// experiment, which looks right on anything submitted once and is wrong
// everywhere else.
func TestExperimentDetailCountsEveryVersion(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "exp", "v1", "exp-v1", "2026-08-01T00:00:00+00:00", nil)
		submit(t, db, "exp", "v2", "exp-v2", "2026-08-02T00:00:00+00:00", nil)
		submit(t, db, "exp", "v3", "exp-v3", "2026-08-03T00:00:00+00:00", nil)
		// A different experiment must not be counted into this one. Its
		// version is deliberately one exp does not have: reusing "v1" would
		// let a query with no name filter return the same 3 and pass.
		submit(t, db, "other", "v9", "other-v9", "2026-08-04T00:00:00+00:00", nil)
	})
	_, got := getDetail(t, h, "exp")
	if got.VersionCount != 3 {
		t.Errorf("version_count = %d, want 3", got.VersionCount)
	}
}

func TestExperimentDetailWithNoTransitions(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "quiet", "v1", "quiet-v1", "2026-08-01T00:00:00+00:00", nil)
	})
	rr, got := getDetail(t, h, "quiet")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(got.StateHistory) != 0 {
		t.Errorf("StateHistory = %v, want empty", got.StateHistory)
	}
}

func TestExperimentDetailNameNeedingEncoding(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "02-muon optimizer", "v1", "muon-v1", "2026-08-01T00:00:00+00:00", nil)
	})
	rr, got := getDetail(t, h, "02-muon%20optimizer")
	if rr.Code != 200 {
		t.Fatalf("status = %d for a name with a space, want 200", rr.Code)
	}
	if got.Name != "02-muon optimizer" {
		t.Errorf("name = %q, want the decoded form", got.Name)
	}
}

// --- Happy paths ---

func TestExperimentDetailUsesNewestSubmit(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "exp", "v1", "exp-v1", "2026-08-01T00:00:00+00:00",
			map[string]any{"outcome": "failure", "gpu_type": "gpu_1x_a10"})
		submit(t, db, "exp", "v2", "exp-v2", "2026-08-05T00:00:00+00:00",
			map[string]any{"outcome": "success", "gpu_type": "gpu_1x_a100"})
	})
	_, got := getDetail(t, h, "exp")
	if got.Outcome != "success" || got.GPUType != "gpu_1x_a100" {
		t.Errorf("got outcome=%q gpu=%q, want the newest submit's",
			got.Outcome, got.GPUType)
	}
	if got.VersionCount != 2 {
		t.Errorf("version_count = %d, want 2", got.VersionCount)
	}
}

func TestExperimentDetailReturnsTransitionsInTimeOrder(t *testing.T) {
	h := detailHandler(t, func(db *sql.DB) {
		submit(t, db, "exp", "v1", "exp-v1", "2026-08-01T00:00:00+00:00", nil)
		// Inserted newest-first, so id order and time order disagree.
		addTransition(t, db, "exp-v1", "COMPLETED", "2026-08-01T05:00:00+00:00")
		addTransition(t, db, "exp-v1", "PENDING", "2026-08-01T00:00:00+00:00")
		addTransition(t, db, "exp-v1", "RUNNING", "2026-08-01T01:00:00+00:00")
	})
	_, got := getDetail(t, h, "exp")
	want := []string{"PENDING", "RUNNING", "COMPLETED"}
	if len(got.StateHistory) != len(want) {
		t.Fatalf("got %d transitions, want %d", len(got.StateHistory), len(want))
	}
	for i, w := range want {
		if got.StateHistory[i].State != w {
			t.Fatalf("transition %d = %q, want %q", i, got.StateHistory[i].State, w)
		}
	}
}
