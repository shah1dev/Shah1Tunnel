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
GITHUB_API="https://api.github.com/repos/shah1dev/Shah1Tunnel/commits/main"
CURL_RETRIES=(--retry 3 --connect-timeout 20 --max-time 300)
INSTALL_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd)" || INSTALL_DIR=''
SPACE_COLOR=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then SPACE_COLOR=1; fi
SC=''; SG=''; SR=''; SY=''
if [ "$SPACE_COLOR" = 1 ]; then SC=$'\e[1;36m'; SG=$'\e[1;32m'; SR=$'\e[0m'; SY=$'\e[1;35m'; fi
space_header() {
  printf '  %s╭%s╮%s\n' "$SC" '────────────────────────────────────────────────────────────────────────' "$SR"
  printf '  %s│%s %s%-70.70s%s %s│%s\n' "$SC" "$SR" "$SC" '✦ SHAH TUNNEL // SPACE INSTALLER · R9K4' "$SR" "$SC" "$SR"
  printf '  %s│%s %s%-70.70s%s %s│%s\n' "$SC" "$SR" "$SY" 'PREFLIGHT ▸ INTEGRITY ▸ DEPLOYMENT ▸ SYSTEMD' "$SR" "$SC" "$SR"
  printf '  %s╰%s╯%s\n' "$SC" '────────────────────────────────────────────────────────────────────────' "$SR"
}
space_info() { printf '  %s▸%s %s\n' "$SC" "$SR" "$*"; }
space_ok() { printf '  %s✔%s %s\n' "$SG" "$SR" "$*"; }
space_error() { printf '  %s✖ SPACE ERROR:%s %s\n' "$SY" "$SR" "$*" >&2; }

space_header
space_info "Bootstrap workspace: $TMPDIR"

for cmd in curl sha256sum awk tar bash sed; do
  command -v "$cmd" >/dev/null 2>&1 || {
    space_error "Required command not found: $cmd"; exit 1;
  }
done

mkdir -p "$TMPDIR/S1T-CAPSULE"

# A full extracted ZIP uses its checked-in capsule; the one-line remote bootstrap
# has no adjacent package and therefore downloads the current checked bundle.
if [[ -n "$INSTALL_DIR" && -s "$INSTALL_DIR/S1T-CHECKS.sha256" && \
      -s "$INSTALL_DIR/S1T-START.sh" && -s "$INSTALL_DIR/S1T-INFO-FA.txt" && \
      -s "$INSTALL_DIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" ]]; then
  space_info 'Local Shah Tunnel package detected; using the bundled SPACE interface.'
  cp -- "$INSTALL_DIR/S1T-CHECKS.sha256" "$TMPDIR/S1T-CHECKS.sha256" || { space_error 'Could not copy the local checksum manifest.'; exit 1; }
  cp -- "$INSTALL_DIR/S1T-START.sh" "$TMPDIR/S1T-START.sh" || { space_error 'Could not copy the local runner.'; exit 1; }
  cp -- "$INSTALL_DIR/S1T-INFO-FA.txt" "$TMPDIR/S1T-INFO-FA.txt" || { space_error 'Could not copy local package notes.'; exit 1; }
  cp -- "$INSTALL_DIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" || { space_error 'Could not copy the local source capsule.'; exit 1; }
else
  space_info '01/03 · Resolving one immutable package revision.'
  curl -fsSL "${CURL_RETRIES[@]}" -H 'Accept: application/vnd.github+json' "$GITHUB_API" -o "$TMPDIR/revision.json" || {
    space_error 'Could not resolve the GitHub commit for the online installer. Download the full ZIP, extract it, then run bash install.sh from the extracted folder.'; exit 1;
  }
  COMMIT=$(sed -nE 's/^[[:space:]]*"sha"[[:space:]]*:[[:space:]]*"([0-9a-f]{40})".*/\1/p' "$TMPDIR/revision.json" | sed -n '1p')
  [[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]] || {
    space_error 'GitHub did not return a valid 40-character commit SHA. Use the full ZIP and run its local install.sh.'; exit 1;
  }
  BASE_RAW="https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/$COMMIT"
  space_info "Pinned source revision: ${COMMIT:0:12}"
  space_info '01/03 · Downloading package manifest and startup files.'
  curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-CHECKS.sha256" -o "$TMPDIR/S1T-CHECKS.sha256" || {
    space_error 'Failed to download S1T-CHECKS.sha256'; exit 1;
  }
  curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-START.sh" -o "$TMPDIR/S1T-START.sh" || {
    space_error 'Failed to download S1T-START.sh'; exit 1;
  }
  curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-INFO-FA.txt" -o "$TMPDIR/S1T-INFO-FA.txt" || {
    space_error 'Failed to download S1T-INFO-FA.txt'; exit 1;
  }

  space_info '02/03 · Downloading the source capsule from the same commit.'
  curl -fsSL "${CURL_RETRIES[@]}" "$BASE_RAW/S1T-CAPSULE/S1T-SOURCE.tar.gz" -o "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" || {
    space_error 'Capsule archive could not be downloaded from the pinned revision.'; exit 1;
  }
fi

# Prevent an empty or incomplete manifest from being treated as valid.
for expected in S1T-START.sh S1T-INFO-FA.txt S1T-CAPSULE/S1T-SOURCE.tar.gz; do
  if ! awk -v want="$expected" '$2 == want { found=1 } END { exit !found }' "$TMPDIR/S1T-CHECKS.sha256"; then
    space_error "Checksum manifest is missing required entry: $expected"
    exit 1
  fi
done

space_info '03/03 · Verifying package checksums and archive integrity.'
if (cd -- "$TMPDIR" && sha256sum --strict --status -c S1T-CHECKS.sha256); then
  space_ok 'Package integrity verified.'
else
  echo 'Checksum verification failed. Mismatched files (if any) are listed below:' >&2
  (cd -- "$TMPDIR" && sha256sum --strict -c S1T-CHECKS.sha256) >&2 || true
  echo 'Installation stopped; no package code was executed.' >&2
  if [[ -n "$INSTALL_DIR" && -d "$INSTALL_DIR/S1T-CAPSULE" ]]; then
    echo 'If this was meant to be a local ZIP install, re-extract the full ZIP and run bash install.sh from its root folder.' >&2
  fi
  exit 2
fi

# Check archive readability before invoking the runner. The runner performs
# its own strict checksum and archive-path checks as a second guard.
if ! tar -tzf "$TMPDIR/S1T-CAPSULE/S1T-SOURCE.tar.gz" >/dev/null; then
  echo 'Capsule archive is invalid or corrupted.' >&2
  exit 2
fi

space_info 'Launching SPACE deployment sequence.'
( cd -- "$TMPDIR" && bash ./S1T-START.sh )
