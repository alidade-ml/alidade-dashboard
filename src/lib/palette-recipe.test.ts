/**
 * Tests for the alidade colour recipe.
 *
 * The load-bearing one is the first: docs/brand.md publishes paper, ink and
 * accent for five presets across two modes, so 30 values grade this module
 * against an external oracle rather than against its own assumptions.
 *
 * The rest fence the two places where being wrong is a legibility bug rather
 * than an aesthetic one — the dimmest text rung, and the state badge.
 */

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";

import {
  DARK_SOFTNESS,
  DEFAULT_PRESET,
  PRESETS,
  emitCss,
  isInGamut,
  recipe,
  toHex,
  tokensFor,
  type Mode,
  type PresetId,
} from "./palette-recipe.ts";

/** The frozen table in docs/brand.md. Transcribed, never computed. */
const PUBLISHED: Record<PresetId, Record<Mode, { paper: string; ink: string; accent: string }>> = {
  brass: {
    light: { paper: "#F7F3EE", ink: "#241D12", accent: "#865900" },
    dark: { paper: "#241D14", ink: "#C8C0B3", accent: "#987334" },
  },
  verdigris: {
    light: { paper: "#EFF5F3", ink: "#14211E", accent: "#007661" },
    dark: { paper: "#15211E", ink: "#B5C5C0", accent: "#3B8A78" },
  },
  prussian: {
    light: { paper: "#F0F4F9", ink: "#171F28", accent: "#3165A0" },
    dark: { paper: "#181F27", ink: "#B8C2CD", accent: "#547DAD" },
  },
  oxide: {
    light: { paper: "#F9F2EF", ink: "#281B15", accent: "#9A4825" },
    dark: { paper: "#271B17", ink: "#CEBDB6", accent: "#AA664B" },
  },
  slate: {
    light: { paper: "#F2F4F6", ink: "#1A1F22", accent: "#4A677B" },
    dark: { paper: "#1B1F22", ink: "#BCC2C6", accent: "#667E8E" },
  },
};

const MODES: Mode[] = ["light", "dark"];

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
/** color-mix(in oklab, fg 15%, transparent) composited over bg. */
const tint = (fg: string, bg: string, p = 0.15) => {
  const [f, b] = [channels(fg), channels(bg)];
  return (
    "#" +
    f
      .map((v, i) => Math.round(255 * (v * p + b[i] * (1 - p))))
      .map((v) => v.toString(16).toUpperCase().padStart(2, "0"))
      .join("")
  );
};

describe("the recipe against brand.md's published table", () => {
  for (const p of PRESETS) {
    for (const mode of MODES) {
      it(`reproduces ${p.id} ${mode} exactly`, () => {
        const got = recipe(p, mode);
        const want = PUBLISHED[p.id][mode];
        assert.equal(got.paper, want.paper, `${p.id} ${mode} paper`);
        assert.equal(got.ink, want.ink, `${p.id} ${mode} ink`);
        assert.equal(got.accent, want.accent, `${p.id} ${mode} accent`);
      });
    }
  }

  it("uses the documented dark softness", () => {
    assert.equal(DARK_SOFTNESS, 0.72);
  });
});

describe("out-of-gamut handling", () => {
  // Named at the hues that actually leave gamut. A no-op would pass every
  // other test in this file, because nothing else needs the behaviour.
  const OUT_OF_GAMUT: [PresetId, number, number, number, number][] = [
    // preset, L, C, H, index of the channel that must clip to zero
    ["brass", 0.5, 0.115, 78, 2],
    ["verdigris", 0.5, 0.115 * 0.9, 176, 0],
  ];

  for (const [id, L, C, H, ch] of OUT_OF_GAMUT) {
    it(`${id}'s light accent is genuinely outside sRGB at its requested chroma`, () => {
      assert.equal(isInGamut(L, C, H), false, `${id} was in gamut; the fixture is wrong`);
    });

    it(`${id} clips the offending channel rather than reducing chroma`, () => {
      const hex = toHex(L, C, H);
      assert.equal(channels(hex)[ch], 0, `expected channel ${ch} of ${hex} to clip to zero`);
      assert.equal(hex, PUBLISHED[id].light.accent);
    });
  }

  it("leaves in-gamut colours untouched", () => {
    assert.equal(isInGamut(0.5, 0.115 * 0.4, 238), true);
  });
});

