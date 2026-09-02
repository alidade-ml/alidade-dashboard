package api

// Tests for include resolution via Aim's search endpoint.
//
// These are the eight behaviours the old index-based resolver was
// tested for, ported to the query path. The behaviours are the contract
// and none of them changes here — only what the resolver asks.
//
// The fake server below ENCODES Aim's wire format rather than replaying
// a captured body, because each test needs its own runs.
//
// The usual objection to that — an encoder and a decoder can agree with
// each other and both be wrong — does not bite here, because the decoder
// is independently pinned by REAL captured responses in search_test.go.
// This encoder only feeds that same decoder, so a mistake in it shows up
// as these tests failing, not as them passing wrongly. What it cannot
// prove is anything about the format itself; search_test.go does that.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeRunRow struct {
	hash         string
	name         string
	experiment   string
	creationTime float64
	archived     bool
	// Empty means the run carries no alidade.version tag, which is what
	// a repo predating version tagging looks like.
	version string
}

func encodeSearchBody(rows []fakeRunRow) []byte {
	var out []byte
	add := func(path []byte, val []byte) {
		out = append(out, encFrame(path)...)
		out = append(out, encFrame(val)...)
	}
	for _, r := range rows {
		add(encPath(r.hash, "props", "name"), encVal(r.name))
		add(encPath(r.hash, "props", "archived"), encVal(r.archived))
		add(encPath(r.hash, "props", "creation_time"), encVal(r.creationTime))
		add(encPath(r.hash, "props", "experiment", "name"), encVal(r.experiment))
		if r.version != "" {
			add(encPath(r.hash, "params", "alidade.version"), encVal(r.version))
		}
	}
	// A streaming marker, so every test exercises the filter that drops it.
	add(encPath("progress_0", "x"), encVal("ignored"))
	return out
}

// --- the fake server ---

// fakeSearchAim answers search queries by filtering `rows` the way Aim
// would, and answers run-info lookups by hash. searches counts search
// calls; infos counts info calls.
func fakeSearchAim(t *testing.T, rows []fakeRunRow, searches, infos *int32) *AimClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/runs/search/run/"):
			if searches != nil {
				atomic.AddInt32(searches, 1)
			}
			q, _ := url.QueryUnescape(r.URL.Query().Get("q"))
			var matched []fakeRunRow
			for _, row := range rows {
				if q == QueryByExperiment(row.experiment) || q == QueryByRunName(row.name) {
					matched = append(matched, row)
				}
			}
			_, _ = w.Write(encodeSearchBody(matched))
		case strings.HasSuffix(r.URL.Path, "/info/"):
			if infos != nil {
				atomic.AddInt32(infos, 1)
			}
			hash := extractPathParam(r.URL.Path, "/api/runs/", "/info/")
			for _, row := range rows {
				if row.hash == hash {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"params":{},"traces":{"metric":[]},"props":{"name":"` +
						row.name + `","experiment":{"id":"e","name":"` + row.experiment + `"}}}`))
					return
				}
			}
			http.NotFound(w, r)
		case r.URL.Path == "/api/experiments/" || strings.HasSuffix(r.URL.Path, "/runs/"):
			// Present so a resolver that falls back to enumerating gets a
			// usable answer rather than an error — otherwise the
			// no-enumeration test would pass for the wrong reason.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return NewAimClient(srv.URL)
}

func resolverFor(t *testing.T, rows []fakeRunRow) *Handler {
	t.Helper()
	return NewHandler(fakeSearchAim(t, rows, nil, nil), nil, nil)
}

var includeFixture = []fakeRunRow{
	{hash: "a1b2c3d4e5f60708090a0b0c", name: "bert-tiny", experiment: "exp-A", creationTime: 100},
	{hash: "b1b2c3d4e5f60708090a0b0c", name: "latent-bert", experiment: "exp-A", creationTime: 200},
	{hash: "c1b2c3d4e5f60708090a0b0c", name: "shared-name", experiment: "exp-A", creationTime: 300},
	{hash: "d1b2c3d4e5f60708090a0b0c", name: "shared-name", experiment: "exp-B", creationTime: 400},
}

