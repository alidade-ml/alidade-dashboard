/**
 * Guards on how the mark is rendered.
 *
 * The constraint that matters: the mark must be inlined. It reads --astro-ink
 * and --astro-accent, and an <img> has no cascade, so a URL-referenced mark
 * looks correct on the default palette and wrong on the other four — a failure
 * nobody notices until someone switches palette.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const read = (...p: string[]) => readFileSync(join(root, ...p), "utf8");

const sourceFiles = () => {
  const out: string[] = [];
  const walk = (dir: string[]) => {
    for (const e of readdirSync(join(root, ...dir), { withFileTypes: true })) {
      if (e.isDirectory() && e.name !== "node_modules") walk([...dir, e.name]);
      // Tests are excluded: this file names the very strings it scans for.
      else if (/\.(tsx?|css)$/.test(e.name) && !e.name.includes(".test.")) {
        out.push(join(...dir, e.name));
      }
    }
  };
  walk(["src"]);
  return out;
};

describe("the mark", () => {
  const mark = read("src", "components", "brand-mark.tsx");

  it("is inlined, not referenced as a URL", () => {
    assert.match(mark, /<svg/, "BrandMark does not render an svg element");
    for (const rel of sourceFiles()) {
      const src = read(rel);
      assert.doesNotMatch(
        src,
        /url\([^)]*mark\.svg|src=\{?["'][^"']*mark\.svg/,
        `${rel} loads the mark by URL; it would not theme`,
      );
    }
  });

  it("reads the brand tokens for every painted part", () => {
    // A vendored copy that hardcoded hex would look right on exactly one palette.
    assert.ok(mark.includes("var(--astro-accent"), "the crescent does not read the accent");
    assert.ok(mark.includes("var(--astro-ink"), "the ink parts do not read the ink token");
    const hardcoded = mark.match(/(?:fill|stroke)="#(?!fff|000)[0-9A-Fa-f]{3,6}"/g) ?? [];
    assert.deepEqual(hardcoded, [], `hardcoded colour on a painted part: ${hardcoded.join(", ")}`);
  });

  it("falls back to the values brand.md publishes", () => {
    // Invisible until the tokens are missing, which is exactly when it matters.
    assert.ok(mark.includes("var(--astro-ink, #241D12)"), "ink fallback is not brass light ink");
    assert.ok(
      mark.includes("var(--astro-accent, #865900)"),
      "accent fallback is not brass light accent",
    );
  });

  it("keeps the masks that produce the figure/ground flip", () => {
    // Drop either and the letter hides behind the crescent instead of cutting
    // through it. This shipped as a real bug in an earlier round of the mark.
    for (const id of ["astro-crescent", "astro-offmass", "astro-channel"]) {
      assert.ok(mark.includes(`id="${id}"`), `mask ${id} is missing`);
    }
    assert.ok(
      mark.includes('mask="url(#astro-offmass)"'),
      "the ink letter is not masked off the mass",
    );
  });

  it("imports no motion", () => {
    // brand.md: decks only, nothing animates in the product.
    for (const rel of sourceFiles()) {
      assert.doesNotMatch(read(rel), /motion\.css/, `${rel} imports motion.css`);
    }
  });

  it("ships a favicon that does not depend on page tokens", () => {
    const fav = read("public", "favicon.svg");
    assert.match(fav, /--astro-ink:\s*#/, "favicon has no baked ink value");
    assert.match(fav, /prefers-color-scheme: dark/, "favicon does not follow the OS theme");
    assert.match(read("index.html"), /rel="icon"[^>]*favicon\.svg/, "favicon is not linked");
  });
});
