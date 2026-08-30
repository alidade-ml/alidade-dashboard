package api

// Tests for HandleRunEvals — the eval-discovery endpoint.
//
// Contract being verified (from plans/eval-runs.md, not from the
// implementation):
//
//   * Returns the set of eval Aim runs that score a given model run,
//     keyed by ``astrolabe.kind == "eval"`` AND
//     ``astrolabe.model_run_hash == <hash>``.
//   * Dedups by ``task_set`` keeping the newest by creation_time —
//     re-eval over time leaves older runs in Aim for forensics; the
//     dashboard shows the latest by default.
//   * Returns deterministic ordering: newest first, ties broken by
//     task_set.
//   * Rejects requests without a run hash with HTTP 400.
//   * Skips eval runs missing a ``task_set`` tag (a section can't be
//     rendered without a label).
//   * Skips non-eval runs (training, metadata) even when they happen
//     to share the eval Aim experiment.
//
// Uses the same fakeAim httptest fixture as cost_test.go so the SDK
// layer (param-shape parsing, ListExperimentRuns paging quirks) goes
// through real code.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// makeEvalFakeRun creates an eval-tagged fakeRun.
func makeEvalFakeRun(hash, taskSet, modelRunHash string, createdAt time.Time) fakeRun {
	return fakeRun{
		// Eval runs are filed under ``eval/<task_set>`` per the
		// log_eval_table helper's convention.
		experiment:   "eval/" + taskSet,
		hash:         hash,
		creationTime: unixSecs(createdAt),
		endTime:      unixSecs(createdAt.Add(time.Minute)),
		tags: map[string]any{
			"astrolabe.kind":           "eval",
			"astrolabe.task_set":       taskSet,
			"astrolabe.model_run_hash": modelRunHash,
		},
	}
}

// withModelRun prepends the training run the eval manifest is being asked
// about. The endpoint verifies the model exists before answering, so a
// fixture that declares only eval runs is describing an Aim where the
// model was never written — which 404s, correctly.
//
// Every test in this file asks about "model-1"; before the existence
// guard, none of them had to say it existed.
func withModelRun(runs []fakeRun) []fakeRun {
	model := fakeRun{
		experiment:   "training",
		hash:         "model-1",
		creationTime: unixSecs(time.Date(2026, 5, 30, 11, 0, 0, 0, time.UTC)),
		tags:         map[string]any{"astrolabe.kind": "training"},
	}
	return append([]fakeRun{model}, runs...)
}

