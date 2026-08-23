/**
 * Regenerates src/brand/palette.generated.css.
 *
 * Run by hand, output committed, so the CSS a reviewer reads is the CSS that
 * ships. Not a build step.
 *
 *   node --experimental-strip-types scripts/gen-palette.ts
 */

import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { emitCss } from "../src/lib/palette-recipe.ts";

const out = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "src",
  "brand",
  "palette.generated.css",
);
writeFileSync(out, emitCss());
process.stdout.write(`${out}\n`);
