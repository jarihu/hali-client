Plan: Refactor bt into a Serverless LAN-First Model Cache System
Context

The original architecture positioned bt as a torrent registry + swarm platform with Hugging Face fallback. That framing creates unnecessary complexity, infrastructure burden, and a severe cold-start problem.

The architecture should instead be reframed as:

A local-first model cache system with optional P2P acceleration.

This changes the system fundamentally:

Hugging Face remains the source of truth for model discovery
Local cache is the primary product
LAN sharing is the main acceleration mechanism
Internet-scale torrent swarm becomes optional later
Central registry is removed from MVP entirely

The system must provide value even with:

zero peers
zero seeders
zero hosted infrastructure

P2P is an optimization layer, not the core product.

Goals

Refactor the architecture to:

remove mandatory central registry dependency
use direct Hugging Face search APIs
support deterministic torrent generation locally
enable automatic LAN peer discovery
support LAN-first downloads
preserve future extensibility for global swarm support
Architectural Changes
1. Remove central registry from MVP

Delete the assumption that hali-registry exists in Phase 1.

Instead:

Discovery

Use Hugging Face APIs directly:

model search
metadata lookup
revision info
tags
file listings
Local state

All runtime state is owned by the local daemon.

No remote authority tracks:

what peers exist
what models are seeded
what torrents are available

This eliminates:

VPS hosting requirements
global consistency problems
auth/API complexity
registry maintenance burden
2. Introduce explicit layer separation

The system now has 3 independent layers.

Layer A — Global metadata layer

Source:
Hugging Face

Responsibilities:

model existence
search
metadata
revisions
licensing

This layer NEVER stores runtime state.

Layer B — Local daemon state layer

Owned by:
anacrolix/torrent daemon

Responsibilities:

downloaded models
local torrents
active seeding
cache metadata

Stored in:

local SQLite DB

This is the only source of truth for:

“what this machine has”

Layer C — LAN discovery layer

This layer is ephemeral and runtime-only. No persistent storage.

Responsibilities:
  - load shared LAN secret from DataDir()/lan.secret (create if missing)
  - announce available models to LAN nodes (signed multicast events)
  - receive, authenticate, validate, and deduplicate node announcements
  - maintain in-memory model hint cache (model_id → []ModelHint) for download acceleration

Implementation:
  - UDP multicast (239.192.42.1:4269)
  - Signed events: HMAC-SHA256 over raw unsigned batch JSON bytes
  - Shared secret file: lan.secret (hex-encoded 32 bytes, mode 0600)
  - Event stream remains batched gossip packets (`v`, `nid`, `ts`, `models[]`, `sig`)
  - Payload version v="1" hard-locked; drop silently if v != "1"
  - Startup phase offset: random 0–25s before first broadcast
  - Broadcast interval: 25–40s (jittered); dirty flag → add/remove, else heartbeat
  - Model hint cache: model_id → []ModelHint; dedup key (model_id, nodeID); unconditional replace
  - Rate limit per nodeID; timestamp skew check at receive path
  - fsnotify fast-path; authoritative reconciliation loop every 120–300s (full state rebuild)
  - Infohash from node is hint only — LAN does not validate correctness
  - All invalid/stale/replayed packets: drop silently — not errors, no fallback triggered

See specs/lan-protocol-v1.md for full wire format and validation rules.

New Core Workflow
bt pull mistral
Step 1 — Search HF directly

CLI queries Hugging Face API.

User selects:

repo
quantization
format

Result resolves to:

model_id + revision
Step 2 — Determine canonical artifact

Normalize into deterministic artifact identity:

mistral:7b:instruct:q4_k_m:gguf:<revision>

This identity must be immutable.

Step 3 — Check local daemon

CLI asks daemon:

already downloaded?
already seeding?
partially available?

If yes:

reuse existing files/torrents
Step 4 — LAN hint lookup

Daemon queries in-memory LAN model hint cache for matching model_id.
Returns raw []ModelHint. Server applies TTL policy and selects one candidate infohash.

If LAN hint found:
  → daemon StartTorrent(infohash)
  → torrent engine discovers peers via LSD only (DHT and PEX are disabled — LAN-only)

If no valid LAN hint:
  → fall through to Step 5

LAN provides infohash only. LAN does not influence swarm behavior.

Step 5 — Registry (Phase 4+) or HF fallback

If no valid LAN result:
  → query global registry for canonical model_id → infohash mapping (Phase 4+)
  → if no registry match or registry unavailable: HF HTTP download
Step 5 — Streaming torrent creation

If downloading from HF:

DO NOT:

fully download
then hash
then torrent

Instead:

download stream
    ↓
write to disk
    ↓
compute piece hashes incrementally
    ↓
build torrent metadata during download
    ↓
begin seeding immediately

Torrent creation is part of the pipeline.

Not a post-processing step.

Deterministic Torrent Generation

This becomes critical after removing central registry.

Every node must generate identical torrents for identical artifacts.

Rules must be deterministic:

fixed piece size rules
deterministic file ordering
deterministic metadata fields
deterministic comment structure
no timestamps in torrent metadata

Otherwise:

identical files produce different infohashes
LAN sharing fails
swarms fragment
Torrent Metadata Format

Embed canonical metadata in torrent comment field.

Example:

{
  "model_id": "mistral:7b:instruct:q4_k_m",
  "revision": "abc123",
  "format": "gguf",
  "source": "huggingface"
}

This allows:

future indexing
future DHT crawling
future optional registry layer

without changing torrent format later.

New Phase Ordering
Phase 1 — Local cache product

Implement:

HF search
local model cache
canonical model IDs
deterministic artifact identity
HF downloads
metadata persistence

NO torrents yet.

Deliverable:

standalone useful product

Phase 2 — Torrent layer

Add:

anacrolix/torrent
torrent generation
local seeding
daemon process

Still no central server.

Phase 3 — LAN acceleration

Enable:

LSD peer discovery
LAN infohash-resolution acceleration (model_id → infohash via hint cache)

Validate:

two-machine LAN download acceleration

LAN reduces time-to-first-infohash. Torrent engine handles all peer discovery independently.

This is the primary differentiator.

Phase 4 — Optional internet swarm

Only after adoption exists:

optional bootstrap seeders
optional registry/index
optional DHT crawling
optional global torrent discovery

This phase must NOT be required for system usefulness.

Daemon Lifecycle Requirements

The daemon must be treated as a first-class system component.

Do NOT rely on:

detached background processes
shell persistence

Implement proper OS integration:

Platform	Required Integration
Linux	systemd
macOS	launchd
Windows	Windows Service

CLI communicates with daemon via:

local Unix socket
named pipe on Windows

CLI itself remains stateless.

Storage Rules

Default storage:

~/.hali/models/

Each artifact directory contains:

model files
metadata.json
torrent metadata
local cache state

The daemon owns consistency.

Explicit Non-Goals

Do NOT implement:

social features
user accounts
blockchain
decentralized governance
enterprise auth
web UI
inference/runtime execution

This project is:

a transport and cache layer only

Success Criteria

Phase 1 succeeds if:

a solo user gets value without peers

Phase 3 succeeds if:

multiple LAN machines avoid duplicate downloads automatically

The project does NOT require:

internet swarm adoption
public torrent ecosystem
hosted infrastructure

to be useful.