![Shah Tunnel](docs/img/shah-tunnel-logo.svg)

# Shah Tunnel — v1.3.4

**Language / زبان:** [🇬🇧 English](README.md) · [🇮🇷 فارسی](README-fa.md)

**Authenticated reverse TCP tunnel for Linux `amd64`** · Binary-only one-command installer · systemd managed

> Release: **1.3.4** — IP/country lookup now tries multiple providers, falls back to an IP-only lookup, and presents the country and public IP together in the terminal menu.

## Quick install

Run on an Ubuntu/Debian `x86_64` server as `root`:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shah1dev/ShahTunnel/main/S1T-START.sh)
```

The installer downloads `S1T-CAPSULE/S1T-CORE`, verifies its SHA-256 using `S1T-CHECKSUM.sha256`, installs the executable and systemd unit, preserves an existing `/etc/ravanreverse/config.json`, and opens the interactive menu. No ZIP or source archive is needed on the server. Requirements: root access, `curl`, `sha256sum`, and systemd.

Open the menu later with:

```bash
shah-tunnel
```

## Technical overview

Shah Tunnel forwards raw TCP connections between a public entry point and a destination reachable from the exit server. It is an application-level TCP tunnel, not a virtual-network interface or a general-purpose IP VPN.

```text
TCP client → Hub public listener → authenticated TLS connection → Agent → configured TCP target
```

- **Hub (entry):** listens on configured public TCP ports and receives the Agent's outbound connection.
- **Agent (exit):** initiates an authenticated connection to the Hub and opens the configured local target for each forwarded stream.
- **Port mappings:** multiple mappings can be configured; IDs must match on both ends.
- **Transport security:** Hub–Agent tunnel traffic uses TLS 1.2 or newer, certificate SHA-256 fingerprint pinning, and a shared random 256-bit authentication token.
- **Resilience:** connection health checks, automatic reconnect/backoff, and systemd restart-on-failure support.
- **Operations:** interactive setup/menu, start/stop/restart, status and logs, and complete uninstall of Shah Tunnel's own service/files.
- **Server identity:** the menu checks the server's public egress IP and country using multiple HTTPS metadata providers. Results are cached for up to six hours; an IP-only fallback keeps the IP visible when country lookup fails. If the server cannot reach any provider, information may still show as unavailable.

## Compatibility and file layout

- Release version: **1.3.4**
- Target: Linux `amd64` / `x86_64` with systemd
- Installed binary: `/usr/local/bin/ravanreverse`
- User command: `/usr/local/bin/shah-tunnel`
- Service/config compatibility names: `ravanreverse` and `/etc/ravanreverse/config.json`
- Public repository layout: only the compiled executable is placed inside `S1T-CAPSULE/`; installer, checksum and documentation remain outside that folder.

The existing logo is stored at [`docs/img/shah-tunnel-logo.svg`](docs/img/shah-tunnel-logo.svg) and displayed above this introduction.

## Security notes

- Keep the Hub token and `/etc/ravanreverse/config.json` private. Never commit live tokens, private keys or server configuration.
- TLS protects the Hub–Agent tunnel connection; it does not make the server's public IP invisible to upstream networks or network observers.
- IP geolocation is approximate network metadata, not proof of a server's physical location or ownership. A provider receives the IP lookup request.
- A stripped, compiled binary does not contain readable Go source files, but no executable can be guaranteed impossible to reverse engineer. Avoid publishing the source or old source archives in a public repository if source confidentiality is required.
- Removing a file from the latest Git commit does not erase its older copies from Git history. Previously published source may remain available in old commits/forks/caches unless history and copies are handled separately.

## Diagnostics

```bash
systemctl status ravanreverse --no-pager -l
journalctl -u ravanreverse -n 50 --no-pager
/usr/local/bin/shah-tunnel version
```

No open-source license is declared by this release. Add an appropriate license before granting redistribution rights.
