#!/usr/bin/env bash
set -Eeuo pipefail

# Shah1Tunnel packaged runner
# Extracts the capsule, searches recursively for a packaged installer entrypoint
# and runs it. This is defensive: some capsules contain a single top-level
# directory (e.g. S1T_<base64>...), so search recursively instead of only
# looking at the root.

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CHECKS="$HERE/S1T-CHECKS.sha256"
ARCHIVE="$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz"

err(){ printf '[Shah1Tunnel] ERROR: %s\n' "$*" >&2; }
info(){ printf '[Shah1Tunnel] %s\n' "$*"; }

[[ -f "$CHECKS" && -f "$ARCHIVE" && -f "$HERE/S1T-INFO-FA.txt" ]] || { err 'package files are missing.'; exit 2; }
(command -v tar >/dev/null 2>&1) || { err 'tar is required.'; exit 2; }
command -v base64 >/dev/null 2>&1 || { err 'base64 is required.'; exit 2; }

# Workspace allocation similar to original script
WORK=''
for base in "${WAT_TMPDIR:-}" /var/tmp /root/.cache/shah1tun /tmp; do
  [[ -n "$base" ]] || continue
  mkdir -p -- "$base" 2>/dev/null || continue
  [[ -d "$base" && -w "$base" ]] || continue
  avail=$(df -Pk -- "$base" 2>/dev/null | awk 'NR==2 {print $4+0}') || continue
  [[ "$avail" =~ ^[0-9]+$ ]] || continue
  (( avail >= 16384 )) || continue
  WORK=$(mktemp -d "$base/s1t-r9k4.XXXXXX" 2>/dev/null) && break
  WORK=''
done
[[ -n "$WORK" && -d "$WORK" ]] || { err 'cannot allocate workspace with at least 16 MiB free; checked WAT_TMPDIR, /var/tmp, /root/.cache/shah1tun and /tmp.'; exit 2; }

cleanup(){ local rc=$?; rm -rf -- "$WORK"; return "$rc"; }
trap cleanup EXIT

info "Using workspace: $WORK"
info 'Extracting package...'
if ! tar -xzf "$ARCHIVE" -C "$WORK"; then
  err 'failed to extract capsule archive; it may be corrupted'
  exit 2
fi

# Search order for entrypoint (prefer explicit names in this order)
candidates=()
# search for exact names first (prefer top-level occurrences)
while IFS= read -r -d $'\0' f; do candidates+=("$f"); done < <(find "$WORK" -maxdepth 3 -type f \( -name 'S1T-START.sh' -o -name 'install.sh' -o -name 'setup.sh' \) -print0)
# then any executable shell scripts anywhere
if [ "${#candidates[@]}" -eq 0 ]; then
  while IFS= read -r -d $'\0' f; do
    # prefer executable ones first
    if [ -x "$f" ]; then
      candidates+=("$f")
    else
      candidates+=("$f")
    fi
  done < <(find "$WORK" -type f -name '*.sh' -print0)
fi

# If still none, consider top-level single directory: list it for debugging
if [ "${#candidates[@]}" -eq 0 ]; then
  err 'No installer entrypoint found inside the capsule.'
  info 'Listing extracted files for debugging:'
  ( cd "$WORK" && ls -la )
  exit 2
fi

# Prefer S1T-START.sh in root of extracted tree
entry=''
for f in "${candidates[@]}"; do
  base=$(basename "$f")
  # prefer exact names in this priority
  case "$base" in
    S1T-START.sh) entry="$f"; break ;;
    install.sh) entry="$f"; break ;;
    setup.sh) entry="$f"; break ;;
  esac
done
# fallback to first candidate
if [ -z "$entry" ]; then entry="${candidates[0]}"; fi

info "Running packaged installer: $(realpath -- "$entry")"
chmod +x "$entry" 2>/dev/null || true
( cd "$WORK" && bash "$entry" ) || { err 'packaged installer failed'; exit 2; }

info 'Packaged installer finished.'
exit 0
