/**
 * Guards the TypeScript types against the Go structs they describe.
 *
 * There is no runtime validation at the fetch boundary: `getJSON<Experiment[]>`
 * asserts a shape it never checks, so a TS interface is a *claim* about the API
 * rather than a fact about it. When the two disagree the compiler is satisfied
 * and the UI renders wrong.
 *
 * That is how `Experiment.duration` sat typed as `number` while every Go struct
 * sent a pre-formatted string. `formatDuration` guards with `isFinite`, which is
 * false for "4h 12m", so the home page showed an em-dash on every row and nothing
 * anywhere failed.
 *
 * This is a text-level check, not a parser. It is deliberately narrow: only
 * fields whose Go type is a plain string, which is where the silent-coercion risk
 * lives.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const read = (...p: string[]) => readFileSync(join(root, ...p), "utf8");

/** `Duration string \`json:"duration"\`` -> "duration" */
function goStringFields(src: string): Set<string> {
  const out = new Set<string>();
  for (const [, name] of src.matchAll(/^\s*\w+\s+string\s+`json:"([a-z_]+)[",]/gm)) {
    out.add(name);
  }
  return out;
}

/** `duration: number;` -> [name, type], skipping commented-out lines. */
function tsFields(src: string): [string, string][] {
  return [...src.matchAll(/^\s{2}([a-z_]+)\??:\s*([^;]+);/gm)].map(
    ([, name, type]) => [name, type.trim()] as [string, string],
  );
}

describe("the TS types match the Go structs", () => {
  const go = goStringFields(read("server", "api", "handlers.go"));
  const ts = tsFields(read("src", "lib", "types.ts"));

  it("finds fields on both sides", () => {
    // Guards the parser, not the types: a regex that stops matching would make
    // every assertion below vacuously true.
    assert.ok(go.size >= 5, `only ${go.size} Go string fields found — the pattern broke`);
    assert.ok(ts.length >= 20, `only ${ts.length} TS fields found — the pattern broke`);
    assert.ok(go.has("duration"), "duration is not among the Go string fields");
  });

  it("never types a Go string as a number", () => {
    const wrong = ts.filter(([name, type]) => go.has(name) && /^number(\s*\|\s*null)?$/.test(type));
    assert.deepEqual(
      wrong.map(([n, t]) => `${n}: ${t}`),
      [],
      "these fields are sent as strings by the Go API and typed as numbers here",
    );
  });
});
