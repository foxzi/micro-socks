#!/usr/bin/env bash
set -euo pipefail

# Minimal RPM package builder for micro-socks
# Usage examples:
#   ./scripts/build-rpm.sh               # auto version from git or 0.1.0
#   VERSION=1.2.3 ./scripts/build-rpm.sh # explicit version
#   ARCH=aarch64 ./scripts/build-rpm.sh  # build for aarch64

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

# Detect architecture (RPM arch values)
ARCH="${ARCH:-}"
if [[ -z "$ARCH" ]]; then
  ARCH="$(uname -m)"
fi

# Map RPM arch to Go arch where needed
GOARCH="$ARCH"
case "$ARCH" in
  x86_64)  GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  armv7l)  GOARCH=arm   ;;
  i686)    GOARCH=386   ;;
  *)       GOARCH=amd64 ;;
esac

DIST_DIR="$ROOT_DIR/dist"
BUILD_ROOT="$DIST_DIR/rpmbuild"
SPEC_FILE="$BUILD_ROOT/SPECS/${PKG_NAME}.spec"

echo "[+] Building $PKG_NAME $VERSION for $ARCH (GOARCH=$GOARCH)"

# Clean and create RPM build structure
rm -rf "$BUILD_ROOT"
mkdir -p "$BUILD_ROOT"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

# Build static binary for Linux target
export CGO_ENABLED=0
export GOOS=linux
export GOARCH="$GOARCH"
export GOCACHE="$DIST_DIR/.gocache"

mkdir -p "$GOCACHE"

echo "[+] Compiling Go binary..."
BINARY_PATH="$BUILD_ROOT/BUILD/$BIN_NAME"
go build -ldflags "-s -w" -o "$BINARY_PATH" ./

# Create spec file
echo "[+] Writing spec file..."
cat > "$SPEC_FILE" <<EOF
Name:           $PKG_NAME
Version:        $VERSION
Release:        1%{?dist}
Summary:        Lightweight SOCKS5 proxy server in Go

License:        MIT
URL:            https://github.com/example/micro-socks
Source0:        %{name}-%{version}.tar.gz

BuildArch:      $ARCH
Requires:       systemd

%description
Minimal SOCKS5 proxy with optional username/password auth and egress interface binding.

%prep
# No prep needed - binary is pre-built

%build
# No build needed - binary is pre-built

%install
rm -rf %{buildroot}
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/lib/systemd/system
mkdir -p %{buildroot}/etc/default
mkdir -p %{buildroot}/etc/micro-socks

# Install binary
install -m 0755 $BINARY_PATH %{buildroot}/usr/bin/$BIN_NAME

# Install systemd unit
cat > %{buildroot}/usr/lib/systemd/system/$PKG_NAME.service <<'SERVICEEOF'
[Unit]
Description=SOCKS5 Proxy (micro-socks)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=socks5
Group=socks5
EnvironmentFile=-/etc/default/micro-socks
ExecStart=/usr/bin/micro-socks \$OPTS
Restart=on-failure
RestartSec=2s
AmbientCapabilities=
NoNewPrivileges=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICEEOF

# Install default config
cat > %{buildroot}/etc/default/micro-socks <<'CONFIGEOF'
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
CONFIGEOF

%files
%attr(0755,root,root) /usr/bin/$BIN_NAME
%attr(0644,root,root) /usr/lib/systemd/system/$PKG_NAME.service
%config(noreplace) %attr(0644,root,root) /etc/default/micro-socks
%dir %attr(0750,root,socks5) /etc/micro-socks

%pre
# Create system user/group if needed
getent group socks5 >/dev/null || groupadd -r socks5
getent passwd socks5 >/dev/null || useradd -r -g socks5 -d /nonexistent -s /sbin/nologin -c "SOCKS5 Proxy" socks5
exit 0

%post
# Reload systemd to pick up new unit
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi
echo "micro-socks installed. Configure /etc/default/micro-socks and enable with:"
echo "  sudo systemctl enable --now micro-socks"
exit 0

%preun
if [ \$1 -eq 0 ]; then
    # Package removal, not upgrade
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop micro-socks.service || true
    fi
fi
exit 0

%postun
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

if [ \$1 -eq 0 ]; then
    # Package removal, not upgrade
    # Leave /etc/micro-socks/ for admin unless empty
    if [ -d /etc/micro-socks ] && [ -z "\$(ls -A /etc/micro-socks 2>/dev/null)" ]; then
        rmdir /etc/micro-socks || true
    fi
fi
exit 0

%changelog
* $(date '+%a %b %d %Y') Builder <builder@example.com> - $VERSION-1
- Release $VERSION
EOF

# Build RPM
if ! command -v rpmbuild >/dev/null 2>&1; then
  echo "[!] rpmbuild not found. Please install rpm-build package."
  echo "    Spec file is at: $SPEC_FILE"
  exit 1
fi

echo "[+] Building RPM package..."
rpmbuild --define "_topdir $BUILD_ROOT" \
         --define "_rpmdir $DIST_DIR" \
         --buildroot "$BUILD_ROOT/BUILDROOT" \
         -bb "$SPEC_FILE"

RPM_PATH="$DIST_DIR/$ARCH/${PKG_NAME}-${VERSION}-1.*.${ARCH}.rpm"
if ls $RPM_PATH 1> /dev/null 2>&1; then
    echo "[✓] Done: $RPM_PATH"
else
    echo "[!] RPM build may have failed. Check $BUILD_ROOT for details."
    exit 1
fi
