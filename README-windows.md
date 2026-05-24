# hali on Windows

## Installation

### Pre-built binaries

Download the latest release and extract `hali.exe`, `halid.exe`, and `hali-tray.exe`
to a directory in your `PATH` (e.g. `C:\Program Files\Hali`).

### Build from source

```powershell
.\build.ps1
```

Outputs to `bin\oss\`. To build the MSI installer:

```powershell
cd installer
.\build.ps1
```

Produces:
- `bin\oss\hali.exe` — CLI
- `bin\oss\halid.exe` — Windows service binary
- `bin\oss\hali-tray.exe` — system tray app
- `installer\Hali.msi` — Windows installer

## Windows Service (SCM)

> **The service is optional.** Without it, `hali pull` auto-starts the daemon after
> each download, and `hali daemon start` works at any time. Install the service only
> if you want the daemon to survive reboots without manual intervention.

hali runs as a Windows service named **HaliDaemon** (display name: "Hali Model Cache Service").
It starts automatically on boot and automatically restarts on failure.

### Install and start

```powershell
hali service install
hali service start
```

Or in a single command (install also enables and starts):

```powershell
hali service install
```

### Manage the service

```powershell
hali service status     # check if running
hali service stop       # stop the service
hali service start      # start the service
hali service uninstall  # remove the service
```

### Manual SCM commands

You can also use standard Windows tools:

```powershell
sc query HaliDaemon
sc start HaliDaemon
sc stop HaliDaemon
sc delete HaliDaemon
```

### Service recovery

The service is configured with automatic recovery:
- Restart after 5 seconds (first failure)
- Restart after 30 seconds (second failure)
- Restart after 60 seconds (subsequent failures)

### Storage locations (service mode)

```
%ProgramData%\Hali\
  config.json
  logs\              hali.log
  models\            <base>\<size>-<variant>\<quant>\model.gguf
  torrents\          <infohash>.torrent
```

### Logs

When running as a Windows service, logs are written to:
```
%ProgramData%\Hali\logs\hali.log
```

## System Tray App

`hali-tray.exe` provides a system tray icon with quick access to:

- **Open Dashboard** — opens `http://127.0.0.1:47433`
- **Pause / Resume Transfers** — pause or resume BitTorrent transfers
- **Open Cache Folder** — opens the model cache directory
- **View Logs** — opens the log file
- **Restart Service** — restarts the Hali daemon
- **Startup Settings** — configure whether the tray app starts on login

### Tray icon colors

| Color | Meaning |
|-------|---------|
| Green | Seeding |
| Cyan | Downloading |
| Amber | Paused |
| Red | Error |
| Gray | Idle |

## Configuration

Config file: `%ProgramData%\Hali\config.json`

```json
{
  "streaming_hash": true,
  "models_dir": "D:\\Models\\Llama",
  "lmstudio_models_dir": "C:\\Users\\<you>\\.lmstudio\\models",
  "ollama_models_dir": "C:\\Users\\<you>\\.ollama\\models"
}
```

### Environment variables

Set via System Properties → Environment Variables or in PowerShell:

```powershell
$env:HALI_MODELS_DIR = "D:\Models\Llama"
$env:ENABLE_STREAMING_HASH = "true"
$env:LMSTUDIO_MODELS_DIR = "C:\Users\jarit\.lmstudio\models"
$env:OLLAMA_HOME = "C:\Users\jarit\.ollama"
```

## Example workflow

### Get a model

```powershell
# Search Hugging Face — results ranked by downloads, filtered to GGUF
hali search llama

# Download interactively — pick a quantization from the list
hali pull llama

# Or use a Hugging Face repo path directly
hali pull TheBloke/Llama-3-8B-Instruct-GGUF

# Or use the canonical model ID to skip all prompts
hali pull llama:8b:instruct:q5_k_m
```

### Inspect your cache and daemon state

```powershell
# List every cached model with size and download date
hali list

# Show daemon PID, uptime, seeding list, and visible LAN peers
hali daemon status

# Live download/upload speeds in the terminal
hali stats

# Open the web dashboard in your browser (http://127.0.0.1:47433)
hali stats --web
```

### Export to a runtime

hali stores and distributes models — it does not run them. Export to a runtime when you want to use a model:

```powershell
# See which runtimes are installed and where they look for models
hali runtime list

# Create an Ollama manifest pointing at the cached GGUF (no file copy, instant)
hali export ollama llama:8b:instruct:q5_k_m

# Copy the GGUF into LM Studio's models directory
hali export lmstudio llama:8b:instruct:q5_k_m

# Export to every detected runtime in one command
hali export all llama:8b:instruct:q5_k_m
```

### Manage the Windows service

The service is optional — `hali pull` auto-launches the daemon when needed. Install it only if you want the daemon to survive reboots without manual action.

```powershell
# Register with Windows SCM (starts on boot, restarts on crash)
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

```powershell
# Print all settings and the config file path (%ProgramData%\Hali\config.json)
hali config show

# Common adjustments:
hali config set max_upload_mbps 50        # Cap upload speed (0 = unlimited)
hali config set max_download_mbps 200     # Cap download speed
hali config set models_dir D:\Models      # Store models on a different drive
hali config set debug true                # Verbose daemon logs
```

After changing config, restart the daemon:

```powershell
hali service restart
# or: hali daemon stop; hali daemon start
```

### Telemetry

```powershell
hali telemetry status     # Check if enabled
hali telemetry enable     # Opt in to anonymous pull-event reporting
hali telemetry disable    # Opt out — queued events stay on disk but are not sent
```

### Publisher profile

If you seed models to others, create a signed profile to attribute your contributions:

```powershell
hali profile create       # Interactive prompts for name, description, website, contact
```

### System tray app

Launch `hali-tray.exe` for a persistent status icon in the Windows notification area:

```powershell
start bin\hali-tray.exe
```

The tray icon changes color based on daemon state (green = seeding, cyan = downloading, amber = paused, red = error, gray = idle) and provides quick access to the dashboard, cache folder, logs, and service restart.

## Web dashboard

Start the daemon and open the dashboard:

```powershell
hali daemon start
hali stats --web
```

Or visit `http://127.0.0.1:47433` directly in your browser.

The dashboard shows:
- Live download and upload speeds
- Session transfer totals
- Active model rows with status and peer count
- Clickable magnet links

## LAN setup

The Windows service and daemon both support LAN discovery out of the box.
No additional configuration needed — UDP multicast on `239.192.42.1:4269` is
enabled automatically when the daemon starts.

hali automatically sends multicast announcements over all usable IPv4 LAN interfaces
to avoid adapter-priority issues on systems with virtual/VPN adapters.

If your network blocks multicast, LAN acceleration degrades silently and
downloads fall back to Hugging Face.

### If This PC Is Not Visible on Another Host

1. Run `hali daemon status` and confirm at least one model is `seeding`.
2. Ensure Windows Defender Firewall allows UDP `4269` inbound and outbound.
3. If group policy is used, ensure multicast is not disabled via policy.
4. Verify remote host receives packets (`tcpdump` on Linux or packet capture on Windows).
