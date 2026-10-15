![Shah Tunnel logo](docs/img/shah-tunnel-logo.svg)

# Shah Tunnel — Installation, Tunnels and Operations

Shah Tunnel is a setup and management layer for creating a tunnel between two Linux servers. In the common layout, users connect to the Iran endpoint and selected TCP/UDP ports are forwarded to services on the overseas endpoint.

[English](README.md) · [فارسی](README.fa.md)

> **Tunnel engine:** Shah Tunnel provides packaging, integrity checks, setup and management adjustments. The package information identifies core version `2.4.17`; the installed core version may differ from the Shah Tunnel package version.

## Contents

- [Capabilities](#capabilities)
- [Requirements](#requirements)
- [Installation](#installation)
- [Connecting two servers](#connecting-two-servers)
- [Choosing a carrier](#choosing-a-carrier)
- [Engine feature comparison and test results](#engine-feature-comparison-and-test-results)
- [Reverse and automatic modes](#reverse-and-automatic-modes)
- [Cloudflare transport](#cloudflare-transport)
- [Management commands](#management-commands)
- [Configuration and port mapping](#configuration-and-port-mapping)
- [Monitoring, security and troubleshooting](#monitoring-security-and-troubleshooting)

## Capabilities

- **Multiple carriers:** the engine supports ICMP, UDP, TCP, TLS, WebSocket, secure WebSocket, HTTP/2 and gRPC. The best choice depends on what the network path between the two endpoints permits.
- **TCP and UDP forwarding:** one managed tunnel can carry multiple port mappings, with a different destination port when required.
- **Interactive manager:** create tunnels, inspect status, review logs, test connectivity, edit mappings and manage updates from a terminal menu.
- **Automatic selection and carrier bonding:** Auto tests the available connection options. The `Resilient` choice (`r` or `--pick resilient`) bonds every successful direct carrier when at least two pass, otherwise choosing the best measured route.
- **Multipath recovery:** the engine probes silent bond paths again. During auto-heal, if any direct variant passes a retest, the manager reapplies the prior bond members plus all newly tested-good direct variants so temporarily failing paths can be probed again; if no direct variant passes, it falls back to the best-ranked working route.
- **Reverse connections:** useful when only one direction can initiate a connection. Reverse support differs by carrier.
- **Monitoring and recovery:** connection and per-port statistics are available. Supported deployments can automatically bring back a disconnected Iran-side tunnel.
- **Package verification:** package files are checked against a SHA-256 manifest before execution; the capsule archive and its extraction paths are also validated.

Real-world throughput depends on the route, latency, packet loss, network filtering, server capacity, MTU and configuration. No single speed figure applies to every deployment.

## Requirements

- Two Linux servers running `systemd`, with administrative access (`root` or `sudo`).
- `amd64` or `arm64` for the supported core binaries.
- Internet access to retrieve the installer and required binaries.
- Basic validation/install tools: `bash`, `curl`, `sha256sum`, `awk` and `tar`. Depending on the path, standard utilities such as `base64`, `find`, `mv`, `mktemp` and `df` are also required.
- Firewall rules that allow the selected carrier and access to the destination service on the overseas server.

## Installation

### Online install

Run this command on both servers:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shah1dev/Shah%31Tunnel/main/install.sh)
```

The installer downloads the checksum manifest, launcher, package information and capsule archive. It verifies SHA-256 values strictly and proceeds only after successful verification. A checksum mismatch or invalid/incomplete archive stops installation.

## Connecting two servers

Install the package on both endpoints. **Configure the overseas server first** so you can generate the connection token.

### 1. Overseas endpoint (Kharej)

1. Run `wattun`.
2. Press `n` to create a tunnel and choose `Kharej` as the role.
3. Choose a carrier. Start with `auto` when you have not yet established which options work on your route.
4. Complete the wizard and keep the generated token private. It contains tunnel credentials.

### 2. Iran endpoint (Iran)

1. Run `wattun`, press `n`, and select `Iran`.
2. When prompted, paste the token generated on Kharej.
3. Enter the ports users should reach and the corresponding service ports on Kharej.
4. Confirm that the firewall permits the chosen carrier.

For example, `8443:443` means clients connect to port `8443` on the Iran server and reach port `443` on the overseas service. In the usual layout, Kharej forwards to its own local service, such as `127.0.0.1:443`.

```text
Client ──► Iran:8443 ──► encrypted tunnel ──► Kharej ──► 127.0.0.1:443
```

Test the connection after setup and also verify that the destination application is actually listening on the expected address and port. A tunnel status alone does not guarantee that the destination service is healthy.

## Choosing a carrier

| Network condition | Carrier to try | Notes |
|---|---|---|
| Only ping/ICMP passes | `icmp` | Requires suitable ICMP access; configurable types include `echo`, `timestamp`, `info` and `mask`. |
| UDP is reachable | `udp` | A lightweight choice for routes that allow UDP. |
| Ordinary TCP connections work | `tcp` | Suited to routes that allow TCP sessions to remain open. |
| A TLS-based connection is appropriate | `tls` | Supports TLS transport; certificate pinning must match the intended certificate. |
| WebSocket transport is needed | `ws` or `wss` | `wss` uses TLS; CDN compatibility depends on domain and proxy settings. |
| HTTP/2 or gRPC is usable | `h2`, `h2c` or `grpc` | The carrier must match the destination's TLS and CDN setup. |
| Unsure what will pass | `auto` | Tests supported options to help choose a route. |
| More than one transport is useful | Example: `carrier: icmp+udp+tcp` | Combine only carriers supported on both endpoints and reachable on the network. |

Some networks require the reverse-connection mode described below. Results from one ISP or route should not be assumed to apply to another.

## Engine feature comparison and test results

### Feature snapshot against related tunnel tools

| Capability | Shah Tunnel engine (Shah Tunnel) | GOST | pingtunnel | Hans | ptunnel-ng |
|---|:---:|:---:|:---:|:---:|:---:|
| ICMP transport | ✅ | ✅ | ✅ | ✅ | ✅ |
| Multiple transport families in the same implementation | ✅ | ✅ | — | — | — |
| TCP/UDP transports in addition to ICMP | ✅ | ✅ | — | — | — |
| CDN path with ECH support | ✅ | — | — | — | — |
| Automatic route tests using one token | ✅ | — | — | — | — |
| Delay-aware pacing | ✅ | — | — | — | — |
| Multiple port mappings in one process | ✅ | ✅ | — | ✅* | — |
| UDP port forwarding | ✅ | ✅ | Limited | ✅ | — |
| Built-in tunnel encryption | ✅ | ✅ | Configuration-dependent | Configuration-dependent | — |
| Adaptive forward error correction (FEC) | ✅ | — | — | — | — |
| Per-port traffic statistics and quotas | ✅ | — | — | — | — |
| Reverse mode for TCP-based carriers | ✅ | ✅ | — | — | — |
| Reverse ICMP mode | ✅ | — | — | — | — |
| Standalone executable | ✅ | ✅ | ✅ | — | — |
| Guided manager, token pairing and recovery | ✅ | — | — | — | — |

**Note:** For Hans, this refers to its TUN-device approach. A dash means “not confirmed in the source comparison,” rather than a definitive statement about every version of that project.

### Published figures from a real ICMP route

The Shah Tunnel technical report records the figures below for a real Xray/VLESS traffic test dated 9 October 2026. These are measurements from that particular route and setup, not a speed guarantee for your servers or a Shah Tunnel-specific benchmark.

| Metric | Shah Tunnel engine | GOST over ICMP |
|---|---:|---:|
| Xray/VLESS download | About 6 MB/s | About 1.6–5.4 MB/s |
| Xray/VLESS upload | About 3.7 MB/s | Not measured in that report |
| Parallel short TLS requests | 120 of 120 succeeded | Not reported |
| CPU time per MB moved on the Iran side | About 0.05–0.06 CPU-s | About 0.15–0.20 CPU-s |
| Median latency for a small request while idle | About 40–45 ms | About 51 ms |
| Reported RAM use | About 40 MB | About 50–70 MB |

### Additional figures from earlier project notes

Earlier release notes also recorded results under other versions and test setups. Because the route, release, rate cap and conditions differed, these figures should not be combined with the table above or treated as expected results on the current server.

| Reported scenario | Published result | Important context |
|---|---|---|
| Earlier ICMP test with Shah Tunnel 1.8.0 | Peak around 33 MB/s; around 25 MB/s for one connection | Applies to the filtered route and settings described for that older test. |
| ICMP with 1400-byte packets and a 30 MB/s cap, 12 October 2026 | About 27.5 MB/s download, 26 MB/s upload, roughly 41 ms ping under load | Specific to that route and cap; test your own path before raising MTU. |
| Cloudflare/WSS production run on 2.4.6, four sessions and around 250 user connections | Initially about 41 MB/s sustained download; speed tests reached 44–48 MB/s down and up to 82 MB/s up, with around 100 MB RAM per server | The same report noted a drop to about 1 MB/s after roughly three hours, which the 2.4.7 fix did not sustain away in that test. |

Starting with 2.4.11, the TCP-based carriers received further congestion-control changes, so the older Cloudflare result should not be used as a direct prediction of 2.4.17 behavior. Test the currently installed release and your own route.

### Separate comparison on an emulated link

Another test used an emulated path with 20 ms of delay each way, 0.2% packet loss and a 100 Mbit/s rate limit. The figures below summarize the median of a small number of rounds; the multi-flow column represents a workload of 16 transfers of 2 MB each.

| Test setup | One flow (MB/s) | Multi-flow load (MB/s) | p50 latency | CPU-s/MB |
|---|---:|---:|---:|---:|
| Direct connection, no tunnel | 10.92 | 10.69 | 82 ms | 0.00 |
| Shah Tunnel over UDP | 10.57 | 10.62 | 43 ms | 0.04 |
| Shah Tunnel over TCP | 10.55 | 10.55 | 43 ms | 0.03 |
| Shah Tunnel over TLS | 10.59 | 10.25 | 43 ms | 0.03 |
| Shah Tunnel over WebSocket | 10.59 | 10.61 | 43 ms | 0.03 |
| GOST over TCP | 10.84 | 10.41 | 83 ms | 0.01 |
| GOST over WebSocket | 10.76 | 10.40 | 124 ms | 0.01 |

In this emulated test, GOST used slightly less CPU and multi-flow throughput was close between the tools, while median latency for small requests was lower in the tested Shah Tunnel configurations. This is not evidence of a universal winner. The `h2`, `h2c`, `wss` and `grpc` carriers are not benchmarked in this table.

### Interpreting these results for your own servers

- For a real-world check, keep both endpoints on compatible releases and run `wattun test NAME`.
- Do not treat emulated-link results as a forecast for a production route. Provider limits, route congestion, ICMP filtering, MTU and firewall rules all affect results.
- Figures from specific dates and older versions are not performance guarantees for the installed `2.4.17` engine.
- For a fair comparison, test the other tools at known versions, on comparable servers, and across the same network route.

## Reverse and automatic modes

### Reverse mode

Reverse mode changes which endpoint initiates the connection; users still connect to Iran and traffic still reaches the service on Kharej. It is intended for one-way reachability problems.

In the manager, open the tunnel, enter advanced settings (`o`), then direction (`d`). Enter the peer address and apply the change on **both servers**. Test the resulting connection:

```bash
wattun test NAME
```

Reverse operation is available for supported TCP-based carriers and ICMP, but `udp` has no reverse mode. For reverse ICMP, test ping reachability from both endpoints before changing the direction.

### Automatic mode and resilient selection

Selecting `auto` during tunnel creation makes the engine test reachable variants and display its results. Run the test again after a network or configuration change with:

```bash
wattun auto-test NAME
```

In the route-selection screen, press `r` for **Resilient**: if at least two direct carriers pass, they are bonded into one session; otherwise the best measured route is selected. Unattended setup can use `--pick resilient`. The engine probes failed bond paths again. During auto-heal, if any direct variant passes a retest, the manager reapplies the prior bond members plus all newly tested-good direct variants so temporarily failing paths can be probed again; if no direct variant passes, it falls back to the best-ranked working route. Applying a manager-level route change restarts the tunnel and may drop active connections. This uses existing bonding, not a new wire protocol. If all paths are blocked, no route-selection policy can guarantee a connection.

If an auto tunnel on Kharej cannot determine the Iran endpoint's IP, set it manually on Kharej:

```bash
wattun auto-ip NAME IRAN_PUBLIC_IP
```

Treat automatic selection as a diagnostic aid and verify actual access to the destination service afterward.

## Cloudflare transport

The `wss` and `grpc` carriers can use a Cloudflare-proxied domain. This depends on DNS, TLS and network configuration; it is not guaranteed to outperform ICMP or a direct connection on every route.

1. Add the domain to Cloudflare and enable its proxy for the relevant DNS record where required.
2. Configure SSL/TLS and networking for the selected carrier. For gRPC, enable it in Cloudflare if the corresponding setting is available.
3. On Kharej, select `wss` or `grpc` and enter the domain.
4. On Iran, let the manager test and configure the connection. For supported modes, the engine can check ECH settings and reachable edge addresses.
5. Verify connection status, logs and real service traffic. If throughput falls later, begin with `wattun test NAME`; simply adding more sessions does not fix every route problem.

To retune a CDN tunnel:

```bash
wattun cdn-tune NAME
```

## Management commands

Running `wattun` opens the interactive tunnel dashboard. The usual menu keys are `n` for a new tunnel, `m` for a live monitor, `u` for updates, `s` for settings and `q` to quit.

| Command | Purpose |
|---|---|
| `wattun` | Open the dashboard |
| `wattun new kharej` | Start a new tunnel with the Kharej role |
| `wattun status` | Summarize tunnel states |
| `wattun monitor` | Live monitoring view |
| `wattun ports NAME` | Edit port mappings |
| `wattun test NAME` | Run connection diagnostics and a speed test |
| `wattun speedtest NAME` | Run a speed test only |
| `wattun logs NAME` | Follow tunnel logs |
| `wattun token NAME` | Print the token again on Kharej |
| `wattun auto-test NAME` | Retest automatic mode |
| `wattun cdn-tune NAME` | Retune Cloudflare parameters |
| `wattun auto-ip NAME IRAN_PUBLIC_IP` | Set the Iran IP for an auto tunnel on Kharej |
| `wattun update` | Check/run an update |
| `wattun rollback` | Return to an earlier version when supported by the installed package |
| `wattun help` | Show command help |

Replace `NAME` with the name assigned to your tunnel. If a command is unavailable in the installed build, use the relevant option in that build's manager.

## Configuration and port mapping

A tunnel configuration is typically stored at `/etc/wattun/NAME.yml` and managed by a `systemd` unit. A basic Iran-side example:

```yaml
# /etc/wattun/iran.yml
mode: client
key-file: /etc/wattun/iran.key
carrier: icmp
peer: KHAREJ_PUBLIC_IP   # replace with the real Kharej address
metrics: 127.0.0.1:19750
ports:
  - 443          # Iran:443 to service port 443 on Kharej
  - 8443:443     # Iran:8443 to Kharej service port 443
  - 53/udp
  - 5353:53/udp
```

Mapping examples:

- `443` — use the same port number for ingress and destination.
- `8443:443` — Iran port `8443` forwards to Kharej port `443`.
- `53/udp` — forward UDP port 53.
- `5353:53/udp` — forward ingress UDP port 5353 to destination UDP port 53.

This is a mapping example, not a ready-made configuration. Replace the peer address and use the key and ports generated for your deployment. On Kharej, destination ports can be restricted through an allowlist; list only the service ports that are required.

In supported builds, changes to `ports:` are reloaded without restarting the tunnel. Other settings generally require restarting that tunnel's service. Back up the configuration before editing it.

## Monitoring, security and troubleshooting

### Status and statistics

The metrics API is local-only and must not be exposed to the public internet:

```bash
curl -s http://127.0.0.1:19750/stats
curl -s http://127.0.0.1:19750/ports
```

To find related services and inspect system logs:

```bash
systemctl list-units --type=service --all | grep -i wattun
journalctl -u wattun@NAME --no-pager -n 100
```

Replace the service name with the unit used by your tunnel. If the unit name differs, locate it first with `systemctl list-units`.

### Security guidance

- Treat the connection token like a password; do not post it in public repositories, tickets or chats.
- Expose only the ports required by your deployment. Prefer keeping the destination service on `127.0.0.1` on Kharej unless your design requires another bind address.
- Keep the metrics API bound to loopback.
- The engine uses authenticated `ChaCha20-Poly1305` encryption; packets that fail authentication are not accepted. This protects tunnel traffic but does not guarantee anonymity or hide every indication that a tunnel exists.
- Review downloaded installer code and package provenance. Never work around a checksum error by disabling verification.

### Common problems

| Symptom | What to check |
|---|---|
| Installer reports a missing command | Install the utility named in the error and rerun the installer. |
| Local launcher cannot create a workspace | Check for at least 16 MiB free and write permission on the temporary path; use `WAT_TMPDIR` if needed. |
| Checksum or archive validation fails | Stop, remove the incomplete download and fetch the package again from the official source. |
| `DISCONNECTED` or no response | Verify the same token/key and carrier on both endpoints, firewall rules and peer reachability. |
| Iran cannot ping Kharej, but the reverse works | Test reverse mode and apply the direction consistently on both endpoints. |
| Tunnel is connected but the service does not open | Confirm the service is listening on the expected address/port and check the mapping. |
| Port is already in use | Identify the listener with `ss -lntup`; change a port only after confirming what uses it. |
| Cloudflare connection fails | Verify DNS proxy status, TLS settings, carrier choice and gRPC support; do not assume `h2` works through Cloudflare. |
| Throughput or MTU is poor | Test path MTU before increasing it. For example, try `ping -M do -s 1372 PEER_IP`; if it fails, choose a smaller value. |

For an initial diagnosis, run `wattun test NAME`, then check the relevant service state and logs.

## Automatic recovery and updates

In supported configurations, a recovery timer can restart an Iran-side tunnel that has remained disconnected for several minutes. It should not restart a tunnel that an administrator intentionally stopped. Check the feature's state in the manager settings.

The Shah Tunnel package verifies files against SHA-256 and checks the core binary hash for supported `amd64` and `arm64` releases. The current package notes say an installed core at `2.4.17` or newer should not be downgraded. When the same version is already present, the binary can be left in place while the management layer is updated. Active tunnels are not supposed to restart automatically solely because the package is updated; check both endpoints after an update.

**Final note:** test the result on your own servers. Firewalls, ISP filtering, the route, MTU and server capacity can materially change how a tunnel performs.

### Auto reverse standby (FIX14)

The `NAME-r` unit on Kharej is the reverse standby path. While Iran is actively using a direct route, reverse listener ports are not necessarily open all the time; `connection refused` on those standby ports alone does not prove the live direct tunnel is broken. The manager opens the applicable reverse listeners during reverse tests or when switching to reverse. If no direct connection exposes Iran’s public IP, `auto-ip` is the manual fallback.

<!-- FIX14 reverse standby note -->
