package api

// Shared test helpers that fake the Aim REST surface. Extracted from
// cost_test.go in v1.8 when the cost handler moved off Aim; the
// remaining consumers (evals_test, experiments_handler_test) still
// need this fixture because their endpoints continue to read from
// Aim. Living in a ``_testhelpers_test.go`` file keeps them out of
// the production binary while remaining importable from every test
// file in the package.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
func fakeAimCounting(t *testing.T, runs []fakeRun, infoCalls *int32) *AimClient {
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
		case path == "/api/experiments/" || path == "/api/experiments":
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
