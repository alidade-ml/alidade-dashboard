/**
 * The alidade colour recipe, ported from docs/brand.md in the alidade repo.
 *
 * Colour there is defined as a recipe over a hue rather than a palette, so this
 * module is the recipe and every token in the dashboard is derived from it.
 */

export type PresetId = "brass" | "verdigris" | "prussian" | "oxide" | "slate";
export type Mode = "light" | "dark";

export interface Preset {
  id: PresetId;
  label: string;
  /** Accent hue, degrees. */
  H: number;
  /** Chroma multiplier. */
  cm: number;
}

export const PRESETS: Preset[] = [
  { id: "brass", label: "Brass", H: 78, cm: 1.0 },
  { id: "verdigris", label: "Verdigris", H: 176, cm: 0.9 },
  { id: "prussian", label: "Prussian", H: 253, cm: 0.95 },
  { id: "oxide", label: "Oxide", H: 42, cm: 1.05 },
  { id: "slate", label: "Slate", H: 238, cm: 0.4 },
];

/**
 * The palette the bare :root resolves to. Change this one line and regenerate to
 * ship a different default; every preset stays available via [data-palette].
 */
export const DEFAULT_PRESET: PresetId = "prussian";

/** "Dark softness" — the recipe's single free parameter. */
export const DARK_SOFTNESS = 0.72;

const lerp = (a: number, b: number, t: number) => a + (b - a) * t;

function oklchToLinearRgb(L: number, C: number, H: number): [number, number, number] {
  const a = C * Math.cos((H * Math.PI) / 180);
  const b = C * Math.sin((H * Math.PI) / 180);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
}

const encodeGamma = (x: number) => (x <= 0.0031308 ? 12.92 * x : 1.055 * x ** (1 / 2.4) - 0.055);

/**
 * Out-of-gamut colours are resolved by clipping the offending channel, not by
 * reducing chroma.
 *
 * brand.md's prose says to binary-search chroma down instead. That is not how
 * the frozen palette was produced: clipping reproduces all 30 published values,
 * chroma reduction misses the light accent at brass and verdigris — the only two
 * that leave gamut. Matching the shipped artifact keeps the hub and the brand
 * assets from disagreeing. The overshoot is under 0.013 in linear light, so the
 * hue shift clipping introduces is not perceptible here.
 */
export function toHex(L: number, C: number, H: number): string {
  return (
    "#" +
    oklchToLinearRgb(L, C, H)
      .map((c) =>
        Math.max(0, Math.min(255, Math.round(encodeGamma(Math.max(0, Math.min(1, c))) * 255))),
      )
      .map((v) => v.toString(16).toUpperCase().padStart(2, "0"))
      .join("")
  );
}

export function isInGamut(L: number, C: number, H: number, eps = 1e-4): boolean {
  return oklchToLinearRgb(L, C, H).every((c) => c >= -eps && c <= 1 + eps);
}

/** The three values brand.md publishes. Everything else is a rung between them. */
export function recipe(p: Preset, mode: Mode, s = DARK_SOFTNESS) {
  const { H, cm } = p;
  if (mode === "light") {
    return {
      paper: toHex(0.966, 0.008 * cm, H),
      ink: toHex(0.235, 0.022 * cm, H),
      accent: toHex(0.5, 0.115 * cm, H),
    };
  }
  return {
    paper: toHex(lerp(0.17, 0.262, s), 0.02 * cm, H),
    ink: toHex(lerp(0.9, 0.775, s), lerp(0.006, 0.026, s) * cm, H),
    accent: toHex(lerp(0.64, 0.556, s), lerp(0.135, 0.076, s) * cm, H),
  };
}

/**
 * Status colours are brand.md's own preset accents, so they arrive already
 * gamut-checked and already carrying a light/dark pair. Fixed across presets:
 * choosing a palette must never move a status colour.
 *
 * Four of the five hues are spoken for, so every preset shares its hue with some
 * status role. That holds only because accent and status never share a form —
 * the accent is never rendered as a chip or a dot.
 */
const SEMANTIC: Record<string, Record<Mode, string>> = {
  "--info": { light: "#3165A0", dark: "#547DAD" },
  "--success": { light: "#007661", dark: "#3B8A78" },
  "--destructive": { light: "#9A4825", dark: "#AA664B" },
  "--warning": { light: "#865900", dark: "#987334" },
};

/**
 * Lightness rungs, chroma as a factor of the preset's `cm`.
 *
 * Light mode inverts the convention this file replaces: paper is 0.966 rather
 * than near-white, so cards step *up* toward white instead of down.
 */
