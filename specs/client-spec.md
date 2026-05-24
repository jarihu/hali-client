# hali-client (CLI + daemon + torrent node)

## Purpose

Local agent that:
- resolves model names via HF search
- caches models locally in `~/.hali/models/`
- downloads models via HF HTTP (Phase 1) or BitTorrent swarm (Phase 2+)
- seeds downloaded models automatically (Phase 2+)
- shares models over LAN (Phase 3)
- exposes simple CLI: `hali pull`, `hali search`, `hali list`, `hali daemon`

Phase 1 (current): CLI is fully standalone. No daemon required for download.
Phase 2 (current): Daemon runs in background, seeds completed downloads over LAN.

---

## Target platforms

- Linux (x86_64, aarch64)
- Windows (x86_64)
- macOS (Intel + Apple Silicon)

---

## Language and build

**Go** — decided, not optional.

Reasons:
- static binaries, trivial cross-compilation
- strong concurrency (goroutines for concurrent networking)
- no runtime dependency on the target machine
- clean Windows/Linux/macOS support

Module: `hali`
Binary: `hali`
Build: `go build -o bin/hali.exe .`

---

## Core dependencies

| Dependency | Purpose | Notes |
|---|---|---|
| `github.com/spf13/cobra` | CLI framework | in use |
| `github.com/anacrolix/torrent` | BitTorrent engine | in use — pure Go, no CGO |
| stdlib `net/http` | HF API + download | in use |

**No libtorrent.** CGO + libtorrent = broken cross-compilation, deployment friction. `anacrolix/torrent` is pure Go, well-maintained, supports LSD.

---

## Internal architecture

### 1. CLI entrypoint

Binary: `bt`
Package: `cmd/` (cobra commands)

Commands:
- `hali search <query>` — search HF, ranked by downloads
- `hali pull <model>` — download model (query, HF repo ID, or canonical ID)
- `hali list` — show local cache
- `hali profile create` — create/update signed publisher profile
- `hali daemon start|stop|status` — manage background daemon
- `hali stats [--web]` — show transfer stats or open the local dashboard

**CLI is always stateless.** It reads from cache, talks to daemon via IPC, and exits cleanly.

---

### 2. Internal packages

```
internal/
  model/
    identity.go    — ModelID struct, Parse(), FromHF(), StorePath()
  cache/
    store.go       — Store struct, Has/Save/List/Dir, Metadata, FormatSize
  config/
    config.go      — config.json loader, File struct, QBittorrentConfig, env/config precedence
  hf/
    client.go      — Client struct, Search/GetFiles/Download, Progress
  torrent/
    engine.go      — Engine, Seed, SeedFromTorrentFile, StartDownload, JobStatus, TorrentDir
    magnet.go      — BuildMagnet, deterministic BitTorrent v1 magnet rendering
  publishing/
    hooks.go       — TorrentPublishedEvent, Hook interface, Register/Emit (daemon-owned)
  seeding/
    seeder.go      — Seeder interface
    qbittorrent/
      client.go    — qBittorrent WebUI API v2 HTTP client (Login, TorrentInfo, AddTorrent)
      seeder.go    — Seeder impl + QBittorrentHook adapter
  daemon/
    server.go      — IPC TCP server, request routing, setupPublishingHooks
    client.go      — IPC client (used by CLI)
    protocol.go    — Request/Response/Cmd types
    lan.go         — LanIndex (model_id → []ModelHint), Announcer, UDP multicast
    web.go         — localhost-only monitoring dashboard and /api/stats
    launch.go      — cross-platform daemon spawning
    launch_unix.go
    launch_windows.go
```

#### `internal/model`

`ModelID` struct:
```go
type ModelID struct {
    Base     string // e.g. mistral
    Size     string // e.g. 7b
    Variant  string // e.g. instruct
    Quant    string // e.g. q4_k_m
    Format   string // gguf, safetensors
    Revision string // HF revision hash — set by caller after FromHF()
}
```

Canonical string: `base:size:variant:quant` (e.g. `mistral:7b:instruct:q4_k_m`)

`FromHF(repoID, filename string) ModelID` — best-effort normalization from HF repo + GGUF filename. Sets `Format: "gguf"` automatically. Returns zero ID if parsing unreliable; caller falls back to slugified repo name. **Caller must set `Revision` after calling `FromHF`.**

