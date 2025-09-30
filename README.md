# micro-socks

Lightweight SOCKS5 proxy server with optional username/password authentication, interface binding, timeouts, and graceful shutdown. Suitable for local development or controlled environments.

## Features
- SOCKS5 `CONNECT` with IPv4/IPv6 targets
- Optional auth via `users5.txt` (username:password)
- Bind outbound traffic to a network interface
- Handshake/dial timeouts, TCP keep-alive, proper reply codes
- Graceful shutdown on SIGINT/SIGTERM

## Build
- Prerequisites: Go 1.20+
- Build: `go build -o micro-socks`

## Debian Package
- Build .deb: `bash scripts/build-deb.sh` (uses git tag or `0.1.0`)
- Override version: `VERSION=1.2.3 bash scripts/build-deb.sh`
- Override arch: `ARCH=arm64 bash scripts/build-deb.sh`
- Output: `dist/micro-socks_<version>_<arch>.deb`

Install example:
- `sudo dpkg -i dist/micro-socks_0.1.0_amd64.deb`

Note: the package installs the binary to `/usr/bin/micro-socks`. Create and secure your `users5.txt` manually if needed (`chmod 600`).

### Systemd Service
- Unit path: `/lib/systemd/system/micro-socks.service`
- Default config: `/etc/default/micro-socks` (conffile)
- Optional creds location: `/etc/micro-socks/users5.txt` (create manually)

Usage:
- Edit `/etc/default/micro-socks` and set either `OPTS="--listen 127.0.0.1:1080 --users /etc/micro-socks/users5.txt"` OR env vars `PROXY_LISTEN`, `PROXY_USERS`, `PROXY_IFACE`.
- Create creds if using auth: `sudo install -d -m 0750 -o root -g socks5 /etc/micro-socks && sudo install -m 0640 -o root -g socks5 users5.txt /etc/micro-socks/users5.txt`
- Enable and start: `sudo systemctl enable --now micro-socks`
- Status and logs: `systemctl status micro-socks` and `journalctl -u micro-socks -f`

## Run
- No auth: `./micro-socks --listen 127.0.0.1:1080`
- With auth: `./micro-socks --users users5.txt`
- Bind egress interface: `./micro-socks --iface eth0`

Flags:
- `--listen`: address:port to listen (default `0.0.0.0:1080`)
- `--users`: path to credentials file
- `--iface`: outbound interface name (best‑effort source IP selection)

Environment variables (used if flags are default/empty):
- `PROXY_LISTEN`, `PROXY_USERS`, `PROXY_IFACE`

## Credentials File
- Location: `users5.txt` (any path is allowed)
- Format: one entry per line — `username:password`
- Comments/blank lines are ignored (`# comment`)
- Example:
  
  user1: secret1
  user2: secret2

## Quick Tests
- Basic reachability:
  - `curl --socks5 127.0.0.1:1080 http://example.com`
- With auth:
  - `curl --socks5 127.0.0.1:1080 --socks5-user user1:secret1 http://example.com`
- Git through proxy:
  - `git config --global http.proxy socks5://127.0.0.1:1080`

## Examples
- Override via env vars:
  - `PROXY_LISTEN=127.0.0.1:1081 PROXY_USERS=users5.txt ./micro-socks`
- Bind egress to interface (best effort):
  - `./micro-socks --iface eth0`

## Notes & Security
- Prefer `127.0.0.1` for local use; avoid exposing to untrusted networks.
- Protect credentials: `chmod 600 users5.txt`.
- Logs omit sensitive data but may include destinations.
- Interface binding relies on source IP selection; it is not equivalent to kernel‑level SO_BINDTODEVICE.
