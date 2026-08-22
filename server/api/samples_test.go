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
	"net/http"
	"net/http/httptest"
	"net/url"
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

// --- what scoping to the run's experiment changes ---
//
// These pin the two behaviours EXAMPLES-1.01b deliberately alters, so a
// later reader can tell a decision from a regression.

func TestSamplesInAnotherExperimentAreReturned(t *testing.T) {
	// INVERTED by RUNSET-1.03, deliberately.
	//
	// EXAMPLES-1.01b narrowed discovery to the model run's own
	// experiment, because the only alternative then was a project-wide
	// walk. It recorded the cost as a known gap: a batch logged with no
	// submit in scope files under "sample/<set>" and became invisible.
	//
	// Asking Aim by tag has no reason to care which experiment a batch is
	// in, so the gap closes and this assertion flips. The old version of
	// this test pinned a trade-off that no longer has to be made.
	now := time.Now()
	stray := makeSampleFakeRun("s-stray", "faces", "model-a", now)
	stray.experiment = "sample/faces"

	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s-sibling", "denoise", "model-a", now),
		stray,
	})
	got := callSamples(t, h, "model-a")

	if len(got) != 2 {
		t.Fatalf("expected both the sibling and the stray batch, got %+v", got)
	}
	found := map[string]bool{}
	for _, e := range got {
		found[e.AimRunHash] = true
	}
	if !found["s-stray"] {
		t.Errorf("the batch filed outside the model run's experiment was not found: %+v", got)
	}
	if !found["s-sibling"] {
		t.Errorf("the sibling batch was lost: %+v", got)
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

// --- HandleSampleBatch: payloads, read from Aim ---
//
// Contract from EXAMPLES-1.02b:
//   * The two sequences are joined by STEP, never by position — they
//     need not share a step set.
//   * Absent and empty are different: a set logged without inputs has no
//     input_text; a model that returned "" has one and it is "".
//   * An image batch is refused, not half-rendered. 03 serves those.
//   * The set becomes a path segment, so a slash in it is rejected.
//
// Served from the same captured bodies aim_encoding_test.go uses, so the
// handler is exercised through the real decoder over real bytes.

// fakeObjectAim serves texts/get-batch from the captured fixtures.
// bodies maps a sequence name to the fixture file serving it; a name
// that is absent gets an empty (but well-formed) response, which is what
// Aim returns for a sequence a run never logged.
func fakeObjectAim(t *testing.T, bodies map[string]string) *AimClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/get-batch/") {
			http.NotFound(w, r)
			return
		}
		var req []struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req) != 1 {
			http.Error(w, "bad request", 400)
			return
		}
		file, ok := bodies[req[0].Name]
		if !ok {
			_, _ = w.Write(nil)
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return NewAimClient(srv.URL)
}

func textBatchAim(t *testing.T) *AimClient {
	return fakeObjectAim(t, map[string]string{
		"sample/completions/input":  "texts_get_batch_input.bin",
		"sample/completions/output": "texts_get_batch_output.bin",
	})
}

