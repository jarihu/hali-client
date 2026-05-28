# hali on Linux

## Installation

The Linux service model is:

- daemon runs as `hali: hali`
- CLI runs as your normal user
- IPC uses a group-writable socket under `/run/hali`

For non-sudo CLI usage, your user must be in the `hali` group.

## hali:// protocol handler (web launch)

Deb installs register `hali://` via desktop integration. For tarball/manual installs,
register it once for your user:

```sh
hali protocol install
hali protocol status
```

Webpages can then launch Hali directly:

```html
<a href="hali://model/Qwen/Qwen3-32B?version=latest">Open in Hali</a>
```

### Debian/Ubuntu (deb package)

```sh
# Download and install
sudo dpkg -i hali_<version>_amd64.deb

# The postinst script automatically:
#   - creates the 'hali' system user
#   - provisions /var/lib/hali, /var/log/hali, /run/hali
#   - enables and starts the halid systemd service
```

After install:

```sh
hali --help
hali search mistral
```

Verify service and group setup:

```sh
systemctl status halid --no-pager
getent group hali
ls -ld /var/lib/hali /var/log/hali /run/hali
```

### Manual install (tarball)

```sh
# Extract
tar xzf hali-linux-amd64.tar.gz
cd hali-linux-amd64

# Copy binaries
sudo cp hali /usr/bin/
sudo cp halid /usr/bin/

# Copy systemd unit
sudo cp halid.service /etc/systemd/system/halid.service

# Create system user
sudo useradd --system --no-create-home --shell /usr/sbin/nologin hali

# Create service directories
sudo install -d -m 2775 -o hali -g hali /var/lib/hali /var/log/hali /run/hali

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable halid
sudo systemctl start halid
```

Then add your user to the `hali` group for non-sudo CLI access:

```sh
sudo usermod -aG hali "$USER"
newgrp hali
sudo systemctl restart halid
```

### Build from source

```sh
# On Linux directly
go build -o bin/hali .
go build -o bin/halid ./cmd/service

# Or use the build script (also packages tar.gz and deb)
installer/linux/build-linux.sh
```

## systemd service

The service is named **halid** and runs under the `hali` system user.

### Unit file

```
[Unit]
Description=Hali Daemon (local model cache + LAN sync)
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/halid
Restart=always
RestartSec=5

User=hali
Group=hali

LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

### systemctl commands

```sh
# Check status
systemctl status halid

# Start / stop / restart
sudo systemctl start halid
sudo systemctl stop halid
sudo systemctl restart halid

# Enable on boot / disable
sudo systemctl enable halid
sudo systemctl disable halid

# View logs
journalctl -u halid -f
journalctl -u halid --since "10 minutes ago"
```

### Or use the hali CLI (aliases to systemctl)

```sh
hali service start          # → systemctl start halid
hali service stop           # → systemctl stop halid
hali service status         # → systemctl status halid
hali service install        # → provision and enable halid
hali service uninstall      # → disable and stop halid

hali daemon start           # same as hali service start
hali daemon stop            # same as hali service stop
hali daemon status          # same as hali service status
```

### Service storage (FHS-compliant)

```
/var/lib/hali/
  cache/
  torrents/
  models/

/var/log/hali/

/run/hali/
  .ready   (startup sentinel)
```

### Run CLI Without sudo (recommended)

The `halid` service runs as user/group `hali`. To run `hali` CLI commands as a normal user:

```sh
sudo usermod -aG hali $USER
newgrp hali
```

Then restart the service once so the IPC socket is recreated with group access:

```sh
sudo systemctl restart halid
```

After that, commands like `hali daemon status` and `hali list` should work without `sudo`.

Quick verification:

```sh
id -nG
ls -l /run/hali
hali daemon status
```

If your current shell still does not show `hali` in groups, log out/in (or reboot) and retry.

### Troubleshooting Linux permissions

If you see `permission denied`, `daemon is not running`, or IPC/socket access errors from CLI:

1. Confirm your active shell has the `hali` group: `id -nG`
2. Confirm runtime dir/socket ownership and mode: `ls -l /run/hali`
3. Restart service to recreate socket with expected permissions: `sudo systemctl restart halid`
4. Re-open shell session (group changes only apply to new sessions)
5. Check service logs: `journalctl -u halid -n 100 --no-pager`

## User mode (without systemd)

If you prefer to run the daemon manually (not as a system service):

```sh
# Data directory: ~/.hali/
hali daemon start

# Check status
hali daemon status

# Stop when done
hali daemon stop
```

In user mode, config and data are stored under `~/.hali/`:

```
~/.hali/
  config.json
  logs/
  cache/
  torrents/
