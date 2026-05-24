# E2E Test Runbook (Verified)

This runbook captures the exact commands and conditions used to run E2E-oriented torrent tests successfully in this repo.

## Environment

- OS: Windows (PowerShell)
- Repo root: `C:\Users\jarit\coding\hali-client`
- Go toolchain available

## Why this runbook exists

The direct-peer download test is environment-gated. It only runs when `HALI_E2E_DIRECT_PEER=1` is set.

## Preconditions

1. Start from repo root.
2. Prefer no competing daemon process when running command-level tests that assume isolated state.
3. Use PowerShell env var syntax for the E2E gate.

## Commands that passed

Run from repo root:

```powershell
Set-Location 'C:\Users\jarit\coding\hali-client'
```

1. Baseline daemon/cmd/cache validation:

```powershell
go test ./internal/cache ./internal/daemon ./cmd -count=1
```

2. Torrent path sanity checks used during LAN work:

```powershell
go test ./internal/torrent -run "StartDownload|Magnet" -count=1
```

3. Direct-peer E2E (env-gated):

```powershell
$env:HALI_E2E_DIRECT_PEER = '1'
go test ./internal/torrent -run TestStartDownloadWithDirectPeerAddr -count=1 -v
Remove-Item Env:HALI_E2E_DIRECT_PEER
```

4. New integration E2E tests (daemon/CLI behavior):

```powershell
go test -tags integration ./test -run "TestDaemonAppliesConfiguredSpeedLimitsE2E|TestDaemonLogsHMACMismatchE2E" -count=1 -v
go test -tags integration ./test -run "TestCLIPullResumesPartialDownloadE2E|TestCLIPullUsesLANBeforeHFForCanonicalID|TestCLIPullModelIsActivelySeededAfterDownload" -count=1 -v
```

## Expected successful outcome

- Each command exits with `ok` for targeted packages/tests.
- `TestStartDownloadWithDirectPeerAddr` runs (not skipped) when env var is set.
- No lingering metadata-provenance warnings are expected from this test path.

## qBittorrent internet seeding E2E

Verifies that a freshly pulled model is automatically registered with qBittorrent for seeding.

### Preconditions

1. qBittorrent running with WebUI enabled on `http://127.0.0.1:8080`.
2. `%ProgramData%\Hali\config.json` contains a `qbittorrent` block with `"enabled": true`, `url`, and credentials.
3. `"debug": true` in `config.json` for full trace.
4. Daemon not yet running (start fresh below).

### Steps

```powershell
cd C:\Users\jarit\coding\hali-client

# 1. Build
go build -tags oss -o bin/oss/hali.exe .

# 2. Fresh daemon start
bin\oss\hali.exe daemon stop 2>$null
bin\oss\hali.exe daemon start

# 3. Identify daemon log file (named daemon.log.<pid> if daemon.log is admin-owned)
$pid = (bin\oss\hali.exe daemon status 2>&1 | Select-String "PID (\d+)" | ForEach-Object { $_.Matches[0].Groups[1].Value })
$logFile = Get-ChildItem "C:\ProgramData\Hali\logs\" | Where-Object { $_.Name -match $pid } | Select-Object -First 1 -ExpandProperty FullName
if (-not $logFile) { $logFile = "C:\ProgramData\Hali\logs\daemon.log" }
Write-Output "Watching: $logFile"

# 4. Pull a small model not yet in cache
bin\oss\hali.exe pull --non-interactive mradermacher/jina-reranker-v1-tiny-en-GGUF

# 5. Wait for seeding to complete, then check log
Start-Sleep -Seconds 10
Get-Content $logFile | Select-String "qbittorrent"

# 6. Confirm torrent appeared in qBittorrent
$login = Invoke-WebRequest -Uri "http://127.0.0.1:8080/api/v2/auth/login" `
    -Method POST -ContentType "application/x-www-form-urlencoded" `
    -Body "username=admin&password=" -SessionVariable s
$torrents = (Invoke-WebRequest -Uri "http://127.0.0.1:8080/api/v2/torrents/info" -WebSession $s).Content | ConvertFrom-Json
$torrents | Where-Object { $_.category -eq "hali" } | Select-Object name, state, progress, save_path
```

### Expected log trace

```
qbittorrent: setupPublishingHooks called
qbittorrent: seeding hook registered  url=http://127.0.0.1:8080
qbittorrent: seed job done, emitting TorrentPublishedEvent  infohash=<hash>  content_dir=<path>
qbittorrent: OnTorrentPublished received  infohash=<hash>
qbittorrent: Seed called
qbittorrent: torrent file found
qbittorrent: login response  status=200  body=Ok.
qbittorrent: info decoded  count=0
qbittorrent: torrent registered for internet seeding
```

### Expected qBittorrent state

- Torrent appears with `state=stalledUP`, `progress=1` (100%).
- `save_path` points directly to `%ProgramData%\Hali\models\...` — no file copying.
- Category matches `"hali"` (or whatever was configured).

### Idempotency check

Re-pull the same model. The log should show:

```
qbittorrent: torrent already registered  infohash=<hash>
```

No duplicate in qBittorrent.

---

---

## Transmission internet seeding E2E

Verifies that a freshly pulled model is automatically registered with Transmission for seeding.

