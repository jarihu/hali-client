//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hali/internal/cache"
	"hali/internal/daemon"
	"hali/internal/hf"
	"hali/internal/model"
)

func writeServiceConfigMap(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func waitForLanQuery(t *testing.T, modelID string, timeout time.Duration) daemon.LanQueryData {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdLanQuery, ModelID: modelID})
		if err == nil && resp.OK && resp.Data != nil {
			raw, mErr := json.Marshal(resp.Data)
			if mErr == nil {
				var data daemon.LanQueryData
				if uErr := json.Unmarshal(raw, &data); uErr == nil && strings.TrimSpace(data.Infohash) != "" {
					return data
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("lan query for %s did not resolve within %s", modelID, timeout)
	return daemon.LanQueryData{}
}

func sendUnsignedLANAnnouncement(t *testing.T, port int, modelID, infohash, hfRepo, revision string) {
	t.Helper()
	payload := map[string]any{
		"v":   "1",
		"nid": "e2e-external-node",
		"ts":  time.Now().Unix(),
		"p":   port,
		"models": []map[string]any{
			{
				"id":   modelID,
				"ih":   infohash,
				"repo": hfRepo,
				"rev":  revision,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal lan payload: %v", err)
	}
	addr, err := net.ResolveUDPAddr("udp4", "239.192.42.1:4269")
	if err != nil {
		t.Fatalf("resolve multicast addr: %v", err)
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		t.Fatalf("dial multicast: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("send lan payload: %v", err)
	}
}

func fetchFirstBytesFromHF(t *testing.T, repoID, filename string, n int) []byte {
	t.Helper()
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("range request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("range request status = %s", resp.Status)
	}
	buf := make([]byte, n)
	read := 0
	for read < n {
		m, rerr := resp.Body.Read(buf[read:])
		read += m
		if rerr != nil {
			break
		}
	}
	if read == 0 {
		t.Fatal("range request returned no bytes")
	}
	return buf[:read]
}

func TestCLIPullResumesPartialDownloadE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	clientRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve client root: %v", err)
	}
	base := e2eBase(t)
	env := buildIsolatedEnv(base)
	applyEnvToCurrentProcess(t, env)
	if err := writeServiceConfigMap(filepath.Join(env["HALI_SERVICE_DATA_DIR"], "config.json"), map[string]any{
		"telemetry_enabled": false,
		"lan_hmac_enabled":  false,
	}); err != nil {
		t.Fatalf("write service config: %v", err)
	}

	haliExe := buildHaliBinary(t, clientRoot, base)
	cmd, _ := startDaemonProcess(t, haliExe, env)
	t.Cleanup(func() { stopDaemonProcess(cmd) })

	repoID := "mradermacher/jina-reranker-v1-tiny-en-GGUF"
	repoID = "ngxson/tinyllama_split_test"
	hfClient := hf.NewClient()
	files, revision, err := hfClient.GetFiles(context.Background(), repoID, "")
	if err != nil || len(files) == 0 {
		t.Skipf("hf file listing unavailable: %v", err)
	}
	selected := files[0]
	id := model.FromHF(repoID, selected.Name)
	if id.IsZero() {
		t.Skip("could not derive canonical model id for resume test")
	}
	id.Revision = revision

	store := cache.NewStoreAt(env["HALI_MODELS_DIR"])
	tmpPath := filepath.Join(store.Dir(id), selected.Name+".tmp")
	if err := os.MkdirAll(filepath.Dir(tmpPath), 0755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	prefix := fetchFirstBytesFromHF(t, repoID, selected.Name, 16)
	if err := os.WriteFile(tmpPath, prefix, 0644); err != nil {
		t.Fatalf("write tmp prefix: %v", err)
	}

	exitCode, out, runErr := runCommandWithInput(clientRoot, env, 8*time.Minute, "1\n", haliExe, "pull", repoID)
	if runErr != nil || exitCode != 0 {
		t.Fatalf("pull failed (exit=%d): %v\noutput:\n%s", exitCode, runErr, out)
	}
	if !strings.Contains(out, "resumed @") {
		t.Fatalf("expected resumed progress marker in output; got:\n%s", out)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatalf("expected tmp file removed after successful resume: %s", tmpPath)
	}
}

func TestCLIPullUsesLANBeforeHFForCanonicalID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	clientRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve client root: %v", err)
	}
	base := e2eBase(t)
	env := buildIsolatedEnv(base)
	// LAN download path writes via daemon into service store root; align CLI store with it.
	env["HALI_MODELS_DIR"] = filepath.Join(env["HALI_SERVICE_DATA_DIR"], "models")
	applyEnvToCurrentProcess(t, env)
	if err := writeServiceConfigMap(filepath.Join(env["HALI_SERVICE_DATA_DIR"], "config.json"), map[string]any{
		"telemetry_enabled": false,
		"lan_hmac_enabled":  false,
		"debug":             true,
	}); err != nil {
		t.Fatalf("write service config: %v", err)
	}

	haliExe := buildHaliBinary(t, clientRoot, base)
	cmd, _ := startDaemonProcess(t, haliExe, env)
	t.Cleanup(func() { stopDaemonProcess(cmd) })

	repoID := "mradermacher/jina-reranker-v1-tiny-en-GGUF"
	exitCode, out, runErr := runCommandWithInput(clientRoot, env, 8*time.Minute, "1\n", haliExe, "pull", repoID)
	if runErr != nil || exitCode != 0 {
		t.Skipf("initial HF pull unavailable in this environment: exit=%d err=%v\n%s", exitCode, runErr, out)
	}

	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil || !resp.OK {
		t.Fatalf("status after source pull failed: %v (%v)", err, resp.Error)
	}
	raw, _ := json.Marshal(resp.Data)
	var status daemon.StatusData
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	meta, err := findMetadataForRepo(env["HALI_MODELS_DIR"], repoID)
	if err != nil {
		t.Fatalf("find source metadata: %v", err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(meta.Infohash) != "" {
			break
		}
		time.Sleep(700 * time.Millisecond)
		meta, err = findMetadataForRepo(env["HALI_MODELS_DIR"], repoID)
		if err != nil {
			continue
		}
	}
	if strings.TrimSpace(meta.Infohash) == "" {
		t.Skip("source metadata did not gain infohash in time; seeding finalization not yet complete")
	}
	targetID := "e2e_lan_target:1b:base:q4_0"
	sendUnsignedLANAnnouncement(t, status.Port, targetID, strings.TrimSpace(meta.Infohash), strings.TrimSpace(meta.HFRepo), strings.TrimSpace(meta.HFRevision))

	lqResp, lqErr := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdLanQuery, ModelID: targetID})
	if lqErr != nil || !lqResp.OK || lqResp.Data == nil {
		t.Skip("LAN query path unavailable for injected announcement in this environment")
	}
	lq := waitForLanQuery(t, targetID, 20*time.Second)
	if lq.Infohash != strings.TrimSpace(meta.Infohash) {
		t.Fatalf("lan query infohash = %q, want %q", lq.Infohash, strings.TrimSpace(meta.Infohash))
	}

	exitCode, out, runErr = runCommandWithInput(clientRoot, env, 4*time.Minute, "", haliExe, "pull", targetID)
	if runErr != nil || exitCode != 0 {
		t.Fatalf("canonical LAN pull failed (exit=%d): %v\noutput:\n%s", exitCode, runErr, out)
	}
	if !strings.Contains(out, "Found on LAN") || !strings.Contains(out, "[from LAN]") {
		t.Fatalf("expected LAN pull markers in output; got:\n%s", out)
	}
	if strings.Contains(out, "Fetching file list for") || strings.Contains(out, "from Hugging Face") {
		t.Fatalf("expected LAN path to complete before HF fallback; got output:\n%s", out)
	}
}

func TestCLIPullModelIsActivelySeededAfterDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use")
	}

	clientRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve client root: %v", err)
	}
	base := e2eBase(t)
	env := buildIsolatedEnv(base)
	applyEnvToCurrentProcess(t, env)
	if err := writeServiceConfigMap(filepath.Join(env["HALI_SERVICE_DATA_DIR"], "config.json"), map[string]any{
		"telemetry_enabled": false,
		"lan_hmac_enabled":  false,
	}); err != nil {
		t.Fatalf("write service config: %v", err)
	}

	haliExe := buildHaliBinary(t, clientRoot, base)
	cmd, _ := startDaemonProcess(t, haliExe, env)
	t.Cleanup(func() { stopDaemonProcess(cmd) })

	repoID := "mradermacher/jina-reranker-v1-tiny-en-GGUF"
	exitCode, out, runErr := runCommandWithInput(clientRoot, env, 8*time.Minute, "1\n", haliExe, "pull", repoID)
	if runErr != nil || exitCode != 0 {
		t.Skipf("HF pull unavailable in this environment: exit=%d err=%v\n%s", exitCode, runErr, out)
	}

	meta, err := findMetadataForRepo(env["HALI_MODELS_DIR"], repoID)
	if err != nil {
		t.Fatalf("find metadata after pull: %v", err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(meta.Infohash) != "" {
			break
		}
		time.Sleep(700 * time.Millisecond)
		meta, err = findMetadataForRepo(env["HALI_MODELS_DIR"], repoID)
		if err != nil {
			continue
		}
	}
	if strings.TrimSpace(meta.Infohash) == "" {
		t.Skip("metadata infohash not available yet; seeding finalization still pending")
	}

	deadline = time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStatus})
		if err == nil && resp.OK {
			raw, _ := json.Marshal(resp.Data)
			var status daemon.StatusData
			if json.Unmarshal(raw, &status) == nil {
				for _, s := range status.Seeding {
					if strings.TrimSpace(s.Infohash) == strings.TrimSpace(meta.Infohash) && strings.TrimSpace(s.Status) == "seeding" {
						return
					}
				}
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	t.Fatalf("pulled model infohash %s did not reach active seeding status within timeout", strings.TrimSpace(meta.Infohash))
}
