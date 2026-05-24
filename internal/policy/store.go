package policy

// Store is a read-only source of policy registry key/value pairs.
// Abstracted so tests can inject a fake implementation without registry access.
type Store interface {
	// ReadDWORD reads a DWORD value from the given subkey and name.
	// Returns (value, true, nil) when found, (0, false, nil) when absent,
	// or (0, false, err) on an unexpected error.
	ReadDWORD(subkey, name string) (uint32, bool, error)
}

// Load reads all policy keys from store and returns a populated Policy.
// Absent registry keys produce nil pointer fields (no override for that setting).
// Errors reading individual keys are silently ignored — the field is left nil.
func Load(store Store) (Policy, error) {
	var p Policy

	// Network
	if v, ok, _ := store.ReadDWORD("Network", "EnableLanSeeding"); ok {
		b := v != 0
		p.EnableLanSeeding = &b
	}
	if v, ok, _ := store.ReadDWORD("Network", "MaxUploadKBps"); ok {
		i := int(v)
		p.MaxUploadKBps = &i
	}
	if v, ok, _ := store.ReadDWORD("Network", "AllowWifiSeeding"); ok {
		b := v != 0
		p.AllowWifiSeeding = &b
	}
	if v, ok, _ := store.ReadDWORD("Network", "AllowVpnSeeding"); ok {
		b := v != 0
		p.AllowVpnSeeding = &b
	}
	if v, ok, _ := store.ReadDWORD("Network", "AdaptiveThrottling"); ok {
		b := v != 0
		p.AdaptiveThrottling = &b
	}

	// Features
	if v, ok, _ := store.ReadDWORD("Features", "MulticastEnabled"); ok {
		b := v != 0
		p.MulticastEnabled = &b
	}
	if v, ok, _ := store.ReadDWORD("Features", "TorrentEnabled"); ok {
		b := v != 0
		p.TorrentEnabled = &b
	}

	// Controls
	if v, ok, _ := store.ReadDWORD("Controls", "AllowUserPause"); ok {
		b := v != 0
		p.AllowUserPause = &b
	}
	if v, ok, _ := store.ReadDWORD("Controls", "MaxPauseMinutes"); ok {
		i := int(v)
		p.MaxPauseMinutes = &i
	}
	if v, ok, _ := store.ReadDWORD("Controls", "AllowFocusMode"); ok {
		b := v != 0
		p.AllowFocusMode = &b
	}
	if v, ok, _ := store.ReadDWORD("Controls", "AllowUserChangeBandwidth"); ok {
		b := v != 0
		p.AllowUserChangeBandwidth = &b
	}
	if v, ok, _ := store.ReadDWORD("Controls", "AllowUserChangeLan"); ok {
		b := v != 0
		p.AllowUserChangeLan = &b
	}

	return p, nil
}
