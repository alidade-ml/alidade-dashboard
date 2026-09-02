"""
Derives one favicon per palette from src/brand/mark.svg, and points
public/favicon.svg at the default preset's.

A favicon loads outside the document, so it cannot read the page's custom
properties and cannot follow a palette chosen at runtime. Baking one file per
preset is what makes it possible for a palette control to swap the <link> href
later; until then the build simply selects the default.

It can still follow light and dark, via a prefers-color-scheme block inside the
SVG, which Chrome and Firefox honour for SVG favicons.

Colours come from scripts/favicon-tokens.json, emitted by the palette recipe, so
they cannot drift from the UI. Regenerate both when the recipe changes:

    node --experimental-strip-types scripts/gen-favicon-tokens.ts
    python3 scripts/build-favicon.py
"""

import json
import pathlib
import re
import shutil
import sys

SRC = pathlib.Path("src/brand/mark.svg")
TOKENS = pathlib.Path("scripts/favicon-tokens.json")
OUT_DIR = pathlib.Path("public/favicons")
DEFAULT_OUT = pathlib.Path("public/favicon.svg")

STYLE = """  <style>
    :root {{ --alidade-mark-ink: {li}; --alidade-accent: {la}; }}
    @media (prefers-color-scheme: dark) {{
      :root {{ --alidade-mark-ink: {di}; --alidade-accent: {da}; }}
    }}
  </style>
"""


def _render(svg: str, light: dict, dark: dict) -> str:
    # mark.svg falls back to brass. Re-point the fallbacks at this preset so the
    # file is still right if the style block is ignored.
    svg = svg.replace("var(--alidade-mark-ink, #241D12)", f"var(--alidade-mark-ink, {light['ink']})")
    svg = svg.replace("var(--alidade-accent, #865900)", f"var(--alidade-accent, {light['accent']})")
    style = STYLE.format(li=light["ink"], la=light["accent"], di=dark["ink"], da=dark["accent"])
    out = re.sub(r"(<svg[^>]*>\n)", r"\1" + style, svg, count=1)
    if "--alidade-mark-ink:" not in out:
        raise SystemExit("style block was not inserted - the <svg> open tag did not match")
    return out


def main() -> None:
    tokens = json.loads(TOKENS.read_text())
    svg = SRC.read_text()
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    for name, modes in tokens["presets"].items():
        path = OUT_DIR / f"{name}.svg"
        path.write_text(_render(svg, modes["light"], modes["dark"]))
        sys.stdout.write(f"{path}\n")

    default = tokens["default"]
    shutil.copyfile(OUT_DIR / f"{default}.svg", DEFAULT_OUT)
    sys.stdout.write(f"{DEFAULT_OUT}  (= {default})\n")


if __name__ == "__main__":
    main()
