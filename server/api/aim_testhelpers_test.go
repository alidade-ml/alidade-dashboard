package api

// Shared test helpers that fake the Aim REST surface. Extracted from
// cost_test.go in v1.8 when the cost handler moved off Aim; the
// remaining consumers (evals_test, experiments_handler_test) still
// need this fixture because their endpoints continue to read from
// Aim. Living in a ``_testhelpers_test.go`` file keeps them out of
// the production binary while remaining importable from every test
// file in the package.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRun describes one Aim run for the fake server. Tag map keys
// should use the dotted form ("astrolabe.version") to mirror what the
// callback writes; the handler also accepts the nested form but the
// dotted form is the production layout.
type fakeRun struct {
	experiment   string // Aim experiment name
	hash         string
	name         string  // Aim run.name; defaults to the hash when empty
	creationTime float64 // unix seconds; 0 means missing
	endTime      float64 // 0 means in-flight
	tags         map[string]any
	archived     bool
}

func (fr fakeRun) displayName() string {
	if fr.name != "" {
		return fr.name
	}
	return fr.hash
}

// fakeAim spins up an httptest.Server that mimics the three Aim REST
// endpoints handlers hit: list experiments, list runs per experiment,
// get run info (for params/tags). The returned client points at the
// server; t.Cleanup tears it down.
func fakeAim(t *testing.T, runs []fakeRun) *AimClient {
	t.Helper()
	return fakeAimCounting(t, runs, nil)
}

// fakeAimCounting is fakeAim with a counter on the run-info route, so a
// test can assert how WIDE a handler's fan-out is rather than only what
// it returned. A handler that walks the whole project and one that walks
// a single experiment return the same body; the call count is the only
// observable difference.
// fakeAimCountingLists is fakeAim with a counter on the experiment- and
// run-LISTING routes, for asserting that a handler asks rather than
// enumerates.
func fakeAimCountingLists(t *testing.T, runs []fakeRun, listCalls *int32) *AimClient {
	t.Helper()
	return fakeAimFull(t, runs, nil, listCalls)
}

func fakeAimCounting(t *testing.T, runs []fakeRun, infoCalls *int32) *AimClient {
	t.Helper()
	return fakeAimFull(t, runs, infoCalls, nil)
}

