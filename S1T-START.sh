#!/usr/bin/env bash
set -Eeuo pipefail

# Simplified Shah1Tunnel package runner
# This trimmed replacement ensures the downloaded installer archive can be
# safely extracted and any packaged installer inside the capsule executed.

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CHECKS="$HERE/S1T-CHECKS.sha256"
ARCHIVE="$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz"

err(){ printf '[Shah1Tunnel] ERROR: %s\n' "$*" >&2; }
info(){ printf '[Shah1Tunnel] %s\n' "$*"; }

[[ -f "$CHECKS" && -f "$ARCHIVE" && -f "$HERE/S1T-INFO-FA.txt" ]] || { err 'package files are missing.'; exit 2; }
(command -v tar >/dev/null 2>&1) || { err 'tar is required.'; exit 2; }

WORKDIR=""
cleanup(){ local rc=$?; if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then rm -rf -- "$WORKDIR"; fi; exit "$rc"; }
trap cleanup EXIT

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/shah1tun.XXXXXX") || { err 'cannot create workspace'; exit 2; }
info "Using workspace: $WORKDIR"

info 'Extracting package...'
if ! tar -xzf "$ARCHIVE" -C "$WORKDIR"; then
  err 'failed to extract capsule archive; it may be corrupted'; exit 2
fi

# Look for common installer entrypoints in the extracted tree.
ENTRY='' 
if [[ -f "$WORKDIR/install.sh" ]]; then
  ENTRY="$WORKDIR/install.sh"
elif [[ -f "$WORKDIR/S1T-START.sh" ]]; then
  ENTRY="$WORKDIR/S1T-START.sh"
elif [[ -f "$WORKDIR/setup.sh" ]]; then
  ENTRY="$WORKDIR/setup.sh"
fi

# If no explicit installer, try to find any executable shell script at top-level
if [[ -z "$ENTRY" ]]; then
  for f in "$WORKDIR"/*.sh; do
    [[ -f "$f" && -x "$f" ]] || continue
    ENTRY="$f"
    break
  done
fi

if [[ -n "$ENTRY" ]]; then
  info "Running packaged installer: $(basename "$ENTRY")"
  chmod +x "$ENTRY" || true
  ( cd "$WORKDIR" && bash "$ENTRY" ) || { err 'packaged installer failed'; exit 2; }
  info 'Packaged installer finished.'
  exit 0
fi

# No installer found; show extracted tree and exit with non-zero
err 'No installer entrypoint found inside the capsule.'
info 'Listing extracted files for debugging:'
( cd "$WORKDIR" && ls -la )
exit 2