```

## Configuration

Config file: `~/.hali/config.json` (user mode) or `/var/lib/hali/config.json` (service mode)

```json
{
  "streaming_hash": true,
  "models_dir": "/mnt/data/llama-models",
  "lmstudio_models_dir": "/home/<you>/.lmstudio/models",
  "ollama_models_dir": "/home/<you>/.ollama/models",
  "daemon_listen_addr": "0.0.0.0"
}
```

### Environment variables

```sh
export HALI_MODELS_DIR="/mnt/data/llama-models"
export ENABLE_STREAMING_HASH="true"
export LMSTUDIO_MODELS_DIR="/home/jarit/.lmstudio/models"
export OLLAMA_HOME="/home/jarit/.ollama"
```

Or add to `~/.bashrc` / `~/.zshrc` for persistence.

## Example workflow

### Get a model

```sh
# Start daemon (required for seeding + .torrent ingest uploads)
hali daemon start

# Search Hugging Face — results ranked by downloads, filtered to GGUF
hali search llama

# Download interactively — pick a quantization from the list
hali pull llama

# Or use a Hugging Face repo path directly
hali pull TheBloke/Llama-3-8B-Instruct-GGUF

# Or use the canonical model ID to skip all prompts
hali pull llama:8b:instruct:q5_k_m

# Download all GGUF files from a repo (whole repo variants)
hali pull TheBloke/Llama-3-8B-Instruct-GGUF --non-interactive
```

Successful pulls enqueue ingest delivery so the backend receives the real `.torrent`
artifact produced by the daemon.

### Inspect your cache and daemon state

```sh
# List every cached model with size and download date
hali list

# Show daemon PID, uptime, seeding list, and visible LAN peers
hali daemon status

# Live download/upload speeds in the terminal
hali stats

# Open the web dashboard in your browser (http://127.0.0.1:47433)
hali stats --web

# Tail daemon logs in real time
journalctl -u halid -f
```

### Export to a runtime

hali stores and distributes models — it does not run them. Export to a runtime when you want to use a model:

```sh
# See which runtimes are installed and where they look for models
hali runtime list

# Create an Ollama manifest pointing at the cached GGUF (no file copy, instant)
hali export ollama mistral:7b:instruct:q4_k_m

# Copy the GGUF into LM Studio's models directory
hali export lmstudio mistral:7b:instruct:q4_k_m

# Export to every detected runtime in one command
hali export all mistral:7b:instruct:q4_k_m
```

### Manage the service

```sh
# Register with systemd — starts on boot, restarts on crash
hali service install

# Check service state
hali service status

# Start, stop, restart
hali service start
hali service stop
hali service restart

# Remove service registration (models are not deleted)
hali service uninstall
```

### Configure hali

```sh
# Print all settings and the config file path
hali config show

# Common adjustments:
hali config set max_upload_mbps 50        # Cap upload speed (0 = unlimited)
hali config set max_download_mbps 200     # Cap download speed
hali config set models_dir /mnt/data      # Custom model storage path
hali config set debug true                # Verbose daemon logs
```

After changing config, restart the daemon to apply:

```sh
sudo systemctl restart halid
# or: hali service restart
```

### Telemetry

```sh
hali telemetry status     # Check if enabled
hali telemetry enable     # Opt in to anonymous pull-event reporting
hali telemetry disable    # Opt out — queued events stay on disk but are not sent
```

### Publisher profile

If you seed models to others, create a signed profile to attribute your contributions:

```sh
hali profile create       # Interactive prompts for name, description, website, contact
```

## LAN acceleration

The daemon broadcasts model availability via UDP multicast on `239.192.42.1:4269`.
Other machines on the LAN with the daemon running will discover these models
and download from peers instead of Hugging Face.

### Verify LAN is working

```sh
hali daemon status
```

Look for the `LAN AVAILABLE` section — it lists models discovered from peers.

### Firewall considerations

If your firewall blocks multicast, open port `4269` for UDP:

```sh
# ufw
sudo ufw allow 4269/udp

# firewalld
sudo firewall-cmd --add-port=4269/udp --permanent
sudo firewall-cmd --reload
```

LAN is always optional — if multicast is unavailable, downloads fall back to
Hugging Face HTTP.

### Troubleshooting: Sender Not Visible

If another host says it is sending but this Linux host does not see it:

```sh
sudo tcpdump -ni any udp port 4269
```

If no packets arrive, the issue is upstream network multicast policy (router/AP/VLAN),
not local hali state. Also verify sender has active `seeding` entries and both hosts
use compatible `lan_hmac_enabled`/shared-secret settings.

## Upgrade

### Deb package

```sh
sudo dpkg -i hali_<new_version>_amd64.deb
sudo systemctl restart halid
```

### Manual install

```sh
# Replace binaries
sudo cp hali /usr/bin/
sudo cp halid /usr/bin/
sudo systemctl restart halid
```
