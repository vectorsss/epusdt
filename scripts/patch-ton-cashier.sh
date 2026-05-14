#!/usr/bin/env bash
# Re-applies the TON cashier patches after a fresh upstream www bundle.
#
# Upstream periodically vendors a new compiled SPA into src/www/. The
# JS chunks are content-hashed (e.g. _trade_id-Fw6UGEEp.js), so the
# filename of the cashier bundle changes on every re-vendor. This
# script locates the cashier bundle by content pattern, injects a TON
# entry into its chain metadata table (`ut`), and is idempotent — re-
# running on an already-patched bundle is a no-op.
#
# Run from the repo root after merging an upstream `update www` commit.

set -euo pipefail

cd "$(dirname "$0")/.."

CHAIN_ICON=src/www/images/chains/ton.png
if [[ ! -f $CHAIN_ICON ]]; then
  echo "error: missing $CHAIN_ICON" >&2
  echo "       restore it from git: git checkout HEAD -- $CHAIN_ICON" >&2
  exit 1
fi

# The cashier bundle is the JS chunk that owns the chain metadata table.
# Locate it by content pattern, not filename hash.
BUNDLE=$(grep -lE 'aliases:\[`binance`,`bnb_chain`\]' src/www/assets/_trade_id-*.js 2>/dev/null | head -1 || true)
if [[ -z $BUNDLE ]]; then
  echo "error: cashier bundle not found — upstream may have restructured the SPA" >&2
  echo "       look for the file containing the chain metadata table ('aliases:[\`binance\`...')" >&2
  exit 1
fi
echo "cashier bundle: $BUNDLE"

if grep -q 'label:`TON`' "$BUNDLE"; then
  echo "already patched — skipping"
  exit 0
fi

python3 - "$BUNDLE" <<'PY'
import sys
path = sys.argv[1]
with open(path) as f:
    src = f.read()
marker = "Binance:{aliases:[`binance`,`bnb_chain`],bg:`hsla(46,91%,49%,0.16)`,color:`hsla(46,91%,55%,1)`,icon:`binance`,label:`Binance`}"
ton = ",Ton:{aliases:[`ton`,`toncoin`],bg:`hsla(204,100%,50%,0.16)`,color:`hsla(204,100%,60%,1)`,icon:`ton`,label:`TON`}"
if marker not in src:
    sys.exit("error: Binance marker not found — bundle structure changed")
with open(path, "w") as f:
    f.write(src.replace(marker, marker + ton, 1))
print("injected TON entry")
PY

echo "done. Re-run go build to embed the patched bundle."
