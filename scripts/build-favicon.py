"""
Derives public/favicon.svg from src/brand/mark.svg.

A favicon is loaded outside the page, so it cannot read the document's custom
properties and cannot follow the palette. Its colours are therefore baked to the
default preset, and a viewer on another palette gets a favicon that does not
match their UI. That is a limitation of favicons, not a bug here.

It can still follow light/dark, via a prefers-color-scheme block inside the SVG,
which Chrome and Firefox honour for SVG favicons.

Run by hand when the mark changes; the output is committed.

    python3 scripts/build-favicon.py
"""

import pathlib
import re
import sys

SRC = pathlib.Path("src/brand/mark.svg")
OUT = pathlib.Path("public/favicon.svg")

# Prussian, matching DEFAULT_PRESET in src/lib/palette-recipe.ts.
LIGHT = {"ink": "#171F28", "accent": "#3165A0"}
DARK = {"ink": "#B8C2CD", "accent": "#547DAD"}

STYLE = """  <style>
    :root {{ --astro-ink: {li}; --astro-accent: {la}; }}
    @media (prefers-color-scheme: dark) {{
      :root {{ --astro-ink: {di}; --astro-accent: {da}; }}
    }}
  </style>
"""


def main() -> None:
    svg = SRC.read_text()

    # The fallbacks in mark.svg are brass. Anything reading this file outside a
    # browser that supports the style block should still get the default preset.
    svg = svg.replace("var(--astro-ink, #241D12)", f"var(--astro-ink, {LIGHT['ink']})")
    svg = svg.replace("var(--astro-accent, #865900)", f"var(--astro-accent, {LIGHT['accent']})")

    style = STYLE.format(li=LIGHT["ink"], la=LIGHT["accent"], di=DARK["ink"], da=DARK["accent"])
    svg = re.sub(r"(<svg[^>]*>\n)", r"\1" + style, svg, count=1)

    if "--astro-ink:" not in svg:
        raise SystemExit("style block was not inserted — the <svg> open tag did not match")

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(svg)
    sys.stdout.write(f"{OUT}  ({OUT.stat().st_size} bytes)\n")


if __name__ == "__main__":
    main()
