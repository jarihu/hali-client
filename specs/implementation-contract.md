IMPLEMENTATION CONTRACT v1
0. Scope

This project is a local-first model cache system.

It provides:

deterministic model identity
Hugging Face-based discovery
local disk caching
BitTorrent-based acceleration (optional via LAN)

It is NOT:

a model registry platform
a decentralized internet swarm network
a web service
a standalone UI application
an inference engine
1. Hard System Rules (NON-NEGOTIABLE)
R1 — Local-first

All functionality must work without any central server.

Hugging Face is used only for:

model search
metadata lookup
download fallback
R2 — Daemon owns state

Only the daemon may:

track downloaded models
manage torrents
manage seeding
store runtime metadata

CLI MUST be stateless.

R3 — Deterministic torrent identity

For identical inputs, torrent output MUST always be identical.

No exceptions.

R4 — No central registry dependency

Do NOT implement:

registry service
global index server
external model mapping API

LAN and HF are sufficient.

R5 — LAN is optional acceleration only

LAN discovery must never be required for correctness.

System must function fully with:

zero nodes
zero multicast
zero LAN availability
R6 — No product UI / no accounts / no platform features

Do NOT implement:

authentication
upload systems
social/discovery features

Allowed exception:

a localhost-only operational monitoring dashboard served by the daemon

The dashboard is diagnostic only. It must not become a state-owning control plane.
R7 — LAN is a passive hint buffer

LAN MUST NOT validate hints, apply TTL policy, rank hints, or influence selection.
All hint policy (TTL filtering, infohash selection) belongs to the server layer.
LAN provides model_id → []ModelHint. Nothing more.
2. Technology Constraints
Language
Go only

Reason:

cross-platform static binaries
concurrency support
networking stability
Torrent engine

Use:

anacrolix/torrent

Must NOT use:

CGO libtorrent
external daemon dependencies
3. File Structure (CURRENT LAYOUT)

```
main.go

cmd/
  root.go
  pull.go
  daemon.go
  search.go
  list.go

internal/
  cache/
    store.go

  config/
    config.go          — File struct, QBittorrentConfig, renderers, accessors

  daemon/
    server.go          — IPC server, setupPublishingHooks, handleEnqueueEvent
    client.go
    protocol.go
    lan.go
    launch.go
    launch_unix.go
    launch_windows.go

  publishing/
    hooks.go           — TorrentPublishedEvent, Hook interface, Register/Emit

  seeding/
    seeder.go          — Seeder interface
    qbittorrent/
      client.go        — qBittorrent WebUI API v2 client
      seeder.go        — Seeder impl + QBittorrentHook

  torrent/
    engine.go          — Engine, TorrentDir accessor

  hf/
    client.go

  model/
    identity.go
```

No additional top-level modules allowed.

4. Core Data Model
Model Identity
```go
type ModelID struct {
    Base     string // e.g. mistral
    Size     string // e.g. 7b
    Variant  string // e.g. instruct
    Quant    string // e.g. q4_k_m
    Format   string // gguf, safetensors
    Revision string // HF revision hash
}
```

Canonical string format: `base:size:variant:quant` (e.g. `mistral:7b:instruct:q4_k_m`)

Torrent Metadata (embedded in comment field)

MUST be deterministic JSON:

```json
{
  "model_id": "mistral:7b:instruct:q4_k_m",
  "revision": "hf_revision_hash",
  "format": "gguf",
  "source": "huggingface"
}
```

Rules:

no timestamps
no random fields
no node-specific data
no hf_repo in torrent comment

Cache Metadata Provenance Rule (strict)

- `metadata.json` identity validation MUST require all of:
  - `model_id`
  - `hf_repo`
  - `hf_revision`
  - `hf_snapshot_hash`
  - `identity_seal`
- Do NOT relax these requirements for LAN pulls.
- LAN pull paths MUST preserve provenance by carrying `hf_repo`/`hf_revision`
  from the announcing seeder's local metadata into LAN query/seen responses.
- If provenance is unavailable from the announcer, save MUST fail loudly (warning
  at CLI level) rather than writing degraded metadata.

Magnet representation (derived only)

