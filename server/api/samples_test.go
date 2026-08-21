package api

// Tests for HandleRunSamples — sample-batch discovery.
//
// Contract being verified, taken from EXAMPLES-1's ticket and the
// producer's docs rather than from the implementation:
//
//   * Returns the sample Aim runs logged against a model run, keyed by
//     astrolabe.kind == "sample" AND astrolabe.model_run_hash == <hash>.
//   * Dedups by sample_set keeping the newest by creation_time.
//   * Drops batches with no sample_set — a block cannot be labelled.
//   * Returns [] and not null when a run has no samples, which is the
//     common case for every run predating the feature.
//   * Rejects a missing run hash with 400, and an unreachable Aim with
//     502 — never an empty list, which reads as "no samples exist".
//   * Reads tags whether Aim serialises them flat or nested.
//
// Runs against the same fakeAim httptest fixture as evals_test.go, so
// param-shape parsing and the ListExperimentRuns paging quirks go
// through real code rather than a stub.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func makeSampleFakeRun(hash, sampleSet, modelRunHash string, createdAt time.Time) fakeRun {
	return fakeRun{
		// log_samples files a batch under the submitting experiment when
		// one is in scope, falling back to "sample/<set>". Neither is
		// what discovery keys on — see the no-pre-filter comment in
		// samples.go — so the tests use the fallback shape.
		experiment:   "sample/" + sampleSet,
		hash:         hash,
		creationTime: unixSecs(createdAt),
		endTime:      unixSecs(createdAt.Add(time.Minute)),
		tags: map[string]any{
			"astrolabe.kind":           "sample",
			"astrolabe.sample_set":     sampleSet,
			"astrolabe.model_run_hash": modelRunHash,
		},
	}
}

func callSamples(t *testing.T, h *Handler, modelRunHash string) []SampleManifestEntry {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/runs/"+modelRunHash+"/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp []SampleManifestEntry
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func sampleHandler(t *testing.T, runs []fakeRun) *Handler {
	t.Helper()
	return NewHandler(fakeAim(t, runs), nil, nil)
}

// --- unhappy paths ---

func TestSamplesEmptyIsArrayNotNull(t *testing.T) {
	// A JSON null breaks the client's .map, and every run logged before
	// this feature existed hits this path. The body matters, not just
	// the decoded value.
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s1", "faces", "other-model", time.Now()),
	})
	req := httptest.NewRequest("GET", "/api/runs/model-a/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)

	if got := rr.Body.String(); got != "[]\n" && got != "[]" {
		t.Fatalf("expected an empty JSON array, got %q", got)
	}
}

func TestSamplesMissingRunHashIsBadRequest(t *testing.T) {
	h := sampleHandler(t, nil)
	req := httptest.NewRequest("GET", "/api/runs//samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)

	if rr.Code != 400 {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestSamplesForAnotherModelAreNotReturned(t *testing.T) {
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s1", "faces", "model-a", time.Now()),
		makeSampleFakeRun("s2", "faces", "model-b", time.Now()),
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 || got[0].AimRunHash != "s1" {
		t.Fatalf("expected only model-a's batch, got %+v", got)
	}
}

func TestSamplesIgnoreNonSampleRuns(t *testing.T) {
	// A training run and a metadata run attributed to the same model.
	// Only kind=sample counts; nothing else about them differs.
	now := time.Now()
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s1", "faces", "model-a", now),
		{
			experiment: "my-experiment", hash: "t1", creationTime: unixSecs(now),
			tags: map[string]any{"astrolabe.model_run_hash": "model-a"},
		},
		{
			experiment: "my-experiment", hash: "m1", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind":           "metadata",
				"astrolabe.model_run_hash": "model-a",
			},
		},
		{
			experiment: "eval/glue", hash: "e1", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind":           "eval",
				"astrolabe.task_set":       "glue",
				"astrolabe.model_run_hash": "model-a",
			},
		},
		// Carries a sample_set but is not a sample. Without this the
		// kind check is never exercised: every other impostor above is
		// excluded by the empty-sample_set filter first, so deleting the
		// kind check entirely left this test green.
		{
			experiment: "my-experiment", hash: "x1", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind":           "metadata",
				"astrolabe.sample_set":     "faces",
				"astrolabe.model_run_hash": "model-a",
			},
		},
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 || got[0].AimRunHash != "s1" {
		t.Fatalf("expected only the sample run, got %+v", got)
	}
}