describe("legibility", () => {
  for (const p of PRESETS) {
    for (const mode of MODES) {
      const t = tokensFor(p, mode);

      it(`${p.id} ${mode}: muted text clears 4.5:1 on the ground`, () => {
        const r = contrast(t["--muted-foreground"], t["--background"]);
        assert.ok(r >= 4.5, `--muted-foreground on --background was ${r.toFixed(2)}:1`);
      });

      it(`${p.id} ${mode}: body text clears 4.5:1 on every surface`, () => {
        for (const bg of ["--background", "--surface", "--card", "--popover", "--muted"]) {
          const r = contrast(t["--foreground"], t[bg]);
          assert.ok(r >= 4.5, `--foreground on ${bg} was ${r.toFixed(2)}:1`);
        }
      });

      // The badge takes its text from ink and leaves the hue on the chip.
      // Reverting the text to the semantic hue must turn this red.
      it(`${p.id} ${mode}: badge ink on a tinted chip clears 4.5:1`, () => {
        for (const tone of ["--info", "--success", "--destructive", "--warning"]) {
          const chip = tint(t[tone], t["--background"]);
          const r = contrast(t["--foreground"], chip);
          assert.ok(r >= 4.5, `ink on a ${tone} chip was ${r.toFixed(2)}:1`);
        }
      });

      it(`${p.id} ${mode}: status dots clear the 3:1 non-text threshold`, () => {
        for (const tone of ["--info", "--success", "--destructive", "--warning"]) {
          const r = contrast(t[tone], t["--background"]);
          assert.ok(r >= 3, `${tone} on --background was ${r.toFixed(2)}:1`);
        }
      });

      it(`${p.id} ${mode}: borders are actually visible against their surface`, () => {
        const r = contrast(t["--border"], t["--background"]);
        assert.ok(r >= 1.09, `--border on --background was ${r.toFixed(3)}:1`);
      });
    }
  }
});

