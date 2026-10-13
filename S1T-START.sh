#!/usr/bin/env bash
set -Eeuo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
fail() { printf '[Shah1Tunnel] ERROR: %s\n' "$*" >&2; exit 2; }
CHECKS="$HERE/S1T-CHECKS.sha256"
ARCHIVE="$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz"
[[ -f "$CHECKS" && -f "$ARCHIVE" && -f "$HERE/S1T-INFO-FA.txt" ]] || fail 'package files are missing.'
(cd "$HERE" && sha256sum --status -c S1T-CHECKS.sha256) || fail 'package integrity check failed; nothing was executed.'
command -v tar >/dev/null 2>&1 || fail 'tar is required.'
command -v base64 >/dev/null 2>&1 || fail 'base64 is required.'
# Use a disk-backed workspace for the source capsule; /tmp may be a small tmpfs.
WORK=''
for base in "${WAT_TMPDIR:-}" /var/tmp /root/.cache/shah1tun /tmp; do
  [[ -n "$base" ]] || continue
  mkdir -p -- "$base" 2>/dev/null || continue
  [[ -d "$base" && -w "$base" ]] || continue
  avail=$(df -Pk -- "$base" 2>/dev/null | awk 'NR==2 {print $4+0}')
  [[ "$avail" =~ ^[0-9]+$ ]] || continue
  (( avail >= 16384 )) || continue
  WORK=$(mktemp -d "$base/s1t-r9k4.XXXXXX" 2>/dev/null) && break
  WORK=''
done
[[ -n "$WORK" && -d "$WORK" ]] || fail 'cannot allocate workspace with at least 16 MiB free; checked WAT_TMPDIR, /var/tmp, /root/.cache/shah1tun and /tmp.'
# Cleanup the extracted package files and its exact matching ZIP after setup exits.
# The ZIP is matched by its embedded checksum manifest; unrelated ZIPs/files are untouched.
PACKAGE_CLEANUP=0
PACKAGE_ZIP=''
cleanup() {
  local rc=$?
  rm -rf -- "$WORK"
  if [[ "$PACKAGE_CLEANUP" == 1 ]]; then
    if [[ -n "$PACKAGE_ZIP" && -f "$PACKAGE_ZIP" ]]; then
      rm -f -- "$PACKAGE_ZIP"
      printf '%s\n' '[Shah1Tunnel] Removed the installer ZIP from /root; no installer archive is left there.'
    fi
    # Remove only files belonging to this package, then remove its directory only if empty.
    if [[ -n "$HERE" && "$HERE" != / && "$HERE" != /root && -d "$HERE" && \
          -f "$HERE/S1T-START.sh" && -f "$HERE/S1T-CHECKS.sha256" && \
          -f "$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz" ]]; then
      rm -f -- "$HERE/S1T-START.sh" "$HERE/S1T-INFO-FA.txt" "$HERE/S1T-CHECKS.sha256"
      rm -f -- "$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz"
      rmdir -- "$HERE/S1T-CAPSULE" 2>/dev/null || true
      rmdir -- "$HERE" 2>/dev/null || true
      printf '%s\n' '[Shah1Tunnel] Removed extracted installer files; unrelated files/directories were preserved.'
    fi
  fi
  return "$rc"
}
trap cleanup EXIT
 tar -xzf "$ARCHIVE" --no-same-owner -C "$WORK" || fail 'source capsule could not be unpacked.'
while IFS= read -r -d '' p; do
  name="${p##*/}"
  [[ "$name" == S1T_* ]] || continue
  token="${name#S1T_}"; token="${token//-/+}"; token="${token//_/\/}"
  while (( ${#token} % 4 != 0 )); do token+="="; done
  decoded="$(printf '%s' "$token" | base64 --decode 2>/dev/null)" || fail 'invalid encoded source path.'
  [[ -n "$decoded" && "$decoded" != . && "$decoded" != .. && "$decoded" != */* && "$decoded" != *$'\n'* ]] || fail 'unsafe decoded source path.'
  dest="${p%/*}/$decoded"
  [[ ! -e "$dest" && ! -L "$dest" ]] || fail 'source filename collision detected.'
  mv -- "$p" "$dest"
done < <(find "$WORK" -mindepth 1 -depth -print0)
SRC="$WORK/Shah1Tunnel-Core"
[[ -f "$SRC/setup.sh" && -f "$SRC/install.sh" && -f "$SRC/build.sh" && -f "$SRC/compile.sh" && -f "$SRC/go.mod" && -f "$SRC/cmd/shah1tun/main.go" && -f "$SRC/cmd/s1probe/main.go" && -f "$SRC/shah1tun-manager" && -f "$SRC/wattun-manager" && -f "$SRC/LICENSE" && -f "$SRC/docs/TECHNICAL.md" && -f "$SRC/docs/img/banner.jpg" && -f "$SRC/.github/workflows/release.yml" ]] || fail 'complete source tree check failed; no installation was started.'
printf '%s\n' '[Shah1Tunnel] Package checksum verified.' '[Shah1Tunnel] Branded manager and complete reference source restored; installation uses the official release binary.' '[Shah1Tunnel] Engine: official WatTunnel v2.4.12 release; the pinned SHA-256 is checked by the installer.'
export S1T_PACKAGE_DIR="$HERE"
# Identify the ZIP whose embedded integrity manifest matches this extracted package.
# Full removal keeps only that recovery archive among Shah1Tunnel ZIPs in /root.
S1T_KEEP_ZIP=''
if command -v unzip >/dev/null 2>&1; then
  shopt -s nullglob
  for candidate in /root/*.zip /root/*.ZIP; do
    [[ -f "$candidate" ]] || continue
    if unzip -p "$candidate" S1T-CHECKS.sha256 2>/dev/null | cmp -s - "$HERE/S1T-CHECKS.sha256"; then
      S1T_KEEP_ZIP="$candidate"
      PACKAGE_ZIP="$candidate"
      break
    fi
  done
fi
export S1T_KEEP_ZIP
# Do not keep the extracted package as the process working directory: option 5 can remove it safely.
cd /
PACKAGE_CLEANUP=1
bash "$SRC/setup.sh" "$@"
rc=$?
exit "$rc"
