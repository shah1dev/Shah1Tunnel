#!/usr/bin/env bash
set -euo pipefail
if [[ ${EUID:-$(id -u)} -ne 0 ]]; then echo 'Run as root: bash install.sh' >&2; exit 1; fi
arch="$(uname -m)"
if [[ "$arch" != x86_64 && "$arch" != amd64 ]]; then echo "This build targets Linux x86_64; detected: $arch" >&2; exit 1; fi
base="$(cd "$(dirname "$0")" && pwd)"
[[ -x "$base/ravanreverse" ]] || { echo "Missing executable: $base/ravanreverse" >&2; exit 1; }
command -v systemctl >/dev/null || { echo 'systemd/systemctl is required on this host' >&2; exit 1; }
install -d -o root -g root -m 0700 /etc/ravanreverse
install -o root -g root -m 0750 "$base/ravanreverse" /usr/local/bin/ravanreverse.new
mv -f /usr/local/bin/ravanreverse.new /usr/local/bin/ravanreverse
ln -sfn /usr/local/bin/ravanreverse /usr/local/bin/shah-tunnel
cat > /etc/systemd/system/ravanreverse.service.new <<'UNIT'
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
RestartSec=3
TimeoutStopSec=15
LimitNOFILE=65535
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
chmod 0644 /etc/systemd/system/ravanreverse.service.new
mv -f /etc/systemd/system/ravanreverse.service.new /etc/systemd/system/ravanreverse.service
systemctl daemon-reload
printf '\nShah Tunnel %s installed. Existing configuration is preserved.\n' "$(/usr/local/bin/ravanreverse version | awk '{print $3}')"
if [[ -f /etc/ravanreverse/config.json ]]; then
  systemctl enable ravanreverse >/dev/null
  if systemctl is-active --quiet ravanreverse; then
    systemctl restart ravanreverse
  else
    systemctl reset-failed ravanreverse >/dev/null 2>&1 || true
    systemctl start ravanreverse
  fi
  if ! systemctl is-active --quiet ravanreverse; then
    echo 'Shah Tunnel did not start. Recent logs:' >&2
    journalctl -u ravanreverse -n 40 --no-pager >&2 || true
    exit 1
  fi
  echo 'Existing tunnel configuration detected: service has been enabled and started/restarted.'
else
  echo 'No tunnel configuration found yet. The setup menu will open after cleanup.'
fi
printf 'Menu command: shah-tunnel\n'

# Remove the extracted release folder and archive only when launched from the
# canonical release ZIP layout. Never delete an arbitrary cloned source tree.
if [[ "$(basename "$base")" == "Shah-Tunnel-1.3.3" ]]; then
  parent="$(dirname "$base")"
  cd "$parent"
  rm -f -- "$parent/Shah-Tunnel-1.3.3-Linux-amd64.zip" "$parent/Shah-Tunnel-1.3.3-Linux-amd64.zip.sha256"
  rm -rf -- "$base"
  # The user requested no leftover tunnel installer bundles. Remove only known
  # historical release paths; leave the installed runtime and /etc config intact.
  rm -rf -- \
    /root/RavanReverse-1.0.0 /root/RavanReverse-1.1.0 /root/RavanReverse-1.2.0 \
    /root/RavanReverse-1.3.0 /root/RavanReverse-1.3.1 /root/RavanReverse-1.3.2
  rm -f -- \
    /root/RavanReverse-1.0.0-Linux-amd64.zip /root/RavanReverse-1.1.0-Linux-amd64.zip \
    /root/RavanReverse-1.2.0-Linux-amd64.zip /root/RavanReverse-1.3.0-Linux-amd64.zip \
    /root/RavanReverse-1.3.1-Linux-amd64.zip /root/RavanReverse-1.3.2-Linux-amd64.zip \
    /root/RavanReverse-1.1.0-Linux-amd64.zip.sha256 /root/RavanReverse-1.2.0-Linux-amd64.zip.sha256 \
    /root/RavanReverse-1.3.0-Linux-amd64.zip.sha256 /root/RavanReverse-1.3.1-Linux-amd64.zip.sha256 \
    /root/RavanReverse-1.3.2-Linux-amd64.zip.sha256
  echo 'Installer package and known historical installer files removed.'
fi

if [[ ! -f /etc/ravanreverse/config.json ]]; then
  exec /usr/local/bin/shah-tunnel
fi
