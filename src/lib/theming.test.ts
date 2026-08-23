/**
 * Guards that rendered components take their colour from tokens.
 *
 * Two rules, both of which fail invisibly:
 *
 *   * A hardcoded colour looks correct on whichever palette and theme the
 *     author had open, and wrong everywhere else.
 *   * The accent must never be rendered as a chip or a dot. Status colours are
 *     drawn from brand.md's hues, so every preset shares its hue with some
 *     status role. They stay distinguishable only because accent and status
 *     never share a form.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const read = (...p: string[]) => readFileSync(join(root, ...p), "utf8");

/** Data modules, not rendered surface: fixtures, seeds, the categorical trace
    palette, and the recipe that defines the tokens. Colour is their payload
    rather than their styling. */
const DATA_MODULES = [
  join("src", "lib", "api.ts"),
  join("src", "lib", "seed-data.ts"),
  join("src", "lib", "sample-fixtures.ts"),
  join("src", "lib", "sample-generator.ts"),
  join("src", "lib", "palette-recipe.ts"),
  // The mark's #fff/#000 are mask fills, where the values carry coverage rather
  // than colour, and its var() fallbacks are required by brand.md. A stricter
  // rule for it lives in brand-mark.test.ts: no hardcoded paint on any part
  // that actually renders.
  join("src", "components", "brand-mark.tsx"),
];

const renderedFiles = () => {
  const out: string[] = [];
  const walk = (dir: string[]) => {
    for (const e of readdirSync(join(root, ...dir), { withFileTypes: true })) {
      if (e.isDirectory() && e.name !== "node_modules") walk([...dir, e.name]);
      else if (/\.tsx?$/.test(e.name) && !e.name.includes(".test.")) out.push(join(...dir, e.name));
    }
  };
  walk(["src"]);
  return out.filter((f) => !DATA_MODULES.includes(f));
};

describe("rendered components read tokens", () => {
  it("paints no hardcoded colour", () => {
    const offenders: string[] = [];
    for (const rel of renderedFiles()) {
      // recharts writes #ccc and #fff into its own DOM; chart.tsx matches those
      // in selectors to map them onto tokens. Matching a colour is the fix, not
      // the problem, so attribute selectors are exempt.
      const src = read(rel).replace(/\[stroke='#[0-9a-f]{3,6}'\]|\[fill='#[0-9a-f]{3,6}'\]/gi, "");
      const hits = src.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
      if (hits.length) offenders.push(`${rel}: ${hits.join(", ")}`);
    }
    assert.deepEqual(
      offenders,
      [],
      `hardcoded colour in rendered components:\n${offenders.join("\n")}`,
    );
  });

  it("names no Tailwind colour-scale utility", () => {
    const scale =
      /\b(?:text|bg|border|ring|fill|stroke|from|to|via)-(?:slate|zinc|gray|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}\b/g;
    const offenders: string[] = [];
    for (const rel of renderedFiles()) {
      const hits = read(rel).match(scale) ?? [];
      if (hits.length) offenders.push(`${rel}: ${hits.join(", ")}`);
    }
    assert.deepEqual(offenders, [], `off-palette utility:\n${offenders.join("\n")}`);
  });

  it("takes the trace palette from the server, not the bundled fallback", () => {
    // Two failures, found one after the other. cost-page first carried a
    // verbatim copy of the first six colours; replacing that with the bundled
    // constant fixed the duplication and not the divergence, because the
    // experiment page fetches the palette and the constant is only the offline
    // fallback. On a NUC whose colors.json differs from the shipped default the
    // two pages coloured the same submitter differently.
    //
    // Only the hook may read the fallback.
    const offenders: string[] = [];
    for (const rel of renderedFiles()) {
      if (rel.endsWith(join("hooks", "use-chart-palette.ts"))) continue;
      if (read(rel).includes("DEFAULT_CHART_PALETTE")) offenders.push(rel);
    }
    assert.deepEqual(
      offenders,
      [],
      `these read the fallback directly instead of useChartPalette:\n${offenders.join("\n")}`,
    );

    const cost = read("src", "components", "cost-page.tsx");
    assert.doesNotMatch(cost, /\[\s*"#[0-9A-Fa-f]{6}"/, "cost-page declares its own palette array");
    assert.match(cost, /useChartPalette\(\)/, "cost-page does not use the shared hook");
  });
});

describe("the accent is never a chip or a dot", () => {
  it("keeps state badges off the accent", () => {
    const badge = read("src", "components", "state-badge.tsx");
    const tone = badge.slice(
      badge.indexOf("TONE_CLASS"),
      badge.indexOf("interface StateBadgeProps"),
    );
    for (const token of ["--primary", "--astro-accent", "bg-primary", "text-primary"]) {
      assert.ok(!tone.includes(token), `state badges reference ${token}`);
    }
  });

  it("keeps status dots off the accent", () => {
    const dot = read("src", "components", "status-dot.tsx");
    const tone = dot.slice(dot.indexOf("TONE_BG"), dot.indexOf("interface Props"));
    for (const token of ["--primary", "--astro-accent", "bg-primary"]) {
      assert.ok(!tone.includes(token), `status dots reference ${token}`);
    }
  });
});
