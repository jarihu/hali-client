package policy

// Settings holds user-configurable resource limits.
// Defined in this package so Resolver.Apply can work without circular imports.
type Settings struct {
	MaxUploadKBps   int `json:"max_upload_kbps"`
	MaxDownloadKBps int `json:"max_download_kbps"`
}

// Policy is the system policy snapshot read from HKLM.
// Pointer fields: nil means the registry key is absent (no override).
type Policy struct {
	EnableLanSeeding         *bool `json:"enable_lan_seeding,omitempty"`
	MaxUploadKBps            *int  `json:"max_upload_kbps,omitempty"`
	AllowWifiSeeding         *bool `json:"allow_wifi_seeding,omitempty"`
	AllowVpnSeeding          *bool `json:"allow_vpn_seeding,omitempty"`
	AdaptiveThrottling       *bool `json:"adaptive_throttling,omitempty"`
	MulticastEnabled         *bool `json:"multicast_enabled,omitempty"`
	TorrentEnabled           *bool `json:"torrent_enabled,omitempty"`
	AllowUserPause           *bool `json:"allow_user_pause,omitempty"`
	MaxPauseMinutes          *int  `json:"max_pause_minutes,omitempty"`
	AllowFocusMode           *bool `json:"allow_focus_mode,omitempty"`
	AllowUserChangeBandwidth *bool `json:"allow_user_change_bandwidth,omitempty"`
	AllowUserChangeLan       *bool `json:"allow_user_change_lan,omitempty"`
}

// Managed returns true if any policy field is configured (any pointer non-nil).
func (p Policy) Managed() bool {
	return p.EnableLanSeeding != nil ||
		p.MaxUploadKBps != nil ||
		p.AllowWifiSeeding != nil ||
		p.AllowVpnSeeding != nil ||
		p.AdaptiveThrottling != nil ||
		p.MulticastEnabled != nil ||
		p.TorrentEnabled != nil ||
		p.AllowUserPause != nil ||
		p.MaxPauseMinutes != nil ||
		p.AllowFocusMode != nil ||
		p.AllowUserChangeBandwidth != nil ||
		p.AllowUserChangeLan != nil
}

// LockedFields returns the set of Settings field names that the current policy
// makes non-user-editable. The UI disables inputs for these fields.
// A field is locked when policy sets it AND the corresponding AllowUserChange*
// flag is absent or false.
func (p Policy) LockedFields() map[string]bool {
	locked := make(map[string]bool)
	if p.MaxUploadKBps != nil {
		canChange := p.AllowUserChangeBandwidth != nil && *p.AllowUserChangeBandwidth
		if !canChange {
			locked["max_upload_kbps"] = true
		}
	}
	if p.AllowUserPause != nil && !*p.AllowUserPause {
		locked["allow_user_pause"] = true
	}
	if p.EnableLanSeeding != nil {
		canChange := p.AllowUserChangeLan != nil && *p.AllowUserChangeLan
		if !canChange {
			locked["enable_lan_seeding"] = true
		}
	}
	return locked
}

// Resolver enforces system policy over user-supplied Settings.
type Resolver struct{ p Policy }

// NewResolver returns a Resolver backed by p. The zero value is a no-op resolver.
func NewResolver(p Policy) Resolver { return Resolver{p: p} }

// Apply returns effective Settings by enforcing policy as a ceiling over user input.
// For numeric limits: policy value is a maximum ceiling; user may set a lower value.
// For boolean controls: policy value overrides the user directly.
func (r Resolver) Apply(user Settings) Settings {
	out := user
	if r.p.MaxUploadKBps != nil {
		ceiling := *r.p.MaxUploadKBps
		if ceiling < 0 {
			ceiling = 0
		}
		// Policy sets a maximum ceiling: clamp user value down, but allow lower.
		// ceiling == 0 means "unlimited" — no restriction on user choice.
		if ceiling > 0 && (out.MaxUploadKBps == 0 || out.MaxUploadKBps > ceiling) {
			out.MaxUploadKBps = ceiling
		}
	}
	return out
}

// Managed delegates to the underlying Policy.
func (r Resolver) Managed() bool { return r.p.Managed() }

// LockedFields delegates to the underlying Policy.
func (r Resolver) LockedFields() map[string]bool { return r.p.LockedFields() }

// AllowPause reports whether the user is permitted to pause transfers.
// Defaults to true when the policy field is absent.
func (r Resolver) AllowPause() bool {
	if r.p.AllowUserPause != nil {
		return *r.p.AllowUserPause
	}
	return true
}

// MaxPauseMinutes returns the policy ceiling on pause duration (minutes).
// Returns 0 when no ceiling is set. Clamped to [0, 1440].
func (r Resolver) MaxPauseMinutes() int {
	if r.p.MaxPauseMinutes == nil {
		return 0
	}
	v := *r.p.MaxPauseMinutes
	if v < 0 {
		return 0
	}
	if v > 1440 {
		return 1440
	}
	return v
}

// RawPolicy returns the underlying Policy for inspection (e.g. API responses).
func (r Resolver) RawPolicy() Policy { return r.p }