func callBatch(t *testing.T, aim *AimClient, hash, set string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(aim, nil, nil)
	req := httptest.NewRequest("GET", "/api/samples/"+hash+"?set="+set, nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBatch(rr, req)
	return rr
}

func TestBatchJoinsOnStepNotPosition(t *testing.T) {
	// The captured input sequence has steps 0,1,2; the output has
	// 0,1,2,3. Joined by position, step 3's output would be paired with
	// nothing and every pair would still look plausible. Joined by step,
	// step 3 is output-only — which is what unconditional generation is.
	rr := callBatch(t, textBatchAim(t), "abc123", "completions")
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var b SampleBatch
	if err := json.NewDecoder(rr.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if len(b.Pairs) != 4 {
		t.Fatalf("got %d pairs, want 4: %+v", len(b.Pairs), b.Pairs)
	}
	for i, p := range b.Pairs {
		if p.Step != int64(i) {
			t.Fatalf("pair %d has step %d — pairs are not in step order", i, p.Step)
		}
	}
	if b.Pairs[3].InputText != nil {
		t.Errorf("step 3 has no input; got %q", *b.Pairs[3].InputText)
	}
	if b.Pairs[3].OutputText == nil || *b.Pairs[3].OutputText != "unconditional" {
		t.Errorf("step 3 output = %v", b.Pairs[3].OutputText)
	}
	if b.Pairs[0].InputText == nil || *b.Pairs[0].InputText != "def fib(n):" {
		t.Errorf("step 0 input = %v", b.Pairs[0].InputText)
	}
}

func TestBatchKeepsAnEmptyInputDistinctFromAnAbsentOne(t *testing.T) {
	// Step 2's input was logged as "". Step 3 has no input at all.
	// Collapsing them loses the only signal saying which happened.
	rr := callBatch(t, textBatchAim(t), "abc123", "completions")
	var b SampleBatch
	if err := json.NewDecoder(rr.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.Pairs[2].InputText == nil {
		t.Fatal("an input logged as \"\" was dropped as absent")
	}
	if *b.Pairs[2].InputText != "" {
		t.Errorf("step 2 input = %q, want empty", *b.Pairs[2].InputText)
	}
	// And on the wire: the absent one must not appear at all.
	raw := callBatch(t, textBatchAim(t), "abc123", "completions").Body.String()
	if !strings.Contains(raw, `"input_text":""`) {
		t.Errorf("the empty input did not serialise: %s", raw)
	}
}

func TestBatchRejectsABadHashOrSet(t *testing.T) {
	aim := textBatchAim(t)
	for _, tc := range []struct{ hash, set string }{
		{"", "completions"},
		{"../etc", "completions"},
		{"abc123", ""},
		{"abc123", "a/b"}, // the set is a path segment in the sequence name
	} {
		rr := callBatch(t, aim, tc.hash, tc.set)
		if rr.Code != 400 {
			t.Errorf("hash=%q set=%q: expected 400, got %d", tc.hash, tc.set, rr.Code)
		}
	}
}

func TestBatchUnreachableAimIs502(t *testing.T) {
	rr := callBatch(t, NewAimClient("http://127.0.0.1:1"), "abc123", "completions")
	if rr.Code != 502 {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
}

func TestBatchWithNoSequencesIsEmptyNotNull(t *testing.T) {
	// A run with no such set: Aim returns an empty body. That is a valid
	// answer meaning "nothing logged", and the client maps over pairs.
	rr := callBatch(t, fakeObjectAim(t, nil), "abc123", "completions")
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"pairs":[]`) {
		t.Errorf("pairs did not serialise as []: %s", rr.Body.String())
	}
}

// --- images: metadata, then blobs (EXAMPLES-1.03) ---
//
// Contract:
//   * The image sequence carries metadata and an opaque blob_uri. Pixels
//     need a second POST to a REPO-level route with no run hash.
//   * Blobs come back keyed by uri. Joining by request order is
//     indistinguishable from correct with one image and wrong with two.
//   * The uri is Aim's Fernet token. It goes back verbatim: never
//     parsed, rebuilt or normalised.
//   * Content-Type comes from the record's format, never from sniffing.

func imageBatchAim(t *testing.T) *AimClient {
	return fakeObjectAim(t, map[string]string{
		"sample/faces/output": "images_get_batch.bin",
	})
}

// blobURIsFromFixture returns the uris in the order the captured
// sequence lists them.
func blobURIsFromFixture(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "images_blob_uris.json"))
	if err != nil {
		t.Fatal(err)
	}
	var uris []string
	if err := json.Unmarshal(b, &uris); err != nil {
		t.Fatal(err)
	}
	if len(uris) != 3 {
		t.Fatalf("fixture has %d uris, want 3 — the order test needs at least 2", len(uris))
	}
	return uris
}

// fakeBlobAim serves the captured blob-batch body for any request.
// requested records what the handler asked for.
func fakeBlobAim(t *testing.T, requested *[]string) *AimClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/get-batch") {
			http.NotFound(w, r)
			return
		}
		var uris []string
		_ = json.NewDecoder(r.Body).Decode(&uris)
		if requested != nil {
			*requested = append(*requested, uris...)
		}
		b, err := os.ReadFile(filepath.Join("testdata", "images_blobs_batch.bin"))
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return NewAimClient(srv.URL)
}

func TestBlobsAreKeyedByURINotByRequestOrder(t *testing.T) {
	// The test that needs more than one image. Asking for the uris in
	// REVERSE order must still return each uri its own bytes; an
	// implementation that zips the response against the request order
	// passes with one image and silently swaps with two.
	uris := blobURIsFromFixture(t)
	aim := fakeBlobAim(t, nil)

	forward, err := aim.GetBlobs(uris)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []string{uris[2], uris[1], uris[0]}
	backward, err := aim.GetBlobs(reversed)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range uris {
		if len(forward[u]) == 0 {
			t.Fatalf("uri %.20s… returned no bytes", u)
		}
		if string(forward[u]) != string(backward[u]) {
			t.Errorf("uri %.20s… got different bytes depending on request order", u)
		}
	}
	// And the three are not all the same blob, or the check above is vacuous.
	if string(forward[uris[0]]) == string(forward[uris[1]]) {
		t.Fatal("fixture blobs are identical; the ordering test proves nothing")
	}
}

func TestBlobsAreRealImages(t *testing.T) {
	uris := blobURIsFromFixture(t)
	blobs, err := fakeBlobAim(t, nil).GetBlobs(uris)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range uris {
		b := blobs[u]
		if len(b) < 8 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Errorf("uri %.20s… did not return a PNG: % x", u, b[:min(8, len(b))])
		}
	}
}

func TestBlobURIGoesBackToAimVerbatim(t *testing.T) {
	// Fernet tokens contain '-', '_' and '=' padding. Any unescaping,
	// re-encoding or normalisation on this side produces a token Aim
	// cannot decrypt, and the failure is a broken image rather than an
	// error.
	uris := blobURIsFromFixture(t)
	var requested []string
	aim := fakeBlobAim(t, &requested)

	h := NewHandler(aim, nil, nil)
	req := httptest.NewRequest("GET", "/api/samples/blob?uri="+url.QueryEscape(uris[0]), nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(requested) != 1 {
		t.Fatalf("expected exactly one uri sent to Aim, got %d", len(requested))
	}
	if requested[0] != uris[0] {
		t.Errorf("uri was altered in transit:\n sent: %q\n want: %q", requested[0], uris[0])
	}
}

func TestBlobContentTypeComesFromTheFormatNotSniffing(t *testing.T) {
	uris := blobURIsFromFixture(t)
	h := NewHandler(fakeBlobAim(t, nil), nil, nil)

	req := httptest.NewRequest("GET", "/api/samples/blob?uri="+url.QueryEscape(uris[0])+"&format=png", nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}

	// A format outside the closed set is a disagreement between the
	// batch and the request, not something to guess at.
	req = httptest.NewRequest("GET", "/api/samples/blob?uri="+url.QueryEscape(uris[0])+"&format=tiff", nil)
	rr = httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)
	if rr.Code != 400 {
		t.Errorf("expected 400 for an unsupported format, got %d", rr.Code)
	}
}

func TestBlobMissingURIIsRejectedBeforeAnyRequest(t *testing.T) {
	var requested []string
	h := NewHandler(fakeBlobAim(t, &requested), nil, nil)
	req := httptest.NewRequest("GET", "/api/samples/blob", nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)

	if rr.Code != 400 {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if len(requested) != 0 {
		t.Errorf("a request left the hub for a missing uri: %v", requested)
	}
}

func TestBlobUnknownURIIs404NotAnEmpty200(t *testing.T) {
	// An empty 200 renders as a blank image; a 404 renders as a broken
	// one. Broken is honest.
	h := NewHandler(fakeBlobAim(t, nil), nil, nil)
	req := httptest.NewRequest("GET", "/api/samples/blob?uri=not-a-real-token", nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)
	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestBlobUnreachableAimIs502(t *testing.T) {
	h := NewHandler(NewAimClient("http://127.0.0.1:1"), nil, nil)
	req := httptest.NewRequest("GET", "/api/samples/blob?uri=x", nil)
	rr := httptest.NewRecorder()
	h.HandleSampleBlob(rr, req)
	if rr.Code != 502 {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
}

func TestAnImageBatchCarriesURIsAndKindImage(t *testing.T) {
	rr := callBatch(t, imageBatchAim(t), "abc123", "faces")
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var b SampleBatch
	if err := json.NewDecoder(rr.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.Kind != "image" {
		t.Errorf("kind = %q, want image", b.Kind)
	}
	if len(b.Pairs) != 3 {
		t.Fatalf("got %d pairs, want 3", len(b.Pairs))
	}
	for _, p := range b.Pairs {
		if p.OutputURI == nil || *p.OutputURI == "" {
			t.Errorf("step %d has no output_uri", p.Step)
		}
		if p.OutputText != nil {
			t.Errorf("step %d has output_text on an image batch: %q", p.Step, *p.OutputText)
		}
	}
}

func TestAnImageBatchCarriesNoPixelBytes(t *testing.T) {
	// The batch route hands back names so a tab can lay out its grid
	// before pulling megabytes. Bytes come from the blob route.
	rr := callBatch(t, imageBatchAim(t), "abc123", "faces")
	if strings.Contains(rr.Body.String(), "\x89PNG") {
		t.Error("the batch response carried image bytes")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestDiscoveryDoesNotEnumerateExperiments(t *testing.T) {
	// Both discovery endpoints ask now. The bodies are identical either
	// way, so the call count is the only observable difference — and a
	// handler that fell back to enumerating would still pass every other
	// test in this file.
	now := time.Now()
	runs := []fakeRun{
		makeModelRun("model-a"),
		makeSampleFakeRun("s1", "faces", "model-a", now),
		{
			experiment: "eval/glue", hash: "e1", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind":           "eval",
				"astrolabe.task_set":       "glue",
				"astrolabe.model_run_hash": "model-a",
			},
		},
	}
	for i := 0; i < 30; i++ {
		runs = append(runs, fakeRun{
			experiment:   fmt.Sprintf("unrelated-%d", i),
			hash:         fmt.Sprintf("u%d", i),
			creationTime: unixSecs(now),
			tags:         map[string]any{"astrolabe.kind": "training"},
		})
	}

	var listCalls int32
	aim := fakeAimCountingLists(t, runs, &listCalls)
	h := NewHandler(aim, nil, nil)

	for _, tc := range []struct {
		name string
		path string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"samples", "/api/runs/model-a/samples", h.HandleRunSamples},
		{"evals", "/api/runs/model-a/evals", h.HandleRunEvals},
	} {
		atomic.StoreInt32(&listCalls, 0)
		req := httptest.NewRequest("GET", tc.path, nil)
		rr := httptest.NewRecorder()
		tc.fn(rr, req)
		if rr.Code != 200 {
			t.Fatalf("%s: expected 200, got %d: %s", tc.name, rr.Code, rr.Body.String())
		}
		if got := atomic.LoadInt32(&listCalls); got != 0 {
			t.Errorf("%s: enumerated experiments %d times; it should ask", tc.name, got)
		}
	}
}

func TestSamplesArchivedBatchesAreExcluded(t *testing.T) {
	// An archived batch is one a user hid on purpose. Nothing covered
	// this until a mutation removed the check and passed.
	now := time.Now()
	archived := makeSampleFakeRun("s-archived", "faces", "model-a", now)
	archived.archived = true

	h := sampleHandler(t, []fakeRun{
		makeSampleFakeRun("s-live", "denoise", "model-a", now),
		archived,
	})
	got := callSamples(t, h, "model-a")

	for _, e := range got {
		if e.AimRunHash == "s-archived" {
			t.Fatalf("an archived batch was returned: %+v", got)
		}
	}
	if len(got) != 1 {
		t.Errorf("expected the live batch only, got %+v", got)
	}
}

// fakeLyingAim returns the given runs for ANY search, ignoring the query.
// It stands in for an Aim whose query semantics differ from ours — a
// version change, a syntax we got subtly wrong, a server-side filter that
// silently matches more than we asked.
func fakeLyingAim(t *testing.T, runs []fakeRun) *AimClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/runs/search/run/") {
			_, _ = w.Write(encodeSearchFromFakeRuns(runs))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/info/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"params":{},"traces":{"metric":[]},"props":{}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewAimClient(srv.URL)
}

func TestDiscoveryRechecksTagsWhenAimAnswersTooBroadly(t *testing.T) {
	// The query is the optimisation; the tag re-check is the correctness.
	// No fake that filters correctly can exercise it, so this one does not
	// filter at all — and the handler must still drop what it did not ask
	// for. Without the re-check, another model's batches reach the tab.
	now := time.Now()
	wrong := []fakeRun{
		makeSampleFakeRun("s-mine", "faces", "model-a", now),
		makeSampleFakeRun("s-theirs", "denoise", "model-b", now),
		{
			experiment: "x", hash: "not-a-sample", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind":           "training",
				"astrolabe.sample_set":     "faces",
				"astrolabe.model_run_hash": "model-a",
			},
		},
	}

	h := NewHandler(fakeLyingAim(t, wrong), nil, nil)
	req := httptest.NewRequest("GET", "/api/runs/model-a/samples", nil)
	rr := httptest.NewRecorder()
	h.HandleRunSamples(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []SampleManifestEntry
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AimRunHash != "s-mine" {
		t.Errorf("the handler trusted a too-broad answer: %+v", got)
	}

	// Same for evals.
	evalWrong := []fakeRun{
		{
			experiment: "eval/glue", hash: "e-mine", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind": "eval", "astrolabe.task_set": "glue",
				"astrolabe.model_run_hash": "model-a",
			},
		},
		{
			experiment: "eval/squad", hash: "e-theirs", creationTime: unixSecs(now),
			tags: map[string]any{
				"astrolabe.kind": "eval", "astrolabe.task_set": "squad",
				"astrolabe.model_run_hash": "model-b",
			},
		},
	}
	h2 := NewHandler(fakeLyingAim(t, evalWrong), nil, nil)
	req2 := httptest.NewRequest("GET", "/api/runs/model-a/evals", nil)
	rr2 := httptest.NewRecorder()
	h2.HandleRunEvals(rr2, req2)
	var gotEvals []EvalManifestEntry
	if err := json.NewDecoder(rr2.Body).Decode(&gotEvals); err != nil {
		t.Fatal(err)
	}
	if len(gotEvals) != 1 || gotEvals[0].AimRunHash != "e-mine" {
		t.Errorf("eval discovery trusted a too-broad answer: %+v", gotEvals)
	}
}