func TestSamplesWithoutASetAreDropped(t *testing.T) {
	now := time.Now()
	unlabelled := makeSampleFakeRun("s2", "", "model-a", now)
	delete(unlabelled.tags, "astrolabe.sample_set")

	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s1", "faces", "model-a", now),
		unlabelled,
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 || got[0].SampleSet != "faces" {
		t.Fatalf("expected the unlabelled batch dropped, got %+v", got)
	}
}

func TestSamplesDedupeBySetKeepingNewest(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now()
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s-old", "faces", "model-a", old),
		makeSampleFakeRun("s-new", "faces", "model-a", recent),
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 {
		t.Fatalf("expected one batch after dedupe, got %d: %+v", len(got), got)
	}
	if got[0].AimRunHash != "s-new" {
		t.Fatalf("expected the newest batch, got %q", got[0].AimRunHash)
	}
}

func TestSamplesReadNestedTagLayout(t *testing.T) {
	// Aim serialises run params either flat ("astrolabe.kind") or nested
	// under a top-level "astrolabe" mapping, depending on version. This
	// is the only test that exercises the nested branch: a SampleSet
	// missing from the fallback's guard condition fails nothing else,
	// and the failure is an empty tab rather than an error.
	now := time.Now()
	h := sampleHandler(t, []fakeRun{{
		experiment:   "sample/faces",
		hash:         "s1",
		creationTime: unixSecs(now),
		tags: map[string]any{
			"astrolabe": map[string]any{
				"kind":           "sample",
				"sample_set":     "faces",
				"model_run_hash": "model-a",
			},
		},
	}})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 {
		t.Fatalf("nested tags were not read: got %+v", got)
	}
	if got[0].SampleSet != "faces" {
		t.Fatalf("expected sample_set from the nested layout, got %q", got[0].SampleSet)
	}
}

func TestSamplesReadNestedSetAmongFlatTags(t *testing.T) {
	// The mixed layout: everything flat except sample_set. This is the
	// only shape that exercises the `tags.SampleSet == ""` clause in the
	// fallback guard — with all-empty params the guard fires on the other
	// clauses anyway, so removing the SampleSet clause broke nothing and
	// the test above stayed green.
	now := time.Now()
	h := sampleHandler(t, []fakeRun{{
		experiment:   "sample/faces",
		hash:         "s1",
		creationTime: unixSecs(now),
		tags: map[string]any{
			"astrolabe.kind":                    "sample",
			"astrolabe.model_run_hash":          "model-a",
			"astrolabe.version":                 "v3",
			"astrolabe.submit_id":               "sub-1",
			"astrolabe.experiment":              "my-experiment",
			"astrolabe.user":                    "nathan",
			"astrolabe.gpu_type":                "A100",
			"astrolabe.outcome":                 "success",
			"astrolabe.repo":                    "orion",
			"astrolabe.backend":                 "lambda",
			"astrolabe.task_set":                "unused",
			"astrolabe.gpu_rate_cents_per_hour": 110,
			"astrolabe": map[string]any{
				"sample_set": "faces",
			},
		},
	}})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 {
		t.Fatalf("a nested sample_set among flat tags was not read: %+v", got)
	}
	if got[0].SampleSet != "faces" {
		t.Fatalf("expected sample_set from the nested mapping, got %q", got[0].SampleSet)
	}
}

func TestSamplesAimUnreachableIsBadGateway(t *testing.T) {
	// Not an empty list. "No samples were logged" is a plausible and
	// wrong answer, and a reader would go looking in the producer repo.
	h := NewHandler(NewAimClient("http://127.0.0.1:1"), nil, nil)
	req := httptest.NewRequest("GET", "/api/runs/model-a/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)

	if rr.Code != 502 {
		t.Fatalf("expected 502 when Aim is unreachable, got %d", rr.Code)
	}
}

// --- happy path ---

func TestSamplesReturnsSetsNewestFirst(t *testing.T) {
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s-faces", "faces", "model-a", older),
		makeSampleFakeRun("s-text", "sentence-completion", "model-a", newer),
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 2 {
		t.Fatalf("expected two batches, got %d: %+v", len(got), got)
	}
	if got[0].SampleSet != "sentence-completion" || got[1].SampleSet != "faces" {
		t.Fatalf("expected newest first, got %q then %q", got[0].SampleSet, got[1].SampleSet)
	}
	if got[0].ModelRunHash != "model-a" {
		t.Fatalf("model_run_hash not carried through: %q", got[0].ModelRunHash)
	}
}