### Automated test (mock Transmission — no real Transmission required)

```powershell
go test -tags integration ./test -run TestTransmissionSeedingRegistrationE2E -count=1 -v
```

This test starts an isolated daemon and a mock Transmission RPC server, seeds a small
fake model, and verifies the hook calls `torrent-add` with the correct `download-dir`.

### Manual test (real Transmission)

#### Preconditions

1. Transmission running with RPC enabled on `http://127.0.0.1:9091`.
2. `%ProgramData%\Hali\config.json` contains a `transmission` block with `"enabled": true` and `url`.
3. `"debug": true` in `config.json` for full trace.
4. Daemon not yet running (start fresh below).

#### Steps

```powershell
cd C:\Users\jarit\coding\hali-client

# 1. Build
go build -tags oss -o bin/oss/hali.exe .

# 2. Fresh daemon start
bin\oss\hali.exe daemon stop 2>$null
bin\oss\hali.exe daemon start

# 3. Identify daemon log file
$pid = (bin\oss\hali.exe daemon status 2>&1 | Select-String "PID (\d+)" | ForEach-Object { $_.Matches[0].Groups[1].Value })
$logFile = Get-ChildItem "C:\ProgramData\Hali\logs\" | Where-Object { $_.Name -match $pid } | Select-Object -First 1 -ExpandProperty FullName
if (-not $logFile) { $logFile = "C:\ProgramData\Hali\logs\daemon.log" }
Write-Output "Watching: $logFile"

# 4. Pull a small model not yet in cache
bin\oss\hali.exe pull --non-interactive mradermacher/jina-reranker-v1-tiny-en-GGUF

# 5. Wait for seeding to complete, then check log
Start-Sleep -Seconds 10
Get-Content $logFile | Select-String "transmission"

# 6. Confirm torrent appeared in Transmission via RPC
$sid = (Invoke-WebRequest -Uri "http://127.0.0.1:9091/transmission/rpc" `
    -Method POST -ContentType "application/json" `
    -Body '{"method":"session-get"}' -ErrorAction SilentlyContinue).Headers["X-Transmission-Session-Id"]
$torrents = (Invoke-WebRequest -Uri "http://127.0.0.1:9091/transmission/rpc" `
    -Method POST -ContentType "application/json" `
    -Headers @{"X-Transmission-Session-Id" = $sid} `
    -Body '{"method":"torrent-get","arguments":{"fields":["name","status","percentDone","downloadDir"]}}').Content | ConvertFrom-Json
$torrents.arguments.'torrents' | Where-Object { $_.downloadDir -like "*Hali*" } | Select-Object name, status, percentDone, downloadDir
```

#### Expected log trace

```
daemon: setupPublishingHooks called
transmission: seeding hook registered  url=http://127.0.0.1:9091
transmission: OnTorrentPublished received  infohash=<hash>
transmission: Seed called
transmission: torrent file found
transmission: session refreshed
transmission: torrent registered for internet seeding  infohash=<hash>
```

#### Expected Transmission state

- Torrent appears with `status=6` (seeding), `percentDone=1.0` (100%).
- `downloadDir` points directly to `%ProgramData%\Hali\models\...` — no file copying.

#### Idempotency check

Re-pull the same model. The log should show:

```
transmission: torrent already registered  infohash=<hash>
```

No duplicate in Transmission.

---

## Common failure causes

- `HALI_E2E_DIRECT_PEER` not set: direct-peer test is skipped.
- Running from wrong directory: package paths fail to resolve.
- Background process state interference: stop/restart daemon and rerun.
- Integration tests skip when daemon is already bound to fixed IPC port (`127.0.0.1:47432`).
- LAN tests require multicast availability on the test host/network.
- Online pull/resume tests need Hugging Face reachable from the test machine.
- **qBittorrent hook not firing:** Check `daemon.log.<pid>` — if the log says "integration disabled", either `qbittorrent.enabled` is `false` or `qbittorrent.url` is missing/empty in `config.json`. If it says "login failed", check the password. If no qbittorrent log lines appear at all, ensure `"debug": true` is set and you are reading the correct PID-specific log file.
- **daemon.log is empty (0 bytes):** The file may be admin-owned and not writable by the current user. The daemon falls back to `daemon.log.<pid>`. Check `Get-ChildItem "C:\ProgramData\Hali\logs\"` for the PID-suffixed file. To fix permanently: run as admin and `icacls "C:\ProgramData\Hali\logs\daemon.log" /grant Everyone:M` (or delete the file and let the daemon recreate it).
- **"daemon started but not responding":** Stale `.ready` file from a hard-killed previous daemon. The daemon starts correctly but `IsRunning()` was called before it bound IPC. Check with `hali daemon status` — the daemon is usually running fine.

## Optional one-liner for CI-like local rerun

```powershell
Set-Location 'C:\Users\jarit\coding\hali-client'; go test ./internal/cache ./internal/daemon ./cmd -count=1; go test ./internal/torrent -run "StartDownload|Magnet" -count=1; $env:HALI_E2E_DIRECT_PEER='1'; go test ./internal/torrent -run TestStartDownloadWithDirectPeerAddr -count=1 -v; Remove-Item Env:HALI_E2E_DIRECT_PEER
```
