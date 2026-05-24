package daemon

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRequestJSONRoundTrip(t *testing.T) {
	req := Request{
		Cmd:        "seed",
		ModelID:    "mistral:7b:instruct:q4_k_m",
		Dir:        "/tmp/model",
		Filename:   "model.gguf",
		HFRepo:     "org/repo",
		HFRevision: "abc123",
		Infohash:   "deadbeef",
		JobID:      "job1",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(Request) failed: %v", err)
	}

	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(Request) failed: %v", err)
	}

	if got.Cmd != req.Cmd || got.ModelID != req.ModelID || got.Dir != req.Dir ||
		got.Filename != req.Filename || got.HFRepo != req.HFRepo || got.HFRevision != req.HFRevision ||
		got.Infohash != req.Infohash || got.JobID != req.JobID ||
		!bytes.Equal(got.Pieces, req.Pieces) || got.FileSize != req.FileSize {
		t.Errorf("Request round-trip mismatch: %+v vs %+v", got, req)
	}
}

func TestResponseJSONRoundTrip(t *testing.T) {
	resp := Response{
		OK:    true,
		Data:  map[string]interface{}{"key": "value"},
		Error: "",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(Response) failed: %v", err)
	}

	var got Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(Response) failed: %v", err)
	}

	if got.OK != resp.OK {
		t.Errorf("OK = %v, want %v", got.OK, resp.OK)
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
}

func TestResponseErrorJSON(t *testing.T) {
	resp := Response{OK: false, Error: "something went wrong"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(Response) failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if ok, _ := raw["ok"].(bool); ok {
		t.Error("ok should be false")
	}
	if errStr, _ := raw["error"].(string); errStr != "something went wrong" {
		t.Errorf("error = %q, want %q", errStr, "something went wrong")
	}
	if _, hasData := raw["data"]; hasData {
		t.Error("data field should be omitted for error responses")
	}
}

func TestStatusDataJSON(t *testing.T) {
	sd := StatusData{
		PID:    12345,
		Uptime: "5m0s",
		Port:   6881,
		Seeding: []SeedInfo{
			{ModelID: "m:7b:instruct:q4_k_m", Infohash: "abcdef0123456789abcdef0123456789abcdef01", MagnetURI: "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01", Status: "seeding", Peers: "3 peers"},
		},
		LAN: []LanEntry{
			{ModelID: "l:7b:chat:q4_0", Infohash: "def0123456789abcdef0123456789abcdef01234", Peers: 2},
		},
	}

	data, err := json.Marshal(sd)
	if err != nil {
		t.Fatalf("json.Marshal(StatusData) failed: %v", err)
	}

	var got StatusData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(StatusData) failed: %v", err)
	}

	if got.PID != 12345 {
		t.Errorf("PID = %d, want 12345", got.PID)
	}
	if got.Uptime != "5m0s" {
		t.Errorf("Uptime = %q, want %q", got.Uptime, "5m0s")
	}
	if got.Port != 6881 {
		t.Errorf("Port = %d, want 6881", got.Port)
	}
	if len(got.Seeding) != 1 {
		t.Errorf("Seeding length = %d, want 1", len(got.Seeding))
	}
	if len(got.LAN) != 1 {
		t.Errorf("LAN length = %d, want 1", len(got.LAN))
	}
}

func TestSeedInfoJSON(t *testing.T) {
	si := SeedInfo{ModelID: "a:1b:base:q4_0", Infohash: "ih123", MagnetURI: "magnet:?xt=urn:btih:ih123", Status: "seeding", Peers: "5 peers"}
	data, err := json.Marshal(si)
	if err != nil {
		t.Fatalf("json.Marshal(SeedInfo) failed: %v", err)
	}

	var got SeedInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(SeedInfo) failed: %v", err)
	}
	if got != si {
		t.Errorf("SeedInfo round-trip mismatch: %+v vs %+v", got, si)
	}
}

func TestLanEntryJSON(t *testing.T) {
	le := LanEntry{ModelID: "m:1b:base:q4_0", Infohash: "ih456", Peers: 3}
	data, err := json.Marshal(le)
	if err != nil {
		t.Fatalf("json.Marshal(LanEntry) failed: %v", err)
	}

	var got LanEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(LanEntry) failed: %v", err)
	}
	if got != le {
		t.Errorf("LanEntry round-trip mismatch: %+v vs %+v", got, le)
	}
}