describe("the emitted stylesheet", () => {
  const css = emitCss();

  it("defines every token in the bare :root", () => {
    // A token defined only inside a media or [data-theme] block renders one
    // theme's text on the other theme's ground.
    const rootBlock = css.slice(css.indexOf(":root {"), css.indexOf("}", css.indexOf(":root {")));
    const inRoot = new Set(rootBlock.match(/--[\w-]+(?=:)/g) ?? []);
    const all = new Set(css.match(/--[\w-]+(?=:)/g) ?? []);
    const missing = [...all].filter((n) => !inRoot.has(n));
    assert.deepEqual(missing, [], `defined only in a conditional block: ${missing.join(", ")}`);
    assert.ok(inRoot.size > 20, `only ${inRoot.size} tokens in :root`);
  });

  it("resolves the bare :root to the default preset", () => {
    // The whole point of DEFAULT_PRESET: changing that one line must change
    // what an un-stamped document renders as.
    const want = tokensFor(PRESETS.find((p) => p.id === DEFAULT_PRESET)!, "light");
    const rootBlock = css.slice(css.indexOf(":root {"), css.indexOf("}", css.indexOf(":root {")));
    for (const [k, v] of Object.entries(want)) {
      assert.ok(rootBlock.includes(`${k}: ${v};`), `:root is missing ${k}: ${v}`);
    }
  });

  it("emits a block for every preset", () => {
    for (const p of PRESETS) {
      assert.ok(css.includes(`[data-palette="${p.id}"]`), `no block for ${p.id}`);
    }
  });

  it("emits only well-formed hex colours", () => {
    for (const [, value] of css.matchAll(/--[\w-]+:\s*([^;]+);/g)) {
      assert.match(value.trim(), /^#[0-9A-F]{6}$/, `not a hex colour: ${value}`);
    }
  });

  it("rejects a default that is not a known preset", () => {
    assert.throws(() => emitCss(PRESETS, "chartreuse" as PresetId), /unknown default preset/);
  });
});

describe("the committed stylesheet", () => {
  // The CSS is generated by hand and committed, so the file and the recipe can
  // drift: change DEFAULT_PRESET, forget to regenerate, ship a stylesheet that
  // disagrees with the module. Nothing else catches that.
  it("is in sync with the recipe", () => {
    const here = dirname(fileURLToPath(import.meta.url));
    const onDisk = readFileSync(join(here, "..", "brand", "palette.generated.css"), "utf8");
    assert.equal(
      onDisk,
      emitCss(),
      "src/brand/palette.generated.css is stale — run: node --experimental-strip-types scripts/gen-palette.ts",
    );
  });
});

describe("tokensFor", () => {
  it("keeps status colours fixed across presets", () => {
    const tones = ["--info", "--success", "--destructive", "--warning"];
    for (const mode of MODES) {
      const first = tokensFor(PRESETS[0], mode);
      for (const p of PRESETS.slice(1)) {
        const t = tokensFor(p, mode);
        for (const tone of tones) {
          assert.equal(t[tone], first[tone], `${tone} moved on ${p.id} ${mode}`);
        }
      }
    }
  });

  it("matches the mark's ink to the body ink in light mode", () => {
    // The correction is dark-only. On a light ground a dark mass does not gain
    // the apparent weight that makes the correction necessary.
    for (const p of PRESETS) {
      const t = tokensFor(p, "light");
      assert.equal(t["--alidade-mark-ink"], t["--alidade-ink"], `${p.id} light`);
    }
  });

  it("measures the mark's ink down in dark mode", () => {
    // The mark fills a crescent across half its box; the wordmark is thin
    // strokes. At equal colour the denser shape reads brighter, so the mark is
    // dimmed to sit level with the text beside it.
    for (const p of PRESETS) {
      const t = tokensFor(p, "dark");
      const lum = (hex: string) => {
        const [r, g, b] = [1, 3, 5]
          .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
          .map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      };
      assert.ok(
        lum(t["--alidade-mark-ink"]) < lum(t["--foreground"]),
        `${p.id} dark: the mark's ink is not below the body ink`,
      );
      // Still a non-text UI element: 3:1 against the ground it sits on.
      const c =
        (Math.max(lum(t["--alidade-mark-ink"]), lum(t["--background"])) + 0.05) /
        (Math.min(lum(t["--alidade-mark-ink"]), lum(t["--background"])) + 0.05);
      assert.ok(c >= 3, `${p.id} dark: the mark measures ${c.toFixed(2)}:1 on its ground`);
    }
  });

  it("carries the brand tokens the inlined mark reads", () => {
    const t = tokensFor(PRESETS[0], "light");
    assert.equal(t["--alidade-paper"], PUBLISHED.brass.light.paper);
    assert.equal(t["--alidade-ink"], PUBLISHED.brass.light.ink);
    assert.equal(t["--alidade-accent"], PUBLISHED.brass.light.accent);
  });

  it("maps the brand accent onto --primary", () => {
    for (const p of PRESETS) {
      for (const mode of MODES) {
        const t = tokensFor(p, mode);
        // Graded against the frozen table, not against the module's own
        // --alidade-accent, which tokensFor sets on the same line.
        assert.equal(t["--primary"], PUBLISHED[p.id][mode].accent, `${p.id} ${mode}`);
        assert.equal(t["--ring"], PUBLISHED[p.id][mode].accent, `${p.id} ${mode} ring`);
      }
    }
  });
});
