#!/usr/bin/env bash
# Re-vendor alidade's contract.py as a test fixture.
#
# The hub types alidade's tag strings into Go source; this fixture is what
# server/api/contract_test.go checks them against. Nothing in the binary reads
# it.
#
# Usage:  tools/vendor-contract.sh [path-to-alidade-checkout]
set -euo pipefail

ENGINE="${1:-$HOME/workspace/alidade}"
SRC="$ENGINE/alidade/contract.py"
DEST="$(dirname "$0")/../server/api/testdata"

[ -f "$SRC" ] || { echo "no contract.py at $SRC" >&2; exit 1; }

REF="$(git -C "$ENGINE" rev-parse --short HEAD)"
cp "$SRC" "$DEST/contract.py"
SHA="$(shasum -a 256 "$DEST/contract.py" | cut -d' ' -f1)"

cat > "$DEST/contract-vendor.json" <<EOF
{
  "_comment": "Pinned engine ref that testdata/contract.py was vendored from, plus its sha256. This is a TEST FIXTURE, not shipped code: nothing in the binary reads it. To update: copy alidade/contract.py here, update both fields, run go test. See tools/vendor-contract.sh.",
  "vendored_from": "alidade@$REF",
  "sha256": "$SHA"
}
EOF

echo "vendored alidade@$REF"
echo "  sha256: $SHA"
echo "now run: (cd server && go test ./api/ -run TestContract)"
