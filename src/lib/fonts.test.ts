/**
 * Guards on how the dashboard gets its typeface.
 *
 * The NUC is frequently offline and the Go binary serves a missing asset as the
 * SPA shell with a 200, so a font that fails to arrive degrades silently to the
 * system stack with nothing logged anywhere. That is the bug this repo already
 * shipped once, by naming "Inter" in a token and never loading it.
 *
 * These are cheap source-level checks. The end-to-end proof — real bytes over
 * HTTP from the Go binary — lives in server/cmd/static_test.go.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const read = (...p: string[]) => readFileSync(join(root, ...p), "utf8");

const cssFiles = () => {
  const out: string[] = [];
  const walk = (dir: string[]) => {
    for (const e of readdirSync(join(root, ...dir), { withFileTypes: true })) {
      if (e.isDirectory() && e.name !== "node_modules") walk([...dir, e.name]);
      else if (e.name.endsWith(".css")) out.push(join(...dir, e.name));
    }
  };
  walk(["src"]);
  return out;
};

describe("fonts are self-hosted", () => {
  it("declares a display, body and mono face", () => {
    const styles = read("src", "styles.css");
    for (const token of ["--font-display", "--font-sans", "--font-mono"]) {
      assert.match(styles, new RegExp(`${token}:`), `${token} is not declared`);
    }
  });

  it("names a face for every declared family", () => {
    // A token whose first entry is a generic keyword means the brand face was
    // dropped and nothing would look wrong locally.
    const styles = read("src", "styles.css");
    for (const token of ["--font-display", "--font-sans", "--font-mono"]) {
      const decl = styles.slice(styles.indexOf(`${token}:`));
      const value = decl.slice(decl.indexOf(":") + 1, decl.indexOf(";"));
      assert.match(value.trim(), /^"[^"]+"/, `${token} does not start with a quoted family name`);
    }
  });

  it("loads every declared family from a package, not a URL", () => {
    const fonts = read("src", "brand", "fonts.css");
    for (const family of ["ysabeau", "inter", "jetbrains-mono"]) {
      assert.ok(
        fonts.includes(`@fontsource/${family}/`),
        `${family} is declared but never imported`,
      );
    }
  });

  it("declares faces directly, never by importing a fontsource stylesheet", () => {
    // Those stylesheets list a .woff beside every .woff2. Vite emits the
    // fallback whether or not anything fetches it, and nothing that can run this
    // app lacks woff2 — it was 200kB of a 1.5MB bundle, larger than the woff2
    // files it backed up.
    const fonts = read("src", "brand", "fonts.css");
    assert.doesNotMatch(
      fonts,
      /@import\s+["']@fontsource/,
      "importing a fontsource stylesheet drags the .woff fallback back into the bundle",
    );
    assert.match(fonts, /@font-face/, "no faces are declared");
    assert.doesNotMatch(fonts, /\.woff["')]/, "a .woff is referenced directly");
  });

  it("references no external host from any stylesheet", () => {
    for (const rel of cssFiles()) {
      const css = read(rel);
      const hits = css.match(/@import\s+url\(|https?:\/\/|\/\/fonts\./g) ?? [];
      assert.deepEqual(hits, [], `${rel} reaches off-host: ${hits.join(", ")}`);
    }
  });

  it("cuts the wordmark face from a local file, not a package", () => {
    // The crossbar-less A is a modified subset and must never be confused with
    // upstream Ysabeau, nor leak into body text.
    const fonts = read("src", "brand", "fonts.css");
    assert.match(fonts, /font-family:\s*"Astrolabe Wordmark"/);
    assert.match(fonts, /url\(\.\/fonts\/astrolabe-wordmark\.woff2\)/);

    const styles = read("src", "styles.css");
    const display = styles.slice(styles.indexOf("--font-display:"));
    assert.doesNotMatch(
      display.slice(0, display.indexOf(";")),
      /Astrolabe Wordmark/,
      "the wordmark face leaked into --font-display; body text would lose its crossbars",
    );
  });

  it("ships the font licences with the bundle", () => {
    // OFL section 2: every copy of the font must carry the licence. public/ is
    // copied verbatim into dist/.
    const text = read("public", "FONT-LICENSES.txt");
    assert.match(text, /SIL Open Font License/);
    for (const family of ["ysabeau", "inter", "jetbrains-mono"]) {
      assert.ok(text.includes(`@fontsource/${family}`), `no licence for ${family}`);
    }
    assert.ok(text.includes("Astrolabe Wordmark"), "no licence for the modified cut");
  });

  it("loads no font from index.html", () => {
    // The classic regression: a <link> to Google Fonts that works on the
    // developer's laptop and silently no-ops on the NUC.
    const html = read("index.html");
    assert.doesNotMatch(html, /fonts\.googleapis|fonts\.gstatic|<link[^>]+font/i);
  });
});
