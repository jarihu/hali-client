#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
STAGE_ROOT="$ROOT_DIR/bin/hali-linux-amd64-stage"
ARCHIVE_DIR="hali-linux-amd64"
STAGE_DIR="$STAGE_ROOT/$ARCHIVE_DIR"

rm -rf "$STAGE_ROOT"
mkdir -p "$STAGE_DIR"

cp "$ROOT_DIR/bin/hali-linux-amd64" "$STAGE_DIR/hali"
cp "$ROOT_DIR/bin/halid-linux-amd64" "$STAGE_DIR/halid"

if [[ -f "$ROOT_DIR/bin/hali-tray-linux-amd64" ]]; then
  cp "$ROOT_DIR/bin/hali-tray-linux-amd64" "$STAGE_DIR/hali-tray"
fi

cp "$ROOT_DIR/installer/linux/halid.service" "$STAGE_DIR/halid.service"

cat > "$STAGE_DIR/README-linux-install.txt" << 'EOF'
Manual install (Ubuntu 24.04 amd64):

  sudo cp hali /usr/bin/
  sudo cp halid /usr/bin/
  sudo cp halid.service /etc/systemd/system/halid.service
  sudo useradd --system --no-create-home --shell /usr/sbin/nologin hali || true
  sudo install -d -o hali -g hali /var/lib/hali /var/log/hali /run/hali
  sudo systemctl daemon-reload
  sudo systemctl enable halid
  sudo systemctl start halid

EOF

tar -C "$STAGE_ROOT" -czf "$ROOT_DIR/bin/hali-linux-amd64.tar.gz" "$ARCHIVE_DIR"
rm -rf "$STAGE_ROOT"
echo "Built $ROOT_DIR/bin/hali-linux-amd64.tar.gz"