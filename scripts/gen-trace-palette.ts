/**
 * Writes the categorical trace palette to the file the Go server serves.
 *
 * One target, not two. The TypeScript consumers import tracePalette() directly
 * and cannot drift; the wheel's copy is produced by build_hook.py, which wipes
 * astrolabe_dashboard/config/ and re-copies server/config/ at build time, so it
 * is derived rather than maintained. Writing there would only dirty a staging
 * directory that is gitignored and rebuilt.
 *
 *   node --experimental-strip-types scripts/gen-trace-palette.ts
 */

import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { tracePalette } from "../src/lib/trace-palette.ts";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const target = join(root, "server", "config", "colors.json");
writeFileSync(target, `${JSON.stringify({ palette: tracePalette() }, null, 2)}\n`);
process.stdout.write(`${target}\n`);
