# hali — usage guide

`hali` is a local-first model cache for LLMs. It downloads models from Hugging Face,
caches them locally, and seeds completed downloads to other machines on
your LAN via BitTorrent. No account, no cloud, no central server required.

Client model is hybrid (not metadata-only):
- resolves GGUF artifacts
- generates torrent artifacts/metadata
- registers artifacts with backend
- starts local LAN seeding

Backend model is registry-only:
- stores artifact/job registration
- does not track or own seeding runtime state

---

## Quick start

```sh
hali search mistral          # find a model on Hugging Face
hali daemon start            # start daemon for seeding + torrent ingest
hali pull mistral            # download it interactively
hali list                    # show everything in the local cache
hali profile create          # create/update signed publisher profile
hali daemon status           # check what is being seeded
hali network status          # inspect LAN networking capabilities
hali protocol install        # register hali:// URL handler for this user
hali protocol status         # verify protocol handler registration
hali config show             # inspect full editable daemon config
hali config set telemetry.enabled false
hali stats --web             # open the live local Web UI
```

---

## Commands

### `hali search <query>`

Search Hugging Face for GGUF models matching the query. Results are ranked by
download count.

```sh
hali search mistral
hali search "llama 3 instruct"
hali search codellama
```

Output:
```
  1  TheBloke/Mistral-7B-Instruct-v0.2-GGUF       2.1M downloads
  2  bartowski/Mistral-7B-Instruct-v0.3-GGUF       840.5K downloads
  ...

Use 'hali pull <repo>' to download a model.
```

---

### `hali pull <model>`

Download a model from Hugging Face (or from a LAN peer if the daemon is running
and a peer has it).

`<model>` can be:

| Format | Example |
|--------|---------|
| Search query | `hali pull mistral` |
| Hugging Face repo ID | `hali pull TheBloke/Mistral-7B-Instruct-v0.2-GGUF` |
| Canonical model ID | `hali pull mistral:7b:instruct:q4_k_m` |

`hali pull` with a query runs a search first and prompts you to select a repo, then
a GGUF file. Repos are sorted by downloads; files are sorted smallest-first.

```sh
hali pull mistral
hali pull TheBloke/Mistral-7B-Instruct-v0.2-GGUF
hali pull mistral:7b:instruct:q4_k_m
```

After download the file is saved under the local cache and the daemon is notified
to start seeding.

For each successful pull, hali also enqueues a publish event so the ingest worker
uploads the real `.torrent` artifact (multipart form with torrent file), not just
metadata.

If the daemon is not running, start it first:

```sh
hali daemon start
```

Download the whole Hugging Face repo (all GGUF files in that repo):

```sh
hali pull owner/repo --non-interactive
```

Do not pass `--file-name` or `--files` when you want every GGUF file.

If a LAN download completes through the daemon, `hali pull` also prints the derived
magnet URI. For local HF downloads, the magnet becomes visible once seeding is
active via `hali daemon status` or the web dashboard.

When telemetry is enabled, pull events are sent to the ingest backend with a
required signed publisher token:

- `publisher_pubkey` (Ed25519 public key, 64-char hex)
- `publisher_sig` (Ed25519 signature, 128-char hex)

The daemon signs each event over a canonical payload including:
`model_id`, `revision`, `infohash`, `magnet`, `source_url`, `local_hash`,
`timestamp`, and `publisher_pubkey`.

The daemon runs in LAN-only mode, and publishing warns that
internet peers may not be able to reach the artifact. In non-interactive contexts,
publishing is blocked unless `--allow-unreachable-publish` is set.

---

### `hali profile create`

Create or update your publisher profile interactively.

This command:

- prompts for `display_name`, `description`, `website`, and `contact`
- loads/creates your local node keypair
- canonicalizes profile JSON, hashes it with SHA256, then signs with your node private key
- stores the signed profile at `<DataDir>/profile.json`
- submits it to the backend `POST /profile` endpoint

Signature contract (must match backend verification):

1. `canonical = Canonicalize(profile)`
2. `digest = SHA256(canonical)`
3. `signature = Ed25519Sign(node_private_key, digest)`

By default, the command requires `HALI_PROFILE_BACKEND` to be set to the profile backend URL.
Set `HALI_PROFILE_BACKEND` to the target endpoint.

```sh
hali profile create
```

If `network.mode=lan_only`, profile submission warns that internet reachability may be
limited. In non-interactive contexts, pass `--allow-unreachable-publish` to continue.

---

### `hali network status`

Show effective network mode, transport capabilities, and reachability diagnostics.

```sh
hali network status
```

Example output:
```
Mode: internet

Capabilities:
- LSD: enabled
- DHT: enabled
- PEX: enabled
- Trackers: enabled
- Public peers: enabled

Diagnostics:
- Public reachability: unknown
- Incoming peers: unavailable
- DHT connectivity: healthy
```

---

### `hali network seen`

