package api

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wall_time is the x-axis for every other series on a run.
//
// It used to be fetched as a second metric and matched on step. That
// cannot work: an index has to be denser than the thing it indexes, and
// Aim caps every series at 200 points server-side. Two series sampled to
// 200 from different raw lengths land on different steps, and a sparse
// metric — a validation curve of six points, returned whole — may share
// none of the wall_time grid at all.
//
// It is now asked of Aim's align endpoint, which looks each plotted step
// up against wall_time's FULL series. One x value per point, by
// construction.
//
// What survives from the old contract, and is still pinned below: steps
// and values must keep every point, and a null must stay distinguishable
// from a measured 0. Filling an unknown with 0 drew the run's last loss
// at the origin.

// fakeAlignAim serves get-batch for the requested metric and the align
// route for its x-axis, mimicking Aim including the behaviour that makes
// this delicate: collect_x_axis_data filters with `if x_val:`, so an
// x value of exactly 0.0 is DROPPED from the response.
func fakeAlignAim(t *testing.T, metrics map[string]MetricData) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/metric/get-batch/"):
			body, _ := io.ReadAll(r.Body)
			var req []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body, &req); err != nil || len(req) == 0 {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			out := []MetricData{}
			if md, ok := metrics[req[0].Name]; ok {
				md.Name = req[0].Name
				out = append(out, md)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)

		case strings.HasSuffix(r.URL.Path, "/search/metric/align/"):
			body, _ := io.ReadAll(r.Body)
			var req struct {
				AlignBy string `json:"align_by"`
				Runs    []struct {
					RunID  string `json:"run_id"`
					Traces []struct {
						Name string `json:"name"`
					} `json:"traces"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(body, &req); err != nil ||
				len(req.Runs) == 0 || len(req.Runs[0].Traces) == 0 {
				http.Error(w, "bad align request", http.StatusBadRequest)
				return
			}
			metric, okM := metrics[req.Runs[0].Traces[0].Name]
			axis, okA := metrics[req.AlignBy]
			if !okM || !okA {
				_, _ = w.Write(nil)
				return
			}

			byStep := map[int]float64{}
			for i, s := range axis.Iters {
				byStep[s] = axis.Values[i]
			}
			var xs []float64
			for _, s := range metric.Iters {
				v, ok := byStep[s]
				if !ok || v == 0 { // Aim's falsy filter, reproduced
					continue
				}
				xs = append(xs, v)
			}
			_, _ = w.Write(encodeAlignResponse(req.Runs[0].RunID, xs))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func encodeAlignResponse(runHash string, xs []float64) []byte {
	blob := make([]byte, 8*len(xs))
	for i, v := range xs {
		binary.LittleEndian.PutUint64(blob[i*8:], math.Float64bits(v))
	}
	add := func(out []byte, path []byte, val []byte) []byte {
		return append(append(out, encFrame(path)...), encFrame(val)...)
	}
	var out []byte
	out = add(out, encPath(runHash, 0, "name"), encVal("train/loss"))
	out = add(out, encPath(runHash, 0, "x_axis_values", "dtype"), encVal("float64"))
	out = add(out, encPath(runHash, 0, "x_axis_values", "shape"), encVal(len(xs)))
	out = add(out, encPath(runHash, 0, "x_axis_values", "blob"), encVal(blob))
	return out
}

type rawMetricResponse struct {
	Steps     []int      `json:"steps"`
	Values    []float64  `json:"values"`
	WallTimes []*float64 `json:"wall_times"`
	raw       map[string]json.RawMessage
}

func getMetric(t *testing.T, metrics map[string]MetricData, hash, metricName string) rawMetricResponse {
	t.Helper()
	srv := fakeAlignAim(t, metrics)
	h := NewHandler(NewAimClient(srv.URL), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+hash+"/metrics/"+metricName, nil)
	rec := httptest.NewRecorder()
	h.HandleMetricData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out rawMetricResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding response: %v (body %s)", err, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out.raw); err != nil {
		t.Fatalf("decoding response as object: %v", err)
	}
	return out
}

func TestMetricWallTimes(t *testing.T) {
	// The case exact-step matching could not serve at all: a sparse
	// metric whose steps are absent from a downsampled wall_time grid.
	// Aim looks them up against the full series, so every point pairs.
	t.Run("a sparse metric gets a value at every point", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 50, 250}, Values: []float64{3.4, 1.2, 0.17}},
			"wall_time":  {Iters: []int{0, 50, 250}, Values: []float64{0, 75, 375}},
		}, "abc123", "train/loss")

		if len(out.WallTimes) != 3 {
			t.Fatalf("wall_times length = %d, want 3", len(out.WallTimes))
		}
		for i, v := range out.WallTimes {
			if v == nil {
				t.Fatalf("wall_times[%d] is null; every plotted point must pair", i)
			}
		}
		if *out.WallTimes[2] != 375 {
			t.Errorf("wall_times[2] = %v, want 375", *out.WallTimes[2])
		}
	})

	// Aim drops x values of exactly 0.0. wall_time is elapsed seconds and
	// reads a literal 0.0 until the first training batch anchors it, so
	// the dropped values are a leading run of zeros — and their value is
	// therefore known, not guessed.
	t.Run("a dropped leading zero is restored as zero", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
			"wall_time":  {Iters: []int{0, 1}, Values: []float64{0, 10}},
		}, "abc123", "train/loss")

		if len(out.WallTimes) != 2 {
			t.Fatalf("wall_times length = %d, want 2", len(out.WallTimes))
		}
		if out.WallTimes[0] == nil {
			t.Fatal("wall_times[0] = null for a measured 0.0, want 0")
		}
		if *out.WallTimes[0] != 0 {
			t.Errorf("wall_times[0] = %v, want 0", *out.WallTimes[0])
		}
		if *out.WallTimes[1] != 10 {
			t.Errorf("wall_times[1] = %v, want 10", *out.WallTimes[1])
		}
	})

	t.Run("several leading zeros are all restored", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1, 2, 3}, Values: []float64{3, 2, 1, 0.5}},
			"wall_time":  {Iters: []int{0, 1, 2, 3}, Values: []float64{0, 0, 5, 10}},
		}, "abc123", "train/loss")

		if len(out.WallTimes) != 4 {
			t.Fatalf("wall_times length = %d, want 4", len(out.WallTimes))
		}
		for i, want := range []float64{0, 0, 5, 10} {
			if out.WallTimes[i] == nil || *out.WallTimes[i] != want {
				t.Errorf("wall_times[%d] = %v, want %v", i, out.WallTimes[i], want)
			}
		}
	})

	// steps and values feed the step-axis chart and must survive whatever
	// happens to the wall-clock axis.
	t.Run("steps and values keep every point", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1, 2}, Values: []float64{3.4, 1.2, 0.17}},
			"wall_time":  {Iters: []int{0, 1, 2}, Values: []float64{0, 5, 10}},
		}, "abc123", "train/loss")

		if len(out.Steps) != 3 || len(out.Values) != 3 {
			t.Fatalf("steps=%d values=%d, want 3 each", len(out.Steps), len(out.Values))
		}
	})

	// No wall_time series at all: the axis is absent, and the chart falls
	// back to steps. Omitted rather than filled.
	t.Run("no wall_time series omits the axis", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
		}, "abc123", "train/loss")

		if _, present := out.raw["wall_times"]; present {
			t.Errorf("wall_times should be omitted when there is no wall_time metric")
		}
	})
}

// restoreLeadingZeros is where a wrong assumption would become a
// confident, wrong chart, so it refuses rather than guesses.
func TestRestoreLeadingZeros(t *testing.T) {
	f := func(vs ...float64) []float64 { return vs }

	t.Run("exact length passes through", func(t *testing.T) {
		got, ok := restoreLeadingZeros(f(5, 10), 2)
		if !ok || len(got) != 2 || *got[0] != 5 {
			t.Fatalf("got %v ok=%v", got, ok)
		}
	})

	t.Run("one missing is restored as a leading zero", func(t *testing.T) {
		got, ok := restoreLeadingZeros(f(10), 2)
		if !ok || len(got) != 2 {
			t.Fatalf("got %v ok=%v", got, ok)
		}
		if *got[0] != 0 || *got[1] != 10 {
			t.Errorf("got [%v %v], want [0 10]", *got[0], *got[1])
		}
	})

	// If the first surviving value is itself zero, the drop was not a
	// clean prefix of zeros and the offset is unknowable. Shifting the
	// series would render every point against the wrong time.
	t.Run("refuses when the first survivor is zero", func(t *testing.T) {
		if _, ok := restoreLeadingZeros(f(0, 10), 3); ok {
			t.Error("expected refusal: the prefix assumption does not hold")
		}
	})

	// A third of the series missing is not a handful of pre-anchor zeros.
	t.Run("refuses an implausible shortfall", func(t *testing.T) {
		if _, ok := restoreLeadingZeros(f(10, 20), 40); ok {
			t.Error("expected refusal: 38 missing is not a leading-zero run")
		}
	})

	t.Run("refuses more values than points", func(t *testing.T) {
		if _, ok := restoreLeadingZeros(f(1, 2, 3), 2); ok {
			t.Error("expected refusal: more x values than plotted points")
		}
	})
}