// --- the eight behaviours, unchanged ---

func TestQueryResolve_Hash(t *testing.T) {
	got := resolverFor(t, includeFixture).resolveIncludeByQuery("a1b2c3d4e5f60708090a0b0c")
	if got.Type != "hash" {
		t.Fatalf("type = %q, want hash", got.Type)
	}
	if len(got.Runs) != 1 || got.Runs[0] != "a1b2c3d4e5f60708090a0b0c" {
		t.Errorf("runs = %v", got.Runs)
	}
	if got.Name != "bert-tiny" {
		t.Errorf("name = %q, want the run's name so the chip is readable", got.Name)
	}
}

func TestQueryResolve_ExperimentName(t *testing.T) {
	got := resolverFor(t, includeFixture).resolveIncludeByQuery("exp-A")
	if got.Type != "experiment" {
		t.Fatalf("type = %q, want experiment", got.Type)
	}
	if len(got.Runs) != 3 {
		t.Errorf("got %d runs, want all 3 in exp-A: %v", len(got.Runs), got.Runs)
	}
}

func TestQueryResolve_RunNamePicksLatestAcrossExperiments(t *testing.T) {
	// "shared-name" exists in exp-A (t=300) and exp-B (t=400). The newest
	// wins, and it is the one from the OTHER experiment — so a resolver
	// that stopped at the first match would pick wrong.
	got := resolverFor(t, includeFixture).resolveIncludeByQuery("shared-name")
	if got.Type != "run-name" {
		t.Fatalf("type = %q, want run-name", got.Type)
	}
	if len(got.Runs) != 1 {
		t.Fatalf("got %d runs, want exactly the newest: %v", len(got.Runs), got.Runs)
	}
	if got.Runs[0] != "d1b2c3d4e5f60708090a0b0c" {
		t.Errorf("resolved to %q, want the newer exp-B run", got.Runs[0])
	}
}

func TestQueryResolve_ExperimentBeforeRunName(t *testing.T) {
	// A string that is both an experiment name and a run name must
	// resolve as the experiment — the documented order.
	rows := append([]fakeRunRow{}, includeFixture...)
	rows = append(rows, fakeRunRow{
		hash: "e1b2c3d4e5f60708090a0b0c", name: "exp-A", experiment: "exp-Z", creationTime: 900,
	})
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-A")
	if got.Type != "experiment" {
		t.Fatalf("type = %q, want experiment to win over run-name", got.Type)
	}
}

func TestQueryResolve_UnknownString(t *testing.T) {
	got := resolverFor(t, includeFixture).resolveIncludeByQuery("does-not-exist")
	if got.Type != "unknown" {
		t.Fatalf("type = %q, want unknown", got.Type)
	}
	if got.Runs == nil {
		t.Error("Runs must be non-nil so the JSON is [] and the chip renders struck out")
	}
	if len(got.Runs) != 0 {
		t.Errorf("runs = %v, want empty", got.Runs)
	}
}

func TestQueryResolve_UnknownHashShape(t *testing.T) {
	// Hash-shaped but Aim does not have it: falls through to the other
	// shapes and ends unknown, rather than erroring.
	got := resolverFor(t, includeFixture).resolveIncludeByQuery("ffffffffffffffffffffffff")
	if got.Type != "unknown" {
		t.Fatalf("type = %q, want unknown", got.Type)
	}
}

