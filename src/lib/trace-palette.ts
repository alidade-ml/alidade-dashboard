/**
 * Categorical colours for run traces.
 *
 * Distinct from the brand palettes in palette-recipe.ts and under the opposite
 * constraint: a preset should be harmonious, this list must stay mutually
 * distinguishable. One list, constant across every palette and both themes,
 * because a run's colour is its identity — a line that changes colour when the
 * reader flips theme breaks the only thing the colour is for.
 */

import { isInGamut, toHex } from "./palette-recipe.ts";

/**
 * Perceptual lightness for every trace.
 *
 * Chosen by measurement. For one colour to clear 3:1 against both the lightest
 * paper and the darkest ground across all five presets it must land in
 * Y = 0.1383..0.2674; OKLCH L from 0.55 to 0.62 puts every hue inside that band,
 * and 0.65 puts only ten of twenty-four inside it.
 */
export const TRACE_L = 0.6;

/** Requested chroma, reduced per hue where sRGB cannot hold it. */
export const TRACE_C = 0.11;

/** Hue of the first trace. Anchored on the default preset's accent. */
export const TRACE_ANCHOR_H = 253;

/**
 * Ten, not twenty.
 *
 * Twenty evenly spaced hues put adjacent entries 18 degrees apart, which measures
 * dE 0.033 in OKLab — two teals nobody separates on a thin line. Twenty pairs sat
 * below a usable threshold. At ten the closest pair is dE 0.067 and none are, which
 * is the same class as the Tableau-10 set this replaces.
 *
 * Ten is the ceiling, not a compromise: holding the list constant across light and
 * dark grounds confines it to a narrow lightness band, so hue is the only channel
 * left to separate entries. Runs cycle past ten, and a chart with eleven lines is
 * unreadable for reasons colour cannot fix.
 */
export const TRACE_COUNT = 10;

/**
 * Resolve out of gamut by reducing chroma, not by clipping channels.
 *
 * The brand recipe clips, because that is what produced the frozen palette.
 * Here hue is the identity: two runs differing only in hue must not converge, and
 * clipping shifts hue. The cost is a marginally duller colour.
 */
export function toHexPreservingHue(L: number, C: number, H: number): string {
  if (isInGamut(L, C, H)) return toHex(L, C, H);
  let lo = 0;
  let hi = C;
  for (let i = 0; i < 40; i++) {
    const mid = (lo + hi) / 2;
    if (isInGamut(L, mid, H)) lo = mid;
    else hi = mid;
  }
  return toHex(L, lo, H);
}

/**
 * Van der Corput sequence — the ordering that keeps every prefix spread.
 *
 * Hues are spaced evenly, so the full set is 18 degrees apart whatever the order.
 * Sorting the *indices* by this sequence means a chart with four runs draws them
 * 72 degrees apart instead of 18, because runs are assigned by index.
 */
export function vanDerCorput(i: number, base = 2): number {
  let f = 1;
  let r = 0;
  let n = i;
  while (n > 0) {
    f /= base;
    r += f * (n % base);
    n = Math.floor(n / base);
  }
  return r;
}

export function traceHues(count = TRACE_COUNT, anchor = TRACE_ANCHOR_H): number[] {
  const even = Array.from({ length: count }, (_, k) => (anchor + (360 * k) / count) % 360);
  const order = Array.from({ length: count }, (_, k) => k).sort(
    (a, b) => vanDerCorput(a) - vanDerCorput(b) || a - b,
  );
  return order.map((k) => even[k]);
}

export function tracePalette(count = TRACE_COUNT): string[] {
  return traceHues(count).map((H) => toHexPreservingHue(TRACE_L, TRACE_C, H));
}
