/**
 * Tests for the chart's axis placement.
 *
 * Contract, derived from what the two axes mean rather than from the code:
 *
 *   * The step axis places every point that has a value. Nothing about
 *     wall_time can remove a point from it.
 *   * The wall-time axis is an axis of SECONDS. A run that logged no
 *     wall_time at all charts against step number instead, so it is still
 *     ordered and visible; a run that logged wall_time but has no reading at
 *     one step cannot borrow the step number for that point, because the two
 *     units would then share one axis.
 *   * A reading of 0 seconds is a measurement, not a gap. It is what the
 *     first point of a run legitimately reads.
 *   * Both axes show the same set of values for a fully covered run.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { mergeSeriesPoints, pointX } from "./metric-series.ts";
import type { MetricSeries } from "./types.ts";

const loss = (over: Partial<MetricSeries> = {}): MetricSeries => ({
  name: "train/loss",
  steps: [0, 1, 2],
  values: [3.4, 1.2, 0.17],
  ...over,
});

describe("pointX", () => {
  it("drops a point whose wall_time reading is null", () => {
    const series = loss({ wall_times: [5, 10, null] });
    assert.equal(pointX(series, 2, "wall_time"), null);
  });

  it("drops a point past the end of a short wall_times array", () => {
    const series = loss({ wall_times: [5, 10] });
    assert.equal(pointX(series, 2, "wall_time"), null);
  });

  it("keeps a measured zero rather than treating it as a gap", () => {
    const series = loss({ wall_times: [0, 10, 20] });
    assert.equal(pointX(series, 0, "wall_time"), 0);
  });

  it("falls back to step number when the run logged no wall_time at all", () => {
    assert.equal(pointX(loss(), 2, "wall_time"), 2);
  });

  it("ignores wall_time entirely on the step axis", () => {
    const series = loss({ wall_times: [5, 10, null] });
    assert.equal(pointX(series, 2, "step"), 2);
  });
});

describe("mergeSeriesPoints", () => {
  // The reported defect: the API paired the final loss step with 0, so the
  // wall-time axis drew the run's last value at the origin — where a line
  // chart sorted by x draws it first, inventing a spike.
  it("does not place a run's final value at the origin when its wall_time is missing", () => {
    const series = loss({ wall_times: [5, 10, null] });
    const points = mergeSeriesPoints(["h1"], { h1: series }, "wall_time");

    assert.deepEqual(
      points.map((p) => p.x),
      [5, 10],
    );
    assert.equal(points[0].h1, 3.4);
  });

  it("keeps every point on the step axis regardless of wall_time gaps", () => {
    const series = loss({ wall_times: [null, 10, null] });
    const points = mergeSeriesPoints(["h1"], { h1: series }, "step");

    assert.deepEqual(
      points.map((p) => p.x),
      [0, 1, 2],
    );
    assert.deepEqual(
      points.map((p) => p.h1),
      [3.4, 1.2, 0.17],
    );
  });

  it("drops a point with a non-finite value", () => {
    const series = loss({ values: [3.4, NaN, 0.17] });
    const points = mergeSeriesPoints(["h1"], { h1: series }, "step");
    assert.deepEqual(
      points.map((p) => p.x),
      [0, 2],
    );
  });

  it("skips a run with no series yet", () => {
    const points = mergeSeriesPoints(["h1", "h2"], { h1: loss() }, "step");
    assert.equal(points.length, 3);
    assert.equal(points[0].h2, undefined);
  });

  it("returns nothing for an empty run list", () => {
    assert.deepEqual(mergeSeriesPoints([], {}, "wall_time"), []);
  });

  it("charts both axes over the same values when every step is covered", () => {
    const series = loss({ wall_times: [0, 10, 20] });
    const byStep = mergeSeriesPoints(["h1"], { h1: series }, "step");
    const byWall = mergeSeriesPoints(["h1"], { h1: series }, "wall_time");

    assert.deepEqual(
      byStep.map((p) => p.h1),
      byWall.map((p) => p.h1),
    );
    assert.deepEqual(
      byWall.map((p) => p.x),
      [0, 10, 20],
    );
  });

  it("merges two runs that share an x onto one point", () => {
    const a = loss({ wall_times: [0, 10, 20] });
    const b = loss({ values: [4.0, 2.0, 0.5], wall_times: [0, 10, 20] });
    const points = mergeSeriesPoints(["h1", "h2"], { h1: a, h2: b }, "wall_time");

    assert.equal(points.length, 3);
    assert.equal(points[0].h1, 3.4);
    assert.equal(points[0].h2, 4.0);
  });
});
