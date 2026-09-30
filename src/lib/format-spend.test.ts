/**
 * Tests for the home page's Spend tile formatter.
 *
 * Contract, derived from what the tile has to communicate rather than from
 * the implementation:
 *
 *   * A total the API reported is never rendered as `$0`. `$0` is reserved
 *     for genuinely nothing spent, because the tile is what someone checks
 *     to see whether cost populated at all.
 *   * Absent is distinguishable from zero: no payload yet renders as a dash.
 *   * Above the point where cents stop carrying information, whole dollars
 *     keep the tile narrow.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { formatSpendCents } from "./format.ts";

describe("formatSpendCents", () => {
  it("renders a dash when there is no total yet", () => {
    assert.equal(formatSpendCents(undefined), "—");
    assert.equal(formatSpendCents(null), "—");
  });

  it("renders a dash rather than NaN for a non-finite total", () => {
    assert.equal(formatSpendCents(NaN), "—");
    assert.equal(formatSpendCents(Infinity), "—");
  });

  it("shows cents for a total that would otherwise round to zero", () => {
    // The run that produced the ticket: 2 cents read as `$0`.
    assert.equal(formatSpendCents(2), "$0.02");
    assert.equal(formatSpendCents(1), "$0.01");
  });

  it("shows cents for a canary-scale total", () => {
    assert.equal(formatSpendCents(20), "$0.20");
  });

  it("keeps cents up to the ten-dollar boundary and drops them at it", () => {
    assert.equal(formatSpendCents(999), "$9.99");
    assert.equal(formatSpendCents(1000), "$10");
  });

  it("shows whole dollars above the boundary", () => {
    assert.equal(formatSpendCents(123456), "$1,235");
  });

  it("reserves $0 for nothing spent", () => {
    assert.equal(formatSpendCents(0), "$0.00");
  });
});
