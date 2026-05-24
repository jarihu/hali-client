package daemon

import "hali/internal/events"

// Cmd names for the IPC protocol.
type Cmd string

const (
	CmdSeed         Cmd = "seed"
	CmdSeedStatus   Cmd = "seed_status"   // poll a seed-finalization job
	CmdEnqueueEvent Cmd = "enqueue_event" // persist a fully formed model pull event
	CmdStatus       Cmd = "status"
	CmdList         Cmd = "list"
	CmdStop         Cmd = "stop"
	CmdLanQuery     Cmd = "lan_query"  // query LAN model hint cache for a model
	CmdLanSeen      Cmd = "lan_seen"   // read observational LAN announcement snapshot
	CmdDownload     Cmd = "download"   // start torrent download by infohash
	CmdCancelJob    Cmd = "cancel_job" // cancel an active torrent download job
	CmdJobStatus    Cmd = "job_status" // poll a download job
	CmdStats        Cmd = "stats"      // get transfer statistics snapshot
)

// Request is sent by the CLI to the daemon.
type Request struct {
	Cmd                     Cmd                    `json:"cmd"`
	Token                   string                 `json:"token,omitempty"` // IPC shared-secret (Windows only)
	ModelID                 string                 `json:"model_id,omitempty"`
	Dir                     string                 `json:"dir,omitempty"`
	Filename                string                 `json:"filename,omitempty"`
	HFRepo                  string                 `json:"hf_repo,omitempty"`
	HFRevision              string                 `json:"hf_revision,omitempty"`
	Infohash                string                 `json:"infohash,omitempty"`
	InfohashV2              string                 `json:"infohash_v2,omitempty"`
	PeerAddrs               []string               `json:"peer_addrs,omitempty"`
	JobID                   string                 `json:"job_id,omitempty"`
	AllowUnreachablePublish bool                   `json:"allow_unreachable_publish,omitempty"`
	Event                   *events.ModelPullEvent `json:"event,omitempty"`
	// Pieces holds precomputed SHA1 piece hashes (flat 20-bytes-per-piece) from
	// PieceHasher.Finalize(). When set alongside FileSize, the daemon skips the
	// post-download BuildFromFilePath re-read and seeds immediately.
	Pieces   []byte `json:"pieces,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}

// Response is sent by the daemon back to the CLI.
type Response struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

// SeedInfo describes one model being seeded.
type SeedInfo struct {
	ModelID    string `json:"model_id"`
	Infohash   string `json:"infohash,omitempty"`
	InfohashV2 string `json:"infohash_v2,omitempty"`
	MagnetURI  string `json:"magnet_uri,omitempty"`
	Status     string `json:"status"`
	Peers      string `json:"peers,omitempty"`
}

// StatusData is the payload for a status response.
type StatusData struct {
	PID          int               `json:"pid"`
	Uptime       string            `json:"uptime"`
	Port         int               `json:"port"`
	Network      NetworkStatusData `json:"network"`
	Seeding      []SeedInfo        `json:"seeding"`
	LAN          []LanEntry        `json:"lan,omitempty"`
	Managed      bool              `json:"managed,omitempty"`
	LockedFields map[string]bool   `json:"locked_fields,omitempty"`
}

type NetworkCapabilitiesData struct {
	LSD bool `json:"lsd"`
}

type NetworkStatusData struct {
	Mode         string                  `json:"mode"`
	Capabilities NetworkCapabilitiesData `json:"capabilities"`
}

// LanEntry describes one model available on the LAN.
type LanEntry struct {
	ModelID    string `json:"model_id"`
	Infohash   string `json:"infohash"`
	InfohashV2 string `json:"infohash_v2,omitempty"`
	Peers      int    `json:"peers"`
}

// LanQueryData is the payload for a lan_query response.
// It returns an infohash hint only — TTL policy and selection are applied by
// the server layer. The torrent engine receives the infohash string; no LAN
// structs are passed further.
type LanQueryData struct {
	ModelID     string   `json:"model_id"`
	Revision    string   `json:"revision,omitempty"`
	Infohash    string   `json:"infohash"`
	InfohashV2  string   `json:"infohash_v2,omitempty"`
	HFRepo      string   `json:"hf_repo,omitempty"`
	ArtifactKey string   `json:"artifact_key,omitempty"`
	PeerCount   int      `json:"peer_count,omitempty"`
	LastSeen    int64    `json:"last_seen,omitempty"`
	PeerAddrs   []string `json:"peer_addrs,omitempty"`
}

// LanSeenEntry is an observational LAN snapshot row.
//
// Values are soft-state observations from multicast announcements and may be
// stale, incomplete, duplicated, delayed, or incorrect.
type LanSeenEntry struct {
	ModelID     string   `json:"model_id"`
	HFRepo      string   `json:"hf_repo,omitempty"`
	Revision    string   `json:"revision,omitempty"`
	Infohash    string   `json:"infohash"`
	InfohashV2  string   `json:"infohash_v2,omitempty"`
	ArtifactKey string   `json:"artifact_key,omitempty"`
	PeerCount   int      `json:"peer_count"`
	LastSeen    int64    `json:"last_seen"`
	PeerAddrs   []string `json:"peer_addrs,omitempty"`
}

// LanSeenData is the payload for lan_seen responses.
type LanSeenData struct {
	Announcements []LanSeenEntry `json:"announcements"`
}

// JobStatusData is the payload for a job_status response.
type JobStatusData struct {
	JobID         string `json:"job_id"`
	ModelID       string `json:"model_id"`
	MagnetURI     string `json:"magnet_uri,omitempty"`
	Filename      string `json:"filename,omitempty"`
	Written       int64  `json:"written"`
	Total         int64  `json:"total"`
	RateBps       int64  `json:"rate_bps,omitempty"`
	ElapsedSec    int64  `json:"elapsed_sec,omitempty"`
	ETASeconds    int64  `json:"eta_sec,omitempty"`
	ActivePeers   int    `json:"active_peers,omitempty"`
	PendingPeers  int    `json:"pending_peers,omitempty"`
	HalfOpenPeers int    `json:"half_open_peers,omitempty"`
	TotalPeers    int    `json:"total_peers,omitempty"`
	Done          bool   `json:"done"`
	Error         string `json:"error,omitempty"`
}

// SeedStatusData is the payload for a seed_status response.
type SeedStatusData struct {
	JobID     string `json:"job_id"`
	ModelID   string `json:"model_id"`
	Infohash  string `json:"infohash,omitempty"`
	MagnetURI string `json:"magnet_uri,omitempty"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
}
