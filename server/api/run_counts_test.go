package api

// Tests for the home page's run count, which nothing asserted before.
//
// Two properties, failing differently: WHAT is counted (sample runs used to be
// included, because two exclusion lists disagreed), and HOW MANY requests it
// takes. A body-only assertion cannot distinguish one search from a walk over
// every run, so the fan-out is asserted directly.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// countedExperiment wires one experiment's worth of Aim runs to one submit,
// with run-count reuse switched off so each call re-asks.
func countedExperiment(t *testing.T, runs []fakeRun, listCalls *int32) *Handler {
	t.Helper()
	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "exp",
			"version":         "v1",
			"submit_id":       "exp-v1",
			"submitted_by":    "nathan",
			"backend":         "local",
			"started_at":      "2026-08-01T00:00:00+00:00",
			"current_state":   "COMPLETED",
		})
	})
	sr, err := NewStateReader(path)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(fakeAimCountingLists(t, runs, listCalls), sr, nil)
	h.SetRunCountTTL(0)
	return h
}

func runCountFor(t *testing.T, h *Handler, name string) int {
	t.Helper()
	for _, e := range callExperiments(t, h) {
		if e.Name == name {
			return e.RunCount
		}
	}
	t.Fatalf("no row for %q", name)
	return 0
}

// --- Unhappy paths ---

// TestRunCountsAimFailureIsNotAPageOfZeros is the failure this design makes
// possible and the walk did not.
//
// The walk fetched runs per experiment, so a partial Aim outage lost some
// rows' counts. One search means one failure point: if it errors and the
// handler swallows it, every experiment renders as having produced nothing,
// which reads as an empty NUC rather than a broken dashboard.
func TestRunCountsAimFailureIsNotAPageOfZeros(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "aim is down", http.StatusInternalServerError)
		}))
	defer dead.Close()

	path := makeStateDBWith(t, func(db *sql.DB) {
		insertSubmit(t, db, map[string]any{
			"experiment_name": "exp", "version": "v1", "submit_id": "exp-v1",
			"submitted_by": "nathan", "backend": "local",
			"started_at": "2026-08-01T00:00:00+00:00", "current_state": "COMPLETED",
		})
	})
	sr, err := NewStateReader(path)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(NewAimClient(dead.URL), sr, nil)

	req := httptest.NewRequest("GET", "/api/experiments", nil)
	rr := httptest.NewRecorder()
	h.HandleExperiments(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("Aim was unreachable and the handler returned 200: %s\n"+
			"A page of zero-run experiments is indistinguishable from a NUC "+
			"nobody has used.", rr.Body.String())
	}
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

// TestRunCountsExcludeSampleRuns is the leak that shipped.
func TestRunCountsExcludeSampleRuns(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "s1", tags: map[string]any{TagKind: "sample"}},
		{experiment: "exp", hash: "s2", tags: map[string]any{TagKind: "sample"}},
	}, nil)
	if got := runCountFor(t, h, "exp"); got != 1 {
		t.Errorf("run_count = %d, want 1 — sample runs are rendered on the "+
			"Examples tab and are not models this experiment produced", got)
	}
}

func TestRunCountsExcludeEvalAndMetadataRuns(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "e1", tags: map[string]any{TagKind: "eval"}},
		{experiment: "exp", hash: "m1", tags: map[string]any{TagKind: "metadata"}},
	}, nil)
	if got := runCountFor(t, h, "exp"); got != 1 {
		t.Errorf("run_count = %d, want 1", got)
	}
}

// TestRunCountsExcludeArchivedRuns documents behaviour it cannot protect,
// and says so rather than reading as a guard.
//
// Mutation-checked: removing `run.archived == False` from the query leaves
// this test green, because Aim excludes archived runs from search by default
// and the fake mirrors that. The term is kept so the count does not depend on
// an undocumented default, but the thing that would actually catch Aim
// changing its mind is TestAimContractSearchStillHidesArchivedRuns, which
// runs against a live server.
func TestRunCountsExcludeArchivedRuns(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "t2", archived: true,
			tags: map[string]any{TagKind: "training"}},
	}, nil)
	if got := runCountFor(t, h, "exp"); got != 1 {
		t.Errorf("run_count = %d, want 1 — the walk skipped archived runs "+
			"and the count must not quietly start including them", got)
	}
}

