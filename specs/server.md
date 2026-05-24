# hali-registry (global model index + torrent mapping service)

> **Status: Phase 4 — deferred.** The implementation contract (R4) explicitly forbids a central registry for Phases 1–3. This document describes the future optional registry layer. Do not implement until LAN-only swarm (Phase 3) is validated.

## Purpose

Stateless truth layer that maps:

- Hugging Face model names → canonical model IDs → artifact variants → torrent infohashes

Provides:
- search (HF-backed, ranked)
- model resolution
- torrent discovery
- torrent registration API

The registry is a **deterministic index, not a social platform**.

---

## What the registry is NOT

It is NOT a file host.
It is NOT a runtime state tracker.
It must NEVER reflect per-machine state (what is downloaded, what is seeding, who is online).

Mixing runtime state into the global registry breaks correctness — registry answers become non-deterministic ("sometimes I have it, sometimes I don't").

---

## Two-registry architecture (critical)

The system has two registries with separate ownership:

| Registry | Owner | Answers | Must never do |
|---|---|---|---|
| **Global registry** (this service) | Central server or self-hosted | "What models exist? What is their torrent?" | Hold per-machine state |
| **Local registry** | Daemon (SQLite on each machine) | "What do I have? What am I seeding?" | Talk to HF at runtime |

Without this split: distributed state confusion, non-deterministic query results, impossible to debug.

---

## Target deployment

- Single VPS (MVP)
- Optionally replicated / mirrored
- Self-hostable (registry is replaceable by design)

---

## Language

**Go** — decided.

- simple HTTP server
- easy concurrency
- lightweight single-binary deployment
- `modernc.org/sqlite` (pure Go, no CGO) for storage

---

## Storage

MVP: SQLite via `modernc.org/sqlite` (pure Go, no CGO).
Later: Postgres only if write throughput requires it.

---

## Core data model

### Model entry (one-to-many artifact variants)

A `model_id` maps to **multiple artifact variants**, not a single torrent:

```json
{
  "model_id": "mistral:7b:instruct",
  "variants": [
    {
      "quant": "q4_k_m",
      "format": "gguf",
      "hf_repo": "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
      "hf_revision": "abc123",
      "torrent_infohash": "xxxx",
      "magnet": "magnet:?xt=urn:btih:xxxx",
      "files": ["mistral-7b-instruct-v0.2.Q4_K_M.gguf"],
      "size": 4368439296,
      "tags": ["gguf", "instruct", "7b"],
      "created_at": "2026-05-17T10:00:00Z"
    },
    {
      "quant": "q5_k_m",
      "format": "gguf",
      ...
    }
  ]
}
```

A flat `model_id → single infohash` schema requires a breaking migration when variants are added. Design for one-to-many from day one.

### Identity stability rule

```
(model_id, hf_revision) → immutable artifact mapping
```

Once a torrent is registered for a given `(model_id, revision)` pair, that mapping never changes. HF updates create new versioned entries — they never silently remap existing ones.

Violation causes swarm fragmentation and inconsistent caches across machines.

---

## API endpoints

### 1. Search models

```
GET /search?q=mistral
```

Returns ranked model list, enriched from HF search API. Does NOT include runtime state (peer count, online seeders, etc.).

Response:
```json
[
  {
    "model_id": "mistral:7b:instruct",
    "hf_repo": "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
    "downloads": 2100000,
    "variants": ["q4_k_m", "q5_k_m", "q8_0", "fp16"]
  }
]
```

### 2. Get model variants

```
GET /model/{model_id}
```

Returns all artifact variants for a model, including torrent infohashes and magnet links.

Response:
```json
{
  "model_id": "mistral:7b:instruct",
  "variants": [ ... ]
}
```

### 3. Get specific variant

```
GET /model/{model_id}/variant?quant=q4_k_m&format=gguf&revision=abc123
```

Returns a single variant. Used by the client to resolve a specific torrent before joining the swarm.

### 4. Register torrent (controlled)

```
POST /register
```

Body:
```json
{
  "model_id": "mistral:7b:instruct",
  "quant": "q4_k_m",
  "format": "gguf",
  "hf_repo": "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
  "hf_revision": "abc123",
  "torrent_infohash": "...",
  "magnet": "magnet:?xt=urn:btih:...",
  "files": ["mistral-7b-instruct-v0.2.Q4_K_M.gguf"],
  "size": 4368439296
}
```

Auth: API key in `Authorization: Bearer <key>` header (MVP) or IP allowlist.
Only trusted client nodes may submit. MVP: single API key in server config.

Behavior:
- Validates `model_id` format
- Validates `hf_revision` presence
- Checks for existing `(model_id, hf_revision, quant, format)` — rejects if already registered (immutability rule)
- Stores mapping, publishes immediately

---

## Hugging Face integration

The registry proxies HF for search and metadata enrichment only:

Used for:
- search API (ranking, discovery)
- model metadata (tags, size, license)
- verifying model existence before registration

NOT used for:
- torrent storage
- file distribution
- identity management

---

## Registry behavior

### On search request
1. Query HF search API with the user's query
2. For each result, check local DB for known variants
3. Return merged results (HF metadata + known torrents)

### On model resolution (`GET /model/{id}`)
1. Look up in DB
2. If found → return variants
3. If not found → return 404 with HF fallback URL if repo is known

### On torrent registration (`POST /register`)
1. Validate `model_id` format (`base:size:type`)
2. Validate `hf_revision` is present and non-empty
3. Check for existing `(model_id, hf_revision, quant, format)` — reject with 409 if exists
4. Store mapping
5. Return 201 with the stored variant

---

## Optional future extensions

- Peer-reported availability stats (how many seeders?)
- LAN discovery aggregation (which LAN networks have which models?)
- Popularity ranking based on pull counts

---

## Non-goals

- No authentication system beyond API key (MVP)
- No UI (MVP)
- No blockchain or crypto
- No heavy microservices
- No runtime state (what machines are online, what is seeding)

---

## Final system view

```
User
  ↓
bt client (CLI) — stateless
  ↓
local daemon (IPC) — owns runtime state
  ↓
global registry API — stateless truth layer
  ↓
torrent infohash + magnet
  ↓
anacrolix/torrent swarm (LAN-first, internet fallback)
  ↓
local cache + seed

HF = always-available fallback at every step
```

> Note: In Phase 3, the daemon consults the LAN peer cache before querying this registry.
> The registry is only reached when no valid LAN hint exists for the requested model.
> See `specs/lan-protocol-v1.md` for the LAN layer specification.
