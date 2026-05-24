package policy

import "testing"

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// ── Apply ────────────────────────────────────────────────────────────────────

func TestApply_PolicyCeilsUser(t *testing.T) {
	r := NewResolver(Policy{MaxUploadKBps: intPtr(100)})
	eff := r.Apply(Settings{MaxUploadKBps: 9999})
	if eff.MaxUploadKBps != 100 {
		t.Fatalf("want 100, got %d", eff.MaxUploadKBps)
	}
}

func TestApply_UserBelowCeilingIsKept(t *testing.T) {
	r := NewResolver(Policy{MaxUploadKBps: intPtr(100)})
	eff := r.Apply(Settings{MaxUploadKBps: 50})
	if eff.MaxUploadKBps != 50 {
		t.Fatalf("want 50, got %d", eff.MaxUploadKBps)
	}
}

func TestApply_NoPolicyPreservesUser(t *testing.T) {
	r := NewResolver(Policy{})
	eff := r.Apply(Settings{MaxUploadKBps: 500})
	if eff.MaxUploadKBps != 500 {
		t.Fatalf("want 500, got %d", eff.MaxUploadKBps)
	}
}

func TestApply_NegativePolicyClamped(t *testing.T) {
	r := NewResolver(Policy{MaxUploadKBps: intPtr(-10)})
	// Negative ceiling → treated as 0 (unlimited) → user value preserved.
	eff := r.Apply(Settings{MaxUploadKBps: 200})
	if eff.MaxUploadKBps != 200 {
		t.Fatalf("negative policy ceiling should not restrict user; got %d", eff.MaxUploadKBps)
	}
}

func TestApply_UserUnlimitedCappedByCeiling(t *testing.T) {
	r := NewResolver(Policy{MaxUploadKBps: intPtr(100)})
	eff := r.Apply(Settings{MaxUploadKBps: 0}) // 0 = unlimited
	if eff.MaxUploadKBps != 100 {
		t.Fatalf("want 100, got %d", eff.MaxUploadKBps)
	}
}

func TestApply_DownloadNotAffectedByPolicy(t *testing.T) {
	r := NewResolver(Policy{MaxUploadKBps: intPtr(100)})
	eff := r.Apply(Settings{MaxDownloadKBps: 9999})
	if eff.MaxDownloadKBps != 9999 {
		t.Fatalf("download should be unaffected; got %d", eff.MaxDownloadKBps)
	}
}

// ── Managed ──────────────────────────────────────────────────────────────────

func TestManaged_EmptyIsFalse(t *testing.T) {
	if (Policy{}).Managed() {
		t.Fatal("empty policy must not be managed")
	}
}

func TestManaged_SetFieldIsTrue(t *testing.T) {
	if !(Policy{MaxUploadKBps: intPtr(100)}).Managed() {
		t.Fatal("policy with a field set must be managed")
	}
}

// ── LockedFields ─────────────────────────────────────────────────────────────

func TestLockedFields_BandwidthLockedWhenChangeDisallowed(t *testing.T) {
	p := Policy{MaxUploadKBps: intPtr(100), AllowUserChangeBandwidth: boolPtr(false)}
	if !p.LockedFields()["max_upload_kbps"] {
		t.Fatal("max_upload_kbps should be locked")
	}
}

func TestLockedFields_BandwidthUnlockedWhenChangeAllowed(t *testing.T) {
	p := Policy{MaxUploadKBps: intPtr(100), AllowUserChangeBandwidth: boolPtr(true)}
	if p.LockedFields()["max_upload_kbps"] {
		t.Fatal("max_upload_kbps should not be locked when user can change it")
	}
}

func TestLockedFields_BandwidthLockedWhenChangeAbsent(t *testing.T) {
	// AllowUserChangeBandwidth absent → conservative default: lock the field
	p := Policy{MaxUploadKBps: intPtr(100)}
	if !p.LockedFields()["max_upload_kbps"] {
		t.Fatal("max_upload_kbps should be locked when AllowUserChangeBandwidth is absent")
	}
}

func TestLockedFields_PauseLockedWhenDisabled(t *testing.T) {
	p := Policy{AllowUserPause: boolPtr(false)}
	if !p.LockedFields()["allow_user_pause"] {
		t.Fatal("allow_user_pause should be locked")
	}
}

func TestLockedFields_PauseNotLockedWhenEnabled(t *testing.T) {
	p := Policy{AllowUserPause: boolPtr(true)}
	if p.LockedFields()["allow_user_pause"] {
		t.Fatal("allow_user_pause should not be locked when pause is allowed")
	}
}

// ── AllowPause / MaxPauseMinutes ─────────────────────────────────────────────

func TestAllowPause_DefaultTrue(t *testing.T) {
	if !NewResolver(Policy{}).AllowPause() {
		t.Fatal("AllowPause should default to true")
	}
}

func TestAllowPause_PolicyFalse(t *testing.T) {
	r := NewResolver(Policy{AllowUserPause: boolPtr(false)})
	if r.AllowPause() {
		t.Fatal("AllowPause should be false when policy says so")
	}
}

func TestMaxPauseMinutes_Clamped(t *testing.T) {
	r := NewResolver(Policy{MaxPauseMinutes: intPtr(9999)})
	if r.MaxPauseMinutes() != 1440 {
		t.Fatalf("want 1440, got %d", r.MaxPauseMinutes())
	}
}

func TestMaxPauseMinutes_ZeroWhenAbsent(t *testing.T) {
	if NewResolver(Policy{}).MaxPauseMinutes() != 0 {
		t.Fatal("MaxPauseMinutes should be 0 when not set")
	}
}

// ── Load (via FakeStore) ──────────────────────────────────────────────────────

func TestLoad_ReadsAllFields(t *testing.T) {
	fs := &FakeStore{}
	fs.set("Network", "MaxUploadKBps", 500)
	fs.set("Controls", "AllowUserPause", 0)
	fs.set("Features", "TorrentEnabled", 1)

	p, err := Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxUploadKBps == nil || *p.MaxUploadKBps != 500 {
		t.Fatalf("MaxUploadKBps: want 500, got %v", p.MaxUploadKBps)
	}
	if p.AllowUserPause == nil || *p.AllowUserPause {
		t.Fatal("AllowUserPause should be false")
	}
	if p.TorrentEnabled == nil || !*p.TorrentEnabled {
		t.Fatal("TorrentEnabled should be true")
	}
}

func TestLoad_AbsentKeyProducesNilField(t *testing.T) {
	p, err := Load(&FakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	if p.EnableLanSeeding != nil {
		t.Fatal("absent key must produce nil field")
	}
	if p.Managed() {
		t.Fatal("empty store must produce unmanaged policy")
	}
}
