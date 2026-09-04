/**
 * Fail the build when a stylesheet in dist/ points at a file that isn't there.
 *
 * Vite resolves `url()` against the module graph. When the target is missing it
 * emits the reference verbatim and exits 0, so the CSS ships a relative path
 * that resolves nowhere. The Go binary answers that request with the SPA shell
 * and a 200, the browser rejects the HTML and falls back to the system stack,
 * and nothing is logged on either side. The wordmark face shipped broken for
 * weeks that way.
 */

import { readdirSync, readFileSync } from "node:fs";
import { join, posix, relative, sep } from "node:path";

export interface MissingAsset {
  stylesheet: string;
  url: string;
  resolved: string;
}

const EXTERNAL = /^(data:|https?:|\/\/|#)/;

/** Every `url()` target a stylesheet expects to find in the same bundle. */
export function bundledUrls(css: string): string[] {
  const found: string[] = [];
  for (const [, , raw] of css.matchAll(/url\(\s*(['"]?)([^'")]+)\1\s*\)/g)) {
    const url = raw.trim();
    if (!url || EXTERNAL.test(url)) continue;
    found.push(url.replace(/[?#].*$/, ""));
  }
  return found;
}

/**
 * @param stylesheets dist-relative path to source, POSIX separators
 * @param present every dist-relative path that shipped, POSIX separators
 */
export function missingBundledAssets(
  stylesheets: Map<string, string>,
  present: Set<string>,
): MissingAsset[] {
  const missing: MissingAsset[] = [];
  for (const [stylesheet, css] of stylesheets) {
    for (const url of bundledUrls(css)) {
      const resolved = url.startsWith("/")
        ? url.slice(1)
        : posix.normalize(posix.join(posix.dirname(stylesheet), url));
      if (!present.has(resolved)) missing.push({ stylesheet, url, resolved });
    }
  }
  return missing;
}

function walk(root: string, dir = root): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory()
      ? walk(root, join(dir, e.name))
      : [relative(root, join(dir, e.name)).split(sep).join(posix.sep)],
  );
}

export function assertBundledAssets(outDir = "dist") {
  return {
    name: "assert-bundled-assets",
    apply: "build" as const,
    // closeBundle, not generateBundle: public/ is copied verbatim and never
    // enters the bundle, so a url() into it would look missing until the
    // directory is on disk.
    closeBundle() {
      const files = walk(outDir);
      const stylesheets = new Map(
        files
          .filter((f) => f.endsWith(".css"))
          .map((f) => [f, readFileSync(join(outDir, f), "utf8")] as const),
      );
      const missing = missingBundledAssets(stylesheets, new Set(files));
      if (missing.length === 0) return;
      const lines = missing.map(
        (m) => `  ${m.stylesheet} → url(${m.url}) resolves to ${outDir}/${m.resolved}, absent`,
      );
      throw new Error(
        `${missing.length} stylesheet reference(s) point outside the bundle:\n${lines.join("\n")}`,
      );
    },
  };
}