`StorePath() string` — returns relative path `base/size-variant/quant` for use under `~/.hali/models/`.

#### `internal/cache`

`Store.Root` = `~/.hali/models/` (resolved from `os.UserHomeDir()`)

`Has(id)` — checks for `metadata.json` presence
`Save(id, meta)` — writes `metadata.json`, sets `model_id` and `downloaded_at`
`List()` — walks store, returns all entries with parsed IDs
`Dir(id)` — full path to model directory
`SetInfohash(modelDir, infohash)` — updates `torrent_infohash` in existing `metadata.json`

`Metadata` JSON fields: `model_id`, `hf_repo`, `hf_revision`, `torrent_infohash` (omitempty), `files`, `size`, `downloaded_at`

#### `internal/config`

Optional user config file: `~/.hali/config.json` (Windows: `%ProgramData%\Hali\config.json`)

Current supported keys:
- `streaming_hash` — enables concurrent piece hashing during HF downloads
- `lmstudio_models_dir` — overrides LM Studio model directory detection
- `ollama_models_dir` — overrides Ollama model directory detection
- `qbittorrent` — optional qBittorrent WebUI integration for internet seeding (see below)

Current env overrides:
- `ENABLE_STREAMING_HASH`
- `LMSTUDIO_MODELS_DIR`
- `OLLAMA_HOME`

Precedence: env vars override config values; config values override built-in defaults.

##### `qbittorrent` config block

When present with `enabled=true` and `url` non-empty, the daemon registers published torrents with a
qBittorrent instance after each successful telemetry event enqueue. Failure is non-fatal
and never blocks publishing.

```json
{
  "qbittorrent": {
    "enabled": true,
    "url": "http://127.0.0.1:8080",
    "username": "admin",
    "password": "changeme",
    "category": "hali",
    "tags": ["hali"],
    "skip_tls_verify": false
  }
}
```

Fields:
- `enabled` — must be `true` for the integration to activate.
- `url` — qBittorrent WebUI base URL. Omit or leave empty to disable the integration.
- `username` / `password` — WebUI credentials.
- `category` — torrent category applied on add (default: none).
- `tags` — list of tags applied on add (default: none).
- `skip_tls_verify` — set `true` for self-signed TLS on remote seedboxes.

The `password` field is intentionally never echoed by the config renderer (`renderConfigJSONC`).
It is stored only in the raw JSON file and loaded into memory at daemon startup.

#### `internal/hf`

`Client.Search(ctx, query)` — `GET /api/models?search=...&filter=gguf&sort=downloads&direction=-1&limit=20`

`Client.GetFiles(ctx, repoID)` — `GET /api/models/{repoID}`, returns GGUF files sorted smallest-first + HEAD SHA (revision)

`Client.Download(ctx, repoID, filename, destDir, progressFn)` — streams to `filename.tmp`, renames on success. No timeout (large files). Reports progress every 150ms via callback.

#### `internal/torrent`

`Engine` wraps an `anacrolix/torrent` client. Fixed piece length: 16 MiB (`lanPieceLen = 1 << 24`).

`BuildMagnet(info, trackers)` builds a deterministic BitTorrent v1 magnet URI from finalized in-memory `metainfo.Info`.
Rules:
- use canonical `MetaInfo.HashInfoBytes()` helper path for btih
- encode btih as lowercase hexadecimal
- render query params in exact order `xt`, `dn`, `tr`, `ws`
- keep magnets derived only; infohash remains authoritative

`Seed(modelDir, filename, modelID, revision)` — hashes file inline, writes `<infohash>.torrent` to `~/.hali/torrents/`, starts seeding. Returns infohash hex string.

`SeedFromTorrentFile(modelDir, infohashHex, modelID)` — loads existing `.torrent` from torrents dir, starts seeding without rehashing.

`StartDownload(modelDir, modelID, ihHex)` — joins swarm by infohash via LSD, returns job ID for polling.

`JobStatus(jobID)` — snapshot of download progress.

Torrent comment field (frozen format):
```json
{ "model_id": "...", "revision": "...", "format": "gguf", "source": "huggingface" }
```

