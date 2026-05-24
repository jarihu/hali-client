package torrent

import (
	"testing"
	"time"
)

func TestSpeedTrackerRecordAndCurrent(t *testing.T) {
	st := &speedTracker{}
	st.record(0, 0)

	// First sample: no previous data, speeds should be 0
	down, up := st.current()
	if down != 0 || up != 0 {
		t.Errorf("first current() = (%d, %d), want (0, 0)", down, up)
	}

	time.Sleep(10 * time.Millisecond)

	// Second sample: 1000 bytes down, 500 up over ~10ms => ~100KB/s down, ~50KB/s up
	st.record(1000, 500)
	down, up = st.current()
	if down <= 0 {
		t.Errorf("second record down = %d, expected > 0", down)
	}
	if up <= 0 {
		t.Errorf("second record up = %d, expected > 0", up)
	}
}

func TestSpeedTrackerCurrentEmpty(t *testing.T) {
	st := &speedTracker{}
	down, up := st.current()
	if down != 0 || up != 0 {
		t.Errorf("current() on empty tracker = (%d, %d), want (0, 0)", down, up)
	}
}

func TestSpeedTrackerHistory(t *testing.T) {
	st := &speedTracker{}

	hist := st.history()
	if len(hist) != 0 {
		t.Errorf("history() on empty tracker = %d entries, want 0", len(hist))
	}

	st.record(0, 0)
	time.Sleep(time.Millisecond)
	st.record(100, 50)

	hist = st.history()
	if len(hist) != 2 {
		t.Errorf("history() = %d entries, want 2", len(hist))
	}
}

func TestSpeedTrackerNegativeSpeedClamping(t *testing.T) {
	st := &speedTracker{}
	st.record(1000, 500)
	time.Sleep(time.Millisecond)
	// Simulate counter reset (should never happen but clamped anyway)
	st.prevTotalDown = 1000
	st.prevTotalUp = 500
	st.record(500, 200) // total went "down" — should clamp to 0

	down, up := st.current()
	if down < 0 {
		t.Errorf("down speed should be clamped to 0, got %d", down)
	}
	if up < 0 {
		t.Errorf("up speed should be clamped to 0, got %d", up)
	}
}

func TestSpeedTrackerRingBufferFull(t *testing.T) {
	st := &speedTracker{}
	// Fill beyond 60 slots
	for i := 0; i < 70; i++ {
		st.record(int64(i*10), int64(i*5))
	}

	hist := st.history()
	if len(hist) > 60 {
		t.Errorf("history() = %d entries, want at most 60", len(hist))
	}
	if st.count > 60 {
		t.Errorf("count = %d, want at most 60", st.count)
	}
}

func TestSpeedTrackerHistoryOrder(t *testing.T) {
	st := &speedTracker{}
	st.record(100, 50)
	time.Sleep(time.Millisecond)
	st.record(200, 100)
	time.Sleep(time.Millisecond)
	st.record(300, 150)

	hist := st.history()
	if len(hist) != 3 {
		t.Fatalf("history() = %d entries, want 3", len(hist))
	}
	// Verify timestamps are monotonically increasing
	for i := 1; i < len(hist); i++ {
		if hist[i].T < hist[i-1].T {
			t.Errorf("history timestamps not monotonic: hist[%d].T=%d < hist[%d].T=%d", i, hist[i].T, i-1, hist[i-1].T)
		}
	}
}

func TestSpeedTrackerCurrentLatest(t *testing.T) {
	st := &speedTracker{}
	st.record(0, 0)
	st.record(100, 50)
	st.record(200, 100)

	down, up := st.current()
	// current() returns the last sample recorded
	// We can't know the exact value (depends on timing), just verify non-negative
	if down < 0 || up < 0 {
		t.Errorf("current() = (%d, %d), both should be >= 0", down, up)
	}
}

func TestStatsCollectorCollectTotals(t *testing.T) {
	c := &StatsCollector{}
	models := []EngineModelState{
		{ModelID: "a", IsJob: true, Down: 10, Up: 5},
		{ModelID: "b", Status: StatusSeeding, Down: 20, Up: 10},
		{ModelID: "c", Status: StatusHashing, Down: 5, Up: 2},
		{ModelID: "d", IsJob: true, Down: 8, Up: 4},
		{ModelID: "e", Status: StatusSeeding, Down: 15, Up: 7},
	}
	totalDown, totalUp, activeDLs, activeSeeds := c.collectTotals(models)

	if totalDown != 58 {
		t.Errorf("totalDown = %d, want 58", totalDown)
	}
	if totalUp != 28 {
		t.Errorf("totalUp = %d, want 28", totalUp)
	}
	if activeDLs != 2 {
		t.Errorf("activeDLs = %d, want 2", activeDLs)
	}
	if activeSeeds != 2 {
		t.Errorf("activeSeeds = %d, want 2", activeSeeds)
	}
}

