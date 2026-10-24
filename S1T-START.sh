#!/usr/bin/env bash
set -Eeuo pipefail

# Downloads only the compiled binary and verifies it before installation.
BASE_URL="https://raw.githubusercontent.com/shah1dev/ShahTunnel/main"
VERSION="1.3.9"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "Run as root: bash <(curl -fsSL ${BASE_URL}/S1T-START.sh)" >&2
  exit 1
fi
case "$(uname -s):$(uname -m)" in
  Linux:x86_64|Linux:amd64) ;;
  *) echo "Shah Tunnel ${VERSION} targets Linux x86_64/amd64." >&2; exit 1 ;;
esac
command -v systemctl >/dev/null 2>&1 || { echo "systemd/systemctl is required." >&2; exit 1; }
for cmd in curl sha256sum awk install; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "Required command missing: $cmd" >&2; exit 1; }
done

TMP="$(mktemp -d /tmp/s1t-install.XXXXXX)"
chmod 0700 "$TMP"
cleanup() { rm -rf -- "$TMP"; }
trap cleanup EXIT

printf '\n=== Shah Tunnel %s | Installer ===\n' "$VERSION"
printf '[1/5] Downloading tunnel binary...\n'
curl -fLsS --retry 3 --connect-timeout 10 "$BASE_URL/S1T-CAPSULE/S1T-CORE" -o "$TMP/S1T-CORE"
curl -fLsS --retry 3 --connect-timeout 10 "$BASE_URL/S1T-CHECKSUM.sha256" -o "$TMP/S1T-CHECKSUM.sha256"
EXPECTED="$(tr -d '[:space:]' < "$TMP/S1T-CHECKSUM.sha256")"
ACTUAL="$(sha256sum "$TMP/S1T-CORE" | awk '{print $1}')"
[[ "$EXPECTED" =~ ^[a-fA-F0-9]{64}$ && "$ACTUAL" == "$EXPECTED" ]] || {
  echo 'Checksum verification failed; the executable was not installed.' >&2
  exit 2
}
chmod 0750 "$TMP/S1T-CORE"
"$TMP/S1T-CORE" version | grep -Fq "Shah Tunnel ${VERSION} (linux/amd64)" || {
  echo 'Downloaded binary version did not match the installer.' >&2; exit 2;
}
echo '[2/5] Checksum and version verified.'

printf '[3/5] Installing binary...\n'
install -d -o root -g root -m 0700 /etc/ravanreverse
install -d -o root -g root -m 0755 /usr/local/bin
install -o root -g root -m 0750 "$TMP/S1T-CORE" /usr/local/bin/ravanreverse.new
mv -f /usr/local/bin/ravanreverse.new /usr/local/bin/ravanreverse
ln -sfn /usr/local/bin/ravanreverse /usr/local/bin/shah-tunnel

printf '[4/5] Installing service...\n'
cat > "$TMP/ravanreverse.service" <<'UNIT'
[Unit]
Description=Shah Tunnel authenticated reverse TCP tunnel
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=10

[Service]
Type=simple
UMask=0077
ExecStart=/usr/local/bin/ravanreverse run
Restart=always
RestartSec=2
TimeoutStopSec=20
LimitNOFILE=262144
TasksMax=4096
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectHostname=true
ProtectClock=true
ProtectProc=invisible
ProcSubset=pid
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
SystemCallArchitectures=native
ReadWritePaths=/etc/ravanreverse

[Install]
WantedBy=multi-user.target
UNIT
install -o root -g root -m 0644 "$TMP/ravanreverse.service" /etc/systemd/system/ravanreverse.service
systemctl daemon-reload

if [[ -f /etc/ravanreverse/config.json ]]; then
  systemctl enable ravanreverse >/dev/null
  if systemctl is-active --quiet ravanreverse; then
    systemctl restart ravanreverse
  else
    systemctl reset-failed ravanreverse >/dev/null 2>&1 || true
    systemctl start ravanreverse
  fi
  if ! systemctl is-active --quiet ravanreverse; then
    echo 'Service failed to start. Recent logs:' >&2
    journalctl -u ravanreverse -n 40 --no-pager >&2 || true
    exit 1
  fi
  echo 'Tunnel service updated; existing config remains intact.'
else
  echo 'No saved tunnel configuration found. Complete setup from the menu next.'
fi

printf '[5/5] Cleaning temporary files and opening menu...\n'
cleanup
trap - EXIT
exec /usr/local/bin/shah-tunnel
