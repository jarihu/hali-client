# AGENTS.md — hali

## Build & run

```pwsh
.\build.ps1                          # builds bin/hali.exe (sets GOTMPDIR/GOCACHE to local dirs)
go build -o bin\bt.exe .             # alternative if env vars aren't needed
go test ./internal/model/            # model identity parsing tests
go test ./internal/torrent/          # torrent engine tests
go test ./internal/daemon/           # daemon server tests
go test ./...                        # all tests
```

No Makefile, no CI, no lint config. Standard `gofmt` expected.

## Architecture rules (non-negotiable)

These come from `specs/implementation-contract.md` — read that file before large changes.

- **CLI (`cmd/`) is stateless.** Never hold state between invocations. Always creates fresh clients.
- **Daemon owns all runtime state.** Only the daemon manages torrents, seeding, LAN index, and local cache index.
- **Hugging Face layer (`internal/hf/`) must never reflect daemon/runtime state.**
- **No central registry in MVP.** Direct HF API calls only. LAN discovery is the only "network" layer.
- **No CGO.** `anacrolix/torrent` is pure Go. Do not introduce C dependencies.
- **No new top-level modules.** File structure is defined in `specs/implementation-contract.md §3`. The `editionapi/` module is a sanctioned exception for build-mode polymorphism.
- **LAN is optional.** System must work with zero peers, zero multicast, zero LAN.

Layer dependency graph (strict DAG, no cycles):
```
cmd  →  hf, daemon, cache, model, torrent (wires everything)
daemon  →  torrent, cache
cache  →  model
model  →  (stdlib only)
hf  →  (stdlib only)
torrent  →  (anacrolix + internal/config, internal/networking, internal/safepath)
```

## Frozen torrent generation rules

Changing any of these breaks swarm compatibility. Requires `CreatedBy` bump (e.g. `"bt/2"`).

| Rule | Value |
|---|---|
| Piece length | Auto-computed by `choosePieceSize` based on file size (2–16 MiB) |
| CreatedBy | `"hali"` |
| CreationDate | omitted (0) |
| Info private flag | `private=1` (always set) |
| Comment JSON field order | `model_id`, `revision`, `format`, `source` — struct, not map |
| File layout | single file (GGUF only) |
| Announce list | empty (LSD + multicast handle discovery) |

Torrent comment JSON (deterministic struct order, in `internal/torrent/engine.go`):
```json
{"model_id":"...", "revision":"...", "format":"gguf", "source":"huggingface"}
```
Note: `hf_repo` is intentionally NOT in the torrent comment — only model identity goes there.

## Daemon internals

- **Hidden `_run` subcommand**: `hali daemon _run` is the actual daemon entry point, launched by `Launch()` as a detached process. Never expose to users.
- **Platform-specific launch**: `launch_windows.go` (build tag `windows` — `CREATE_NEW_PROCESS_GROUP` + `HideWindow`) vs `launch_unix.go` (build tag `!windows` — `Setsid`).
- **IPC**: JSON-over-TCP on `127.0.0.1:47432` (fixed port, loopback only). One JSON object per line. Max request 1 MiB. Address stored at `~/.hali/daemon.addr`.
- **Web dashboard**: TCP on `127.0.0.1:47433` (fixed port). Single-page HTML dashboard with SVG sparkline. Uses `createElement`/`textContent` (no `innerHTML` for dynamic data). Bearer token auth when listening on non-loopback.
- **IPC write deadline**: `writeResp` sets a 5-second write deadline before encoding JSON to prevent hanging on disconnected clients.
- **generateNodeID**: Falls back to a timestamp-based ID and logs an error instead of panicking on `crypto/rand` failure.

## Shared CLI utilities

- **`daemon.ArtifactKey(modelID, revision)`** — computes `modelID + "@" + revision` for artifact lookups. Shared between `cmd/pull.go` and `cmd/network.go`.
- **`FinishDownloadJob(ctx, dc, jobID, modelDir, id, store, ...)`** — polls a download job to completion, hashes the file, saves metadata, prints a summary, and enqueues the ingest event. Used by both `tryLanDownload` (pull.go) and `runNetworkPullFromSeen` (network.go).
- **`startSeedJobAndWait` / `pollSeedJob`** — generic seed-job polling used by `seedAndWait`, `seedCollectionAndWait`, and `seedFromTorrentFileAndWait`.

## Error handling rules

- All error-ignoring patterns (`_ =`, `//nolint:errcheck`) in daemon, CLI, and web handler code must log the error at `Warn` level via `slog`.
- Third-party library calls that can fail (shutdown, close, write sentinel files) are logged, never silently discarded.
- `generateNodeID` must not panic — errors from `crypto/rand.Read` are logged and a fallback ID is returned.
- Web dashboard JSON encode errors must be logged via the `writeJSON` helper in `internal/daemon/web.go`.

## Gotchas

- **HF download has no HTTP timeout** (`internal/hf/client.go` `Download()`). Large models can take hours. Do not wrap in a timeout.
- **`pickOne()` is defined in `cmd/search.go`** but also used by `cmd/pull.go`. If refactoring, keep it accessible.
- **`.gitignore` has `.go*`** which blocks Go work files but also `.gomodcache` etc. Don't commit files matching this pattern.
- **Storage layout**: `~/.hali/models/<base>/<size>-<variant>/<quant>/model.gguf` + `metadata.json`. Path derived from `model.ModelID.StorePath()`.
- **LAN multicast**: `239.192.42.1:4269` (UDP). Degrades silently if unavailable. Semantic-only (model identity + infohash); actual peer connectivity handled by anacrolix LSD.
- **LAN-only peer discovery**: DHT (`NoDHT = true`) and PEX (`DisablePEX = true`) are both disabled in the torrent engine. The only peer discovery mechanism is LSD, which is link-local and cannot reach internet peers.
- **LAN observability is soft-state only**: multicast-derived peer counts and "seen recently" indicators may be stale/incomplete/incorrect. Never treat LAN observability as transfer/correctness authority; torrent piece verification remains the sole authority.
- **`cmd/` `init()` functions** register commands with `rootCmd.AddCommand()`. All command files use this pattern.
- Only `model_id + hf_revision` constitutes an immutable artifact mapping. Never silently remap existing entries.

## Reference docs

- `specs/implementation-contract.md` — hard rules, forbidden behaviors, acceptance criteria
- `CLAUDE.md` — full architectural context, design principles, phase plan
- `specs/serverless-spec.md` — refactoring rationale and layer separation
- `USAGE.md` — user-facing command reference