- Magnet URI is a derived BitTorrent v1 representation, never the source of truth.
- Authoritative identity remains the infohash.
- Magnet URIs may be regenerated at any time from canonical metadata or known infohash.
- Magnet output must use btih with lowercase hexadecimal SHA1.
- Query parameter order must be deterministic: `xt`, `dn`, `tr`, `ws`.
- `tr` and `ws` are distinct parameters and must never be conflated.
Piece size (FIXED)
16 * 1024 * 1024 bytes (16 MiB)

MUST be constant across all torrents.

5. Component Responsibilities
5.1 CLI (hali)

Responsibilities:

parse commands
call HF search API
send requests to daemon
display progress

Must NOT:

manage torrents directly
store state
perform seeding

Configuration inputs (for behavior toggles and runtime overrides):

- Optional file: `~/.hali/config.json` (Windows: `%ProgramData%\Hali\config.json`)
- Env overrides: `ENABLE_STREAMING_HASH`, `LMSTUDIO_MODELS_DIR`, `OLLAMA_HOME`

Precedence must be deterministic:

`env > config.json > built-in defaults`

Supported config keys (subset relevant to CLI behavior):
- `streaming_hash` — piece hashing during HF download
- `qbittorrent.enabled` — must be `true` to enable internet seeding integration (daemon-side)
- `qbittorrent.url` — qBittorrent WebUI base URL (required when enabled)
5.2 Daemon

Responsibilities:

manage torrent session
manage downloads
manage seeding
manage LAN discovery
maintain local model cache

IPC:

TCP socket on 127.0.0.1 (random port); address stored at ~/.hali/daemon.addr

Commands:

seed
download
status
list
stop
lan_query
job_status
5.3 Torrent Engine

Responsibilities:

create torrents
manage piece hashing
download from swarm
seed files

Rules:

torrent creation happens during download stream
NOT after download completes
5.4 Hugging Face layer

Responsibilities:

search models
fetch metadata
download fallback

Must NOT:

store torrent state
participate in LAN logic
5.5 Publishing hooks layer

Owned exclusively by the daemon. The CLI must never import or call publishing hooks directly.

`internal/publishing.TorrentPublishedEvent` is emitted by `handleSeedStatus` when the torrent
seed job completes: `job.Done == true`, no error, non-empty `InfohashV1`, non-empty `Dir`.
The emit is independent of telemetry — it fires even when telemetry is disabled or the node
is LAN-only. `handleEnqueueEvent` (telemetry path) does NOT emit this event.

`Hook` implementations run in goroutines and must never block the publish pipeline.
Hook registration happens at daemon startup (`setupPublishingHooks`).

Current implementations:
- `QBittorrentHook` — registers the torrent with qBittorrent WebUI for internet seeding

Rules:
- Hook failure MUST be logged as a warning only (never error, never panic)
- Hook failure MUST NOT affect the `handleSeedStatus` response
- Hooks are fire-and-forget; no result is returned to the daemon
- Password and session credentials MUST NOT appear in any log output

5.6 Internet seeding layer (`internal/seeding`)

`seeding.Seeder` interface: `Seed(ctx, infohash, contentDir) error`

`Seeder` contract:
- Idempotent — "already registered" is success, not an error
- Validates infohash format before any network call
- Verifies `contentDir` exists on disk before any network call
- Verifies `<torrentDir>/<infohash>.torrent` exists before any network call (no silent fallback)
- Uses `filepath.ToSlash(savePath)` when sending paths to qBittorrent WebUI
- Logs password, SID cookie, and auth headers at no log level (omit entirely)

`QBittorrentSeeder` flow:
1. Validate infohash (40 hex chars)
2. `os.Stat(contentDir)` — fail fast
3. Locate `<torrentDir>/<infohash>.torrent` — fail if missing
4. `Login` (fresh per Seed call — no session persistence assumed across calls)
5. `TorrentInfo` pre-check — if already registered, return success without re-adding
6. `AddTorrent(torrentPath, contentDir, category, tags)` — `skip_checking=false`
7. Map `"Fails."` response on add → `ErrAlreadyRegistered` → return success

5.7 LAN layer

Mechanism: UDP multicast 239.192.42.1:4269

