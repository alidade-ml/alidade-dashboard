package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wall-time zip is index-aligned with `steps`, so a step the wall_time
// series does not cover has to be encoded as an absent reading rather than
// dropped: `steps` and `values` still feed the step-axis chart and must keep
// every point. These tests pin `null` as that encoding, because the previous
// zero-value fill was indistinguishable from an elapsed reading of 0 and got
// drawn at the origin.

// fakeMetricAim serves Aim's metric/get-batch/ route for a fixed set of
// metrics, keyed by name.
func fakeMetricAim(t *testing.T, metrics map[string]MetricData) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/metric/get-batch/") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req) == 0 {
			t.Errorf("unexpected get-batch body: %s", body)
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
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rawMetricResponse decodes the handler's JSON without going through
// MetricResponse, so a null in wall_times stays distinguishable from a 0.
type rawMetricResponse struct {
	Steps     []int      `json:"steps"`
	Values    []float64  `json:"values"`
	WallTimes []*float64 `json:"wall_times"`
	raw       map[string]json.RawMessage
}

func getMetric(t *testing.T, metrics map[string]MetricData, hash, metricName string) rawMetricResponse {
	t.Helper()
	srv := fakeMetricAim(t, metrics)
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
	// The reported shape: the two series are downsampled to their own step
	// sets, so the loss series' final step has no partner and its value was
	// being paired with 0 — the run's last loss drawn at the origin.
	t.Run("trailing unpaired step is null, not zero", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1, 2}, Values: []float64{3.4, 1.2, 0.17}},
			"wall_time":  {Iters: []int{0, 1}, Values: []float64{5, 10}},
		}, "abc123", "train/loss")

		if len(out.WallTimes) != 3 {
			t.Fatalf("wall_times length = %d, want 3 (index-aligned with steps)", len(out.WallTimes))
		}
		if out.WallTimes[2] != nil {
			t.Errorf("wall_times[2] = %v for unpaired step 2, want null", *out.WallTimes[2])
		}
		if out.WallTimes[1] == nil || *out.WallTimes[1] != 10 {
			t.Errorf("wall_times[1] = %v, want 10", out.WallTimes[1])
		}
	})

	t.Run("leading unpaired step is null, not zero", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
			"wall_time":  {Iters: []int{1}, Values: []float64{10}},
		}, "abc123", "train/loss")

		if out.WallTimes[0] != nil {
			t.Errorf("wall_times[0] = %v for unpaired step 0, want null", *out.WallTimes[0])
		}
	})

	// The reason a sentinel cannot work: zero elapsed seconds is what the
	// first point of a run legitimately reads.
	t.Run("a genuine zero reading survives as zero", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
			"wall_time":  {Iters: []int{0, 1}, Values: []float64{0, 10}},
		}, "abc123", "train/loss")

		if out.WallTimes[0] == nil {
			t.Fatal("wall_times[0] = null for a measured 0.0, want 0")
		}
		if *out.WallTimes[0] != 0 {
			t.Errorf("wall_times[0] = %v, want 0", *out.WallTimes[0])
		}
	})

	t.Run("no overlapping step omits wall_times entirely", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
			"wall_time":  {Iters: []int{7, 8}, Values: []float64{10, 20}},
		}, "abc123", "train/loss")

		if _, present := out.raw["wall_times"]; present {
			t.Errorf("wall_times present with no paired step: %s", out.raw["wall_times"])
		}
	})

	t.Run("a run with no wall_time metric omits wall_times", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1}, Values: []float64{3.4, 1.2}},
		}, "abc123", "train/loss")

		if _, present := out.raw["wall_times"]; present {
			t.Errorf("wall_times present for a run without the metric: %s", out.raw["wall_times"])
		}
	})

	t.Run("fully paired steps all carry a reading", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"train/loss": {Iters: []int{0, 1, 2}, Values: []float64{3.4, 1.2, 0.17}},
			"wall_time":  {Iters: []int{0, 1, 2}, Values: []float64{1, 2, 3}},
		}, "abc123", "train/loss")

		for i, wt := range out.WallTimes {
			if wt == nil {
				t.Fatalf("wall_times[%d] = null, want a reading", i)
			}
			if *wt != float64(i+1) {
				t.Errorf("wall_times[%d] = %v, want %v", i, *wt, i+1)
			}
		}
	})

	// Zipping wall_time against itself would be circular; the handler skips
	// the fetch, so the response carries the series and no zip.
	t.Run("requesting wall_time itself omits wall_times", func(t *testing.T) {
		out := getMetric(t, map[string]MetricData{
			"wall_time": {Iters: []int{0, 1}, Values: []float64{0, 10}},
		}, "abc123", "wall_time")

		if _, present := out.raw["wall_times"]; present {
			t.Errorf("wall_times present on the wall_time series itself: %s", out.raw["wall_times"])
		}
		if len(out.Values) != 2 {
			t.Errorf("values length = %d, want 2", len(out.Values))
		}
	})
}