#### `internal/daemon`

**IPC protocol:** TCP on `127.0.0.1` (random port). Address stored at `~/.hali/daemon.addr`. Client has 3s dial timeout, 10s read/write deadline.

Commands: `seed`, `download`, `status`, `list`, `stop`, `lan_query`, `job_status`

Status payloads expose derived `magnet_uri` values when available:
- `StatusData.Seeding[].magnet_uri` for active seeded models
- `JobStatusData.magnet_uri` for in-progress LAN download jobs

The daemon also serves a localhost-only monitoring dashboard at the URL stored in `~/.hali/daemon.web`.
`/api/stats` returns `torrent.StatsSnapshot`, and each model row may include a derived `magnet_uri` for web UI rendering.

**LAN:** UDP multicast `239.192.42.1:4269`. Signed event protocol (see `specs/lan-protocol-v1.md`).

Shared secret auth: `DataDir()/lan.secret` (typically `~/.hali/lan.secret`, hex-encoded 32-byte secret, mode `0600`).
Daemon loads this secret on startup and creates it if missing.
Packet signature: `sig = base64(HMAC-SHA256(secret, unsigned_batch_json_bytes))`.
Receiver drops packets with missing/invalid `sig`.

Message shape remains batched JSON (`v`, `nid`, `ts`, `models[]`, `sig`).
Each `models[]` item carries:
- `id` (canonical model ID)
- `ih` / `ih2` (torrent infohash v1/v2)
- `repo` (HF repo provenance)
- `rev` (HF revision)
Payload version `v="1"` hard-locked; drop silently if `v != "1"`.
Startup phase offset: random 0–25s before first broadcast.
Broadcast: 25–40s jittered. Dirty flag → `add`/`remove`; else `heartbeat`.

Model hint cache (in-memory only): `model_id → []ModelHint{infohash, hf_repo, revision, nodeID, seenAt}`.
Dedup key: `(model_id, nodeID)` — one entry per node per model. Replace unconditionally.
Rate limiting per `nodeID`: max 10 events/sec.
Timestamp skew check is enforced in receive processing.
Capacity: max 100 hints per `model_id`.
LAN does NOT validate infohash correctness. Server layer owns TTL filtering and hint selection.

### 2.1 LAN provenance regression note

Observed failure mode:
- LAN transfer completes, but metadata save warns with missing `hf_repo`.

Root cause:
- Strict metadata seal validation requires `hf_repo`, but LAN announce/query path only carried revision and infohash.

Required fix:
- Keep strict validation unchanged.
- Propagate `hf_repo` from seeder metadata through LAN announce → hint index → lan_query/lan_seen responses.
- LAN pull save path must write `HFRepo` from LAN row/query payload.

Drop condition: invalid/malformed packets silently dropped. MUST NOT log at warning. MUST NOT trigger fallback.
Failures MUST NOT crash daemon. SHOULD be logged at debug level.

**Daemon lifecycle:** `hali daemon start` deletes any stale `.ready` sentinel, then spawns a detached child process (`daemon _run`). The child writes its `.ready` sentinel at `ServiceRunDir()/.ready` when IPC is listening. The parent polls for that file (up to `LaunchTimeoutMs`, default 5s), falling back to a live IPC probe. `hali daemon stop` sends `stop` command over IPC.

**Daemon logging:** Writes structured JSON to `ServiceLogDir()/daemon.log`. Log level is `INFO` by default; set `"debug": true` in `config.json` to enable `DEBUG`-level output. If `daemon.log` cannot be opened for writing (e.g. created by a privileged service process with restrictive ACLs), the daemon falls back to `daemon.log.<pid>` in the same directory and prints a notice to stderr. The `cmd/service` binary also reads the `debug` flag from config so the Windows service logs at the same level.

---

### 3. Local model storage

Root: `~/.hali/models/`

Structure:
```
~/.hali/
  models/
    mistral/
      7b-instruct/
        q4_k_m/
          model.gguf
          metadata.json
  torrents/
    <infohash>.torrent
  lan.secret        ← shared LAN HMAC secret (hex, 0600)
  daemon.addr
```

On interrupted download: `.tmp` file is left in the model directory.

---

### 4. Metadata format

