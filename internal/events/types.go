package events

import "time"

type ModelPullEvent struct {
	ModelID          string    `json:"model_id"`
	Revision         string    `json:"revision"`
	InfoHash         string    `json:"infohash"`
	PublisherPubKey  string    `json:"publisher_pubkey,omitempty"`
	PublisherSig     string    `json:"publisher_sig,omitempty"`
	ModelSizeBytes   int64     `json:"model_size_bytes,omitempty"`
	Quantization     string    `json:"quantization,omitempty"`
	Magnet           string    `json:"magnet"`
	SourceURL        string    `json:"source_url"`
	LocalHash        string    `json:"local_hash"`
	Timestamp        time.Time `json:"timestamp"`
	DeliveryAttempts int       `json:"delivery_attempts,omitempty"`
	NextAttemptAfter time.Time `json:"next_attempt_after,omitempty"`
}