const RAMP: Record<Mode, Record<string, [number, number]>> = {
  light: {
    "--surface": [0.978, 0.006],
    "--surface-elevated": [0.992, 0.004],
    "--card": [0.992, 0.004],
    "--popover": [0.992, 0.004],
    "--muted": [0.945, 0.01],
    "--secondary": [0.945, 0.01],
    "--accent": [0.95, 0.012],
    "--input": [0.915, 0.012],
    "--border": [0.905, 0.012],
    "--border-strong": [0.855, 0.016],
    "--muted-foreground": [0.535, 0.02],
    "--accent-foreground": [0.3, 0.06],
    "--primary-foreground": [0.985, 0.004],
    "--destructive-foreground": [0.985, 0.004],
  },
  dark: {
    "--surface": [0.2612, 0.018],
    "--surface-elevated": [0.2812, 0.016],
    "--card": [0.2812, 0.016],
    "--popover": [0.2812, 0.016],
    "--muted": [0.2662, 0.018],
    "--secondary": [0.2662, 0.018],
    "--accent": [0.2962, 0.04],
    "--input": [0.3062, 0.016],
    "--border": [0.3062, 0.016],
    "--border-strong": [0.3662, 0.014],
    "--muted-foreground": [0.63, 0.02],
    "--accent-foreground": [0.9, 0.03],
    "--destructive-foreground": [0.985, 0.004],
  },
};

/**
 * Lightness of the mark's ink in dark mode, against `ink` at 0.810.
 *
 * The mark and the wordmark are set in the same colour, but the mark fills a
 * crescent across half its box while the word is thin strokes over the ground.
 * Equal colour at very unequal coverage reads as unequal brightness, so the mark
 * is measured down to sit level with the text beside it.
 *
 * Dark only. On light ground a dark mass does not gain the same apparent weight,
 * and brand.md's rule that dark mode is not an inversion applies here too.
 */
const DARK_MARK_INK_L = 0.72;

export function tokensFor(p: Preset, mode: Mode): Record<string, string> {
  const r = recipe(p, mode);
  // paper, ink and accent are the three values brand.md publishes, so they are
  // taken from the recipe rather than restated as rungs. Restating them is how
  // a ramp drifts from the table that grades it.
  const out: Record<string, string> = {
    "--astro-paper": r.paper,
    "--astro-ink": r.ink,
    "--astro-accent": r.accent,
    "--background": r.paper,
    "--foreground": r.ink,
    "--card-foreground": r.ink,
    "--popover-foreground": r.ink,
    "--secondary-foreground": r.ink,
    "--primary": r.accent,
    "--ring": r.accent,
    "--astro-mark-ink":
      mode === "dark"
        ? toHex(DARK_MARK_INK_L, lerp(0.006, 0.026, DARK_SOFTNESS) * p.cm, p.H)
        : r.ink,
  };
  if (mode === "dark") out["--primary-foreground"] = r.paper;
  for (const [name, [L, cf]] of Object.entries(RAMP[mode])) {
    out[name] = toHex(L, cf * p.cm, p.H);
  }
  for (const [name, byMode] of Object.entries(SEMANTIC)) {
    out[name] = byMode[mode];
  }
  return out;
}

const block = (sel: string, t: Record<string, string>, indent = "  ") =>
  `${sel} {\n` +
  Object.entries(t)
    .map(([k, v]) => `${indent}${k}: ${v};`)
    .join("\n") +
  `\n}`;

export function emitCss(presets: Preset[] = PRESETS, defaultId: PresetId = DEFAULT_PRESET): string {
  const def = presets.find((p) => p.id === defaultId);
  if (!def) throw new Error(`unknown default preset: ${defaultId}`);

  const lines = [
    "/* Generated by scripts/gen-palette.ts from src/lib/palette-recipe.ts.",
    "   Do not edit by hand. The recipe lives in docs/brand.md (alidade repo).",
    "",
    "   Light is the bare :root so the un-stamped state resolves; dark is redefined",
    "   twice, behind prefers-color-scheme and behind [data-theme], so a toggle wins",
    "   in both directions. Every token is defined in the bare :root — a token that",
    "   exists only inside a conditional block renders one theme on the other's ground. */",
    "",
    block(":root", tokensFor(def, "light")),
    "",
    `@media (prefers-color-scheme: dark) {`,
    `  :root:not([data-theme="light"]) {`,
    Object.entries(tokensFor(def, "dark"))
      .map(([k, v]) => `    ${k}: ${v};`)
      .join("\n"),
    `  }`,
    `}`,
    "",
    block(':root[data-theme="dark"]', tokensFor(def, "dark")),
    "",
    "/* ---- Presets. Apply by setting data-palette on the document root. ---- */",
  ];

  for (const p of presets) {
    lines.push("", block(`:root[data-palette="${p.id}"]`, tokensFor(p, "light")));
  }
  lines.push("", `@media (prefers-color-scheme: dark) {`);
  for (const p of presets) {
    lines.push(
      `  :root:not([data-theme="light"])[data-palette="${p.id}"] {`,
      Object.entries(tokensFor(p, "dark"))
        .map(([k, v]) => `    ${k}: ${v};`)
        .join("\n"),
      `  }`,
    );
  }
  lines.push(`}`);
  for (const p of presets) {
    lines.push("", block(`:root[data-theme="dark"][data-palette="${p.id}"]`, tokensFor(p, "dark")));
  }
  return lines.join("\n") + "\n";
}