func callEvals(t *testing.T, h *Handler, modelRunHash string) []EvalManifestEntry {
	t.Helper()
	url := "/api/runs/" + modelRunHash + "/evals"
	req := httptest.NewRequest("GET", url, nil)
	rr := httptest.NewRecorder()
	h.HandleRunEvals(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp []EvalManifestEntry
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// --- Empty/edge cases ---

func TestHandleRunEvalsNoEvals(t *testing.T) {
	aim := fakeAim(t, withModelRun(nil))
	h := makeHandlerWithAim(t, aim)
	got := callEvals(t, h, "model-1")
	if len(got) != 0 {
		t.Fatalf("expected empty manifest, got %v", got)
	}
}

func TestHandleRunEvalsMissingHashReturns400(t *testing.T) {
	aim := fakeAim(t, withModelRun(nil))
	h := makeHandlerWithAim(t, aim)
	// URL with /api/runs//evals — empty path segment — must 400.
	req := httptest.NewRequest("GET", "/api/runs//evals", nil)
	rr := httptest.NewRecorder()
	h.HandleRunEvals(rr, req)
	if rr.Code != 400 {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// --- Filtering by tag contract ---

func TestHandleRunEvalsFiltersByModelRunHash(t *testing.T) {
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		makeEvalFakeRun("e1", "glue", "model-1", t0),
		makeEvalFakeRun("e2", "glue", "model-2", t0),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got[0].AimRunHash != "e1" {
		t.Errorf("expected e1, got %s", got[0].AimRunHash)
	}
}

func TestHandleRunEvalsSkipsNonEvalKind(t *testing.T) {
	// Training and metadata runs filed under eval/glue (an accident or
	// misconfigured producer) must NOT surface in the eval manifest.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		{
			experiment:   "eval/glue",
			hash:         "training-run",
			creationTime: unixSecs(t0),
			tags: map[string]any{
				"astrolabe.kind":           "training",
				"astrolabe.model_run_hash": "model-1",
				"astrolabe.task_set":       "glue",
			},
		},
		{
			experiment:   "eval/glue",
			hash:         "metadata-run",
			creationTime: unixSecs(t0),
			tags: map[string]any{
				"astrolabe.kind":           "metadata",
				"astrolabe.model_run_hash": "model-1",
				"astrolabe.task_set":       "glue",
			},
		},
		makeEvalFakeRun("eval-run", "glue", "model-1", t0),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got[0].AimRunHash != "eval-run" {
		t.Errorf("expected eval-run, got %s", got[0].AimRunHash)
	}
}

func TestHandleRunEvalsSkipsEmptyTaskSet(t *testing.T) {
	// A section can't render without a label; drop these from the
	// manifest rather than display a blank-titled section.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		{
			experiment:   "eval/glue",
			hash:         "no-tag",
			creationTime: unixSecs(t0),
			tags: map[string]any{
				"astrolabe.kind":           "eval",
				"astrolabe.model_run_hash": "model-1",
				// astrolabe.task_set missing
			},
		},
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 0 {
		t.Errorf("expected empty manifest, got %v", got)
	}
}

func TestHandleRunEvalsFindsEvalRunsRegardlessOfExperimentFiling(t *testing.T) {
	// Discovery is TAG-BASED, not experiment-name-based (see
	// plans/eval-runs.md). Real-world case: in local-aim mode the
	// sidecar's experiment association stamps synced eval runs with
	// the *training* experiment name (e.g. ``06b-rtd-calibration``),
	// not ``eval/<task_set>``. An experiment-name pre-filter would
	// silently drop these; the tag check is the source of truth.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		{
			experiment:   "06b-rtd-calibration",
			hash:         "sidecar-attributed",
			creationTime: unixSecs(t0),
			tags: map[string]any{
				"astrolabe.kind":           "eval",
				"astrolabe.task_set":       "glue",
				"astrolabe.model_run_hash": "model-1",
			},
		},
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 1 {
		t.Fatalf("expected sidecar-attributed run to be discovered, got %v", got)
	}
	if got[0].AimRunHash != "sidecar-attributed" {
		t.Errorf("expected sidecar-attributed, got %s", got[0].AimRunHash)
	}
	if got[0].TaskSet != "glue" {
		t.Errorf("expected task_set=glue, got %s", got[0].TaskSet)
	}
}

// --- Re-eval / dedup semantics ---

func TestHandleRunEvalsDedupsByTaskSetKeepingNewest(t *testing.T) {
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	older := t0
	newer := t0.Add(2 * time.Hour)
	runs := []fakeRun{
		makeEvalFakeRun("eval-old", "glue", "model-1", older),
		makeEvalFakeRun("eval-new", "glue", "model-1", newer),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 1 {
		t.Fatalf("expected dedup to one row, got %d: %v", len(got), got)
	}
	if got[0].AimRunHash != "eval-new" {
		t.Errorf("expected newer eval-new, got %s", got[0].AimRunHash)
	}
}

func TestHandleRunEvalsMultipleTaskSets(t *testing.T) {
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		makeEvalFakeRun("e-glue", "glue", "model-1", t0),
		makeEvalFakeRun("e-mmlu", "mmlu", "model-1", t0.Add(time.Hour)),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	taskSets := map[string]bool{got[0].TaskSet: true, got[1].TaskSet: true}
	if !taskSets["glue"] || !taskSets["mmlu"] {
		t.Errorf("expected glue and mmlu, got %v", taskSets)
	}
}

// --- Ordering ---

func TestHandleRunEvalsOrdersNewestFirst(t *testing.T) {
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		// Out-of-order on purpose — the handler must sort.
		makeEvalFakeRun("e-old", "mmlu", "model-1", t0),
		makeEvalFakeRun("e-new", "glue", "model-1", t0.Add(2*time.Hour)),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].AimRunHash != "e-new" {
		t.Errorf("expected newest first, got %s", got[0].AimRunHash)
	}
}

func TestHandleRunEvalsTaskSetBreaksTimeTies(t *testing.T) {
	// Two eval runs created at the same instant — deterministic order
	// requires a secondary key. Plan says task_set ascending.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		makeEvalFakeRun("e-mmlu", "mmlu", "model-1", t0),
		makeEvalFakeRun("e-glue", "glue", "model-1", t0),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].TaskSet != "glue" {
		t.Errorf("expected glue first (alphabetical tiebreak), got %s", got[0].TaskSet)
	}
}

