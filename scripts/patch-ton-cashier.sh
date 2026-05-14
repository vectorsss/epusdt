#!/usr/bin/env bash
# Re-applies the TON cashier patches after a fresh upstream www bundle.
#
# Upstream periodically vendors a new compiled SPA into src/www/. The
# JS chunks are content-hashed (e.g. _trade_id-Fw6UGEEp.js), so the
# cashier-bundle filename changes on every re-vendor. This script
# locates the cashier bundle by content pattern, applies two patches,
# and is idempotent — re-running on an already-patched bundle is a
# no-op.
#
# Patches:
#   1. Inject a Ton entry into the hardcoded chain metadata table
#      (`ut`) so network=ton renders with the brand name + chain logo.
#   2. Make the token <img> route TON directly to a locally-shipped
#      icon at /images/tokens/ton.png. The upstream bundle hard-codes
#      atomiclabs CDN which lacks ton.png; an onError fallback
#      flickers on every dropdown open, so we short-circuit at src
#      assignment for the known-missing symbols.
#   3. Bump the workbox precache revision for the patched JS so
#      browsers that already cached the unpatched bundle re-fetch it
#      on next visit instead of serving stale.
#
# Run from the repo root after merging an upstream `update www` commit.

set -euo pipefail

cd "$(dirname "$0")/.."

require_file() {
  local f=$1
  if [[ ! -f $f ]]; then
    echo "error: missing $f — restore with: git checkout HEAD -- $f" >&2
    exit 1
  fi
}
require_file src/www/images/chains/ton.png
require_file src/www/images/tokens/ton.png

# Find the cashier bundle by content pattern, not by filename hash.
BUNDLE=$(grep -lE 'aliases:\[`binance`,`bnb_chain`\]' src/www/assets/_trade_id-*.js 2>/dev/null | head -1 || true)
if [[ -z $BUNDLE ]]; then
  echo "error: cashier bundle not found — upstream may have restructured the SPA" >&2
  echo "       look for the JS chunk containing the chain metadata table ('aliases:[\`binance\`...')" >&2
  exit 1
fi
echo "cashier bundle: $BUNDLE"

python3 - "$BUNDLE" <<'PY'
import sys
path = sys.argv[1]
with open(path) as f:
    src = f.read()
changed = False

# Patch 1: inject Ton into the chain metadata table.
marker_chain = "Binance:{aliases:[`binance`,`bnb_chain`],bg:`hsla(46,91%,49%,0.16)`,color:`hsla(46,91%,55%,1)`,icon:`binance`,label:`Binance`}"
ton_chain = ",Ton:{aliases:[`ton`,`toncoin`],bg:`hsla(204,100%,50%,0.16)`,color:`hsla(204,100%,60%,1)`,icon:`ton`,label:`TON`}"
if "label:`TON`" in src:
    print("chain-table patch: already present")
elif marker_chain in src:
    src = src.replace(marker_chain, marker_chain + ton_chain, 1)
    print("chain-table patch: injected")
    changed = True
else:
    sys.exit("error: chain-table anchor missing — bundle structure changed")

# Patch 2: route specific tokens (currently just TON) directly to
# the local icon path, bypassing the atomiclabs CDN entirely. The
# previous attempt used onError as a runtime fallback, but that
# flickers every time the dropdown re-renders an item, since the
# bad CDN request runs first. Short-circuiting at src assignment
# eliminates the flicker.
#
# Two-stage so re-runs from any prior state converge cleanly:
#   stage 1: strip any leftover onError fallback from earlier runs
#   stage 2: replace the plain src with a conditional that picks
#            the local path for known-missing tokens.

prior_onerror = (
    ",onError:t=>{t.currentTarget.onerror=null;"
    "t.currentTarget.src=`/images/tokens/${e.toLowerCase()}.png`}"
)
if prior_onerror in src:
    src = src.replace(prior_onerror, "", 1)
    print("img patch: stripped prior onError fallback")
    changed = True

local_short_circuit_marker = "[`ton`].includes(e.toLowerCase())"
plain_src = "src:`${lt}${e.toLowerCase()}.png`,width:16"
conditional_src = (
    "src:[`ton`].includes(e.toLowerCase())"
    "?`/images/tokens/${e.toLowerCase()}.png`"
    ":`${lt}${e.toLowerCase()}.png`,width:16"
)
if local_short_circuit_marker in src:
    print("img patch: conditional src already present")
elif plain_src in src:
    src = src.replace(plain_src, conditional_src, 1)
    print("img patch: conditional src injected")
    changed = True
else:
    sys.exit("error: img src anchor missing — bundle structure changed")

if changed:
    with open(path, "w") as f:
        f.write(src)
    print("file updated")
else:
    print("no changes; already patched")
PY

# Patch 3: bump the service-worker precache revision for the patched
# JS so already-cached browsers refresh it. Filename is content-
# hashed but the *content* changed without the URL changing, which
# would otherwise let workbox serve stale.
python3 - "$BUNDLE" <<'PY'
import re, sys, hashlib, pathlib
bundle = pathlib.Path(sys.argv[1]).name  # e.g. _trade_id-Fw6UGEEp.js
sw_path = "src/www/sw.js"
sw_src = open(sw_path).read()
target_url = f"assets/{bundle}"
content_hash = hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest()[:12]
new_revision = f'"ton-patch-{content_hash}"'

# Match the precache entry for our bundle.
pattern = re.compile(
    r'\{url:"' + re.escape(target_url) + r'",revision:(null|"[^"]+")\}'
)
m = pattern.search(sw_src)
if not m:
    sys.exit(f"error: sw.js precache entry for {target_url} not found")

current = m.group(1)
if current == new_revision:
    print(f"sw.js precache revision: already at {new_revision}")
else:
    new_entry = m.group(0).replace(f"revision:{current}", f"revision:{new_revision}")
    open(sw_path, "w").write(sw_src.replace(m.group(0), new_entry, 1))
    print(f"sw.js precache revision: {current} -> {new_revision}")
PY

echo "done. Re-run go build to embed the patched bundle."