Show recent LAN announcements observed by the daemon.

```sh
hali network seen
hali network seen -p
```

`-p` opens an interactive picker and then downloads from LAN using the selected announcement.

### `hali network pull [model_id]`

Pull directly from recent LAN announcements only (no Hugging Face fallback).

```sh
hali network pull
hali network pull mistral:7b:instruct:q4_k_m
```

If LAN peers do not provide torrent metadata or are unreachable, the command fails with a LAN error.

---

### `hali open <hali://url>`

Open a model from a `hali://` URL and route into normal pull behavior.
Routing is strict:
- `?file=<name>`: single GGUF file mode only
- `?all=1`: full-repo GGUF mode (all GGUF files)
- no flag: single best GGUF fallback mode

`open` delegates to `pull` after selecting the route.

```sh
hali open "hali://model/HauhauCS/Qwen3.6-35B-A3B-Uncensored-HauhauCS-Aggressive"
```

To launch Hali from a webpage, register the protocol handler once:

```sh
hali protocol install
```

Then webpages can use links like:

```html
<a href="hali://model/HauhauCS/Qwen3.6-35B-A3B-Uncensored-HauhauCS-Aggressive">Open in Hali</a>
<a href="hali://model/HauhauCS/Qwen3.6-35B-A3B-Uncensored-HauhauCS-Aggressive?all=1">Download all GGUF variants</a>
```

Manage registration:

```sh
hali protocol status
hali protocol uninstall
```

---

### `hali config show`

Show the active daemon config values and config file path.

```sh
hali config show
```

---

### `hali config set <key> <value>`

Set persistent daemon configuration values.

Currently supported:
- `streaming_hash` -> `true|false`
- `debug` -> `true|false`
- `telemetry.enabled` -> `true|false`
- `lan.hmac_enabled` -> `true|false`
- `lan.hmac_shared_secret` -> `<64-char-hex> | default`
- `models_dir` -> `<path> | default`
- `lmstudio_models_dir` -> `<path> | default`
- `ollama_models_dir` -> `<path> | default`
- `max_upload_mbps` -> `<integer Mbps>` (0 = unlimited)
- `max_download_mbps` -> `<integer Mbps>` (0 = unlimited)

```sh
hali config set lan.hmac_enabled false
hali config set lan.hmac_shared_secret 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
hali daemon restart
```

---

### `hali list`

List all models in the local cache.

```sh
hali list
```

Output:
```
MODEL ID                                    SIZE        DOWNLOADED
------------------------------------------  ----------  ----------
mistral:7b:instruct:q4_k_m                 4.1 GB      2026-05-18
llama:8b:instruct:q5_k_m                   5.7 GB      2026-05-17
```

---

### `hali service install|start|stop|status`

Manage Hali with the host service manager.

The service is **optional**. It exists solely for persistence — it starts the daemon
automatically on boot and restarts it on crash. Without the service, `hali pull`
auto-launches the daemon after each download, and LAN sharing works normally.
Install the service when you want the daemon to survive reboots without manual steps.

- Windows: uses Service Control Manager (SCM)
- Linux: shells out to `systemctl` for `halid`

```sh
hali service install
hali service start
hali service status
hali service stop
```

Linux `install` additionally provisions the `hali` system user and ensures:

- `/var/lib/hali`
- `/var/log/hali`
- `/run/hali`

all exist with `hali:hali` ownership.

For non-sudo CLI access on Linux, add your user to the `hali` group and restart the service:

```sh
sudo usermod -aG hali "$USER"
newgrp hali
sudo systemctl restart halid
```

Quick checks:

```sh
id -nG
ls -l /run/hali
hali daemon status
```

---

### `hali export ollama <model_id>`

Create or update an Ollama-compatible manifest for a cached GGUF model.

This command:
- keeps hali as the source of truth
- does not copy files
- does not use symlinks
- is safe to run repeatedly (idempotent)

```sh
hali export ollama mistral:7b:instruct:q4_k_m
```

Output:
```
Exported to Ollama:
  model: mistral:7b:instruct:q4_k_m
  path: ~/.ollama/models/manifests/mistral_7b_instruct_q4_k_m.json
```

If the manifest already matches the current model, the command reports
`status: up-to-date`.

---

### Linux service mode (systemd)

When running as `halid` under systemd, daemon-managed paths follow FHS:

```
/var/lib/hali/
  cache/
  torrents/
  models/

/var/log/hali/

/run/hali/
  .ready   (debug sentinel)
```

### Linux build and packaging

Use the dedicated build script:

```sh
installer/linux/build-linux.sh
```

It builds Linux amd64 artifacts in `bin/` and produces:

- `hali-linux-amd64.tar.gz`
- `hali_<version>_amd64.deb` (when `dpkg-deb` is available)

---

### `hali daemon start`

Launch the hali daemon in the background.

```sh
hali daemon start
```

