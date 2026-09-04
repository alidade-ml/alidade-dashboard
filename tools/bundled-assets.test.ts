/**
 * The build must exit non-zero when a stylesheet names an asset that did not
 * ship. Nothing downstream can catch this: the Go handler answers a missing
 * asset with the SPA shell and a 200 (server/cmd/static_test.go), so the page
 * renders and only the typeface is wrong.
 */

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { assertBundledAssets, bundledUrls, missingBundledAssets } from "./bundled-assets.ts";

const face = (url: string) => `@font-face{font-family:X;src:url(${url}) format("woff2")}`;

describe("bundledUrls", () => {
  it("ignores targets that were never meant to be in the bundle", () => {
    const css = [
      "a{background:url(data:font/woff2;base64,d09GMgAB)}",
      "b{background:url(https://fonts.gstatic.com/s/x.woff2)}",
      "c{background:url(//cdn.example.com/x.woff2)}",
      "d{clip-path:url(#circle)}",
    ].join("");
    assert.deepEqual(bundledUrls(css), []);
  });

  it("drops the cache-buster so the name matches what shipped", () => {
    assert.deepEqual(bundledUrls(face("./x.woff2?v=3")), ["./x.woff2"]);
    assert.deepEqual(bundledUrls(face("./x.eot#iefix")), ["./x.eot"]);
  });

  it("reads a target through either quoting style", () => {
    assert.deepEqual(bundledUrls(face('"./a.woff2"')), ["./a.woff2"]);
    assert.deepEqual(bundledUrls(face("'./b.woff2'")), ["./b.woff2"]);
    assert.deepEqual(bundledUrls(face("  ./c.woff2  ")), ["./c.woff2"]);
  });
});

describe("missingBundledAssets", () => {
  // The shape Vite emits when it cannot resolve the target: the source path,
  // unhashed, relative to a stylesheet that lives in assets/.
  it("reports a relative target that resolves outside the bundle", () => {
    const missing = missingBundledAssets(
      new Map([["assets/index-abc.css", face("./brand/fonts/alidade-wordmark.woff2")]]),
      new Set(["assets/index-abc.css", "index.html"]),
    );
    assert.equal(missing.length, 1);
    assert.equal(missing[0].resolved, "assets/brand/fonts/alidade-wordmark.woff2");
  });

  it("resolves a relative target against its own stylesheet, not the root", () => {
    // Same url() from two stylesheets is two different files.
    const css = face("./x.woff2");
    assert.deepEqual(
      missingBundledAssets(
        new Map([["assets/deep/a.css", css]]),
        new Set(["assets/deep/a.css", "assets/x.woff2"]),
      ).map((m) => m.resolved),
      ["assets/deep/x.woff2"],
    );
  });

  it("resolves a root-relative target against the bundle root", () => {
    const css = face("/assets/y.woff2");
    assert.deepEqual(
      missingBundledAssets(new Map([["assets/a.css", css]]), new Set(["assets/y.woff2"])),
      [],
    );
    assert.equal(
      missingBundledAssets(new Map([["assets/a.css", css]]), new Set(["assets/a.css"])).length,
      1,
    );
  });

  it("accepts a target that is copied verbatim rather than bundled", () => {
    // public/ never enters the module graph; it is still in dist/.
    assert.deepEqual(
      missingBundledAssets(
        new Map([["assets/a.css", face("/legacy.woff2")]]),
        new Set(["legacy.woff2"]),
      ),
      [],
    );
  });

  it("passes a bundle whose every reference resolves", () => {
    assert.deepEqual(
      missingBundledAssets(
        new Map([["assets/a.css", face("./a-hash.woff2") + face("/b.woff2")]]),
        new Set(["assets/a-hash.woff2", "b.woff2"]),
      ),
      [],
    );
  });
});

describe("the plugin against a directory on disk", () => {
  const bundle = (files: Record<string, string>) => {
    const dir = mkdtempSync(join(tmpdir(), "dist-"));
    for (const [rel, body] of Object.entries(files)) {
      mkdirSync(join(dir, rel, ".."), { recursive: true });
      writeFileSync(join(dir, rel), body);
    }
    return dir;
  };

  it("throws, naming the stylesheet and the path it wanted", () => {
    const dir = bundle({ "assets/index.css": face("./fonts/wordmark.woff2") });
    assert.throws(
      () => assertBundledAssets(dir).closeBundle(),
      (e: Error) => {
        assert.match(e.message, /assets\/index\.css/);
        assert.match(e.message, /assets\/fonts\/wordmark\.woff2/);
        return true;
      },
    );
  });

  it("stays silent when the referenced file is there", () => {
    const dir = bundle({
      "assets/index.css": face("./wordmark-hash.woff2"),
      "assets/wordmark-hash.woff2": "wOF2",
    });
    assert.doesNotThrow(() => assertBundledAssets(dir).closeBundle());
  });

  it("only runs on a build, so `vite dev` is untouched", () => {
    assert.equal(assertBundledAssets("dist").apply, "build");
  });
});
