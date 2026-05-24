package daemon

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hali/internal/buildinfo"
	"hali/internal/cache"
	"hali/internal/policy"
	"hali/internal/torrent"
)

//go:embed web/index.html web/static web/_fragments
var webFS embed.FS

// webMux wires all HTTP routes for the dashboard and API.
func (s *Server) webMux() http.Handler {
	mux := http.NewServeMux()

	// Parse fragment templates from the embedded FS.
	tStatus := mustTmpl(webFS, "web/_fragments/status.html")
	tDownloads := mustTmpl(webFS, "web/_fragments/downloads.html")
	tLAN := mustTmpl(webFS, "web/_fragments/lan.html")
	tActivity := mustTmpl(webFS, "web/_fragments/activity.html")
	tSettings := mustTmpl(webFS, "web/_fragments/settings.html")

	// Static assets (CSS, htmx.min.js).
	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic("embed: " + err.Error())
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// ── JSON API ─────────────────────────────────────────
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"status":  "ok",
			"version": buildinfo.Version,
			"commit":  buildinfo.Commit,
			"uptime":  int64(time.Since(s.startedAt).Seconds()),
		})
	})
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.statsSnapshot()) //nolint:errcheck
	})
	mux.HandleFunc("/api/pause-state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		paused, untilUnix := s.pauseState()
		json.NewEncoder(w).Encode(map[string]any{"paused": paused, "pause_until": untilUnix}) //nolint:errcheck
	})
	mux.HandleFunc("/api/lan-sharing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]bool{"enabled": s.lanShare.Load()}) //nolint:errcheck
		case http.MethodPost:
			var payload struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
				return
			}
			s.lanShare.Store(payload.Enabled)
			s.applyEngineSettings("lan_sharing_api")
			if payload.Enabled {
				s.activity.Append(Event{Kind: "lan.sharing", Message: "LAN sharing enabled"})
			} else {
				s.activity.Append(Event{Kind: "lan.sharing", Message: "LAN sharing disabled"})
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true, "enabled": s.lanShare.Load()}) //nolint:errcheck
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/activity", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.activity.Snapshot(50)) //nolint:errcheck
	})
	mux.HandleFunc("/api/lan-seen", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rows := summarizeLANSeen(s.lanIndex.Snapshot(), time.Now())
		if len(rows) > maxLANSeenRows {
			rows = rows[:maxLANSeenRows]
		}
		json.NewEncoder(w).Encode(LanSeenData{Announcements: rows}) //nolint:errcheck
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			// Always return effective (policy-applied) settings so the UI reflects enforced values.
			json.NewEncoder(w).Encode(s.effectiveSettings()) //nolint:errcheck
		case http.MethodPost:
			incoming, err := parseIncomingSettings(r)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
				return
			}
			s.settings.Lock()
			prev := s.cfg
			s.cfg = incoming
			s.settings.Unlock()

			// If nothing changed, avoid writing config.json. This prevents stale UI
			// submissions from overwriting newer file edits made via CLI/config file.
			if incoming == prev {
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "effective": s.applySettings(incoming)}) //nolint:errcheck
				return
			}

			effective := s.applySettings(incoming)
			if effective.MaxUploadKBps != incoming.MaxUploadKBps && incoming.MaxUploadKBps != 0 {
				slog.Info("settings_clamped",
					"field", "max_upload_kbps",
					"requested", incoming.MaxUploadKBps,
					"applied", effective.MaxUploadKBps,
				)
			}
			slog.Info("settings_applied",
				"max_upload_kbps", effective.MaxUploadKBps,
				"max_download_kbps", effective.MaxDownloadKBps,
			)
			s.applyEngineSettings("settings_api")

			s.persistSettings()

			s.activity.Append(Event{Kind: "settings.update", Message: "resource limits updated"})
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "effective": effective}) //nolint:errcheck
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/policy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.resolverMu.RLock()
		managed := s.resolver.Managed()
		lockedFields := s.resolver.LockedFields()
		rawPolicy := s.resolver.RawPolicy()
		s.resolverMu.RUnlock()
		effective := s.effectiveSettings()
		json.NewEncoder(w).Encode(struct { //nolint:errcheck
			Managed      bool            `json:"managed"`
			LockedFields map[string]bool `json:"locked_fields"`
			Effective    policy.Settings `json:"effective"`
			Policy       policy.Policy   `json:"policy"`
		}{
			Managed:      managed,
			LockedFields: lockedFields,
			Effective:    effective,
			Policy:       rawPolicy,
		})
	})
	mux.HandleFunc("/api/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.resolverMu.RLock()
		allowPause := s.resolver.AllowPause()
		s.resolverMu.RUnlock()
		if !allowPause {
			slog.Warn("pause_denied")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "pause is disabled by organization policy"}) //nolint:errcheck
			return
		}

		var payload struct {
			Minutes int `json:"minutes"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !strings.Contains(err.Error(), "EOF") {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
				return
			}
		}

		requested := payload.Minutes
		if requested < 0 {
			requested = 0
		}
		ceiling := s.resolver.MaxPauseMinutes()
		applied := requested
		if ceiling > 0 && (applied == 0 || applied > ceiling) {
			applied = ceiling
		}

		s.pauseFor(applied)
		s.applyEngineSettings("pause_api")
		slog.Info("pause_applied")
		if applied > 0 {
			s.activity.Append(Event{Kind: "transfers.pause", Message: fmt.Sprintf("transfers paused for %d minutes", applied)})
		} else {
			s.activity.Append(Event{Kind: "transfers.pause", Message: "transfers paused"})
		}
		_, untilUnix := s.pauseState()
		json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"paused":          true,
			"pause_until":     untilUnix,
			"applied_minutes": applied,
		}) //nolint:errcheck
	})
	mux.HandleFunc("/api/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resumed := s.resumeNow("resume_api")
		if resumed {
			slog.Info("resume_applied")
			s.activity.Append(Event{Kind: "transfers.resume", Message: "transfers resumed"})
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "paused": false, "pause_until": 0}) //nolint:errcheck
	})

	// ── HTML fragments (HTMX polling targets) ────────────
	mux.HandleFunc("/fragments/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTmpl(w, tStatus, buildStatusData(s.statsSnapshot(), s.paused.Load(), s.lanShare.Load()))
	})
	mux.HandleFunc("/fragments/downloads", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTmpl(w, tDownloads, buildDownloadsData(s.statsSnapshot(), modelMetadataByID(s.store), s.paused.Load(), s.lanShare.Load()))
	})
	mux.HandleFunc("/fragments/lan", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		rows := summarizeLANSeen(s.lanIndex.Snapshot(), time.Now())
		if len(rows) > maxLANSeenRows {
			rows = rows[:maxLANSeenRows]
		}
		renderTmpl(w, tLAN, buildLANData(rows))
	})
	mux.HandleFunc("/fragments/activity", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTmpl(w, tActivity, struct{ Events []Event }{s.activity.Snapshot(50)})
	})
	mux.HandleFunc("/fragments/settings", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTmpl(w, tSettings, buildSettingsData(s.effectiveSettings()))
	})

	// ── HTML shell ────────────────────────────────────────
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		w.Write(data) //nolint:errcheck
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/fragments/") || strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		mux.ServeHTTP(w, r)
	})
}

func parseIncomingSettings(r *http.Request) (policy.Settings, error) {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/json") {
		var incoming policy.Settings
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			return policy.Settings{}, err
		}
		return incoming, nil
	}

	if err := r.ParseForm(); err != nil {
		return policy.Settings{}, err
	}

	// UI posts MB/s values; convert to persisted KB/s. 0 means unlimited.
	uploadKBps := parseRateFromForm(r.Form.Get("max_upload_mbps"))
	downloadKBps := parseRateFromForm(r.Form.Get("max_download_mbps"))

	return policy.Settings{
		MaxUploadKBps:   uploadKBps,
		MaxDownloadKBps: downloadKBps,
	}, nil
}

func parseRateFromForm(mbRaw string) int {
	mbRaw = strings.TrimSpace(mbRaw)
	if mbRaw != "" {
		if mb, err := strconv.ParseFloat(mbRaw, 64); err == nil {
			if mb <= 0 {
				return 0
			}
			return int(mb*1024 + 0.5)
		}
	}
	return 0
}

// mustTmpl parses a template file from the embedded FS or panics.
func mustTmpl(fsys embed.FS, path string) *template.Template {
	data, err := fsys.ReadFile(path)
	if err != nil {
		panic("embed template: " + err.Error())
	}
	return template.Must(template.New(path).Parse(string(data)))
}

// renderTmpl executes a template and ignores write errors (connection closed).
func renderTmpl(w http.ResponseWriter, t *template.Template, data any) {
	t.Execute(w, data) //nolint:errcheck
}

// ── Template data builders ────────────────────────────────

type statusTmplData struct {
	DownSpeed string
	UpSpeed   string
	TotalDown string
	TotalUp   string
	Seeds     int
	Downloads int
	Models    int
	State     string
	Seeding   []statusSeedRow
}

type statusSeedRow struct {
	ModelID string
	Peers   int
}

func buildStatusData(snap torrent.StatsSnapshot, paused bool, lanSharing bool) statusTmplData {
	state := "idle"
	if paused {
		state = "paused"
	} else if !lanSharing {
		state = "sharing-off"
	} else if snap.ActiveDLs > 0 {
		state = "downloading"
	} else if snap.ActiveSeeds > 0 {
		state = "seeding"
	}
	seedingRows := statusSeedRows(snap)
	if !lanSharing {
		seedingRows = nil
	}
	return statusTmplData{
		DownSpeed: formatSpeed(snap.DownSpeed),
		UpSpeed:   formatSpeed(snap.UpSpeed),
		TotalDown: formatBytes(snap.TotalDown),
		TotalUp:   formatBytes(snap.TotalUp),
		Seeds:     len(seedingRows),
		Downloads: snap.ActiveDLs,
		Models:    len(snap.Models),
		State:     state,
		Seeding:   seedingRows,
	}
}

func statusSeedRows(snap torrent.StatsSnapshot) []statusSeedRow {
	rows := make([]statusSeedRow, 0, len(snap.Models))
	for _, m := range snap.Models {
		if m.Status != "seeding" {
			continue
		}
		rows = append(rows, statusSeedRow{ModelID: m.ModelID, Peers: m.Peers})
	}
	return rows
}

type modelRow struct {
	ModelID      string
	Status       string
	DownSpeedStr string
	UpSpeedStr   string
	Peers        int
	Progress     int
	MagnetURI    string
	ShareCommand string
	SizeStr      string
	TrafficStr   string
	PullsStr     string
	SharedStr    string
}

type downloadsTmplData struct {
	Models []modelRow
}

type settingsTmplData struct {
	MaxUploadMBps   string
	MaxDownloadMBps string
}

func buildSettingsData(cfg policy.Settings) settingsTmplData {
	return settingsTmplData{
		MaxUploadMBps:   formatKBpsAsMBps(cfg.MaxUploadKBps),
		MaxDownloadMBps: formatKBpsAsMBps(cfg.MaxDownloadKBps),
	}
}

func formatKBpsAsMBps(kbps int) string {
	if kbps <= 0 {
		return "0"
	}
	mb := float64(kbps) / 1024.0
	text := fmt.Sprintf("%.2f", mb)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if text == "" {
		return "0"
	}
	return text
}

func buildDownloadsData(snap torrent.StatsSnapshot, metaByID map[string]cache.Metadata, paused bool, lanSharing bool) downloadsTmplData {
	rows := make([]modelRow, len(snap.Models))
	for i, m := range snap.Models {
		displayStatus := m.Status
		if paused && (m.Status == "downloading" || m.Status == "seeding" || m.Status == "hashing") {
			displayStatus = "paused"
		} else if !lanSharing && m.Status == "seeding" {
			displayStatus = "sharing-off"
		}

		size := m.SizeBytes
		if meta, ok := metaByID[m.ModelID]; ok && meta.Size > 0 {
			size = meta.Size
		}
		estPulls := "-"
		estShares := "-"
		if size > 0 {
			estPulls = fmt.Sprintf("%dx", m.TotalDown/size)
			estShares = fmt.Sprintf("%dx", m.TotalUp/size)
		}

		rows[i] = modelRow{
			ModelID:      m.ModelID,
			Status:       displayStatus,
			DownSpeedStr: formatSpeed(m.DownSpeed),
			UpSpeedStr:   formatSpeed(m.UpSpeed),
			Peers:        m.Peers,
			Progress:     m.Progress,
			MagnetURI:    m.MagnetURI,
			ShareCommand: "hali pull " + m.ModelID,
			SizeStr:      formatBytes(size),
			TrafficStr:   fmt.Sprintf("down %s / up %s", formatBytes(m.TotalDown), formatBytes(m.TotalUp)),
			PullsStr:     estPulls,
			SharedStr:    estShares,
		}
		if !lanSharing && m.Status == "seeding" {
			rows[i].Peers = 0
			rows[i].UpSpeedStr = "—"
		}
	}
	return downloadsTmplData{Models: rows}
}

func modelMetadataByID(store *cache.Store) map[string]cache.Metadata {
	if store == nil {
		return nil
	}
	entries, err := store.List()
	if err != nil {
		return nil
	}
	out := make(map[string]cache.Metadata, len(entries))
	for _, e := range entries {
		out[e.ID.String()] = e.Meta
	}
	return out
}

type lanRow struct {
	ModelID    string
	Revision   string
	Peers      int
	LastSeen   string
	Variant    string
	Quant      string
	InfohashV1 string // debug view
	InfohashV2 string // debug view — empty if v1-only
}

type lanTmplData struct {
	Rows []lanRow
}

func buildLANData(rows []LanSeenEntry) lanTmplData {
	out := make([]lanRow, len(rows))
	for i, r := range rows {
		out[i] = lanRow{
			ModelID:    r.ModelID,
			Revision:   shortRevision(r.Revision),
			Peers:      r.PeerCount,
			LastSeen:   humanizeAge(time.Unix(r.LastSeen, 0)),
			Variant:    modelVariantFromID(r.ModelID),
			Quant:      modelQuantFromID(r.ModelID),
			InfohashV1: r.Infohash,
			InfohashV2: r.InfohashV2,
		}
	}
	return lanTmplData{Rows: out}
}

// ── Speed / size formatting ───────────────────────────────

func formatSpeed(bytesPerSec int64) string {
	if bytesPerSec == 0 {
		return "—"
	}
	return formatBytes(bytesPerSec) + "/s"
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func humanizeAge(t time.Time) string {
	age := time.Since(t)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
}

func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) <= 10 {
		return rev
	}
	return rev[:10] + "…"
}

func modelVariantFromID(modelID string) string {
	parts := strings.Split(modelID, ":")
	if len(parts) < 3 {
		return "—"
	}
	return parts[2]
}

func modelQuantFromID(modelID string) string {
	parts := strings.Split(modelID, ":")
	if len(parts) < 4 {
		return "—"
	}
	return parts[3]
}

// secureHandler wraps h with Host-header, Origin, and bearer-token checks
// to prevent DNS-rebinding, browser-based CSRF, and LAN-based attacks.
//
// When listening on a non-loopback address, a bearer token (stored in
// DataDir()/daemon.token) is required via Authorization header, cookie, or
// query param. Loopback-only deployments skip the token check.
func (s *Server) secureHandler(h http.Handler, listenAddr string) http.Handler {
	requireToken := s.webToken != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject requests whose Host doesn't match the bind address.
		// Prevents DNS-rebinding from malicious pages substituting a trusted hostname.
		if host := r.Host; host != "" && !isAllowedHost(host, listenAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// For state-mutation requests from a browser, require a matching Origin.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if !isAllowedOrigin(origin, listenAddr) {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		// Bearer token required when listening on a non-loopback interface.
		if requireToken && !verifyWebToken(s.webToken, r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// verifyWebToken checks the request for a valid web bearer token via
// Authorization header, hali_token cookie, or token query parameter.
func verifyWebToken(token string, r *http.Request) bool {
	if token == "" {
		return true
	}
	// Authorization: Bearer <token>
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(auth[7:]) == token
	}
	// X-Hali-Token header
	if r.Header.Get("X-Hali-Token") == token {
		return true
	}
	// Cookie
	if cookie, err := r.Cookie("hali_token"); err == nil && cookie.Value == token {
		return true
	}
	// Query param (for initial browser access before cookie is set)
	if r.URL.Query().Get("token") == token {
		return true
	}
	return false
}

// isAllowedHost reports whether the Host header value matches the listen address.
// Allows localhost and 127.0.0.1 as aliases for the loopback interface.
func isAllowedHost(host, listenAddr string) bool {
	hostOnly := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostOnly = h
	}
	switch strings.ToLower(hostOnly) {
	case "localhost", "127.0.0.1":
		return true
	}
	bindHost, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		bindHost = listenAddr
	}
	return strings.EqualFold(hostOnly, bindHost)
}

// isAllowedOrigin reports whether the Origin header value matches the listen address.
func isAllowedOrigin(origin, listenAddr string) bool {
	origin = strings.TrimPrefix(origin, "https://")
	origin = strings.TrimPrefix(origin, "http://")
	return isAllowedHost(origin, listenAddr)
}
