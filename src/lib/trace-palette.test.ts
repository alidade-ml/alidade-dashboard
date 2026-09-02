/**
 * Tests for the categorical trace palette.
 *
 * The contract is deliberately tighter than "looks nice": one list, constant
 * across every palette and both themes, every entry legible on all ten grounds,
 * and adjacent entries far enough apart in hue to tell two lines apart.
 *
 * The list this replaces was Tableau-10, chosen against a white ground. Six of
 * its ten entries fell under 3:1 on one of the brand grounds.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { PRESETS, tokensFor } from "./palette-recipe.ts";
import {
  TRACE_C,
  TRACE_COUNT,
  TRACE_L,
  toHexPreservingHue,
  traceHues,
  tracePalette,
  vanDerCorput,
} from "./trace-palette.ts";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

const channels = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
const linear = (c: number) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const luminance = (hex: string) => {
  const [r, g, b] = channels(hex).map(linear);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a: string, b: string) => {
  const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m);
  return (x + 0.05) / (y + 0.05);
};

/** Every ground a trace can land on: five presets, both modes. */
const GROUNDS = PRESETS.flatMap((p) => [
  tokensFor(p, "light")["--background"],
  tokensFor(p, "dark")["--background"],
]);

const hueOf = (hex: string) => {
  const [r, g, b] = channels(hex).map(linear);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return { H: ((Math.atan2(B, A) * 180) / Math.PI + 360) % 360, C: Math.hypot(A, B) };
};
const arc = (a: number, b: number) => Math.min(Math.abs(a - b), 360 - Math.abs(a - b));

describe("legibility on every ground", () => {
  it("clears 3:1 on all ten grounds, for every entry", () => {
    // The whole point. Raising TRACE_L to 0.65 must turn this red.
    const failures: string[] = [];
    for (const c of tracePalette()) {
      for (const g of GROUNDS) {
        const r = contrast(c, g);
        if (r < 3) failures.push(`${c} on ${g} = ${r.toFixed(2)}:1`);
      }
    }
    assert.deepEqual(failures, [], `traces below 3:1:\n${failures.join("\n")}`);
  });

  it("covers ten distinct grounds", () => {
    // Guards the fixture, not the palette: if presets collapsed to one ground
    // the test above would pass while proving almost nothing.
    assert.equal(new Set(GROUNDS).size, 10);
  });
});

describe("separability", () => {
  it("keeps every pair perceptually apart, not merely numerically", () => {
    // Hue distance is not the thing a reader sees; OKLab distance is. At twenty
    // entries this measured 0.033 for the closest pair and twenty pairs sat under
    // 0.06 — distinct in the data, indistinguishable on a thin line.
    const oklab = (hex: string) => {
      const [r, g, b] = channels(hex).map(linear);
      const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
      const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
      const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
      return [
        0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
        1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
        0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
      ];
    };
    const p = tracePalette();
    let worst = Infinity;
    for (let i = 0; i < p.length; i++) {
      for (let j = i + 1; j < p.length; j++) {
        const [a, b] = [oklab(p[i]), oklab(p[j])];
        worst = Math.min(worst, Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]));
      }
    }
    assert.ok(worst >= 0.06, `closest pair is only dE ${worst.toFixed(4)} apart`);
  });

  it("keeps every pair at least 30 degrees apart in hue", () => {
    const hues = tracePalette().map((c) => hueOf(c).H);
    let worst = 360;
    for (let i = 0; i < hues.length; i++) {
      for (let j = i + 1; j < hues.length; j++) worst = Math.min(worst, arc(hues[i], hues[j]));
    }
    assert.ok(worst >= 35, `closest pair is ${worst.toFixed(1)} degrees apart`);
  });

  it("spreads the early entries further than the full set", () => {
    // Runs take colours by index, so a small chart uses the first few. This
    // reads the design hues rather than measuring them back out of the rendered
    // hex, where 8-bit quantisation costs a few tenths of a degree.
    const h = traceHues();
    const spread = (n: number) => {
      let worst = 360;
      for (let i = 0; i < n; i++) {
        for (let j = i + 1; j < n; j++) worst = Math.min(worst, arc(h[i], h[j]));
      }
      return worst;
    };
    const all = spread(h.length);
    assert.ok(
      spread(4) > all,
      "the ordering buys nothing: four runs are no better spread than ten",
    );
    assert.ok(spread(4) >= 70, `first four only ${spread(4).toFixed(1)} degrees apart`);
  });

  it("orders indices by the van der Corput sequence", () => {
    assert.deepEqual(
      [0, 1, 2, 3, 4].map((i) => vanDerCorput(i)),
      [0, 0.5, 0.25, 0.75, 0.125],
    );
  });
});