func fakeAimFull(t *testing.T, runs []fakeRun, infoCalls, listCalls *int32) *AimClient {
	t.Helper()

	// Bucket runs by experiment, assigning stable IDs.
	byExp := map[string][]fakeRun{}
	for _, r := range runs {
		byExp[r.experiment] = append(byExp[r.experiment], r)
	}
	expIDs := map[string]string{}
	i := 0
	for name := range byExp {
		expIDs[name] = fmt.Sprintf("exp-%d", i)
		i++
	}
	idToName := map[string]string{}
	for name, id := range expIDs {
		idToName[id] = name
	}

	// One dispatcher handles all three routes. Using net/http's default
	// pattern matcher here is fiddly (overlapping /api/experiments/
	// prefixes), so we just inspect the path ourselves.
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/runs/search/run/"):
			q := r.URL.Query().Get("q")
			var matched []fakeRun
			for _, list := range byExp {
				for _, fr := range list {
					if matchesAimQuery(fr, q) {
						matched = append(matched, fr)
					}
				}
			}
			_, _ = w.Write(encodeSearchFromFakeRuns(matched))

		case path == "/api/experiments/" || path == "/api/experiments":
			if listCalls != nil {
				atomic.AddInt32(listCalls, 1)
			}
			out := make([]Experiment, 0, len(expIDs))
			for name, id := range expIDs {
				out = append(out, Experiment{
					ID:       id,
					Name:     name,
					RunCount: len(byExp[name]),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)

		case strings.HasPrefix(path, "/api/experiments/") && strings.HasSuffix(path, "/runs/"):
			if listCalls != nil {
				atomic.AddInt32(listCalls, 1)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/experiments/"), "/runs/")
			name, ok := idToName[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			runs := byExp[name]
			out := ExperimentRuns{ID: id}
			for _, fr := range runs {
				out.Runs = append(out.Runs, AimRun{
					RunID:        fr.hash,
					Name:         fr.displayName(),
					CreationTime: fr.creationTime,
					EndTime:      fr.endTime,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)

		case strings.HasPrefix(path, "/api/runs/"):
			rest := strings.TrimPrefix(path, "/api/runs/")
			parts := strings.Split(rest, "/")
			if len(parts) < 2 || parts[1] != "info" {
				http.NotFound(w, r)
				return
			}
			hash := parts[0]
			if infoCalls != nil {
				atomic.AddInt32(infoCalls, 1)
			}
			for _, list := range byExp {
				for _, fr := range list {
					if fr.hash == hash {
						w.Header().Set("Content-Type", "application/json")
						// Props matters for runs looked up by hash from
						// outside their own experiment — that is the only
						// place the requester does not already know the
						// name or the owning experiment.
						_ = json.NewEncoder(w).Encode(RunInfo{
							Params: fr.tags,
							Props: RunProps{
								Name:         fr.displayName(),
								Experiment:   RunExperiment{ID: expIDs[fr.experiment], Name: fr.experiment},
								CreationTime: fr.creationTime,
								EndTime:      fr.endTime,
							},
						})
						return
					}
				}
			}
			http.NotFound(w, r)

		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(dispatch)
	t.Cleanup(srv.Close)
	return NewAimClient(srv.URL)
}

// makeHandlerWithAim wires a handler with the given AimClient and no
// state DB. Used by tests that exercise pure-Aim endpoints (evals,
// experiments listing with no state).
func makeHandlerWithAim(t *testing.T, aim *AimClient) *Handler {
	t.Helper()
	return NewHandler(aim, nil, nil)
}

// unixSecs returns the Unix-seconds float representation of a time —
// matches Aim's serialization.
func unixSecs(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// --- a minimal Aim-format encoder, for building fake search responses ---

func encFrame(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(b)))
	copy(out[4:], b)
	return out
}

func encPath(segs ...any) []byte {
	var out []byte
	for _, s := range segs {
		switch v := s.(type) {
		case string:
			out = append(out, []byte(v)...)
			out = append(out, pathSentinel)
		case int:
			out = append(out, encPathInt(uint64(v))...)
		case int64:
			out = append(out, encPathInt(uint64(v))...)
		}
	}
	return out
}

// encPathInt renders an integer path segment. Big-endian, unlike the frame
// lengths, so keys sort correctly in RocksDB.
func encPathInt(v uint64) []byte {
	out := []byte{pathSentinel}
	n := make([]byte, 8)
	binary.BigEndian.PutUint64(n, v)
	out = append(out, n...)
	return append(out, pathSentinel)
}

func encVal(v any) []byte {
	switch x := v.(type) {
	case int:
		return encValInt(int64(x))
	case int64:
		return encValInt(x)
	case string:
		return append([]byte{tagString}, []byte(x)...)
	case bool:
		b := byte(0)
		if x {
			b = 1
		}
		return []byte{tagBool, b}
	case []byte:
		return append([]byte{tagBytes}, x...)
	case float64:
		out := make([]byte, 9)
		out[0] = tagFloat
		binary.LittleEndian.PutUint64(out[1:], math.Float64bits(x))
		return out
	}
	return []byte{tagNone}
}

func encValInt(x int64) []byte {
	out := make([]byte, 9)
	out[0] = tagInt
	binary.LittleEndian.PutUint64(out[1:], uint64(x))
	return out
}

// --- serving /api/runs/search/run/ from the same fakeRun fixtures ---
//
// So the handlers that switched from enumerating to querying are exercised by
// the SAME tests that pinned their behaviour before. If an existing assertion
// needed editing to pass, either behaviour moved or the test was over-fitted to
// the old implementation.

// aimQueryTerm is one `run['tag'] == 'value'` equality.
var aimQueryTerm = regexp.MustCompile(`run\['([^']+)'\] == '((?:[^'\\]|\\.)*)'`)

// matchesAimQuery reports whether a run satisfies every equality in q.
// Only the subset the hub builds is understood: tag equalities joined by
// `and`, plus run.experiment and run.name.
// aimNotInTerm matches `run['<tag>'] not in ['a','b']`.
var aimNotInTerm = regexp.MustCompile(`run\['([^']*)'\] not in \[([^\]]*)\]`)

// aimArchivedTerm matches `run.archived == True|False`.
var aimArchivedTerm = regexp.MustCompile(`run\.archived == (True|False)`)

func matchesAimQuery(fr fakeRun, q string) bool {
	// Archived runs are hidden unless the query asks for them. This mirrors
	// live Aim, where a plain `run.experiment == X` returns 5 of 6 runs and
	// omits the archived one, and `run.archived == True` returns exactly it.
	// Measured, not assumed — and asserted against a real server in
	// TestAimContractSearchStillHidesArchivedRuns, which is what keeps this
	// fake from quietly disagreeing with the thing it stands in for.
	wantArchived := false
	if m := aimArchivedTerm.FindStringSubmatch(q); m != nil {
		wantArchived = m[1] == "True"
	}
	if fr.archived != wantArchived {
		return false
	}

	matched := false
	for _, m := range aimNotInTerm.FindAllStringSubmatch(q, -1) {
		matched = true
		tag := m[1]
		have := tagValue(fr, tag)
		for _, lit := range strings.Split(m[2], ",") {
			lit = strings.Trim(strings.TrimSpace(lit), "'")
			// A run with no value for the tag is NOT in any list, so it
			// matches — the behaviour that keeps untagged legacy runs in
			// the count, and the same behaviour that makes this query
			// match everything on an unindexed repo.
			if have != nil && fmt.Sprint(have) == lit {
				return false
			}
		}
	}
	if aimArchivedTerm.MatchString(q) {
		matched = true
	}
	for _, m := range aimQueryTerm.FindAllStringSubmatch(q, -1) {
		matched = true
		tag, want := m[1], strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(m[2])
		if fmt.Sprint(tagValue(fr, tag)) != want {
			return false
		}
	}
	if m := regexp.MustCompile(`run\.experiment == '([^']*)'`).FindStringSubmatch(q); m != nil {
		matched = true
		if fr.experiment != m[1] {
			return false
		}
	}
	if m := regexp.MustCompile(`run\.name == '([^']*)'`).FindStringSubmatch(q); m != nil {
		matched = true
		if fr.displayName() != m[1] {
			return false
		}
	}
	return matched
}

// tagValue reads a tag from a fakeRun's params, honouring both the flat
// and nested layouts Aim uses — the same two shapes
// AstrolabeTagsFromParams handles.
func tagValue(fr fakeRun, tag string) any {
	if v, ok := fr.tags[tag]; ok {
		return v
	}
	if nested, ok := fr.tags["astrolabe"].(map[string]any); ok {
		if v, ok := nested[strings.TrimPrefix(tag, "astrolabe.")]; ok {
			return v
		}
	}
	return nil
}

// encodeSearchFromFakeRuns renders matching runs in Aim's wire format,
// including their params so tag-reading handlers see what they expect.
func encodeSearchFromFakeRuns(runs []fakeRun) []byte {
	var out []byte
	add := func(path, val []byte) {
		out = append(out, encFrame(path)...)
		out = append(out, encFrame(val)...)
	}
	for _, fr := range runs {
		out = append(out, encFrame(encPath(fr.hash, "props", "name"))...)
		out = append(out, encFrame(encVal(fr.displayName()))...)
		add(encPath(fr.hash, "props", "archived"), encVal(fr.archived))
		add(encPath(fr.hash, "props", "creation_time"), encVal(fr.creationTime))
		// Verified present in a live search response. Omitting it here made
		// any test asserting on a searched run's end time or active flag
		// measure this fixture's gap rather than the code.
		add(encPath(fr.hash, "props", "end_time"), encVal(fr.endTime))
		add(encPath(fr.hash, "props", "experiment", "name"), encVal(fr.experiment))
		for k, v := range fr.tags {
			if s, ok := v.(string); ok {
				add(encPath(fr.hash, "params", k), encVal(s))
			}
			// The nested layout: astrolabe -> {kind: ..., ...}
			if nested, ok := v.(map[string]any); ok {
				for nk, nv := range nested {
					if s, ok := nv.(string); ok {
						add(encPath(fr.hash, "params", k+"."+nk), encVal(s))
					}
				}
			}
		}
	}
	// A streaming marker, so every search in every test exercises the
	// filter that drops it.
	add(encPath("progress_0", "x"), encVal("ignored"))
	return out
}
