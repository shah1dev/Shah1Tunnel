#!/usr/bin/env bash
set -Eeuo pipefail
# Compatibility: avoid Bash < 5.1 lastpipe job-table bug in installer process/wait handling.
shopt -u lastpipe 2>/dev/null || true
umask 077

# Shah1Tunnel installer. Downloads the manifest and all package artifacts,
# verifies every listed SHA-256 strictly, then runs the packaged installer.

TMPDIR=$(mktemp -d)
cleanup(){ rm -rf -- "$TMPDIR"; }
trap cleanup EXIT

BASE_RAW="https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main"
CURL_RETRIES=(--retry 3 --connect-timeout 20 --max-time 300)

echo "Shah1Tunnel installer — temporary workspace: $TMPDIR"

for cmd in curl sha256sum awk tar bash; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "Required command not found: $cmd" >&2; exit 1;
  }
done

mkdir -p "$TMPDIR/S1T-CAPSULE"

echo 'Downloading manifest and installer files...'
curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-CHECKS.sha256" -o "$TMPDIR/S1T-CHECKS.sha256" || {
  echo 'Failed to download S1T-CHECKS.sha256' >&2; exit 1;
}
curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-START.sh" -o "$TMPDIR/S1T-START.sh" || {
  echo 'Failed to download S1T-START.sh' >&2; exit 1;
}
curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-INFO-FA.txt" -o "$TMPDIR/S1T-INFO-FA.txt" || {
  echo 'Failed to download S1T-INFO-FA.txt' >&2; exit 1;
}

echo 'Downloading capsule archive...'
if curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-CAPSULE/S1T-SOURCE.tar.gz" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz"; then
  :
elif curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-CAPSULE/null" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz"; then
  :
else
  echo 'Capsule archive not found (tried S1T-CAPSULE/S1T-SOURCE.tar.gz and S1T-CAPSULE/null).' >&2
  exit 1
fi

# Prevent an empty or incomplete manifest from being treated as valid.
for expected in S1T-START.sh S1T-INFO-FA.txt S1T-CAPSULE/S1T-SOURCE.tar.gz; do
  if ! awk -v want="$expected" '$2 == want { found=1 } END { exit !found }' "$TMPDIR/S1T-CHECKS.sha256"; then
    echo "Checksum manifest is missing required entry: $expected" >&2
    exit 1
  fi
done

echo 'Verifying package integrity...'
if (cd -- "$TMPDIR" && sha256sum --strict -c S1T-CHECKS.sha256); then
  echo 'Integrity verified.'
else
  echo 'Checksum verification failed. Installation stopped; no package code was executed.' >&2
  exit 2
fi

# Check archive readability before invoking the runner. The runner performs
# its own strict checksum and archive-path checks as a second guard.
if ! tar -tzf "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" >/dev/null; then
  echo 'Capsule archive is invalid or corrupted.' >&2
  exit 2
fi

echo 'Extracting and running installer...'
( cd -- "$TMPDIR" && bash ./S1T-START.sh )
