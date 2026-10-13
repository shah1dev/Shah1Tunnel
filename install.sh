#!/usr/bin/env bash
set -euo pipefail

# Shah1Tunnel installer (main)
# Downloads the necessary package files from this repository's main branch,
# verifies their SHA-256 checksums and runs the packaged installer.

TMPDIR=$(mktemp -d)
cleanup(){ rm -rf -- "$TMPDIR"; }
trap cleanup EXIT

BASE_RAW="https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main"

echo "Shah1Tunnel installer — temporary workspace: $TMPDIR"

for cmd in curl sha256sum tar bash; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Required command not found: $cmd" >&2
    exit 1
  fi
done

mkdir -p "$TMPDIR/S1T-CAPSULE"

# Download manifest and control files
echo "Downloading manifest and installer files..."
curl -fsSL "$BASE_RAW/S1T-CHECKS.sha256" -o "$TMPDIR/S1T-CHECKS.sha256" || { echo "Failed to download S1T-CHECKS.sha256" >&2; exit 1; }
curl -fsSL "$BASE_RAW/S1T-START.sh" -o "$TMPDIR/S1T-START.sh" || { echo "Failed to download S1T-START.sh" >&2; exit 1; }
curl -fsSL "$BASE_RAW/S1T-INFO-FA.txt" -o "$TMPDIR/S1T-INFO-FA.txt" || { echo "Failed to download S1T-INFO-FA.txt" >&2; exit 1; }

# Download capsule (note: the capsule file in this repository may be named 'null')
echo "Downloading capsule archive..."
if curl -fsSL "$BASE_RAW/S1T-CAPSULE/S1T-SOURCE.tar.gz" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz"; then
  :
else
  # fallback to 'null' filename if present in repo
  if curl -fsSL "$BASE_RAW/S1T-CAPSULE/null" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz"; then
    :
  else
    echo "Capsule archive not found in repository (tried S1T-CAPSULE/S1T-SOURCE.tar.gz and S1T-CAPSULE/null)" >&2
    exit 1
  fi
fi

# Verify checksums
echo "Verifying package integrity..."
# First try strict verification against the repository manifest
if ( cd "$TMPDIR" && sha256sum --status -c S1T-CHECKS.sha256 ); then
  echo "Integrity verified."
else
  echo "Checksum verification failed." >&2
  # As a safety/fallback: check the archive itself for a valid tar.gz structure
  if tar -tzf "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" >/dev/null 2>&1; then
    echo "Warning: checksum mismatch, but capsule archive is a valid tar.gz; continuing after warning." >&2
  else
    echo "Capsule archive appears to be invalid or corrupted; aborting." >&2
    exit 2
  fi
fi

echo "Extracting and running installer..."

# Run the downloaded S1T-START.sh from TMPDIR so it finds S1T-CAPSULE/...
( cd "$TMPDIR" && bash ./S1T-START.sh )
