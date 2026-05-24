# LAN Model Discovery Protocol v1

## 0. Purpose

Enable LAN-based discovery and sharing of local GGUF models between daemon instances.

Goals:
- discover models on LAN without central infrastructure
- provide torrent infohash hints for faster pull startup
- block outsider packet injection and tampering
- stay fully functional with zero LAN peers

LAN is optional acceleration only. Correctness never depends on LAN availability.

---

## 1. Security Model

The LAN trust model is a single shared-secret domain.

- all nodes in one LAN group share the same secret
- packet authenticity and integrity are enforced with HMAC-SHA256
- no per-node keys
- no PKI
- no identity attribution system

This is an authenticated gossip channel, not an identity framework.

---

## 2. Shared Secret

Path:
- `DataDir()/lan.secret`

Format:
- 32 random bytes
- stored as lowercase hex text

Lifecycle:
- load on daemon startup
- if missing, create automatically
- file mode must be `0600`

Operational notes:
- rotating the secret partitions LAN groups until nodes share the same value again
- never log the secret
- never log signing payload bytes

---

## 3. Transport

- UDP multicast IPv4
- group: `239.192.42.1:4269`

---

## 4. Wire Message Format

The protocol keeps the existing batched JSON packet shape:

```json
{
  "v": "1",
  "nid": "<node-id>",
  "ts": 1716019200,
  "models": [
    {"id": "mistral:7b:instruct:q4_k_m", "ih": "a1b2...", "rev": "main"}
  ],
  "sig": "<base64 hmac>"
}
```

Field notes:
- `v`: protocol version string (`"1"`)
- `nid`: sender node id
- `ts`: unix seconds
- `models`: batch of model announcements (`id`, `ih`, optional `rev`)
- `sig`: base64 HMAC signature

---

## 5. Signing Rules

### 5.1 What is signed

Sign the raw JSON bytes of the **unsigned** batch object (same message with `sig` cleared/omitted).

Algorithm:

```text
sig = base64(HMAC-SHA256(shared_secret, unsigned_json_bytes))
```

### 5.2 Sender flow

1. Build message fields (`v`, `nid`, `ts`, `models`) with empty signature.
2. Marshal unsigned message to JSON bytes.
3. Compute HMAC-SHA256 over those bytes.
4. Set `sig`.
5. Marshal signed message and send.

Signature must be generated as the final step before send. Signed fields must not be mutated afterward.

### 5.3 Receiver flow

1. Parse JSON packet.
2. If `sig` is missing/empty: drop.
3. Rebuild unsigned message by clearing `sig`.
4. Marshal unsigned message to JSON bytes.
5. Verify HMAC with shared secret.
6. If verification fails: drop.
7. If verification succeeds: continue normal LAN processing.

All signature failures are fail-closed.

---

## 6. Validation and Drop Rules

Packets are dropped silently (debug logging allowed) for:
- JSON parse failure
- missing/empty signature
- invalid signature
- unsupported version (`v != "1"`)
- empty/oversized node id
- invalid model id
- invalid infohash
- stale/future timestamps outside current receive guard

No warning/error-level logging is required for routine invalid packets.

---

## 7. Broadcast Behavior

- startup offset: random 0-25s before first announce
- periodic announce interval: random 25-40s
- batching is allowed and used
- multicast failures must not crash daemon

---

## 8. Hint Cache and Dedup

In-memory cache:
- `model_id -> []ModelHint`

Dedup key:
- `(model_id, node_id)`

Update semantics:
- unconditional replace for existing `(model_id, node_id)`
- append new node hint if capacity allows

Capacity:
- max 100 hints per model

Rate limit:
- max 10 events/second per `node_id`

The LAN cache stores hints only. Selection policy remains in the server layer.

---

## 9. LAN Boundary

LAN layer responsibilities:
- send authenticated batch announcements
- verify authenticated incoming announcements
- keep ephemeral hint cache

LAN layer must not:
- perform PKI or key attribution
- persist peer trust state
- influence torrent peer selection
- become an authority for artifact correctness

---

## 10. Summary

This protocol is intentionally minimal:
- shared-secret authenticated gossip
- raw JSON batch signing
- strict fail-closed verification
- no identity stack, no PKI, no per-node crypto lifecycle
