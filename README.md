        👑 SHAH1TUNNEL 👑

███████╗██╗  ██╗ █████╗ ██╗  ██╗
██╔════╝██║  ██║██╔══██╗██║  ██║
███████╗███████║███████║███████║
╚════██║██╔══██║██╔══██║██╔══██║
███████║██║  ██║██║  ██║██║  ██║
╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝

         SHAH1TUNNEL
         VERSION 1.0.0

[English](README.md) | [فارسی](README.fa.md)

## What Shah1Tunnel is
Shah1Tunnel is a compact Linux tunnel designed to establish and manage reliable tunnel connections with a small, focused manager. It prioritizes simplicity, maintainability and straightforward service lifecycle management on systemd-based Linux servers.

## Features
- Easy tunnel setup and configuration
- Connection and lifecycle management via a lightweight manager
- Flexible server-side configuration for published ports
- Designed for Linux servers (systemd-friendly)

## Quick install
Run the commands below on a compatible Linux host with git and bash installed.

```bash
git clone https://github.com/shah1dev/Shah1Tunnel.git
cd Shah1Tunnel
sudo bash S1T-START.sh
```

Notes
- The installer performs integrity checks before making changes and will abort on verification failures. Run the installer on a trusted test host first.
