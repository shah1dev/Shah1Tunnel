#!/usr/bin/env bash
set -euo pipefail

# Shah1Tunnel simple installer (safe branch)
# Downloads the packaged files from this repository's safe/null-archive-and-uninstall-guidance branch,
# verifies checksums and runs the packaged S1T-START.sh from a temporary workspace.

TMPDIR=$(mktemp -d)
cleanup(){ rm -rf -- "$TMPDIR"; }
trap cleanup EXIT

BASE_RAW="https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/safe/null-archive-and-uninstall-guidance"

echo "Shah1Tunnel installer — fetching package into $TMPDIR"
mkdir -p "$TMPDIR/S1T-CAPSULE"

files=(S1T-CHECKS.sha256 S1T-START.sh S1T-INFO-FA.txt)
for f in "${files[@]}"; do
  url="$BASE_RAW/$f"
  echo " - downloading $f"
  curl -fsSL "$url" -o "$TMPDIR/$f"
done

echo " - downloading capsule archive"
curl -fsSL "$BASE_RAW/S1T-CAPSULE/null" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz"

echo "Verifying package integrity..."
(cd "$TMPDIR" && sha256sum --status -c S1T-CHECKS.sha256) || { echo "Checksum verification failed." >&2; exit 2; }

echo "Integrity ok. Running the packaged installer (S1T-START.sh)."
# Run the packaged installer from the temporary directory
bash "$TMPDIR/S1T-START.sh"
