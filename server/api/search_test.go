package api

// Tests for SearchRuns — asking Aim which runs match, instead of
// enumerating every run and filtering here.
//
// Contract from Aim's search endpoint as measured against a live server,
// not from this implementation:
//
//   * The response is the same encoded tree as the object routes.
//   * It interleaves top-level "progress_N" keys as streaming
//     bookkeeping. Those are not runs.
//   * A query matching nothing returns a body with no runs in it, NOT
//     every run — verified against a live server before this was built.
//   * Each run carries props (name, archived, creation_time, and
//     experiment one level deeper) and params (the astrolabe.* tags).
//
// The fixtures are real bodies captured off a live `aim up` serving 15
// runs across two experiments, three of them tagged kind=sample.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

// --- the response shape ---

func TestSearchDropsProgressKeys(t *testing.T) {
	// The captured body holds 3 runs and 4 progress_N markers. Without
	// the filter this returns 7, and the phantoms carry a plausible-
	// looking hash.
	runs, err := ParseSearchedRuns(searchFixture(t, "search_run_by_kind.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3: %+v", len(runs), runs)
	}
	for _, r := range runs {
		if strings.HasPrefix(r.Hash, "progress_") {
			t.Errorf("a streaming marker was returned as a run: %q", r.Hash)
		}
	}
}

func TestSearchEmptyResultIsEmptyNotEverything(t *testing.T) {
	// The failure that would make this whole approach worse than the walk
	// it replaces. Asserted rather than assumed: the fixture is a real
	// response to a query matching nothing.
	runs, err := ParseSearchedRuns(searchFixture(t, "search_run_empty.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("a no-match query returned %d runs: %+v", len(runs), runs)
	}
}

func TestSearchCarriesPropsAndParams(t *testing.T) {
	runs, err := ParseSearchedRuns(searchFixture(t, "search_run_by_kind.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.Hash == "" || len(r.Hash) < 16 {
			t.Errorf("run has no usable hash: %q", r.Hash)
		}
		if r.Name == "" {
			t.Errorf("run %s has no name", r.Hash)
		}
		if r.CreationTime == 0 {
			t.Errorf("run %s has no creation_time; dedupe-by-newest needs it", r.Hash)
		}
		if r.Params[TagKind] != "sample" {
			t.Errorf("run %s params did not carry the kind tag: %v", r.Hash, r.Params)
		}
	}
}

func TestSearchReadsExperimentOneLevelDeeper(t *testing.T) {
	// props/experiment/name sits a level below every other prop. A walker
	// that assumes a flat props map silently leaves this empty, and the
	// fixture spans two experiments so a single-experiment fixture could
	// not catch it.
	runs, err := ParseSearchedRuns(searchFixture(t, "search_run_by_kind.bin"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range runs {
		if r.ExperimentName == "" {
			t.Errorf("run %s has no experiment name", r.Hash)
		}
		seen[r.ExperimentName] = true
	}
	if len(seen) < 2 {
		t.Fatalf("fixture spans %d experiments; the cross-experiment case is not "+
			"being tested", len(seen))
	}
}

func TestSearchTruncatedBodyErrors(t *testing.T) {
	body := searchFixture(t, "search_run_by_kind.bin")
	if _, err := ParseSearchedRuns(body[:len(body)/2]); err == nil {
		t.Fatal("a truncated body decoded without error")
	}
}

// --- query construction ---

func TestQueryEscapesValues(t *testing.T) {
	// The expression is evaluated server-side by RestrictedPython, and
	// experiment names are user-supplied. An unescaped quote either
	// breaks the query or changes what it asks.
	got := QueryByExperiment("it's a name")
	if strings.Contains(got, "'it's") {
		t.Fatalf("quote was interpolated raw: %s", got)
	}
	if !strings.Contains(got, `it\'s`) {
		t.Errorf("quote was not escaped: %s", got)
	}

	got = QueryByTag("astrolabe.kind", `back\slash`)
	if !strings.Contains(got, `back\\slash`) {
		t.Errorf("backslash was not escaped: %s", got)
	}
}

func TestQueryByTagsIsDeterministic(t *testing.T) {
	// Built from a map, so without an explicit sort two identical
	// requests produce different strings.
	pairs := map[string]string{
		"astrolabe.kind":           "sample",
		"astrolabe.model_run_hash": "abc",
		"astrolabe.sample_set":     "faces",
	}
	first := QueryByTags(pairs)
	for i := 0; i < 20; i++ {
		if got := QueryByTags(pairs); got != first {
			t.Fatalf("query is not deterministic:\n %s\n %s", first, got)
		}
	}
	if !strings.Contains(first, " and ") {
		t.Errorf("terms were not joined: %s", first)
	}
}

// --- the HTTP layer ---

func TestSearchSurfacesAQuerySyntaxError(t *testing.T) {
	// A 400 is a syntax error in an expression this package built. An
	// empty result would read as "no such runs" and hide the bug.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid query", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := NewAimClient(srv.URL).SearchRuns("run[bad")
	if err == nil {
		t.Fatal("a 400 produced no error")
	}
	if !strings.Contains(err.Error(), "run[bad") {
		t.Errorf("the error does not name the query that failed: %v", err)
	}
}

func TestSearchUnreachableAimErrors(t *testing.T) {
	if _, err := NewAimClient("http://127.0.0.1:1").SearchRuns("run.experiment == 'x'"); err == nil {
		t.Fatal("an unreachable Aim produced no error")
	}
}

func TestSearchSendsTheQueryUrlEncoded(t *testing.T) {
	// The query contains quotes, spaces and brackets. If it reaches Aim
	// mangled, the server answers a different question than the one asked.
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = w.Write(searchFixture(t, "search_run_empty.bin"))
	}))
	defer srv.Close()

	want := QueryByTag("astrolabe.kind", "sample")
	if _, err := NewAimClient(srv.URL).SearchRuns(want); err != nil {
		t.Fatal(err)
	}
	if gotQuery != want {
		t.Errorf("query arrived as %q, sent %q", gotQuery, want)
	}
}

// --- happy path ---

func TestSearchRunsEndToEndOverAFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(searchFixture(t, "search_run_by_kind.bin"))
	}))
	defer srv.Close()

	runs, err := NewAimClient(srv.URL).SearchRuns(QueryByTag("astrolabe.kind", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	// Serialisable: the handlers hand these onward.
	if _, err := json.Marshal(runs); err != nil {
		t.Errorf("SearchedRun does not marshal: %v", err)
	}
}