// TestRunCountsIncludeUntaggedRuns covers runs that predate alidade.kind.
// A query that dropped them would undercount every old experiment, and the
// undercount would look like data loss rather than a filter.
func TestRunCountsIncludeUntaggedRuns(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "old", tags: map[string]any{}},
	}, nil)
	if got := runCountFor(t, h, "exp"); got != 2 {
		t.Errorf("run_count = %d, want 2 — a run with no kind tag is a "+
			"legacy training run, not a thing to drop", got)
	}
}

// TestRunCountsForExperimentAbsentFromAim keeps a submit visible when Aim
// has nothing for it — a metadata-only backfill, or a submit that died
// before writing a run.
func TestRunCountsForExperimentAbsentFromAim(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "somewhere-else", hash: "t1",
			tags: map[string]any{TagKind: "training"}},
	}, nil)
	if got := runCountFor(t, h, "exp"); got != 0 {
		t.Errorf("run_count = %d, want 0", got)
	}
}

// --- The fan-out, which is the point of the ticket ---

// Body assertions cannot see the difference between asking Aim once and
// enumerating every run — both produce the same numbers. Only the request count
// can, and a timing assertion would be flaky where this is exact.
func TestRunCountsAskInsteadOfWalking(t *testing.T) {
	var listCalls int32
	runs := make([]fakeRun, 0, 40)
	for i := 0; i < 40; i++ {
		runs = append(runs, fakeRun{
			experiment: "exp",
			hash:       string(rune('a'+i%26)) + string(rune('0'+i/26)),
			tags:       map[string]any{TagKind: "training"},
		})
	}
	h := countedExperiment(t, runs, &listCalls)

	callExperiments(t, h)

	if n := atomic.LoadInt32(&listCalls); n != 0 {
		t.Errorf("the handler made %d experiment/run LISTING calls; want 0. "+
			"Listing means walking, and the walk is what this ticket removed.", n)
	}
}

// --- The cache ---

func TestRunCountsAreReusedWithinTheTTL(t *testing.T) {
	var listCalls int32
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
	}, &listCalls)
	h.SetRunCountTTL(time.Minute)

	first := runCountFor(t, h, "exp")
	second := runCountFor(t, h, "exp")
	if first != 1 || second != 1 {
		t.Fatalf("counts = %d then %d, want 1 both times", first, second)
	}
	if h.runCounts.fetched.IsZero() {
		t.Fatal("nothing was cached, so the reuse assertion below proves nothing")
	}
	before := h.runCounts.fetched
	runCountFor(t, h, "exp")
	if !h.runCounts.fetched.Equal(before) {
		t.Error("the count map was refetched inside its TTL")
	}
}

func TestRunCountsRefreshAfterTheTTL(t *testing.T) {
	h := countedExperiment(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
	}, nil)
	h.SetRunCountTTL(time.Nanosecond)

	runCountFor(t, h, "exp")
	before := h.runCounts.fetched
	time.Sleep(time.Millisecond)
	runCountFor(t, h, "exp")
	if h.runCounts.fetched.Equal(before) {
		t.Error("the count map was reused past its TTL")
	}
}

// --- The drift guard ---

// TestNonRowKindsAreNotRows ties the count's exclusion list to the run list's
// switch. The two disagreed for a whole release; adding a kind to NonRowKinds
// without teaching the switch fails here.
func TestNonRowKindsAreNotRows(t *testing.T) {
	for _, kind := range NonRowKinds {
		t.Run(kind, func(t *testing.T) {
			aim := fakeAim(t, []fakeRun{
				{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
				{experiment: "exp", hash: "x1", tags: map[string]any{TagKind: kind}},
			})
			h := makeHandlerWithAim(t, aim)

			req := httptest.NewRequest("GET", "/api/experiments/exp/runs", nil)
			rr := httptest.NewRecorder()
			h.HandleExperimentRuns(rr, req)
			var rows []RunDetail
			if err := json.NewDecoder(rr.Body).Decode(&rows); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Hash == "x1" {
					t.Fatalf("kind %q is excluded from the run COUNT but "+
						"HandleExperimentRuns still renders it as a row; the "+
						"two lists have drifted apart again", kind)
				}
			}
		})
	}
}

