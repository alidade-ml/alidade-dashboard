/**
 * Tests for what a metric chart's header says about the runs selected for it.
 *
 * Contract, from what a reader takes the header to mean:
 *
 *   * The count is runs drawn on this chart. A selected run with no series for
 *     the metric, or none this axis can place, is not drawn and not counted.
 *   * When some selected runs draw nothing, the header says "N of M" and names
 *     the ones known to have no data, so a missing line is never silent.
 *   * A run whose fetch has not answered is not yet "no data": it stays in the
 *     total and is not named.
 *   * A fetch that failed is not "has no metric". It is named as unloaded,
 *     until a later fetch answers.
 *   * A run toggled off is not selected, so it is neither counted nor named.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { chartHeader, mergeSeriesPoints, type ChartRunRef } from "./metric-series.ts";
import type { MetricSeries } from "./types.ts";

const METRIC = "val/perplexity";

const run = (hash: string, name: string, visible = true): ChartRunRef => ({ hash, name, visible });

const series = (values: number[], over: Partial<MetricSeries> = {}): MetricSeries => ({
  name: METRIC,
  steps: values.map((_, i) => i * 100),
  values,
  ...over,
});

const empty = series([]);

function header(
  runs: ChartRunRef[],
  loaded: Record<string, MetricSeries>,
  failed: Record<string, string> = {},
  xMode: "step" | "wall_time" = "step",
) {
  const data = mergeSeriesPoints(
    runs.map((r) => r.hash),
    loaded,
    xMode,
  );
  return chartHeader(METRIC, runs, data, loaded, failed);
}

describe("chartHeader", () => {
  it("does not count selected runs that have no series for the metric, and names them", () => {
    const got = header([run("l", "lightning"), run("h", "hf"), run("c", "composer")], {
      l: series([30, 20, 15]),
      h: empty,
      c: empty,
    });
    assert.equal(got.count, "1 of 3 runs");
    assert.equal(got.absent, "hf, composer have no val/perplexity");
  });

  it("says has, not have, for one run without data", () => {
    const got = header([run("l", "lightning"), run("h", "hf")], { l: series([1]), h: empty });
    assert.equal(got.count, "1 of 2 runs");
    assert.equal(got.absent, "hf has no val/perplexity");
  });

  it("does not count a run whose points this axis cannot place", () => {
    const got = header(
      [run("l", "lightning"), run("h", "hf")],
      { l: series([1, 2]), h: series([3, 4], { wall_times: [null, null] }) },
      {},
      "wall_time",
    );
    assert.equal(got.count, "1 of 2 runs");
    assert.equal(got.absent, "hf has no val/perplexity");
  });

  it("says nothing is drawn when no selected run has data", () => {
    const got = header([run("h", "hf"), run("c", "composer")], { h: empty, c: empty });
    assert.equal(got.count, "0 of 2 runs");
  });

  it("keeps a run still loading in the total without naming it", () => {
    const got = header([run("l", "lightning"), run("h", "hf")], { l: series([1]) });
    assert.equal(got.count, "1 of 2 runs");
    assert.equal(got.absent, null);
  });

  it("names a run whose fetch failed as unloaded, not as lacking the metric", () => {
    const got = header(
      [run("l", "lightning"), run("h", "hf"), run("c", "composer")],
      { l: series([1]), c: empty },
      { h: "aim API returned 502" },
    );
    assert.equal(got.count, "1 of 3 runs");
    assert.equal(got.absent, "composer has no val/perplexity · hf could not be loaded");
  });

  it("names a run as lacking the metric once a later fetch answers, despite an earlier failure", () => {
    const got = header(
      [run("l", "lightning"), run("h", "hf")],
      { l: series([1]), h: empty },
      { h: "aim API returned 502" },
    );
    assert.equal(got.absent, "hf has no val/perplexity");
  });

  it("neither counts nor names a run toggled off", () => {
    const got = header([run("l", "lightning"), run("h", "hf", false)], {
      l: series([1]),
      h: empty,
    });
    assert.equal(got.count, "1 run");
    assert.equal(got.absent, null);
  });

  it("counts every selected run when each draws a line", () => {
    const got = header([run("l", "lightning"), run("h", "hf"), run("c", "composer")], {
      l: series([3, 2]),
      h: series([4, 3]),
      c: series([5, 1]),
    });
    assert.equal(got.count, "3 runs");
    assert.equal(got.absent, null);
  });
});
