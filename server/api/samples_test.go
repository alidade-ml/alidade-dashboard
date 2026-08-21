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
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The experiment a submit's runs share. log_samples files a batch under
// the submitting experiment, so a model run and its sample batches are
// siblings — which is what makes scoped discovery possible.
const fixtureExperiment = "my-experiment"

// makeModelRun is the run being sampled. Discovery starts from it now, so
// it has to exist: the old project-wide scan returned [] for a hash Aim
// had never heard of, which was indistinguishable from "no samples".
func makeModelRun(hash string) fakeRun {
	return fakeRun{
		experiment:   fixtureExperiment,
		hash:         hash,
		creationTime: unixSecs(time.Now().Add(-3 * time.Hour)),
		tags:         map[string]any{"astrolabe.kind": "training"},
	}
}

func makeSampleFakeRun(hash, sampleSet, modelRunHash string, createdAt time.Time) fakeRun {
	return fakeRun{
		experiment:   fixtureExperiment,
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

// sampleHandler wires a fake Aim holding the given runs plus the two
// model runs every test queries. Injected rather than repeated in each
// fixture: discovery resolves the model run's experiment first, so its
// absence would fail every test for the same uninteresting reason.
func sampleHandler(t *testing.T, runs []fakeRun) *Handler {
	t.Helper()
	have := map[string]bool{}
	for _, r := range runs {
		have[r.hash] = true
	}
	for _, h := range []string{"model-a", "model-b"} {
		if !have[h] {
			runs = append(runs, makeModelRun(h))
		}
	}
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
		experiment:   fixtureExperiment,
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
		experiment:   fixtureExperiment,
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

// --- HandleSampleBatch: payloads, read from astrolabe's export ---
//
// Contract taken from astrolabe's docs/samples-export.md, not from the
// reader:
//
//   * One directory per sample Aim run, containing manifest.json.
//   * format_version is checked, never best-effort parsed — the dashboard
//     and astrolabe ship on separate cadences and a changed shape decodes
//     into something plausible and wrong.
//   * An absent export is 404 ("not exported yet"), which is a different
//     fact from a batch with no pairs (200, []).
//   * Absent halves are absent fields, not empty strings.
//   * kind describes the OUTPUT. A prompt-to-image batch is kind "image"
//     with input_text set.
//   * The hash arrives in a URL and becomes a filesystem path.

func writeExport(t *testing.T, dir, hash, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, hash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hash, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func callBatch(t *testing.T, samplesDir, hash string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(nil, nil, nil).WithSamplesDir(samplesDir)
	req := httptest.NewRequest("GET", "/api/samples/"+hash, nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBatch(rr, req)
	return rr
}

func decodeBatch(t *testing.T, rr *httptest.ResponseRecorder) SampleBatch {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var b SampleBatch
	if err := json.NewDecoder(rr.Body).Decode(&b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b
}

const textBatchManifest = `{
  "format_version": 1,
  "aim_run_hash": "abc123",
  "sample_set": "sentence-completion",
  "model_run_hash": "model-a",
  "kind": "text",
  "exported_at": "2026-08-21T19:28:13+00:00",
  "pairs": [
    {"step": 0, "input_text": "def fib(n):", "input_file": null,
     "output_text": "\n    return n", "output_file": null},
    {"step": 1, "input_text": "The capital of France is", "input_file": null,
     "output_text": " Paris.", "output_file": null},
    {"step": 2, "input_text": "", "input_file": null,
     "output_text": "unconditional", "output_file": null}
  ]
}`

func TestBatchRefusesAnUnknownFormatVersion(t *testing.T) {
	// Not parsed on a best effort: a manifest whose shape changed decodes
	// into something plausible, and the reader believes it. The error has
	// to name both versions or an operator cannot tell which side is behind.
	dir := t.TempDir()
	writeExport(t, dir, "abc123", `{"format_version": 99, "pairs": []}`)

	rr := callBatch(t, dir, "abc123")
	if rr.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "99") || !strings.Contains(body, "1") {
		t.Errorf("error names neither version: %q", body)
	}
}

func TestBatchNotExportedIs404NotAnEmptyBatch(t *testing.T) {
	// Discovery lists what exists in Aim; the export can lag it. An empty
	// grid for a batch that simply has not been written yet reads as data
	// loss, so the two answers must be distinguishable.
	rr := callBatch(t, t.TempDir(), "neverexported")
	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestBatchWithNoPairsIs200WithAnEmptyArray(t *testing.T) {
	// The other half of the pair above, and the JSON has to be [] rather
	// than null — the client maps over it.
	dir := t.TempDir()
	writeExport(t, dir, "abc123", `{"format_version": 1, "aim_run_hash": "abc123",
	  "sample_set": "faces", "kind": "text", "pairs": null}`)

	rr := callBatch(t, dir, "abc123")
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"pairs":[]`) {
		t.Errorf("pairs did not serialise as []: %s", rr.Body.String())
	}
}

func TestBatchMalformedManifestIs502(t *testing.T) {
	dir := t.TempDir()
	writeExport(t, dir, "abc123", `{"format_version": 1, "pairs": [`)

	rr := callBatch(t, dir, "abc123")
	if rr.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestBatchRejectsAPathTraversingHash(t *testing.T) {
	// The hash comes off a URL and ends up joined into a filesystem path.
	// A well-formed manifest is planted exactly where the traversal would
	// land, so this fails loudly if the guard is removed rather than
	// passing because the target happened not to exist.
	root := t.TempDir()
	samplesDir := filepath.Join(root, "samples")
	if err := os.MkdirAll(samplesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExport(t, root, "elsewhere", textBatchManifest)

	for _, hash := range []string{"../elsewhere", "..%2Felsewhere", "a/../../elsewhere", "/etc"} {
		rr := callBatch(t, samplesDir, hash)
		if rr.Code != 400 {
			t.Errorf("%q: expected 400, got %d: %s", hash, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "sentence-completion") {
			t.Errorf("%q: served the manifest outside the samples dir", hash)
		}
	}
}

func TestBatchKindDescribesTheOutputNotTheInput(t *testing.T) {
	// A prompt-to-image batch: kind "image", input_text set. Inferring the
	// input's type from kind is the asymmetry that has already caused bugs
	// on both sides of this contract.
	dir := t.TempDir()
	writeExport(t, dir, "abc123", `{
	  "format_version": 1, "aim_run_hash": "abc123", "sample_set": "faces",
	  "model_run_hash": "model-a", "kind": "image",
	  "exported_at": "2026-08-21T19:28:13+00:00",
	  "pairs": [{"step": 0, "input_text": "a golden retriever",
	             "input_file": null, "output_text": null,
	             "output_file": "0-output.png"}]}`)

	b := decodeBatch(t, callBatch(t, dir, "abc123"))
	if b.Kind != "image" {
		t.Fatalf("kind = %q, want image", b.Kind)
	}
	p := b.Pairs[0]
	if p.InputText == nil || *p.InputText != "a golden retriever" {
		t.Errorf("input_text lost on an image batch: %v", p.InputText)
	}
	if p.OutputFile == nil || *p.OutputFile != "0-output.png" {
		t.Errorf("output_file = %v, want 0-output.png", p.OutputFile)
	}
	if p.OutputText != nil {
		t.Errorf("output_text should be absent, got %q", *p.OutputText)
	}
}

func TestBatchServesFilenamesNotBytes(t *testing.T) {
	// Serving the files is EXAMPLES-1.03. This route hands back names so a
	// tab can lay out its grid before pulling megabytes.
	dir := t.TempDir()
	writeExport(t, dir, "abc123", `{
	  "format_version": 1, "aim_run_hash": "abc123", "sample_set": "denoise",
	  "kind": "image", "pairs": [{"step": 0, "input_file": "0-input.png",
	                              "output_file": "0-output.png"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "abc123", "0-output.png"), []byte("\x89PNGnotreally"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := callBatch(t, dir, "abc123")
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if strings.Contains(rr.Body.String(), "PNGnotreally") {
		t.Error("the response carried image bytes")
	}
	b := decodeBatch(t, rr)
	if b.Pairs[0].InputFile == nil || *b.Pairs[0].InputFile != "0-input.png" {
		t.Errorf("input_file = %v", b.Pairs[0].InputFile)
	}
}

func TestBatchAbsentHalfIsAnAbsentFieldNotAnEmptyString(t *testing.T) {
	// Absent and empty are different facts: a set logged without inputs
	// has no input_text, while a model that returned "" has one and it is
	// empty. The manifest above has both, one per pair.
	dir := t.TempDir()
	writeExport(t, dir, "abc123", textBatchManifest)

	b := decodeBatch(t, callBatch(t, dir, "abc123"))
	if b.Pairs[0].OutputFile != nil {
		t.Errorf("absent output_file decoded as %q", *b.Pairs[0].OutputFile)
	}
	if b.Pairs[2].InputText == nil {
		t.Fatal("an input logged as \"\" was dropped as absent")
	}
	if *b.Pairs[2].InputText != "" {
		t.Errorf("input_text = %q, want empty", *b.Pairs[2].InputText)
	}
	// And the serialised form: an absent half must not appear at all,
	// or the client cannot tell it from "".
	raw := callBatch(t, dir, "abc123").Body.String()
	if strings.Contains(raw, `"output_file"`) {
		t.Errorf("absent output_file was serialised: %s", raw)
	}
}

// --- happy path ---

func TestBatchRoundTripsATextBatch(t *testing.T) {
	dir := t.TempDir()
	writeExport(t, dir, "abc123", textBatchManifest)

	b := decodeBatch(t, callBatch(t, dir, "abc123"))
	if b.SampleSet != "sentence-completion" || b.ModelRunHash != "model-a" {
		t.Errorf("identity lost: %+v", b)
	}
	if b.ExportedAt != "2026-08-21T19:28:13+00:00" {
		t.Errorf("exported_at = %q", b.ExportedAt)
	}
	if len(b.Pairs) != 3 {
		t.Fatalf("got %d pairs, want 3", len(b.Pairs))
	}
	for i, p := range b.Pairs {
		if p.Step != i {
			t.Errorf("pair %d has step %d — order is the manifest's, not the reader's", i, p.Step)
		}
	}
	if *b.Pairs[0].OutputText != "\n    return n" {
		t.Errorf("newline did not survive: %q", *b.Pairs[0].OutputText)
	}
}

// --- what scoping to the run's experiment changes ---
//
// These pin the two behaviours EXAMPLES-1.01b deliberately alters, so a
// later reader can tell a decision from a regression.

func TestSamplesInAnotherExperimentAreNotReturned(t *testing.T) {
	// The behaviour this slice removes on purpose. log_samples files a
	// batch under the submitting experiment, so a batch filed elsewhere
	// only happens when it ran with no submit in scope and fell back to
	// "sample/<set>". That is a known gap owned by the producer, NOT a
	// reason to restore a project-wide scan.
	now := time.Now()
	stray := makeSampleFakeRun("s-stray", "faces", "model-a", now)
	stray.experiment = "sample/faces"

	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s-sibling", "denoise", "model-a", now),
		stray,
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 1 {
		t.Fatalf("expected only the sibling batch, got %+v", got)
	}
	if got[0].AimRunHash != "s-sibling" {
		t.Fatalf("expected s-sibling, got %q", got[0].AimRunHash)
	}
}

func TestSamplesUnknownRunIsNotFound(t *testing.T) {
	// Three distinct facts that the old scan collapsed into one empty
	// array: the run does not exist, the run has no samples, and Aim is
	// unreachable. A typo'd hash used to render as "no samples".
	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s1", "faces", "model-a", time.Now()),
	})
	req := httptest.NewRequest("GET", "/api/runs/no-such-run/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 for an unknown run, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSamplesDoNotWalkOtherExperiments(t *testing.T) {
	// The cost claim, asserted rather than described. Discovery must not
	// call GetRunInfo on runs outside the model run's experiment, so a
	// project with many unrelated runs costs the same as a small one.
	now := time.Now()
	runs := []fakeRun{
		makeModelRun("model-a"),
		makeSampleFakeRun("s1", "faces", "model-a", now),
	}
	for i := 0; i < 40; i++ {
		runs = append(runs, fakeRun{
			experiment:   fmt.Sprintf("unrelated-%d", i),
			hash:         fmt.Sprintf("u%d", i),
			creationTime: unixSecs(now),
			tags:         map[string]any{"astrolabe.kind": "training"},
		})
	}

	var infoCalls int32
	aim := fakeAimCounting(t, runs, &infoCalls)
	h := NewHandler(aim, nil, nil)

	req := httptest.NewRequest("GET", "/api/runs/model-a/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// One for the model run itself, one for its single sibling. The 40
	// unrelated runs must cost nothing.
	if got := atomic.LoadInt32(&infoCalls); got > 4 {
		t.Fatalf("GetRunInfo called %d times; scoped discovery should not "+
			"touch the %d unrelated runs", got, 40)
	}
}
