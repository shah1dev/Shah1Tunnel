# Shah Tunnel

A small authenticated reverse TCP tunnel for Linux `amd64`, with an English, colorized terminal menu. The project uses Go's standard library and systemd.

## Features

- Hub (Iran-side), Agent (outside-side), and optional Relay roles.
- TLS 1.2+ with a pinned SHA-256 certificate fingerprint and a random 256-bit token.
- Multiple TCP port mappings, automatic reconnect, and systemd restart-on-failure behavior.
- Menu displays the server's observed public egress IP and country. The first lookup uses `https://ipapi.co/json/` and is cached for six hours; lookup has a short timeout and never prevents the tunnel service from running. If geolocation is unavailable, the menu displays `Unavailable / Unknown`.
- Installation from the official release ZIP removes the extracted release folder, ZIP, checksum, and known older RavanReverse installer bundles under `/root` after a successful install from the canonical release folder. The installed root-owned runtime, configuration, and systemd unit remain so the service can run. This is basic access control and cleanup, not a way to hide files from a root administrator.

## Install from a GitHub Release

Use a Linux `x86_64`/`amd64` server running systemd. Upload the release ZIP to `/root`, then run:

```bash
cd /root
unzip -o Shah-Tunnel-1.3.3-Linux-amd64.zip
cd /root/Shah-Tunnel-1.3.3
bash install.sh
```

On a new installation, the English setup menu opens automatically. On an existing installation, the installer preserves `/etc/ravanreverse/config.json`, upgrades the binary, and restarts the service. The internal service/config path remains `ravanreverse` for in-place compatibility; the user-facing command is `shah-tunnel`.

Open the menu later with:

```bash
shah-tunnel
```

Service checks:

```bash
systemctl status ravanreverse --no-pager -l
journalctl -u ravanreverse -n 50 --no-pager
```

## Build from source

Requires Go 1.22 or later and a Linux amd64 target for the packaged release.

```bash
go test ./...
go vet ./...
GOOS=linux GOARCH=amd64 go build -trimpath -o ravanreverse .
sudo bash install.sh
```

For a source checkout, the installer intentionally does not delete the checkout. Automatic cleanup is limited to the canonical release directory named `Shah-Tunnel-1.3.3`.

## GitHub releases

The repository includes `.github/workflows/release.yml`. Push a version tag matching the version in `main.go` (for this release, `v1.3.3`) to run tests, build the Linux amd64 archive, generate SHA-256, and publish a GitHub Release.

## Security notes

- Keep the Hub token private. The Hub and every Agent in its tunnel must use the same token.
- Keep the Agent fingerprint identical to the Hub's actual certificate fingerprint. If the Hub certificate is regenerated, update the Agent configuration accordingly.
- Do not commit `/etc/ravanreverse/config.json`, private keys, tokens, or any live server credentials.
- The server IP/country line is based on the server's outbound public IP as seen by the geolocation API. It is approximate location metadata, not proof of server ownership or physical location.
- This project does not hide a server's IP from network observers who can see its connections. A Relay can change the IP shown to client configs but does not hide the IP from the Relay operator or upstream networks.

## License

No license has been assigned in this package. Add a license file before accepting outside contributions or redistributing the source under an open-source license.