func TestStatsCollectorCollectModels(t *testing.T) {
	c := &StatsCollector{}
	engineModels := []EngineModelState{
		{ModelID: "a", MagnetURI: "magnet:?xt=urn:btih:aaa", IsJob: true, Written: 50, Total: 100, Down: 500, Up: 100, Peers: 3},
		{ModelID: "b", MagnetURI: "magnet:?xt=urn:btih:bbb", Status: StatusSeeding, Down: 1000, Up: 500, Peers: 5},
	}
	modelSpeeds := map[string][2]int64{
		"a": {1024, 512},
		"b": {2048, 256},
	}

	models := c.collectModels(engineModels, modelSpeeds)
	if len(models) != 2 {
		t.Fatalf("collectModels() = %d models, want 2", len(models))
	}

	// First model is a job
	if models[0].ModelID != "a" {
		t.Errorf("models[0].ModelID = %q, want %q", models[0].ModelID, "a")
	}
	if models[0].Status != "downloading" {
		t.Errorf("models[0].Status = %q, want %q", models[0].Status, "downloading")
	}
	if models[0].Progress != 50 {
		t.Errorf("models[0].Progress = %d, want 50", models[0].Progress)
	}
	if models[0].DownSpeed != 1024 {
		t.Errorf("models[0].DownSpeed = %d, want 1024", models[0].DownSpeed)
	}
	if models[0].MagnetURI != "magnet:?xt=urn:btih:aaa" {
		t.Errorf("models[0].MagnetURI = %q, want magnet URI", models[0].MagnetURI)
	}

	// Second model is seeding
	if models[1].ModelID != "b" {
		t.Errorf("models[1].ModelID = %q, want %q", models[1].ModelID, "b")
	}
	if models[1].Status != "seeding" {
		t.Errorf("models[1].Status = %q, want %q", models[1].Status, "seeding")
	}
	if models[1].Progress != 0 {
		t.Errorf("models[1].Progress = %d, want 0 (not a job)", models[1].Progress)
	}
	if models[1].MagnetURI != "magnet:?xt=urn:btih:bbb" {
		t.Errorf("models[1].MagnetURI = %q, want magnet URI", models[1].MagnetURI)
	}
}

func TestStatsCollectorCollectModelsProgressZero(t *testing.T) {
	c := &StatsCollector{}
	engineModels := []EngineModelState{
		{ModelID: "z", IsJob: true, Total: 0, Written: 10}, // Total is 0, progress shouldn't divide by zero
	}
	modelSpeeds := map[string][2]int64{"z": {0, 0}}

	models := c.collectModels(engineModels, modelSpeeds)
	if len(models) != 1 {
		t.Fatalf("collectModels() = %d models, want 1", len(models))
	}
	if models[0].Progress != 0 {
		t.Errorf("Progress with zero Total = %d, want 0", models[0].Progress)
	}
}

func TestStatsCollectorCollectSpeeds(t *testing.T) {
	c := NewStatsCollector(nil)
	c.modelSpeeds["a"] = [2]int64{1024, 512}
	c.modelSpeeds["b"] = [2]int64{2048, 256}

	down, up, ms := c.collectSpeeds()
	if down != 0 || up != 0 {
		t.Logf("tracker speeds: (%d, %d) — expected 0 on empty tracker", down, up)
	}
	if len(ms) != 2 {
		t.Errorf("modelSpeeds length = %d, want 2", len(ms))
	}
	if ms["a"] != [2]int64{1024, 512} {
		t.Errorf("modelSpeeds[a] = %v, want [1024, 512]", ms["a"])
	}
}

func TestStatsCollectorSnapshot(t *testing.T) {
	// mockEngine implements EngineStats for testing Snapshot
	mock := &mockEngine{
		totalBytes: func() (int64, int64) { return 1000, 500 },
		activeModels: func() []EngineModelState {
			return []EngineModelState{
				{ModelID: "m1", Status: StatusSeeding, Down: 600, Up: 300, Peers: 2},
				{ModelID: "m2", IsJob: true, Down: 400, Up: 200, Written: 45, Total: 100, Peers: 1},
			}
		},
	}

	c := NewStatsCollector(mock)
	// Do a sample to populate data
	c.sample()

	snap := c.Snapshot(5 * time.Minute)
	if snap.Uptime != "5m0s" {
		t.Errorf("Uptime = %q, want %q", snap.Uptime, "5m0s")
	}
	if snap.ActiveDLs != 1 {
		t.Errorf("ActiveDLs = %d, want 1", snap.ActiveDLs)
	}
	if snap.ActiveSeeds != 1 {
		t.Errorf("ActiveSeeds = %d, want 1", snap.ActiveSeeds)
	}
	if snap.TotalDown != 1000 {
		t.Errorf("TotalDown = %d, want 1000", snap.TotalDown)
	}
	if snap.TotalUp != 500 {
		t.Errorf("TotalUp = %d, want 500", snap.TotalUp)
	}
	if len(snap.Models) != 2 {
		t.Errorf("Models length = %d, want 2", len(snap.Models))
	}
}

type mockEngine struct {
	totalBytes   func() (int64, int64)
	activeModels func() []EngineModelState
}

func (m *mockEngine) TotalBytes() (int64, int64)       { return m.totalBytes() }
func (m *mockEngine) ActiveModels() []EngineModelState { return m.activeModels() }

func TestStatsCollectorStartStop(t *testing.T) {
	mock := &mockEngine{
		totalBytes:   func() (int64, int64) { return 0, 0 },
		activeModels: func() []EngineModelState { return nil },
	}
	c := NewStatsCollector(mock)
	c.Start()
	c.Stop()
	// Verify Stop is idempotent
	c.Stop()
}