describe("the set reads as one family", () => {
  it("holds lightness uniform", () => {
    const ys = tracePalette().map(luminance);
    // Perceptual lightness is fixed by construction; relative luminance still
    // varies a little by hue, and the band is what has to hold.
    assert.ok(
      Math.max(...ys) - Math.min(...ys) < 0.06,
      `luminance spread ${(Math.max(...ys) - Math.min(...ys)).toFixed(3)}`,
    );
  });

  it("holds chroma near-uniform", () => {
    // Tableau-10 ranged 0.013 to 0.173 — a near-grey beside a saturated red.
    const cs = tracePalette().map((c) => hueOf(c).C);
    assert.ok(
      Math.max(...cs) - Math.min(...cs) < 0.03,
      `chroma spread ${(Math.max(...cs) - Math.min(...cs)).toFixed(3)}`,
    );
  });
});

describe("gamut handling preserves hue", () => {
  it("reduces chroma instead of clipping, so the hue asked for is the hue rendered", () => {
    // Hue is a trace's identity. Clipping a channel shifts it; chroma reduction
    // does not. Named at a hue that actually needs the reduction.
    const H = 145;
    const rendered = toHexPreservingHue(TRACE_L, 0.4, H);
    assert.ok(
      Math.abs(hueOf(rendered).H - H) < 2,
      `hue drifted to ${hueOf(rendered).H.toFixed(1)}`,
    );
    assert.ok(hueOf(rendered).C < 0.4, "chroma was not reduced");
  });

  it("leaves an in-gamut colour alone", () => {
    assert.equal(
      toHexPreservingHue(TRACE_L, TRACE_C, 253),
      toHexPreservingHue(TRACE_L, TRACE_C, 253),
    );
    assert.ok(hueOf(toHexPreservingHue(TRACE_L, TRACE_C, 253)).C > 0.1);
  });
});

describe("the generated file is not stale", () => {
  // Only one file is checked in. The TypeScript consumers import tracePalette()
  // and cannot drift, and the wheel's copy is derived: build_hook.py wipes
  // alidade_dashboard/config/ and re-copies server/config/ at build time. That
  // directory is gitignored, so a test reading it would pass here and throw on a
  // fresh clone.
  it("matches what the generator would write today", () => {
    const onDisk = JSON.parse(
      readFileSync(join(root, "server", "config", "colors.json"), "utf8"),
    ).palette;
    assert.deepEqual(
      onDisk,
      tracePalette(),
      "server/config/colors.json is stale — run: node --experimental-strip-types scripts/gen-trace-palette.ts",
    );
  });
});

describe("shape", () => {
  it("emits the expected count of well-formed colours", () => {
    const p = tracePalette();
    assert.equal(p.length, TRACE_COUNT);
    for (const c of p) assert.match(c, /^#[0-9A-F]{6}$/);
    assert.equal(new Set(p).size, TRACE_COUNT, "duplicate colours in the palette");
  });

  it("spaces the underlying hues evenly", () => {
    const sorted = [...traceHues()].sort((a, b) => a - b);
    const gaps = sorted.map((h, i) => arc(h, sorted[(i + 1) % sorted.length]));
    assert.ok(Math.max(...gaps) - Math.min(...gaps) < 0.001, "hues are not evenly spaced");
  });
});
