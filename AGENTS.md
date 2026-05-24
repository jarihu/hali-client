# AGENTS.md — hali

## Build & run

```pwsh
.\build.ps1                          # builds bin/hali.exe (sets GOTMPDIR/GOCACHE to local dirs)
go build -o bin\bt.exe .             # alternative if env vars aren't needed
go test ./internal/model/            # only test suite in the repo (identity parsing)
```

No Makefile, no CI, no lint config. Standard `gofmt` expected.

## Architecture rules (non-negotiable)

These come from `specs/implementation-contract.md` — read that file before large changes.

- **CLI (`cmd/`) is stateless.** Never hold state between invocations. Always creates fresh clients.
- **Daemon owns all runtime state.** Only the daemon manages torrents, seeding, LAN index, and local cache index.
- **Hugging Face layer (`internal/hf/`) must never reflect daemon/runtime state.**
- **No central registry in MVP.** Direct HF API calls only. LAN discovery is the only "network" layer.
- **No CGO.** `anacrolix/torrent` is pure Go. Do not introduce C dependencies.
- **No new top-level modules.** File structure is frozen in `specs/implementation-contract.md §3`.
- **LAN is optional.** System must work with zero peers, zero multicast, zero LAN.

Layer dependency graph (strict DAG, no cycles):
```
cmd  →  hf, daemon, cache, model, torrent (wires everything)
daemon  →  torrent, cache
cache  →  model
model  →  (stdlib only)
hf  →  (stdlib only)
torrent  →  (anacrolix only, no other internal/ packages)
```

## Frozen torrent generation rules

Changing any of these breaks swarm compatibility. Requires `CreatedBy` bump (e.g. `"bt/2"`).

| Rule | Value |
|---|---|
| Piece length | `1 << 24` (16 MiB) — constant, never auto-computed |
| CreatedBy | `"bt/1"` |
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
- **IPC**: JSON-over-TCP on `127.0.0.1:0` (ephemeral, loopback only). One JSON object per line. Max request 1 MiB. Address stored at `~/.hali/daemon.addr`.
- **Web dashboard**: second ephemeral port, address at `~/.hali/daemon.web`. Single-page HTML dashboard with SVG sparkline. Uses `createElement`/`textContent` (no `innerHTML` for dynamic data).

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
