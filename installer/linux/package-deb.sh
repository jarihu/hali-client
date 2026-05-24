#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
WORK_ROOT="$(mktemp -d)"
PKG_ROOT="$WORK_ROOT/deb-pkg"
VERSION="${VERSION:-1.0.0}"

sanitize_deb_version() {
  local raw="$1"
  local cleaned
  cleaned="$(printf '%s' "$raw" | sed -E 's/[^0-9A-Za-z.+:~\-]/-/g')"
  if [[ "$cleaned" =~ ^[0-9] ]]; then
    printf '%s' "$cleaned"
    return
  fi
  printf '1.0.0+%s' "$cleaned"
}

VERSION="$(sanitize_deb_version "$VERSION")"

mkdir -p "$PKG_ROOT/DEBIAN"
mkdir -p "$PKG_ROOT/usr/bin"
mkdir -p "$PKG_ROOT/etc/systemd/system"
mkdir -p "$PKG_ROOT/usr/share/applications"

cp "$ROOT_DIR/bin/hali-linux-amd64" "$PKG_ROOT/usr/bin/hali"
cp "$ROOT_DIR/bin/halid-linux-amd64" "$PKG_ROOT/usr/bin/halid"

if [[ -f "$ROOT_DIR/bin/hali-tray-linux-amd64" ]]; then
  cp "$ROOT_DIR/bin/hali-tray-linux-amd64" "$PKG_ROOT/usr/bin/hali-tray"
fi

cp "$ROOT_DIR/installer/linux/halid.service" "$PKG_ROOT/etc/systemd/system/halid.service"
cp "$ROOT_DIR/installer/linux/hali.desktop" "$PKG_ROOT/usr/share/applications/hali.desktop"
cp "$ROOT_DIR/installer/linux/deb/control" "$PKG_ROOT/DEBIAN/control"
cp "$ROOT_DIR/installer/linux/deb/postinst" "$PKG_ROOT/DEBIAN/postinst"
cp "$ROOT_DIR/installer/linux/deb/prerm" "$PKG_ROOT/DEBIAN/prerm"

sed -i "s/^Version: .*/Version: ${VERSION}/" "$PKG_ROOT/DEBIAN/control"

chmod 0755 "$PKG_ROOT/DEBIAN/postinst" "$PKG_ROOT/DEBIAN/prerm"
chmod 0755 "$PKG_ROOT/DEBIAN"
chmod 0644 "$PKG_ROOT/DEBIAN/control"
chmod 0755 "$PKG_ROOT/usr/bin/hali" "$PKG_ROOT/usr/bin/halid"
if [[ -f "$PKG_ROOT/usr/bin/hali-tray" ]]; then
  chmod 0755 "$PKG_ROOT/usr/bin/hali-tray"
fi
chmod 0644 "$PKG_ROOT/etc/systemd/system/halid.service"
chmod 0644 "$PKG_ROOT/usr/share/applications/hali.desktop"

dpkg-deb --build "$PKG_ROOT" "$WORK_ROOT/hali_${VERSION}_amd64.deb"
cp "$WORK_ROOT/hali_${VERSION}_amd64.deb" "$ROOT_DIR/bin/hali_${VERSION}_amd64.deb"
rm -rf "$WORK_ROOT"
echo "Built $ROOT_DIR/bin/hali_${VERSION}_amd64.deb"