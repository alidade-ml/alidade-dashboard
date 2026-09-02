"""
Builds the vendored font assets: cuts the wordmark face, collects the licences.

Cuts the wordmark face from Ysabeau.

Two changes to the upstream font, both permitted by the OFL (Ysabeau declares no
Reserved Font Name):

  * subset to the letters of ALIDADE, so the face is a few kB rather than 30
  * delete the crossbar from A, leaving a bare chevron

The crossbar is its own contour, so removing it is a delete rather than a
redraw, and the leg taper survives untouched. brand.md wants this: the mark's
letterform has no crossbar, and it cites the NASA worm as the precedent.

The family is renamed because a font whose A has no crossbar must not be
mistaken for Ysabeau. Run by hand; the output is committed.

    python3 scripts/build-fonts.py
"""

import pathlib
import sys

from fontTools.pens.ttGlyphPen import TTGlyphPen  # noqa: F401
from fontTools.subset import Subsetter
from fontTools.ttLib import TTFont
from fontTools.ttLib.tables._g_l_y_f import GlyphCoordinates

WORD = "ALIDADE"
FAMILY = "Alidade Wordmark"
SRC = pathlib.Path("node_modules/@fontsource/ysabeau/files/ysabeau-latin-500-normal.woff2")
OUT = pathlib.Path("src/brand/fonts/alidade-wordmark.woff2")
LICENSE_SRC = pathlib.Path("node_modules/@fontsource/ysabeau/LICENSE")
LICENSE_OUT = pathlib.Path("src/brand/fonts/alidade-wordmark.LICENSE.txt")

# OFL section 2 requires the licence to travel with every copy of the font, so
# it has to reach the built bundle. public/ is copied verbatim into dist/.
BUNDLED_LICENSES = pathlib.Path("public/FONT-LICENSES.txt")
VENDORED = ["ysabeau", "inter", "jetbrains-mono"]


def _strip_crossbar(font: TTFont, glyph_name: str) -> None:
    g = font["glyf"][glyph_name]
    g.expand(font["glyf"])
    if g.numberOfContours != 2:
        raise SystemExit(
            f"{glyph_name} has {g.numberOfContours} contours, expected 2. "
            "Upstream redrew the glyph; re-derive which contour is the crossbar "
            "before trusting this script."
        )
    ends = list(g.endPtsOfContours)
    keep = ends[0] + 1
    # Slicing GlyphCoordinates yields a plain list, which cannot recompute its
    # own bounds at compile time. Rebuild the wrapper.
    g.coordinates = GlyphCoordinates(list(g.coordinates)[:keep])
    g.flags = g.flags[:keep]
    g.endPtsOfContours = [ends[0]]
    g.numberOfContours = 1
    # Point indices moved, so any hinting that referenced them is now wrong.
    g.program.fromBytecode(b"")


def _rename(font: TTFont, family: str) -> None:
    ps = family.replace(" ", "")
    for record in font["name"].names:
        nid = record.nameID
        if nid in (1, 16):
            record.string = family
        elif nid in (4, 18):
            record.string = family
        elif nid == 6:
            record.string = ps
        elif nid == 3:
            record.string = f"{ps};custom"


def _collect_licenses() -> None:
    parts = [
        "Fonts bundled with the alidade dashboard.\n",
        "Every face below is licensed under the SIL Open Font License 1.1,\n"
        "which permits commercial use, modification and redistribution.\n",
    ]
    for name in VENDORED:
        src = pathlib.Path("node_modules/@fontsource") / name / "LICENSE"
        parts.append(f"\n{'=' * 72}\n@fontsource/{name}\n{'=' * 72}\n")
        parts.append(src.read_text())
    parts.append(f"\n{'=' * 72}\n{FAMILY}\n{'=' * 72}\n")
    parts.append(LICENSE_OUT.read_text())
    BUNDLED_LICENSES.parent.mkdir(parents=True, exist_ok=True)
    BUNDLED_LICENSES.write_text("".join(parts))


def main() -> None:
    if not SRC.exists():
        raise SystemExit(f"{SRC} not found — run npm install first")

    font = TTFont(SRC)
    _strip_crossbar(font, "A")

    sub = Subsetter()
    sub.populate(text=WORD + " ")
    sub.subset(font)

    _rename(font, FAMILY)
    font.flavor = "woff2"
    OUT.parent.mkdir(parents=True, exist_ok=True)
    font.save(OUT)

    # OFL section 2: every copy must carry the licence.
    LICENSE_OUT.write_text(
        f"{FAMILY} is a modified subset of Ysabeau.\n"
        "Changes: subset to the letters of ALIDADE; the crossbar removed from A.\n"
        "Renamed so it is not mistaken for the original. The Ysabeau authors do\n"
        "not endorse this modification.\n\n" + LICENSE_SRC.read_text()
    )

    _collect_licenses()

    glyphs = len(font.getGlyphOrder())
    sys.stdout.write(f"{OUT}  ({OUT.stat().st_size} bytes, {glyphs} glyphs)\n")
    sys.stdout.write(f"{LICENSE_OUT}\n")
    sys.stdout.write(f"{BUNDLED_LICENSES}  ({BUNDLED_LICENSES.stat().st_size} bytes)\n")


if __name__ == "__main__":
    main()
