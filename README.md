![Shah1Tunnel logo](docs/img/shah1tunnel-logo.svg)

# Shah1Tunnel — Secure Tunnel Setup, Configuration and Management

A compact, practical tunneling toolkit focused on making a single managed tunnel between two Linux servers easy to deploy and operate. Shah1Tunnel provides an installer and control scripts in this repository; it is intended for administrators who manage both endpoints of the connection.

[English](README.md) | [فارسی](README.fa.md)

---

## Overview

Shah1Tunnel helps establish and manage a server-to-server tunnel between two endpoints (for example, an Iran-based server and an overseas server). The repository contains the installer and related control scripts used to prepare, validate, and run the packaged tunnel components.

Shah1Tunnel is intended for Linux server environments. Before using it in production, review the configuration and test connectivity in a safe environment.

## What this repository contains

- install.sh — a top-level installer that downloads package files from this repo, verifies SHA-256 checksums and runs the packaged installer (see Installation).
- S1T-START.sh and related control files (downloaded by the installer at runtime).
- S1T-CHECKS.sha256 — checksum manifest used by the installer.
- docs/ — documentation and images (includes the repository logo shown above).

## Verified capabilities (what you can expect from the repo contents)

- An installer script (install.sh) that downloads package artifacts from this repository's main branch and verifies their SHA-256 checksums before proceeding.
- Installer logic that looks for the capsule archive in S1T-CAPSULE/S1T-SOURCE.tar.gz and falls back to a file named S1T-CAPSULE/null if present in the repository.
- Documentation and helper scripts referenced by the installer.

(Do not assume additional features such as specific transports, encryption suites, throughput limits, or other runtime properties unless you inspect the packaged code that the installer extracts.)

## Installation (verified)

The repository includes an installer script at `install.sh`. You can run it on a supported Linux server after reviewing the script and understanding its actions:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/shah1dev/Shah1Tunnel/main/install.sh)
```

Read the `install.sh` source before running it. The installer will download and run additional packaged files from this repository and will fail if required commands (curl, sha256sum, tar, bash) are missing or if the capsule archive/checksums are not present or do not match.

## Configuration checklist

Before bringing a tunnel online, verify:

- The public and control ports you plan to use are reachable from the expected peers.
- Destination services are listening on the expected addresses and ports.
- Firewall and routing rules on both endpoints allow the intended traffic.
- You have tested the installer in a non-production environment and validated checksum verification succeeded.
- Any private keys or credentials used by your deployment are kept out of the repository and stored securely.

## Troubleshooting

Start by checking system logs and the installer output. Common issues:

- Missing required tools (curl, sha256sum, tar, bash).
- Capsule archive not present or checksum mismatch.
- Ports blocked by a firewall or occupied by another process.

## Security notes

- Keep private keys and tokens out of the repository. Never commit secrets.
- Review the downloaded package contents before running them in production.
- Limit exposed ports to the minimum required for operation.

## Development and contributions

Contributions and bug reports are welcome. When opening an issue, include the repository version (commit), operating system and architecture, and relevant non-sensitive logs or steps to reproduce. Remove passwords, private keys, and tokens from any pasted logs.

## Disclaimer

This repository provides installation and packaging logic. Actual runtime behavior, transports, performance, or cryptographic properties depend on the packaged code that the installer extracts; those are not guaranteed by the presence of this repository alone.
