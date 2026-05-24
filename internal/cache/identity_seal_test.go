package cache

import "testing"

func validSealMeta() Metadata {
	m := Metadata{
		ModelID:    "mistral:7b:instruct:q4_k_m",
		HFRepo:     "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
		HFRevision: "abc123",
		HFSnapshot: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Infohash:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		InfohashV2: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Files:      []string{"model.gguf"},
		Size:       123,
	}
	m.IdentitySeal = ComputeIdentitySeal(m)
	return m
}

func TestComputeIdentitySealDeterministic(t *testing.T) {
	m := validSealMeta()
	a := ComputeIdentitySeal(m)
	b := ComputeIdentitySeal(m)
	if a != b {
		t.Fatalf("ComputeIdentitySeal not deterministic: %q != %q", a, b)
	}
}

func TestComputeIdentitySealSensitiveToInputs(t *testing.T) {
	base := validSealMeta()
	baseSeal := ComputeIdentitySeal(base)

	cases := []struct {
		name   string
		mutate func(*Metadata)
	}{
		{"hf_revision", func(m *Metadata) { m.HFRevision = "def456" }},
		{"hf_snapshot_hash", func(m *Metadata) { m.HFSnapshot = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" }},
		{"torrent_infohash", func(m *Metadata) { m.Infohash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" }},
		{"torrent_infohash_v2", func(m *Metadata) { m.InfohashV2 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mutate(&m)
			if got := ComputeIdentitySeal(m); got == baseSeal {
				t.Fatalf("seal did not change for %s mutation", tc.name)
			}
		})
	}
}

func TestValidateIdentitySeal(t *testing.T) {
	m := validSealMeta()
	if err := ValidateIdentitySeal(m); err != nil {
		t.Fatalf("ValidateIdentitySeal(valid) error: %v", err)
	}

	m.HFRevision = "tampered"
	if err := ValidateIdentitySeal(m); err == nil {
		t.Fatal("ValidateIdentitySeal(tampered) expected mismatch error")
	}
}