// --- /api/runs, the documented endpoint ---

// TestHandleRunsAsksInsteadOfWalking is the same guard as the experiments
// list, for the endpoint that kept the walk alive.
//
// /api/runs has no caller in this repo's frontend, but is published in
// docs/alternative-frontends.md as API a third-party UI can build on. Since it
// stays, it must not be the last thing walking the project.
func TestHandleRunsAsksInsteadOfWalking(t *testing.T) {
	var listCalls int32
	h := NewHandler(fakeAimCountingLists(t, []fakeRun{
		{experiment: "exp", hash: "t1", creationTime: 200,
			tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "t2", creationTime: 100,
			tags: map[string]any{TagKind: "training"}},
	}, &listCalls), nil, nil)

	req := httptest.NewRequest("GET", "/api/runs", nil)
	rr := httptest.NewRecorder()
	h.HandleRuns(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if n := atomic.LoadInt32(&listCalls); n != 0 {
		t.Errorf("made %d listing calls; want 0", n)
	}
}

// TestHandleRunsKeepsItsDocumentedShape pins the fields
// docs/alternative-frontends.md publishes. A consumer this repo cannot see
// reads these names, so a rename has to fail here rather than in their UI.
func TestHandleRunsKeepsItsDocumentedShape(t *testing.T) {
	h := NewHandler(fakeAim(t, []fakeRun{
		{experiment: "bert-pretrain", hash: "a1b2c3", name: "bert-tiny",
			creationTime: 1745321780.5, endTime: 1745367020.3,
			tags: map[string]any{
				TagKind: "training", "alidade.version": "v3",
				TagSubmitID: "abc-123-def", "alidade.user": "alice",
			}},
	}), nil, nil)

	req := httptest.NewRequest("GET", "/api/runs", nil)
	rr := httptest.NewRecorder()
	h.HandleRuns(rr, req)

	var got []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d runs, want 1", len(got))
	}
	for key, want := range map[string]any{
		"hash": "a1b2c3", "name": "bert-tiny", "experiment": "bert-pretrain",
		"active": false, "version": "v3", "submit_id": "abc-123-def",
		"submitted_by": "alice",
	} {
		if got[0][key] != want {
			t.Errorf("%s = %v, want %v — this field is published in "+
				"docs/alternative-frontends.md", key, got[0][key], want)
		}
	}
}

// TestHandleRunsExcludesEvalAndMetadata pins the filter the previous
// implementation applied, so "make it faster" did not quietly become "return
// a different set". Sample runs are included, which is a quirk rather than a
// design — correcting it belongs in a ticket that says so.
func TestHandleRunsExcludesEvalAndMetadata(t *testing.T) {
	h := NewHandler(fakeAim(t, []fakeRun{
		{experiment: "exp", hash: "t1", tags: map[string]any{TagKind: "training"}},
		{experiment: "exp", hash: "e1", tags: map[string]any{TagKind: "eval"}},
		{experiment: "exp", hash: "m1", tags: map[string]any{TagKind: "metadata"}},
		{experiment: "exp", hash: "s1", tags: map[string]any{TagKind: "sample"}},
	}), nil, nil)

	req := httptest.NewRequest("GET", "/api/runs", nil)
	rr := httptest.NewRecorder()
	h.HandleRuns(rr, req)
	var got []RunSummary
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, r := range got {
		seen[r.Hash] = r.Kind
	}
	if _, ok := seen["e1"]; ok {
		t.Error("eval run leaked into /api/runs")
	}
	if _, ok := seen["m1"]; ok {
		t.Error("metadata run leaked into /api/runs")
	}
	if kind, ok := seen["s1"]; !ok {
		t.Error("sample run dropped from /api/runs — a behaviour change this " +
			"ticket did not intend to make")
	} else if kind != "sample" {
		t.Errorf("sample run's kind = %q, want \"sample\"; consumers filter on it", kind)
	}
}