The daemon:
- seeds all locally cached models via BitTorrent
- announces them to LAN peers on a jittered 25-40 second interval (UDP multicast `239.192.42.1:4269` with directed broadcast fallback on UDP `4269`)
- listens for LAN peer announcements and builds an in-memory index
- handles LAN download requests from `hali pull`

LAN announcements are authenticated with a shared secret at `DataDir()/lan.secret`
(`~/.hali/lan.secret` on Linux/macOS, `%ProgramData%\\Hali\\lan.secret` on Windows).
The daemon auto-creates this file on first start and requires matching secrets
across peers for LAN gossip to be accepted.

If the daemon is already running this is a no-op.

---

### `hali daemon stop`

Stop the running daemon.

```sh
hali daemon stop
```

---

### `hali daemon status`

Show PID, uptime, torrent port, seeding list, derived magnet URIs, and LAN peers.

```sh
hali daemon status
```

Output:
```
Daemon running  PID 12345  uptime 2h15m0s  port 51234

SEEDING                                     STATUS    PEERS
------------------------------------------  --------  -----
mistral:7b:instruct:q4_k_m                 seeding   3 peers
  magnet  magnet:?xt=urn:btih:a3f9c21b4d67...
llama:8b:instruct:q5_k_m                   hashing   —

LAN AVAILABLE                               PEERS     INFOHASH
------------------------------------------  -----     --------
mistral:7b:instruct:q4_k_m                 1         a3f9c21b4d67…
```

### `hali stats --web`

Open the local web dashboard served by the daemon.

```sh
hali stats --web
```

The dashboard shows live transfer stats and active models. When a model has a
derived magnet URI available, the model row includes a clickable `Magnet` link.

---

## Web UI

`hali` includes a local Web UI (dashboard) served by the daemon on
`http://127.0.0.1:47433`.

Start and open it:

```sh
hali daemon start
hali stats --web
```

What it shows:
- live download and upload speeds
- session totals
- active model rows with status and peer count
- clickable magnet links when available

Notes:
- the UI is local-only and uses a loopback address (not exposed publicly)
- if `hali stats --web` says `Daemon is not running.`, run `hali daemon start` first

---

## Storage layout

### Windows

```
%ProgramData%\Hali\
  config.json
  logs\
    hali.log
  cache\
    <base>\
      <size>-<variant>\
        <quant>\
          model.gguf
          metadata.json
  torrents\
    <infohash>.torrent
```

### Linux / macOS

```
~/.hali/
  config.json
  logs\
  cache\
    ...
  torrents\
```

### `config.json`

Optional local configuration.

Example:

```json
{
  "streaming_hash": true,
  "debug": false,
  "telemetry_enabled": true,
  "registry_ingest_url": "https://api.hali.network/ingest",
  "lan_hmac_enabled": false,
  "models_dir": "C:\\Users\\jarit\\.hali\\models",
  "lmstudio_models_dir": "C:\\Users\\jarit\\.lmstudio\\models",
  "ollama_models_dir": "C:\\Users\\jarit\\.ollama\\models",
  "max_upload_mbps": 0,
  "max_download_mbps": 0
}
```

Precedence is:

`CLI flags` (if added later) > environment variables > `config.json` > built-in defaults

Current env var overrides still supported:
- `ENABLE_STREAMING_HASH`
- `LMSTUDIO_MODELS_DIR`
- `OLLAMA_HOME`

### `metadata.json` format

```json
{
  "model_id": "mistral:7b:instruct:q4_k_m",
  "hf_repo": "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
  "hf_revision": "abc123",
  "torrent_infohash": "a3f9c21b4d67...",
  "files": ["mistral-7b-instruct-v0.2.Q4_K_M.gguf"],
  "size": 4368439296,
  "downloaded_at": "2026-05-18T10:00:00Z"
}
```

---

## Model ID format

Canonical model IDs follow the pattern `base:size:variant:quant`:

```
mistral:7b:instruct:q4_k_m
llama:8b:instruct:q5_k_m
codellama:13b:code:q4_k_m
```

`hali pull` derives this automatically from the Hugging Face repo and filename.
If automatic parsing fails, a best-effort slug is used instead.

---

## LAN acceleration

When the daemon is running, `hali pull` checks whether any machine on the LAN
already has the model before downloading from Hugging Face. If found, the file
is pulled from the local peer at LAN speed instead.

LAN discovery requires:
- UDP multicast reachable on the network (`239.192.42.1:4269`)
- `hali daemon start` running on at least one machine that has the model

LAN is always optional — the system falls back to Hugging Face if no peers are
found or if multicast is unavailable.

---

## Phases

| Phase | Status | What it adds |
|-------|--------|-------------|
| 1 | done | HF search + download + local cache |
| 2 | done | daemon + BitTorrent seeding |
| 3 | done | LAN peer discovery + LAN-first downloads |
| 4 | future | optional global registry for internet-scale swarms |
