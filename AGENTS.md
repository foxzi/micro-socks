# Repository Guidelines

## Project Structure & Module Organization
- `main.go`: SOCKS5 proxy server implementation (single binary).
- `users5.txt`: Optional credentials file (`username:password` per line).
- Suggested growth: place CLI under `cmd/socks5-proxy/` and shared code in `pkg/` when refactoring beyond a single file.

## Build, Test, and Development Commands
- Build: `go build -o socks5-proxy` — produces the proxy binary.
- Run (no auth): `./socks5-proxy --listen 0.0.0.0:1080` — starts a public SOCKS5 endpoint.
- Run (with auth): `./socks5-proxy --users users5.txt` — enables username/password auth.
- Bind outbound interface: `./socks5-proxy --iface eth0` — attempts egress via `eth0`.
- Format: `go fmt ./...` — applies standard Go formatting.
- Vet: `go vet ./...` — catches common issues.

## Coding Style & Naming Conventions
- Language: Go; follow Effective Go and standard library patterns.
- Formatting: tabs, idiomatic casing (`CamelCase` types, `mixedCaps` funcs/vars).
- Keep package `main` minimal; extract protocol logic to small, testable functions if expanding.
- Prefer explicit errors; log context with destinations (avoid secrets).

## Testing Guidelines
- Framework: Go `testing` package.
- File names: `*_test.go`; function names: `TestXxx`.
- Run tests: `go test ./...` and coverage: `go test -cover ./...`.
- Add table-driven tests for parsing and auth negotiation; use `net.Pipe` for handshake flows.

## Commit & Pull Request Guidelines
- Commits: concise, imperative subject; prefer Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`).
- PRs: include purpose, summary of changes, flags used for local verification, and screenshots/logs if relevant.
- Link issues and describe test coverage or manual steps (e.g., `curl --socks5 ...`).

## Security & Configuration Tips
- Credentials: store in `users5.txt` as `username:password`; restrict permissions (`chmod 600 users5.txt`).
- Listening: avoid `0.0.0.0` on untrusted networks; restrict via firewall or bind to `127.0.0.1` for local use.
- Logging: do not print credentials; current logs show destinations only.
- Interfaces: validate `--iface` exists; verify traffic egresses as expected (use `tcpdump`/`ss`).

