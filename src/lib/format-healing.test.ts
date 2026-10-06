/**
 * Tests for the healing note on the home page row and the experiment header.
 *
 * Contract, from what the CLI and Slack already say:
 *
 *   * A run that never healed says nothing. Zero, absent and nonsense counts
 *     all render no note, so a clean run stays visually clean.
 *   * A run that healed reads `healing attempted N×`, the CLI's wording, and
 *     never "healed": a healer session is an attempt, not a confirmed fix.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { formatHealingAttempts } from "./format.ts";

describe("formatHealingAttempts", () => {
  it("says nothing for a run that never healed", () => {
    assert.equal(formatHealingAttempts(0), null);
  });

  it("says nothing for a dashboard API that predates the field", () => {
    assert.equal(formatHealingAttempts(undefined), null);
    assert.equal(formatHealingAttempts(null), null);
  });

  it("says nothing for a count that is not a count", () => {
    assert.equal(formatHealingAttempts(-1), null);
    assert.equal(formatHealingAttempts(NaN), null);
  });

  it("uses the CLI's wording for one attempt and for several", () => {
    assert.equal(formatHealingAttempts(1), "healing attempted 1×");
    assert.equal(formatHealingAttempts(2), "healing attempted 2×");
  });
});
