package api

import (
	"testing"
)

// wall_time is the x-axis for every other series on a run, and the two are
// fetched in separate requests that Aim downsamples independently. Matching
// on exact step number therefore produced interior nulls that do not exist
// in the underlying data — a step kept in one response and dropped from the
// other had nothing to pair with.
//
// The hole count scales with how hard the series are downsampled, so a
// longer run is worse. A test on a short run cannot fail.

func md(iters []int, values []float64) *MetricData {
	return &MetricData{Iters: iters, Values: values}
}

func deref(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatal("expected a paired wall_time, got nil")
	}
	return *p
}

// The original bug, in miniature: loss keeps step 5, wall_time does not.
func TestWallTimeInterpolatesAStepTheSamplerDropped(t *testing.T) {
	wt := md([]int{0, 10}, []float64{0, 100})

	got, any := wallTimesForSteps(wt, []int{0, 5, 10})
	if !any {
		t.Fatal("no wall times produced")
	}
	for i, v := range got {
		if v == nil {
			t.Fatalf("index %d is nil; the whole point is that it is not", i)
		}
	}
	if v := deref(t, got[1]); v != 50 {
		t.Errorf("step 5 between (0,0) and (10,100) should be 50, got %v", v)
	}
}

// An exact hit must not be interpolated — that would introduce error where
// the real observation was available.
func TestWallTimeUsesTheExactSampleWhenThereIsOne(t *testing.T) {
	wt := md([]int{0, 5, 10}, []float64{0, 42, 100})

	got, _ := wallTimesForSteps(wt, []int{5})
	if v := deref(t, got[0]); v != 42 {
		t.Errorf("expected the observed 42, got %v", v)
	}
}

// Past the last observation there is nothing to read. Extrapolating would
// invent elapsed time, and nil already renders.
func TestWallTimeDoesNotExtrapolate(t *testing.T) {
	wt := md([]int{0, 10}, []float64{0, 100})

	got, _ := wallTimesForSteps(wt, []int{-1, 20})
	if got[0] != nil {
		t.Errorf("before the first sample should be nil, got %v", *got[0])
	}
	if got[1] != nil {
		t.Errorf("after the last sample should be nil, got %v", *got[1])
	}
}

// The realistic shape: two independently sampled grids over one run. Every
// requested step inside the wall_time range must pair.
func TestNoInteriorNullsAcrossDisjointSampling(t *testing.T) {
	// wall_time sampled on multiples of 3, loss on multiples of 7 — they
	// share only step 0 and 21, so exact matching would null almost
	// everything.
	var wIters []int
	var wVals []float64
	for s := 0; s <= 300; s += 3 {
		wIters = append(wIters, s)
		wVals = append(wVals, float64(s)*1.5)
	}
	var lossSteps []int
	for s := 0; s <= 294; s += 7 {
		lossSteps = append(lossSteps, s)
	}

	got, any := wallTimesForSteps(md(wIters, wVals), lossSteps)
	if !any {
		t.Fatal("no wall times produced")
	}
	for i, step := range lossSteps {
		if got[i] == nil {
			t.Fatalf("step %d inside the sampled range must pair, got nil", step)
		}
		// wall_time here is exactly 1.5*step, so interpolation is checkable
		// rather than merely non-nil.
		if want := float64(step) * 1.5; *got[i] != want {
			t.Errorf("step %d: got %v, want %v", step, *got[i], want)
		}
	}
}

// Unsorted input must not break the search. Aim returns ascending iters
// today; relying on that silently is how a resorted response becomes a
// wrong chart rather than an error.
func TestWallTimeToleratesUnsortedSamples(t *testing.T) {
	wt := md([]int{10, 0, 5}, []float64{100, 0, 50})

	got, _ := wallTimesForSteps(wt, []int{7})
	if v := deref(t, got[0]); v != 70 {
		t.Errorf("expected 70 from a sorted read of the samples, got %v", v)
	}
}

func TestEmptyWallTimeSeriesReportsNothingRatherThanNulls(t *testing.T) {
	if _, any := wallTimesForSteps(md(nil, nil), []int{1, 2}); any {
		t.Error("an empty series must not claim to have produced wall times")
	}
}
