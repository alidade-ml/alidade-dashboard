/**
 * Tests for which tabs an experiment page offers.
 *
 * Contract, from what the page has to survive:
 *
 *   * Unknown counts as present. A tab hidden by a failed request is worse
 *     than one shown empty — the reader cannot tell "nothing was logged" from
 *     "the dashboard could not ask", and goes looking in the wrong place.
 *   * Zero is only zero when it was actually counted.
 *   * The reader's chosen tab survives a poll that changes what is available,
 *     unless their choice is the thing that went away.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { anyTabVisible, resolveActiveTab, visibleTabs } from "./tab-presence.ts";

describe("visibleTabs", () => {
  it("shows a tab whose count is unknown", () => {
    const tabs = visibleTabs({ training: false, evalEntries: null, sampleEntries: null });
    assert.equal(tabs.eval, true, "an unknown eval count hid the tab");
    assert.equal(tabs.samples, true, "an unknown sample count hid the tab");
  });

  it("hides a tab only on a counted zero", () => {
    const tabs = visibleTabs({ training: false, evalEntries: 0, sampleEntries: 0 });
    assert.deepEqual(tabs, { training: false, eval: false, samples: false });
  });

  it("shows a tab on any nonzero count", () => {
    const tabs = visibleTabs({ training: false, evalEntries: 1, sampleEntries: 3 });
    assert.equal(tabs.eval, true);
    assert.equal(tabs.samples, true);
  });

  it("takes training from the run set, not from a count", () => {
    // A training run with no metrics yet is still a training run: the hash
    // exists, and the numbers may arrive while the page is open.
    const tabs = visibleTabs({ training: true, evalEntries: 0, sampleEntries: 0 });
    assert.deepEqual(tabs, { training: true, eval: false, samples: false });
  });
});

describe("anyTabVisible", () => {
  it("is false only when every tab is a counted zero", () => {
    assert.equal(
      anyTabVisible(visibleTabs({ training: false, evalEntries: 0, sampleEntries: 0 })),
      false,
    );
    assert.equal(
      anyTabVisible(visibleTabs({ training: false, evalEntries: null, sampleEntries: 0 })),
      true,
      "an unresolved count must not render as 'no data yet'",
    );
  });
});

describe("resolveActiveTab", () => {
  it("keeps the reader's choice when it still has data", () => {
    const tabs = { training: true, eval: true, samples: true };
    assert.equal(resolveActiveTab(tabs, "eval"), "eval");
  });

  it("moves off a tab that lost its data", () => {
    const tabs = { training: true, eval: false, samples: true };
    assert.equal(resolveActiveTab(tabs, "eval"), "training");
  });

  it("ignores a deep link naming a tab this experiment has nothing for", () => {
    const tabs = { training: false, eval: false, samples: true };
    assert.equal(resolveActiveTab(tabs, "training"), "samples");
  });

  it("falls to the first tab with data when nothing is chosen", () => {
    assert.equal(
      resolveActiveTab({ training: false, eval: true, samples: true }, undefined),
      "eval",
    );
    assert.equal(
      resolveActiveTab({ training: false, eval: false, samples: true }, undefined),
      "samples",
    );
  });

  it("returns null when there is nothing to show", () => {
    assert.equal(resolveActiveTab({ training: false, eval: false, samples: false }, "eval"), null);
  });
});