func TestLanQueryDataJSON(t *testing.T) {
	lq := LanQueryData{ModelID: "x:1b:base:q4_0", Infohash: "ih999", ArtifactKey: "x:1b:base:q4_0@rev1", PeerCount: 2, LastSeen: 1716000000}
	data, err := json.Marshal(lq)
	if err != nil {
		t.Fatalf("json.Marshal(LanQueryData) failed: %v", err)
	}

	var got LanQueryData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(LanQueryData) failed: %v", err)
	}
	if !reflect.DeepEqual(got, lq) {
		t.Errorf("LanQueryData round-trip mismatch: %+v vs %+v", got, lq)
	}
}

func TestLanSeenDataJSON(t *testing.T) {
	ls := LanSeenData{Announcements: []LanSeenEntry{
		{ModelID: "m:7b:instruct:q4_k_m", Revision: "abc123", Infohash: "ih999", ArtifactKey: "m:7b:instruct:q4_k_m@abc123", PeerCount: 2, LastSeen: 1716000000},
	}}
	data, err := json.Marshal(ls)
	if err != nil {
		t.Fatalf("json.Marshal(LanSeenData) failed: %v", err)
	}

	var got LanSeenData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(LanSeenData) failed: %v", err)
	}
	if len(got.Announcements) != 1 {
		t.Fatalf("announcements = %d, want 1", len(got.Announcements))
	}
	if !reflect.DeepEqual(got.Announcements[0], ls.Announcements[0]) {
		t.Errorf("LanSeenData round-trip mismatch: %+v vs %+v", got.Announcements[0], ls.Announcements[0])
	}
}

func TestJobStatusDataJSON(t *testing.T) {
	js := JobStatusData{
		JobID:     "job-1",
		ModelID:   "m:7b:instruct:q4_k_m",
		MagnetURI: "magnet:?xt=urn:btih:abc",
		Filename:  "model.gguf",
		Written:   500,
		Total:     1000,
		Done:      false,
		Error:     "",
	}

	data, err := json.Marshal(js)
	if err != nil {
		t.Fatalf("json.Marshal(JobStatusData) failed: %v", err)
	}

	var got JobStatusData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(JobStatusData) failed: %v", err)
	}
	if got != js {
		t.Errorf("JobStatusData round-trip mismatch: %+v vs %+v", got, js)
	}
}

func TestJobStatusDataOmitEmpty(t *testing.T) {
	js := JobStatusData{
		JobID:   "j1",
		ModelID: "m1",
		Written: 0,
		Total:   0,
		Done:    true,
	}
	data, err := json.Marshal(js)
	if err != nil {
		t.Fatalf("json.Marshal(JobStatusData) failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, hasError := raw["error"]; hasError {
		t.Error("error field should be omitted when empty")
	}
	if _, hasFilename := raw["filename"]; hasFilename {
		t.Error("filename field should be omitted when empty")
	}
	if _, hasMagnet := raw["magnet_uri"]; hasMagnet {
		t.Error("magnet_uri field should be omitted when empty")
	}
}

func TestRequestOmitEmpty(t *testing.T) {
	req := Request{Cmd: "status"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(Request) failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Only "cmd" should be present
	if len(raw) != 1 {
		t.Errorf("Request with only Cmd should have 1 field, got %d: %v", len(raw), raw)
	}
}

func TestCmdConstants(t *testing.T) {
	tests := []struct {
		cmd  Cmd
		want string
	}{
		{CmdSeed, "seed"},
		{CmdSeedStatus, "seed_status"},
		{CmdEnqueueEvent, "enqueue_event"},
		{CmdStatus, "status"},
		{CmdList, "list"},
		{CmdStop, "stop"},
		{CmdLanQuery, "lan_query"},
		{CmdLanSeen, "lan_seen"},
		{CmdDownload, "download"},
		{CmdCancelJob, "cancel_job"},
		{CmdJobStatus, "job_status"},
		{CmdStats, "stats"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.cmd) != tt.want {
				t.Errorf("Cmd = %q, want %q", tt.cmd, tt.want)
			}
		})
	}
}

func TestSeedStatusDataJSON(t *testing.T) {
	dataIn := SeedStatusData{
		JobID:     "seed-1",
		ModelID:   "m:7b:instruct:q4_k_m",
		Infohash:  "abc123abc123abc123abc123abc123abc123abc1",
		MagnetURI: "magnet:?xt=urn:btih:abc123abc123abc123abc123abc123abc123abc1",
		Done:      true,
	}

	data, err := json.Marshal(dataIn)
	if err != nil {
		t.Fatalf("json.Marshal(SeedStatusData) failed: %v", err)
	}

	var got SeedStatusData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(SeedStatusData) failed: %v", err)
	}
	if got != dataIn {
		t.Errorf("SeedStatusData round-trip mismatch: %+v vs %+v", got, dataIn)
	}
}
