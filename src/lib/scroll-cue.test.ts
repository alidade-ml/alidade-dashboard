/**
 * Tests for the Samples tab's scroll cue.
 *
 * Contract: a strip says which ends hide content, and a strip that fits says
 * nothing. The defect was a set of four pairs, 1004px in 882px, cut mid-word
 * with no cue at all.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { scrollCue } from "./scroll-cue.ts";

describe("scrollCue", () => {
  it("cues nothing for a strip that fits", () => {
    assert.deepEqual(scrollCue(0, 882, 882), { before: false, after: false });
  });

  it("cues nothing for a strip one rounding pixel too wide", () => {
    assert.deepEqual(scrollCue(0, 883, 882), { before: false, after: false });
  });

  it("cues the far end of an unscrolled strip that overflows", () => {
    assert.deepEqual(scrollCue(0, 1004, 882), { before: false, after: true });
  });

  it("cues both ends partway along", () => {
    assert.deepEqual(scrollCue(60, 1004, 882), { before: true, after: true });
  });

  it("cues only the near end once scrolled to the last column", () => {
    assert.deepEqual(scrollCue(122, 1004, 882), { before: true, after: false });
  });

  it("treats a fractional scroll position at the end as the end", () => {
    assert.deepEqual(scrollCue(121.4, 1004, 882), { before: true, after: false });
  });
});
