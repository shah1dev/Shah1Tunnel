#!/usr/bin/env bash
set -Eeuo pipefail

# Shah1Tunnel packaged runner.
# Verifies the downloaded/local package, extracts it into a private temporary
# workspace, restores S1T_<base64url> filenames, then runs the real installer.

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CHECKS="$HERE/S1T-CHECKS.sha256"
ARCHIVE="$HERE/S1T-CAPSULE/S1T-SOURCE.tar.gz"

err(){ printf '[Shah1Tunnel] ERROR: %s\n' "$*" >&2; }
info(){ printf '[Shah1Tunnel] %s\n' "$*"; }

[[ -f "$CHECKS" && -f "$ARCHIVE" && -f "$HERE/S1T-INFO-FA.txt" ]] || {
  err 'package files are missing.'; exit 2;
}
for cmd in tar base64 sha256sum find mv mktemp df awk; do
  command -v "$cmd" >/dev/null 2>&1 || { err "required command not found: $cmd"; exit 2; }
done

# Validate the package manifest itself before trusting its result.
for expected in S1T-START.sh S1T-INFO-FA.txt S1T-CAPSULE/S1T-SOURCE.tar.gz; do
  if ! awk -v want="$expected" '$2 == want { found=1 } END { exit !found }' "$CHECKS"; then
    err "checksum manifest is missing entry: $expected"; exit 2
  fi
done
if ! (cd -- "$HERE" && sha256sum --status -c S1T-CHECKS.sha256); then
  err 'package checksum verification failed; nothing was executed.'
  exit 2
fi
info 'Package checksums verified.'

# Workspace allocation: prefer disk-backed paths and require at least 16 MiB.
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
[[ -n "$WORK" && -d "$WORK" ]] || {
  err 'cannot allocate workspace with at least 16 MiB free; checked WAT_TMPDIR, /var/tmp, /root/.cache/shah1tun and /tmp.'
  exit 2
}
chmod 700 "$WORK"
cleanup(){ local rc=$?; rm -rf -- "$WORK"; return "$rc"; }
trap cleanup EXIT

info "Using workspace: $WORK"
info 'Checking archive paths...'
if ! tar -tzf "$ARCHIVE" >/dev/null 2>&1; then
  err 'capsule archive is corrupt or unreadable.'; exit 2
fi
while IFS= read -r member; do
  case "$member" in
    /*|../*|*/../*|*/..)
      err "unsafe path found in capsule archive: $member"; exit 2 ;;
  esac
done < <(tar -tzf "$ARCHIVE")

info 'Extracting package...'
if ! tar -xzf "$ARCHIVE" -C "$WORK"; then
  err 'failed to extract capsule archive.'; exit 2
fi

# Filenames/directories inside the capsule are stored as S1T_<base64url>.
# Decode deepest paths first so nested paths remain valid while renaming.
decode_name() {
  local encoded="$1" normalized padding decoded
  [[ "$encoded" == S1T_* ]] || return 1
  normalized="${encoded#S1T_}"
  normalized="${normalized//-/+}"
  normalized="${normalized//_/\/}"
  while (( ${#normalized} % 4 != 0 )); do normalized+='='; done
  decoded=$(printf '%s' "$normalized" | base64 --decode 2>/dev/null) || return 1
  [[ -n "$decoded" && "$decoded" != '.' && "$decoded" != '..' ]] || return 1
  [[ "$decoded" != */* && "$decoded" != *$'\n'* && "$decoded" != *$'\r'* ]] || return 1
  printf '%s' "$decoded"
}

while IFS= read -r -d '' path; do
  name=${path##*/}
  [[ "$name" == S1T_* ]] || continue
  decoded=$(decode_name "$name") || {
    err "could not decode capsule path name: $name"; exit 2;
  }
  parent=${path%/*}
  [[ "$parent" != "$path" ]] || parent="$WORK"
  target="$parent/$decoded"
  if [[ -e "$target" || -L "$target" ]]; then
    err "filename collision while restoring capsule: $decoded"; exit 2
  fi
  mv -- "$path" "$target" || { err "could not restore capsule path: $name"; exit 2; }
done < <(find "$WORK" -depth -mindepth 1 -print0)

# Confirm the expected source layout instead of accidentally running an arbitrary script.
root="$WORK/Shah1Tunnel-Core"
[[ -d "$root" && -f "$root/install.sh" && -f "$root/setup.sh" && \
   -f "$root/compile.sh" && -f "$root/go.mod" && \
   -f "$root/cmd/shah1tun/main.go" && -f "$root/cmd/s1probe/main.go" ]] || {
  err 'capsule layout is incomplete after filename restoration.'
  info 'Top-level extracted entries:'
  find "$WORK" -mindepth 1 -maxdepth 2 -print | sed -n '1,80p'
  exit 2
}

info 'Capsule paths restored and required files verified.'
info 'Running packaged installer...'
( cd -- "$root" && bash ./install.sh ) || {
  err 'packaged installer failed; see the error above.'; exit 2;
}
info 'Packaged installer finished.'