func TestQueryResolve_ArchivedRunsAreExcluded(t *testing.T) {
	rows := []fakeRunRow{
		{hash: "a1b2c3d4e5f60708090a0b0c", name: "live", experiment: "exp-C", creationTime: 100},
		{hash: "b1b2c3d4e5f60708090a0b0c", name: "gone", experiment: "exp-C", creationTime: 200, archived: true},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-C")
	if len(got.Runs) != 1 || got.Runs[0] != "a1b2c3d4e5f60708090a0b0c" {
		t.Errorf("archived run was not excluded: %v", got.Runs)
	}
}

func TestQueryResolve_UnreachableAimIsUnknownNotAnError(t *testing.T) {
	// One unresolvable include must not fail the endpoint and hide the
	// others. It renders as a struck-out chip, which is what the frontend
	// already does for an unknown.
	h := NewHandler(NewAimClient("http://127.0.0.1:1"), nil, nil)
	got := h.resolveIncludeByQuery("exp-A")
	if got.Type != "unknown" {
		t.Fatalf("type = %q, want unknown", got.Type)
	}
}

// --- the cost claim ---

func TestQueryResolve_DoesNotEnumerateTheProject(t *testing.T) {
	// The whole point of the slice, and the body is identical either way
	// — so the call count is the only observable difference. Counts
	// search calls (expected) against experiment-listing calls (must be
	// zero).
	var searches, infos int32
	aim := fakeSearchAim(t, includeFixture, &searches, &infos)

	// Wrap the transport to notice any /api/experiments/ traffic.
	var enumerations int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/experiments") {
			atomic.AddInt32(&enumerations, 1)
		}
		http.Redirect(w, r, aim.baseURL+r.URL.String(), http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	h := NewHandler(NewAimClient(srv.URL), nil, nil)
	h.resolveIncludeByQuery("exp-A")

	if got := atomic.LoadInt32(&enumerations); got != 0 {
		t.Fatalf("resolver hit /api/experiments %d times; it should ask, not enumerate", got)
	}
}

// TestIsHashLike survives the deletion of include_resolver_test.go.
// isHashLike is still the first branch of the resolver, and the >=16
// floor is what stops a short hex-shaped experiment name (say "beef")
// being treated as a run hash.
func TestIsHashLike(t *testing.T) {
	for _, s := range []string{
		"a1b2c3d4e5f60708090a0b0c", // 24, a real Aim hash
		"0123456789abcdef",         // 16, the floor
	} {
		if !isHashLike(s) {
			t.Errorf("%q should look like a hash", s)
		}
	}
	for _, s := range []string{
		"",
		"beef",                     // hex, but far too short to be a hash
		"0123456789abcde",          // 15, one below the floor
		"exp-A",                    // not hex
		"a1b2c3d4e5f60708090a0b0z", // right length, not hex
		"my-experiment-name",
	} {
		if isHashLike(s) {
			t.Errorf("%q should not be treated as a hash", s)
		}
	}
}

func TestQueryResolve_HashShapedExperimentNameFallsThrough(t *testing.T) {
	// A hash-shaped string that Aim has no run for, but which IS an
	// experiment name. Contrived — it needs an experiment named in 24 hex
	// characters — but it is the only thing that makes the fall-through
	// after the hash branch reachable.
	//
	// Without this test, deleting that fall-through changes nothing that
	// any test observes, because every other hash-shaped miss ends at
	// "unknown" by either route.
	const hexName = "a1b2c3d4e5f60708090a0b0c"
	rows := []fakeRunRow{
		// The experiment is named in hex; the RUN inside it is not, so
		// the hash branch finds no run with that hash.
		{hash: "999999999999999999999999", name: "inner", experiment: hexName, creationTime: 10},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery(hexName)
	if got.Type != "experiment" {
		t.Fatalf("type = %q, want experiment — the hash branch missed and should "+
			"have fallen through", got.Type)
	}
	if len(got.Runs) != 1 || got.Runs[0] != "999999999999999999999999" {
		t.Errorf("runs = %v", got.Runs)
	}
}

// --- include-by-name resolves to the newest version only ---
//
// intended-behavior.md section 2 says an include resolves "against the most
// recent submit of that experiment". The resolver returned every run of
// every version instead, so naming one experiment pulled in every run it
// had ever produced — for a two-version experiment with evals and samples
// that was eight runs, six of them evidence rather than models.

func versionedFixture() []fakeRunRow {
	return []fakeRunRow{
		{hash: "v1-train", name: "r", experiment: "exp-V", creationTime: 100, version: "v1"},
		{hash: "v1-eval", name: "r", experiment: "exp-V", creationTime: 110, version: "v1"},
		{hash: "v2-train", name: "r", experiment: "exp-V", creationTime: 200, version: "v2"},
		{hash: "v10-train", name: "r", experiment: "exp-V", creationTime: 300, version: "v10"},
		{hash: "v10-eval", name: "r", experiment: "exp-V", creationTime: 310, version: "v10"},
	}
}

func TestQueryResolve_ExperimentReturnsNewestVersionOnly(t *testing.T) {
	got := resolverFor(t, versionedFixture()).resolveIncludeByQuery("exp-V")
	if got.Type != "experiment" {
		t.Fatalf("type = %q, want experiment", got.Type)
	}
	want := map[string]bool{"v10-train": true, "v10-eval": true}
	if len(got.Runs) != len(want) {
		t.Fatalf("got %d runs, want %d (newest version only): %v",
			len(got.Runs), len(want), got.Runs)
	}
	for _, h := range got.Runs {
		if !want[h] {
			t.Errorf("run %q is from an older version and should not resolve", h)
		}
	}
}

// v10 must beat v9. A string comparison gets this backwards, and only on
// experiments resubmitted enough times for it to matter — which is exactly
// where a wrong comparison set is most expensive.
func TestQueryResolve_VersionsCompareNumericallyNotLexically(t *testing.T) {
	rows := []fakeRunRow{
		{hash: "nine", name: "r", experiment: "exp-N", creationTime: 100, version: "v9"},
		{hash: "ten", name: "r", experiment: "exp-N", creationTime: 200, version: "v10"},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-N")
	if len(got.Runs) != 1 || got.Runs[0] != "ten" {
		t.Fatalf("runs = %v, want just v10's run", got.Runs)
	}
}

// A repo predating version tagging must still resolve to something. Every
// run untagged means there is no newest version to pick, so all of them
// stay — the pre-existing behaviour, now stated rather than incidental.
func TestQueryResolve_UntaggedExperimentReturnsEveryRun(t *testing.T) {
	rows := []fakeRunRow{
		{hash: "a", name: "r", experiment: "exp-U", creationTime: 100},
		{hash: "b", name: "r", experiment: "exp-U", creationTime: 200},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-U")
	if len(got.Runs) != 2 {
		t.Fatalf("got %d runs, want both: %v", len(got.Runs), got.Runs)
	}
}

// An untagged straggler beside tagged runs must not outrank them. Keeping
// it would put a run of unknown vintage in a comparison the reader thinks
// is one version.
func TestQueryResolve_UntaggedRunDoesNotSurviveBesideTaggedOnes(t *testing.T) {
	rows := []fakeRunRow{
		{hash: "legacy", name: "r", experiment: "exp-M", creationTime: 100},
		{hash: "v3", name: "r", experiment: "exp-M", creationTime: 200, version: "v3"},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-M")
	if len(got.Runs) != 1 || got.Runs[0] != "v3" {
		t.Fatalf("runs = %v, want only the tagged v3 run", got.Runs)
	}
}

// Archived runs are excluded before the newest version is chosen — an
// archived v4 must not hide a live v3.
func TestQueryResolve_ArchivedRunsDoNotDecideTheVersion(t *testing.T) {
	rows := []fakeRunRow{
		{hash: "live-v3", name: "r", experiment: "exp-A2", creationTime: 100, version: "v3"},
		{hash: "gone-v4", name: "r", experiment: "exp-A2", creationTime: 200, version: "v4", archived: true},
	}
	got := resolverFor(t, rows).resolveIncludeByQuery("exp-A2")
	if len(got.Runs) != 1 || got.Runs[0] != "live-v3" {
		t.Fatalf("runs = %v, want the live v3 run", got.Runs)
	}
}
