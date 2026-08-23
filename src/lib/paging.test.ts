/**
 * Tests for the experiments table's display windowing.
 *
 * Contract, derived from what the page has to survive rather than from the
 * implementation:
 *
 *   * A requested page is a request, not a fact. It arrives from a URL anyone
 *     can edit, and the list behind it is re-fetched every 3 seconds.
 *   * There is always exactly one page, even with nothing in the list — a
 *     pageCount of 0 makes "page 1 of 0" and divides by zero downstream.
 *   * The window always offers the first and last page, so an inbox of any
 *     depth is two clicks from either end.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { PAGE_SIZE, pageSlice, pageWindow } from "./paging.ts";

describe("pageSlice", () => {
  it("clamps a page past the end back onto the last one", () => {
    // The reader is on page 9 when a filter cuts the list to two pages.
    const { page, pageStart } = pageSlice(PAGE_SIZE * 2, 9);
    assert.equal(page, 2);
    assert.equal(pageStart, PAGE_SIZE);
  });

  it("clamps zero and negatives to the first page", () => {
    for (const requested of [0, -1, -999]) {
      assert.equal(pageSlice(500, requested).page, 1, `page=${requested}`);
    }
  });

  it("treats a fractional or unparseable page as the first", () => {
    assert.equal(pageSlice(500, 2.7).page, 2);
    assert.equal(pageSlice(500, NaN).page, 1);
    assert.equal(pageSlice(500, undefined).page, 1);
  });

  it("reports one page for an empty list, not zero", () => {
    const { page, pageCount, pageStart } = pageSlice(0, 1);
    assert.equal(pageCount, 1);
    assert.equal(page, 1);
    assert.equal(pageStart, 0);
  });

  it("does not add an empty trailing page at an exact multiple", () => {
    assert.equal(pageSlice(PAGE_SIZE, 1).pageCount, 1);
    assert.equal(pageSlice(PAGE_SIZE * 3, 1).pageCount, 3);
    assert.equal(pageSlice(PAGE_SIZE * 3 + 1, 1).pageCount, 4);
  });

  it("starts each page where the previous one ended", () => {
    const total = PAGE_SIZE * 4 + 7;
    const { pageCount } = pageSlice(total, 1);
    let covered = 0;
    for (let p = 1; p <= pageCount; p++) {
      const { pageStart } = pageSlice(total, p);
      assert.equal(pageStart, covered, `page ${p} starts at the wrong row`);
      covered += Math.min(PAGE_SIZE, total - pageStart);
    }
    assert.equal(covered, total, "the pages do not cover every row exactly once");
  });
});

describe("pageWindow", () => {
  it("lists every page when they fit", () => {
    assert.deepEqual(pageWindow(1, 1), [1]);
    assert.deepEqual(pageWindow(4, 7), [1, 2, 3, 4, 5, 6, 7]);
  });

  it("always offers the first and last page", () => {
    for (const current of [1, 2, 25, 49, 50]) {
      const w = pageWindow(current, 50);
      assert.equal(w[0], 1, `current=${current}`);
      assert.equal(w[w.length - 1], 50, `current=${current}`);
    }
  });

  it("always contains the current page", () => {
    for (const current of [1, 2, 3, 8, 47, 49, 50]) {
      assert.ok(pageWindow(current, 50).includes(current), `current=${current}`);
    }
  });

  it("collapses long runs on both sides", () => {
    assert.deepEqual(pageWindow(25, 50), [1, "gap", 24, 25, 26, "gap", 50]);
  });

  it("does not put a gap where a single page would fit", () => {
    // 1 [2] 3 — a gap standing in for one number is longer than the number.
    assert.deepEqual(pageWindow(2, 20), [1, 2, 3, "gap", 20]);
    assert.deepEqual(pageWindow(19, 20), [1, "gap", 18, 19, 20]);
  });

  it("never repeats a page number", () => {
    for (const total of [8, 9, 20, 200]) {
      for (let current = 1; current <= total; current++) {
        const nums = pageWindow(current, total).filter((e) => e !== "gap");
        assert.equal(new Set(nums).size, nums.length, `total=${total} current=${current}`);
      }
    }
  });

  it("stays ascending", () => {
    for (let current = 1; current <= 60; current++) {
      const nums = pageWindow(current, 60).filter((e): e is number => e !== "gap");
      for (let i = 1; i < nums.length; i++) {
        assert.ok(nums[i] > nums[i - 1], `current=${current} not ascending`);
      }
    }
  });
});
