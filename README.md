![Shah Tunnel logo](docs/img/shah-tunnel-logo.svg)

# Shah Tunnel — Version 1.3.9

**Language / زبان:** [🇬🇧 English](README.md) · [🇮🇷 فارسی](README-fa.md)

Shah Tunnel is an **authenticated reverse TCP tunnel** that forwards incoming connections from a public entry server to a TCP service on a destination server. It is useful when the destination service should not be exposed directly to the Internet or when the entry server has no suitable direct path to that destination.

## How it works

```text
Client ── TCP ──▶ Hub (public entry)
                    ║
                    ║ authenticated TLS channel
                    ║
                 Agent (exit) ── TCP ──▶ Destination service
```

- **Hub:** Listens on configured public ports and accepts incoming connections.
- **Agent:** Initiates an outbound authenticated connection to the Hub and forwards TCP streams to configured destinations.
- **Port mappings:** Each public listener can map to a separate TCP destination; the mapping identifier must match on both ends.
- **Channel security:** The Hub↔Agent channel uses TLS 1.2 or newer, SHA-256 certificate-fingerprint pinning for the Hub, and a shared random authentication token.
- **Resilience:** The Agent retries the connection after a drop; systemd can supervise and restart the service after failures.
- **Optional Relay:** In supported layouts, a Relay forwards TCP streams to its configured destination.

## Typical uses

Forward TCP connections for panels, web applications, or other services listening on a TCP port. For example, the Hub accepts a public port and the Agent forwards it to a destination such as `127.0.0.1:PORT`. The actual address and port depend on the application.

## What it is not

Shah Tunnel is not a general-purpose VPN such as WireGuard: it does not create a virtual network interface or route all IP traffic for a device or server. It forwards only the configured TCP connections. TLS protects the Hub-to-Agent channel; application-level encryption, when required, must be provided by the destination service itself.

## Easy installation

On an Ubuntu/Debian `x86_64` server, run as `root`:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shah1dev/ShahTunnel/main/S1T-START.sh)
```

For Persian, open the [Persian README](README-fa.md).
