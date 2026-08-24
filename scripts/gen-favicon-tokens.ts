/**
 * Emits the colours build-favicon.py bakes into each favicon.
 *
 * A bridge, not a source: the values come from the palette recipe, so a favicon
 * cannot drift from the UI it sits beside. Python cannot import the recipe, hence
 * the intermediate file.
 *
 *   node --experimental-strip-types scripts/gen-favicon-tokens.ts
 */

import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { DEFAULT_PRESET, PRESETS, tokensFor } from "../src/lib/palette-recipe.ts";

const presets = Object.fromEntries(
  PRESETS.map((p) => [
    p.id,
    (["light", "dark"] as const).reduce(
      (acc, mode) => {
        const t = tokensFor(p, mode);
        acc[mode] = { ink: t["--astro-mark-ink"], accent: t["--astro-accent"] };
        return acc;
      },
      {} as Record<string, { ink: string; accent: string }>,
    ),
  ]),
);

const out = join(dirname(fileURLToPath(import.meta.url)), "favicon-tokens.json");
writeFileSync(out, `${JSON.stringify({ default: DEFAULT_PRESET, presets }, null, 2)}\n`);
process.stdout.write(`${out}\n`);
