package torrent

import (
	"sync"
	"time"
)

// EngineStats is the only contract StatsCollector needs from the torrent engine.
// It intentionally exposes only cumulative byte counters and model state.
type EngineStats interface {
	TotalBytes() (down int64, up int64)
	ActiveModels() []EngineModelState
}

// SpeedSample is a point-in-time speed measurement.
type SpeedSample struct {
	T    int64 `json:"t"`    // unix seconds
	Down int64 `json:"down"` // bytes/s
	Up   int64 `json:"up"`   // bytes/s
}

// ModelStats holds per-model transfer stats for a snapshot.
type ModelStats struct {
	ModelID   string `json:"model_id"`
	MagnetURI string `json:"magnet_uri,omitempty"`
	Status    string `json:"status"`     // "downloading" | "seeding" | "hashing" | "error"
	DownSpeed int64  `json:"down_speed"` // bytes/s from last sample
	UpSpeed   int64  `json:"up_speed"`   // bytes/s from last sample
	Peers     int    `json:"peers"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	TotalDown int64  `json:"total_down"`         // cumulative bytes downloaded
	TotalUp   int64  `json:"total_up"`           // cumulative bytes uploaded
	Progress  int    `json:"progress,omitempty"` // 0–100, set for "downloading" only
}

// StatsSnapshot is the full stats picture from the daemon at a point in time.
type StatsSnapshot struct {
	Uptime      string        `json:"uptime"`
	ActiveDLs   int           `json:"active_downloads"`
	ActiveSeeds int           `json:"active_seeds"`
	TotalDown   int64         `json:"total_down"` // session bytes
	TotalUp     int64         `json:"total_up"`   // session bytes
	DownSpeed   int64         `json:"down_speed"` // bytes/s aggregate
	UpSpeed     int64         `json:"up_speed"`   // bytes/s aggregate
	Models      []ModelStats  `json:"models"`
	History     []SpeedSample `json:"speed_history,omitempty"` // last 60 samples
}

// speedTracker is a 60-slot ring buffer of per-second speed samples.
// Not thread-safe — callers must hold StatsCollector.mu.
type speedTracker struct {
	buf           [60]SpeedSample
	head          int // next write slot
	count         int // filled slots (0–60)
	prevTotalDown int64
	prevTotalUp   int64
	prevAt        time.Time
}

func (t *speedTracker) record(totalDown, totalUp int64) {
	now := time.Now()
	var down, up int64
	if !t.prevAt.IsZero() {
		if elapsed := now.Sub(t.prevAt).Seconds(); elapsed > 0 {
			down = int64(float64(totalDown-t.prevTotalDown) / elapsed)
			up = int64(float64(totalUp-t.prevTotalUp) / elapsed)
			if down < 0 {
				down = 0
			}
			if up < 0 {
				up = 0
			}
		}
	}
	t.buf[t.head] = SpeedSample{T: now.Unix(), Down: down, Up: up}
	t.head = (t.head + 1) % 60
	if t.count < 60 {
		t.count++
	}
	t.prevTotalDown = totalDown
	t.prevTotalUp = totalUp
	t.prevAt = now
}

func (t *speedTracker) current() (down, up int64) {
	if t.count == 0 {
		return 0, 0
	}
	s := t.buf[(t.head-1+60)%60]
	return s.Down, s.Up
}

func (t *speedTracker) history() []SpeedSample {
	out := make([]SpeedSample, t.count)
	start := ((t.head-t.count)%60 + 60) % 60
	for i := 0; i < t.count; i++ {
		out[i] = t.buf[(start+i)%60]
	}
	return out
}

// StatsCollector samples the engine every second, maintains a speed ring buffer,
// and produces StatsSnapshot on demand. It is owned by the daemon server.
type StatsCollector struct {
	engine      EngineStats
	mu          sync.RWMutex
	tracker     speedTracker
	prevCumDown map[string]int64    // model_id → last cumulative download bytes
	prevCumUp   map[string]int64    // model_id → last cumulative upload bytes
	modelSpeeds map[string][2]int64 // model_id → [downSpeed, upSpeed] from last 1s delta
	lastSample  time.Time
	stopCh      chan struct{}
	startOnce   sync.Once
	stopOnce    sync.Once
}

func NewStatsCollector(e EngineStats) *StatsCollector {
	return &StatsCollector{
		engine:      e,
		prevCumDown: make(map[string]int64),
		prevCumUp:   make(map[string]int64),
		modelSpeeds: make(map[string][2]int64),
		stopCh:      make(chan struct{}),
	}
}

func (c *StatsCollector) Start() {
	c.startOnce.Do(func() {
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					c.sample()
				case <-c.stopCh:
					return
				}
			}
		}()
	})
}

func (c *StatsCollector) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

func (c *StatsCollector) sample() {
	now := time.Now()
	totalDown, totalUp := c.engine.TotalBytes()
	models := c.engine.ActiveModels()

	var elapsed float64
	c.mu.RLock()
	if !c.lastSample.IsZero() {
		elapsed = now.Sub(c.lastSample).Seconds()
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		seen[m.ModelID] = struct{}{}

		dDown := m.Down - c.prevCumDown[m.ModelID]
		dUp := m.Up - c.prevCumUp[m.ModelID]
		if dDown < 0 {
			dDown = 0
		}
		if dUp < 0 {
			dUp = 0
		}

		var downSpeed, upSpeed int64
		if elapsed > 0 {
			downSpeed = int64(float64(dDown) / elapsed)
			upSpeed = int64(float64(dUp) / elapsed)
		}
		c.modelSpeeds[m.ModelID] = [2]int64{downSpeed, upSpeed}
		c.prevCumDown[m.ModelID] = m.Down
		c.prevCumUp[m.ModelID] = m.Up
	}

	for modelID := range c.modelSpeeds {
		if _, ok := seen[modelID]; !ok {
			delete(c.modelSpeeds, modelID)
			delete(c.prevCumDown, modelID)
			delete(c.prevCumUp, modelID)
		}
	}

	c.tracker.record(totalDown, totalUp)
	c.lastSample = now
}

// Snapshot returns a complete stats picture. uptime is the daemon's running time.
func (c *StatsCollector) Snapshot(uptime time.Duration) StatsSnapshot {
	engineModels := c.engine.ActiveModels()
	downSpeed, upSpeed, modelSpeeds := c.collectSpeeds()
	totalDown, totalUp, activeDLs, activeSeeds := c.collectTotals(engineModels)
	models := c.collectModels(engineModels, modelSpeeds)
	hist := c.collectHistory()

	return StatsSnapshot{
		Uptime:      uptime.Round(time.Second).String(),
		ActiveDLs:   activeDLs,
		ActiveSeeds: activeSeeds,
		TotalDown:   totalDown,
		TotalUp:     totalUp,
		DownSpeed:   downSpeed,
		UpSpeed:     upSpeed,
		Models:      models,
		History:     hist,
	}
}

func (c *StatsCollector) collectTotals(models []EngineModelState) (int64, int64, int, int) {
	var totalDown, totalUp int64
	var activeDLs, activeSeeds int
	for _, m := range models {
		totalDown += m.Down
		totalUp += m.Up
		if m.IsJob {
			activeDLs++
			continue
		}
		if m.Status == StatusSeeding {
			activeSeeds++
		}
	}
	return totalDown, totalUp, activeDLs, activeSeeds
}

func (c *StatsCollector) collectSpeeds() (int64, int64, map[string][2]int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	downSpeed, upSpeed := c.tracker.current()
	modelSpeeds := make(map[string][2]int64, len(c.modelSpeeds))
	for k, v := range c.modelSpeeds {
		modelSpeeds[k] = v
	}
	return downSpeed, upSpeed, modelSpeeds
}

func (c *StatsCollector) collectModels(engineModels []EngineModelState, modelSpeeds map[string][2]int64) []ModelStats {
	models := make([]ModelStats, 0, len(engineModels))
	for _, m := range engineModels {
		sp := modelSpeeds[m.ModelID]
		status := string(m.Status)
		if m.IsJob {
			status = "downloading"
		}

		var progress int
		if m.IsJob && m.Total > 0 {
			progress = int(m.Written * 100 / m.Total)
		}

		models = append(models, ModelStats{
			ModelID:   m.ModelID,
			MagnetURI: m.MagnetURI,
			Status:    status,
			DownSpeed: sp[0],
			UpSpeed:   sp[1],
			Peers:     m.Peers,
			SizeBytes: m.SizeBytes,
			TotalDown: m.Down,
			TotalUp:   m.Up,
			Progress:  progress,
		})
	}
	return models
}

func (c *StatsCollector) collectHistory() []SpeedSample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tracker.history()
}