// --- Happy path summary ---

func TestHandleRunEvalsHappyPath(t *testing.T) {
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runs := []fakeRun{
		makeEvalFakeRun("e1", "glue", "model-1", t0.Add(2*time.Hour)),
		makeEvalFakeRun("e2", "mmlu", "model-1", t0.Add(time.Hour)),
		// Older re-eval of glue — should NOT surface.
		makeEvalFakeRun("e3", "glue", "model-1", t0),
		// Different model — should NOT surface.
		makeEvalFakeRun("e4", "glue", "model-2", t0.Add(time.Hour)),
	}
	aim := fakeAim(t, withModelRun(runs))
	h := makeHandlerWithAim(t, aim)

	got := callEvals(t, h, "model-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (glue-newest, mmlu), got %d: %v",
			len(got), got)
	}
	// glue newer than mmlu, so glue first.
	if got[0].TaskSet != "glue" || got[0].AimRunHash != "e1" {
		t.Errorf("expected glue/e1 first, got %s/%s",
			got[0].TaskSet, got[0].AimRunHash)
	}
	if got[1].TaskSet != "mmlu" || got[1].AimRunHash != "e2" {
		t.Errorf("expected mmlu/e2 second, got %s/%s",
			got[1].TaskSet, got[1].AimRunHash)
	}
}

func TestHandleRunEvalsFiltersByModelAcrossDifferentTaskSets(t *testing.T) {
	// TestHandleRunEvalsFiltersByModelRunHash puts both models' evals
	// under the SAME task_set, so dedupe-by-task-set collapses them to
	// one entry and the count is 1 whether or not the model filter
	// works. Found by a mutation that removed the filter and passed.
	//
	// Different task sets, so a leak shows up as an extra entry.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	h := makeHandlerWithAim(t, fakeAim(t, withModelRun([]fakeRun{
		makeEvalFakeRun("e1", "glue", "model-1", t0),
		makeEvalFakeRun("e2", "squad", "model-2", t0),
	})))

	got := callEvals(t, h, "model-1")
	if len(got) != 1 {
		t.Fatalf("expected only model-1's eval, got %d: %v", len(got), got)
	}
	if got[0].TaskSet != "glue" {
		t.Errorf("another model's eval leaked in: %+v", got)
	}
}

// --- Existence of the model being asked about ---

// A hash Aim does not know must 404, not answer 200 with an empty list.
//
// An empty list reads as "this model has no evals", which is a plausible
// and wrong answer: a hash truncated in transcription was read exactly
// that way, and the eval was reported missing when it had landed fine.
// The samples endpoint has always drawn this distinction.
func TestHandleRunEvalsUnknownModelReturns404(t *testing.T) {
	// The fixture holds an eval for a DIFFERENT model, so the search
	// route answers normally and only the existence check can produce
	// the 404. Without that, an empty project would pass this test.
	t0 := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	aim := fakeAim(t, withModelRun([]fakeRun{
		makeEvalFakeRun("e1", "glue", "model-1", t0),
	}))
	h := makeHandlerWithAim(t, aim)

	req := httptest.NewRequest("GET", "/api/runs/definitely-not-a-run/evals", nil)
	rr := httptest.NewRecorder()
	h.HandleRunEvals(rr, req)

	if rr.Code != 404 {
		t.Fatalf("unknown model hash: expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// The guard must not swallow the ordinary case. A model that exists and
// has no evals still answers 200 with an empty manifest — deleting the
// handler body would satisfy the 404 test above and prove nothing.
func TestHandleRunEvalsKnownModelWithNoEvalsStill200(t *testing.T) {
	h := makeHandlerWithAim(t, fakeAim(t, withModelRun(nil)))

	got := callEvals(t, h, "model-1")
	if len(got) != 0 {
		t.Fatalf("expected an empty manifest, got %d: %v", len(got), got)
	}
}
