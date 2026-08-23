/**
 * Tests for resolving and locating a stored palette choice.
 *
 * localStorage is user-writable and outlives releases, so the stored value is
 * untrusted input: it can be absent, empty, garbage, or the name of a preset that
 * has since been renamed. Stamping an unknown value on the root is worse than
 * ignoring it — the stylesheet has no block for it, so the page silently renders
 * the bare :root instead of the palette the user picked.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { DEFAULT_PRESET, PRESETS } from "./palette-recipe.ts";
import {
  PALETTE_STORAGE_KEY,
  faviconHref,
  paletteFromStorageEvent,
  resolvePalette,
} from "./palette.ts";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("resolvePalette", () => {
  for (const bad of [null, undefined, "", " ", "chartreuse", "PRUSSIAN", "brass ", "0", "[]"]) {
    it(`falls back to the default for ${JSON.stringify(bad)}`, () => {
      assert.equal(resolvePalette(bad as string | null), DEFAULT_PRESET);
    });
  }

  it("keeps a value that is a real preset", () => {
    for (const p of PRESETS) assert.equal(resolvePalette(p.id), p.id);
  });

  it("returns something the generated stylesheet has a block for", () => {
    // The failure this prevents: a resolver that passes its input through would
    // stamp data-palette="chartreuse" and render the bare :root, which looks like
    // the default and is not.
    for (const bad of ["chartreuse", null]) {
      assert.ok(
        PRESETS.some((p) => p.id === resolvePalette(bad as string | null)),
        `resolved to something with no [data-palette] block: ${bad}`,
      );
    }
  });
});

describe("the favicon each palette points at", () => {
  it("names a file that exists for every preset", () => {
    // A missing favicon does not 404: spaFallback serves index.html with a 200,
    // so the browser gets HTML where it asked for an image and shows nothing.
    for (const p of PRESETS) {
      const href = faviconHref(p.id);
      assert.match(href, /^\/favicons\/[a-z]+\.svg$/, `unexpected href shape: ${href}`);
      assert.ok(
        existsSync(join(root, "public", href.replace(/^\//, ""))),
        `${href} does not exist in public/`,
      );
    }
  });

  it("gives each preset its own file", () => {
    assert.equal(new Set(PRESETS.map((p) => faviconHref(p.id))).size, PRESETS.length);
  });
});

describe("storage key", () => {
  it("does not collide with the theme key", () => {
    assert.notEqual(PALETTE_STORAGE_KEY, "astrolabe-theme");
  });
});

describe("a storage event from another tab", () => {
  it("ignores keys that are not the palette", () => {
    // Both providers listen on the same window. Reacting to the theme key would
    // reset the palette to the default every time someone toggled dark mode.
    for (const key of ["astrolabe-theme", "", null, "astrolabe-palette-old"]) {
      assert.equal(paletteFromStorageEvent(key, "brass"), null, `reacted to ${key}`);
    }
  });

  it("adopts a palette another tab wrote", () => {
    assert.equal(paletteFromStorageEvent(PALETTE_STORAGE_KEY, "oxide"), "oxide");
  });

  it("falls back when another tab wrote something unusable", () => {
    // Includes the clear-site-data case, where newValue is null.
    assert.equal(paletteFromStorageEvent(PALETTE_STORAGE_KEY, null), DEFAULT_PRESET);
    assert.equal(paletteFromStorageEvent(PALETTE_STORAGE_KEY, "chartreuse"), DEFAULT_PRESET);
  });
});

describe("one definition of the default", () => {
  it("is not redeclared outside the recipe", () => {
    const src = readFileSync(join(root, "src", "lib", "palette.ts"), "utf8");
    assert.doesNotMatch(
      src,
      /export const DEFAULT_(PRESET|PALETTE)\s*[:=]/,
      "the default is declared a second time; two constants that must agree will drift",
    );
  });
});

describe("providers read stored state lazily", () => {
  /*
   * A structural guard, not a behavioural one — this repo has no DOM test setup,
   * so the actual regression was caught by reloading the page and reading
   * localStorage. What it pins is the shape that was wrong.
   *
   * Both providers used to seed state with the default and read localStorage in a
   * mount effect. The write effect then ran with the default still in state and
   * persisted it over the stored choice, so neither the theme nor the palette
   * survived a reload. The theme toggle had shipped that way.
   *
   * Passing the reader to useState removes the ordering question rather than
   * reordering it.
   */
  const providers: [string, string, string][] = [
    ["theme", join("src", "hooks", "theme-provider.tsx"), "getInitial"],
    ["palette", join("src", "hooks", "palette-provider.tsx"), "readStoredPalette"],
  ];

  for (const [name, path, reader] of providers) {
    it(`${name}: seeds state from the reader, not a constant`, () => {
      const src = readFileSync(join(root, path), "utf8");
      assert.ok(
        src.includes(`useState<Theme>(${reader})`) || src.includes(`useState<PresetId>(${reader})`),
        `${name} does not pass ${reader} to useState as a lazy initialiser`,
      );
    });

    it(`${name}: does not re-read storage in a mount effect`, () => {
      const src = readFileSync(join(root, path), "utf8");
      const mountEffects = [...src.matchAll(/useEffect\(\(\) => \{([\s\S]*?)\}, \[\]\);/g)].map(
        (m) => m[1],
      );
      for (const body of mountEffects) {
        assert.ok(
          !/getItem|getInitial|readStoredPalette/.test(body),
          `${name} reads stored state in a mount effect, which races the write effect`,
        );
      }
    });
  }
});
