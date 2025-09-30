#!/usr/bin/env bash
set -euo pipefail

# Minimal Debian package builder for micro-socks
# Usage examples:
#   ./scripts/build-deb.sh               # auto version from git or 0.1.0
#   VERSION=1.2.3 ./scripts/build-deb.sh # explicit version
#   ARCH=arm64 ./scripts/build-deb.sh    # build for arm64

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

PKG_NAME="micro-socks"
BIN_NAME="micro-socks"

# Detect version
VERSION="${VERSION:-}"
if [[ -z "$VERSION" ]]; then
  if git describe --tags --abbrev=0 >/dev/null 2>&1; then
    VERSION="$(git describe --tags --abbrev=0 | sed 's/^v//')"
  else
    VERSION="0.1.0"
  fi
fi

# Detect architecture (Debian arch values)
ARCH="${ARCH:-}"
if [[ -z "$ARCH" ]]; then
  if command -v dpkg >/dev/null 2>&1; then
    ARCH="$(dpkg --print-architecture)"
  else
    ARCH="amd64"
  fi
fi

# Map Debian arch to Go arch where needed
GOARCH="$ARCH"
case "$ARCH" in
  amd64) GOARCH=amd64 ;;
  arm64) GOARCH=arm64 ;;
  armhf) GOARCH=arm   ;;
  i386)  GOARCH=386   ;;
  *)     GOARCH=amd64 ;;
esac

DIST_DIR="$ROOT_DIR/dist"
STAGE_DIR="$DIST_DIR/${PKG_NAME}_${VERSION}_${ARCH}"

echo "[+] Building $PKG_NAME $VERSION for $ARCH (GOARCH=$GOARCH)"

rm -rf "$STAGE_DIR"
mkdir -p \
  "$STAGE_DIR/DEBIAN" \
  "$STAGE_DIR/usr/bin" \
  "$STAGE_DIR/lib/systemd/system" \
  "$STAGE_DIR/etc/default" \
  "$STAGE_DIR/etc/micro-socks"

# Build static-ish binary for Linux target
export CGO_ENABLED=0
export GOOS=linux
export GOARCH="$GOARCH"
export GOCACHE="$DIST_DIR/.gocache"

mkdir -p "$GOCACHE"

echo "[+] Compiling Go binary..."
go build -ldflags "-s -w" -o "$STAGE_DIR/usr/bin/$BIN_NAME" ./

# Control file
echo "[+] Writing control file..."
cat > "$STAGE_DIR/DEBIAN/control" <<EOF
Package: $PKG_NAME
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: Unknown Maintainer <unknown@example.com>
Description: Lightweight SOCKS5 proxy server in Go
 Minimal SOCKS5 proxy with optional username/password auth and egress interface binding.
EOF

chmod 0755 "$STAGE_DIR/usr/bin/$BIN_NAME"
chmod 0755 "$STAGE_DIR/DEBIAN"
chmod 0644 "$STAGE_DIR/DEBIAN/control"

# Systemd unit
echo "[+] Adding systemd unit and defaults..."
cat > "$STAGE_DIR/lib/systemd/system/$PKG_NAME.service" <<'EOF'
[Unit]
Description=SOCKS5 Proxy (micro-socks)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=socks5
Group=socks5
EnvironmentFile=-/etc/default/micro-socks
ExecStart=/usr/bin/micro-socks $OPTS
Restart=on-failure
RestartSec=2s
AmbientCapabilities=
NoNewPrivileges=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

# Defaults
cat > "$STAGE_DIR/etc/default/micro-socks" <<'EOF'
# Configuration for micro-socks systemd service

# Either set explicit flags in OPTS (uncomment and edit), e.g.:
OPTS="--listen 0.0.0.0:1080 --users /etc/micro-socks/users5.txt"

# Or set environment variables used by the binary when flags are default:
# PROXY_LISTEN="127.0.0.1:1080"
# PROXY_USERS="/etc/micro-socks/users5.txt"
# PROXY_IFACE=""

# Notes:
# - Create /etc/micro-socks/users5.txt with lines: username:password
# - Restrict permissions: chmod 640 /etc/micro-socks/users5.txt && chgrp socks5 /etc/micro-socks/users5.txt
EOF

chmod 0644 "$STAGE_DIR/lib/systemd/system/$PKG_NAME.service"
chmod 0644 "$STAGE_DIR/etc/default/micro-socks"

# Maintainer scripts
cat > "$STAGE_DIR/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e

# Create system user/group if needed
if ! getent group socks5 >/dev/null 2>&1; then
    if command -v addgroup >/dev/null 2>&1; then
        addgroup --system socks5 || true
    elif command -v groupadd >/dev/null 2>&1; then
        groupadd -r socks5 || true
    fi
fi
if ! getent passwd socks5 >/dev/null 2>&1; then
    if command -v adduser >/dev/null 2>&1; then
        adduser --system --no-create-home --ingroup socks5 --disabled-login --shell /usr/sbin/nologin socks5 || true
    elif command -v useradd >/dev/null 2>&1; then
        useradd -r -g socks5 -d /nonexistent -s /usr/sbin/nologin socks5 || true
    fi
fi

# Ensure config dir
mkdir -p /etc/micro-socks
chmod 0750 /etc/micro-socks
if getent group socks5 >/dev/null 2>&1; then
    chown root:socks5 /etc/micro-socks || true
fi

# Reload systemd to pick up new unit
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

echo "micro-socks installed. Configure /etc/default/micro-socks and enable with:"
echo "  sudo systemctl enable --now micro-socks"
exit 0
EOF

cat > "$STAGE_DIR/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e

if [ "$1" = "remove" ] || [ "$1" = "deconfigure" ]; then
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop micro-socks.service || true
    fi
fi
exit 0
EOF

cat > "$STAGE_DIR/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

if [ "$1" = "purge" ]; then
    # Best-effort remove system user
    if command -v deluser >/dev/null 2>&1; then
        deluser --system socks5 || true
    elif command -v userdel >/dev/null 2>&1; then
        userdel socks5 || true
    fi
    # Leave /etc/micro-socks/ for admin unless empty
    if [ -d /etc/micro-socks ] && [ -z "$(ls -A /etc/micro-socks 2>/dev/null)" ]; then
        rmdir /etc/micro-socks || true
    fi
fi
exit 0
EOF

chmod 0755 "$STAGE_DIR/DEBIAN/postinst" "$STAGE_DIR/DEBIAN/prerm" "$STAGE_DIR/DEBIAN/postrm"

# conffiles
cat > "$STAGE_DIR/DEBIAN/conffiles" <<'EOF'
/etc/default/micro-socks
EOF
chmod 0644 "$STAGE_DIR/DEBIAN/conffiles"

# Build .deb
if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "[!] dpkg-deb not found. Please install dpkg-dev or build-essential tooling."
  echo "    Staged tree is at: $STAGE_DIR"
  exit 1
fi

mkdir -p "$DIST_DIR"
DEB_PATH="$DIST_DIR/${PKG_NAME}_${VERSION}_${ARCH}.deb"
echo "[+] Building package: $DEB_PATH"
dpkg-deb --build "$STAGE_DIR" "$DEB_PATH" >/dev/null
echo "[✓] Done: $DEB_PATH"