Authentication:
  Shared secret loaded from DataDir()/lan.secret.
  If missing, daemon creates a 32-byte random secret on startup.
  Secret file mode must be 0600.
  Signature algorithm: HMAC-SHA256 over raw unsigned batch JSON bytes.
  Signature field: base64-encoded HMAC in message.sig.

Message format:
  Batched JSON packet with fields: v, nid, ts, models[], sig.
  models[] entries use: id, ih, rev.
  v = "1" always — drop silently if v != "1".

Signing and verification:
  Sender signs unsigned JSON (same message with sig empty), then sets sig.
  Receiver requires non-empty sig, reconstructs unsigned JSON, verifies HMAC.
  Missing or invalid sig: drop immediately (fail closed).

This model explicitly does NOT include:
  per-node keys, Ed25519 signatures, PKI, or attribution system.

LAN infohash is an acceleration hint, not an authority.
  LAN does NOT validate infohash correctness.
  Hint validation (if any) belongs to the server layer.

Broadcast: random startup offset 0-25s, then every 25-40s (jittered).
  Uses batched packets.

Model hint cache (in-memory only): model_id → []ModelHint
  ModelHint: {infohash, revision, nodeID, seenAt}
  Dedup key: (model_id, nodeID) — one entry per node per model.
  Overwrite: unconditional for same dedup key. No timestamp comparison.
  Capacity: max 100 hints per model_id.
  MUST NOT store: IP addresses, peer network addresses.
  MUST NOT apply: TTL, time-based filtering, or hint ranking.

Rate limiting per node_id: max 10 events/sec.
Timestamp skew check: enforced in receive pipeline before normal update processing.

All drops are silent. MUST NOT log at warning level. MUST NOT trigger fallback logic.
Failures MUST NOT crash daemon. SHOULD be logged at debug level.

Server layer owns: TTL filtering, hint selection, infohash candidate choice.

Full specification: specs/lan-protocol-v1.md
6. Core Workflows
6.1 hali pull
HF search for model
user selects model
normalize model_id
check local cache
if received LAN hint available → StartTorrent(infohash) via daemon
else → download from HF
stream download to disk
notify daemon to seed
start seeding immediately
6.2 Seeding
daemon auto-seeds all completed downloads
seeding starts immediately after file write begins
torrents are reused if identical model_id exists
6.3 LAN acceleration
  1. daemon listens for multicast events on 239.192.42.1:4269
  2. validates: v == "1", non-empty sig, HMAC signature over unsigned JSON batch, timestamp skew
  3. replaces unconditionally for same (model_id, node_id)
  4. maintains in-memory model hint cache: model_id → []ModelHint{infohash, revision, nodeID, seenAt}
  5. rate limit: 10 events/sec per node_id
  6. fsnotify fast-path; authoritative reconciliation loop every 120–300s rebuilds full in-memory state snapshot
  7. on pull: server queries hint cache → applies TTL (server-layer policy) → selects candidate infohash
     if hint found → StartTorrent(infohash); torrent engine discovers peers independently
     no hint → HF download
7. Storage Rules

Default:

~/.hali/models/         — model files + metadata.json per model
~/.hali/torrents/       — <infohash>.torrent files
DataDir()/lan.secret  — shared LAN HMAC secret (hex-encoded, 0600)
~/.hali/daemon.addr     — TCP address of running daemon

Rules:

model files and torrent metadata separated
metadata.json is source of local truth (per-model)
SQLite deferred to Phase 2+
8. Performance Rules
CLI polling interval: ≥ 1 second
daemon polling interval: ≥ 500 ms
avoid busy loops
all network calls must be async
9. Forbidden Behaviors

DO NOT implement:

distributed registry
blockchain or DHT crawling logic
web UI
user accounts
upload system
torrent marketplace
peer ranking systems
custom swarm routing
10. Acceptance Criteria

Phase is correct ONLY IF:

Phase 1:
works without daemon
works without torrents
HF download only works
Phase 2:
daemon seeds files
torrent reuse works locally
Phase 3:
two machines can accelerate downloads over LAN
no central server required
11. Key Principle

This system must be useful even if it never becomes popular.

P2P is optional optimization, not a dependency.