`metadata.json` — written after successful download:
```json
{
  "model_id": "mistral:7b:instruct:q4_k_m",
  "hf_repo": "mistralai/Mistral-7B-Instruct-v0.2",
  "hf_revision": "abc123",
  "torrent_infohash": "",
  "files": ["model.gguf"],
  "size": 4368439296,
  "downloaded_at": "2026-05-18T10:00:00Z"
}
```

`torrent_infohash` is empty until the daemon seeds the file and writes back the infohash.

---

### 4.1 Profile format and publish

Local signed profile path: `<DataDir>/profile.json`

Profile JSON shape:
```json
{
  "profile": {
    "pubkey": "<64-hex-ed25519-pubkey>",
    "display_name": "...",
    "description": "...",
    "website": "...",
    "contact": "...",
    "timestamp": 1700000000
  },
  "signature": "<128-hex-ed25519-signature>"
}
```

`hali profile create` prompts for profile fields, signs with the local node key,
saves to disk, then submits to backend `POST /profile`.

Profile signature algorithm (locked):

1. `canonical = internal/crypto.Canonicalize(profile)`
2. `digest = SHA256(canonical)`
3. `signature = Ed25519Sign(node_private_key, digest)`

Important: the CLI MUST reuse the shared canonicalizer implementation.
Do not add an alternative profile-only canonicalization path.

Default endpoint: `http://127.0.0.1:3000/profile`
Override env: `HALI_PROFILE_BACKEND`

---

### 5. hali pull flow

```
bt pull <query>
  ↓
HF search → user selects repo
  ↓
GetFiles → user selects GGUF file
  ↓
FromHF() → ModelID{..., Revision: <hf_revision>}
  ↓
local cache check (Has) → if cached: done
  ↓
if daemon running:
  query LAN model hint cache (lan_query IPC)
  → daemon queries hint cache for model_id → []ModelHint
  → server applies TTL policy and selects candidate infohash

  if LAN hint found:
    → daemon StartTorrent(infohash) → poll JobStatus
    → torrent engine discovers all peers independently (no LAN involvement after this point)
  else (no LAN hint):
    → HF HTTP download → Save metadata
  ↓
notifyDaemonSeed → CmdSeed → daemon StartSeed (async torrent creation)
  → CLI polls CmdSeedStatus (with Dir) until job.Done
  → handleSeedStatus: when job.Done && no error && infohash non-empty && Dir non-empty:
      → publishing.Emit(TorrentPublishedEvent{InfoHash, ContentDir})    [always, telemetry-independent]
      → QBittorrentHook (if configured) → Seeder.Seed → qBittorrent WebUI API
  ↓
CLI sends CmdEnqueueEvent (with Dir) → daemon queues registry event (telemetry, separate path)
else (no daemon):
  → HF HTTP download → Save metadata
```

If telemetry ingest is enabled, each pull event sent to `/ingest` includes a
required signed publisher token:

- `publisher_pubkey` (64-char hex)
- `publisher_sig` (128-char hex)

Signature payload canonicalization (newline-separated fields, in order):

1. `model_id`
2. `revision`
3. lowercased `infohash`
4. `magnet`
5. `source_url`
6. `local_hash`
7. `timestamp` (`RFC3339Nano`, UTC)
8. lowercased `publisher_pubkey`

Derived magnet visibility:
- LAN download path: magnet is available immediately from the externally known infohash and can be surfaced during job polling.
- Local HF download path: magnet becomes available after finalized torrent metadata exists in the seeding path.

---

## Key design rules

- CLI is stateless — daemon owns all torrent state
- No CGO — `anacrolix/torrent` only
- Phase 1 works with zero peers, zero daemon, zero registry
- Torrent creation is inline — never a post-download step
- Canonical model ID is write-once per (model, revision) pair
- LAN is optional — missing multicast degrades silently to HF
- Publishing hooks are daemon-owned — CLI never emits `TorrentPublishedEvent`
- qBittorrent integration is optional, non-blocking, and never fails a publish
- Hook failures are logged as warnings only; the core publish flow is unaffected

---

## Non-goals

- No standalone product UI or remote web app
- No model inference
- No enterprise auth system
- No heavy plugin system
- No libtorrent (CGO)
- No central registry (Phase 1–3)
