package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ComputeIdentitySeal returns a deterministic SHA256 hex digest over
// canonical identity inputs using strict newline separators.
func ComputeIdentitySeal(m Metadata) string {
	repo := strings.ToLower(strings.TrimSpace(m.HFRepo))
	rev := strings.ToLower(strings.TrimSpace(m.HFRevision))
	snapshot := strings.ToLower(strings.TrimSpace(m.HFSnapshot))
	ih := strings.ToLower(strings.TrimSpace(m.Infohash))
	ihv2 := strings.ToLower(strings.TrimSpace(m.InfohashV2))

	canonical := repo + "\n" + rev + "\n" + snapshot + "\n" + ih + "\n" + ihv2
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// ValidateIdentitySeal enforces strict metadata requirements and verifies
// the stored seal against recomputed deterministic input.
func ValidateIdentitySeal(m Metadata) error {
	if strings.TrimSpace(m.ModelID) == "" {
		return fmt.Errorf("missing required metadata field: model_id")
	}
	if strings.TrimSpace(m.HFRepo) == "" {
		return fmt.Errorf("missing required metadata field: hf_repo")
	}
	if strings.TrimSpace(m.HFRevision) == "" {
		return fmt.Errorf("missing required metadata field: hf_revision")
	}
	if strings.TrimSpace(m.HFSnapshot) == "" {
		return fmt.Errorf("missing required metadata field: hf_snapshot_hash")
	}
	if strings.TrimSpace(m.IdentitySeal) == "" {
		return fmt.Errorf("missing required metadata field: identity_seal")
	}

	expected := ComputeIdentitySeal(m)
	got := strings.ToLower(strings.TrimSpace(m.IdentitySeal))
	if got != expected {
		return fmt.Errorf("identity seal mismatch")
	}
	return nil
}
