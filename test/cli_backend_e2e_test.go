//go:build integration

package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"hali/internal/cache"
	"hali/internal/daemon"
)

const (
	defaultBackendDir = `C:\Users\jarit\coding\hali-backend`
	backendListenAddr = "127.0.0.1:39080"
	defaultBackendURL = "http://127.0.0.1:3000"
)

var testHTTPClient = &http.Client{Timeout: 5 * time.Second}

type backendRuntime struct {
	baseURL     string
	logs        bytes.Buffer
	apiCmd      *exec.Cmd
	backendDir  string
	startedDB   bool
	databaseURL string
}

func TestCLIBackendTelemetryE2E(t *testing.T) {
	if daemonProtocolReachable() {
		t.Skip("daemon already running on fixed IPC port; stop it before running this e2e")
	}

	backendDir := strings.TrimSpace(os.Getenv("HALI_E2E_BACKEND_DIR"))
	if backendDir == "" {
		backendDir = defaultBackendDir
	}
	if _, err := os.Stat(backendDir); err != nil {
		t.Skipf("backend repo not found at %s: %v", backendDir, err)
	}

	clientRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve client root: %v", err)
	}

	base := e2eBase(t)
	env := buildIsolatedEnv(base)
	applyEnvToCurrentProcess(t, env)
	if !ipcPortAvailable() {
		t.Skip("IPC port 47432 is already in use by another process; free that port and rerun")
	}

	// Start backend first so we know the real ingest URL before writing daemon config.
	t.Log("[e2e] starting backend runtime")
	rt := startBackendRuntime(t, backendDir)
	t.Cleanup(func() { rt.stop(t) })
	t.Logf("[e2e] backend ready at %s", rt.baseURL)

	if err := writeServiceConfig(filepath.Join(env["HALI_SERVICE_DATA_DIR"], "config.json"), rt.baseURL); err != nil {
		t.Fatalf("write service config: %v", err)
	}

	t.Log("[e2e] building hali binary")
	haliExe := buildHaliBinary(t, clientRoot, base)

	t.Log("[e2e] starting daemon")
	daemonCmd, daemonLogs := startDaemonProcess(t, haliExe, env)
	t.Cleanup(func() {
		stopDaemonProcess(daemonCmd)
	})

	// Pull a real .gguf artifact through the CLI path to exercise download + seed + telemetry.
	// Keep this pinned to a small, real HF GGUF to reduce test runtime.
	repoID := "mradermacher/jina-reranker-v1-tiny-en-GGUF"
	pullEnv := map[string]string{}
	for k, v := range env {
		pullEnv[k] = v
	}

	t.Log("[e2e] pulling a real gguf file from Hugging Face")
	exitCode, out, err := runCommandWithInput(clientRoot, pullEnv, 6*time.Minute, "1\n", haliExe, "pull", repoID)
	if err != nil || exitCode != 0 {
		t.Fatalf("pull failed (exit=%d): %v\noutput:\n%s\ndaemon logs:\n%s", exitCode, err, out, daemonLogs.String())
	}

	meta, err := findMetadataForRepo(env["HALI_MODELS_DIR"], repoID)
	if err != nil {
		t.Fatalf("find metadata for repo %s: %v\npull output:\n%s", repoID, err, out)
	}
	infohash := strings.TrimSpace(meta.Infohash)
	if infohash == "" {
		t.Fatalf("downloaded metadata missing infohash\npull output:\n%s", out)
	}
	t.Log("[e2e] gguf pulled and seeded; polling backend model detail")
	detail := waitForModelDetail(t, rt.baseURL, infohash, 2*time.Minute)
	if got := strings.TrimSpace(asString(detail["model_id"])); got != repoID {
		t.Fatalf("detail model_id = %q, want %q", got, repoID)
	}
	if got := strings.TrimSpace(asString(detail["revision"])); got != strings.TrimSpace(meta.HFRevision) {
		t.Fatalf("detail revision = %q, want %q", got, strings.TrimSpace(meta.HFRevision))
	}
	if got := strings.TrimSpace(asString(detail["magnet"])); got == "" {
		t.Fatal("detail magnet empty")
	}
	if got := strings.TrimSpace(asString(detail["infohash"])); got != infohash {
		t.Fatalf("detail infohash = %q, want %q", got, infohash)
	}
}

func buildIsolatedEnv(base string) map[string]string {
	serviceData := filepath.Join(base, "service-data")
	serviceRun := filepath.Join(base, "service-run")
	serviceLog := filepath.Join(base, "service-log")
	modelsDir := filepath.Join(base, "models")
	programData := filepath.Join(base, "programdata")
	_ = os.MkdirAll(serviceData, 0755)
	_ = os.MkdirAll(serviceRun, 0755)
	_ = os.MkdirAll(serviceLog, 0755)
	_ = os.MkdirAll(modelsDir, 0755)
	_ = os.MkdirAll(programData, 0755)
	return map[string]string{
		"HALI_SERVICE_DATA_DIR": serviceData,
		"HALI_SERVICE_RUN_DIR":  serviceRun,
		"HALI_SERVICE_LOG_DIR":  serviceLog,
		"HALI_MODELS_DIR":       modelsDir,
		"PROGRAMDATA":           programData,
	}
}

func writeServiceConfig(path, ingestURL string) error {
	cfg := map[string]any{
		"telemetry_enabled":   true,
		"registry_ingest_url": ingestURL,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func startBackendRuntime(t *testing.T, backendDir string) *backendRuntime {
	t.Helper()
	rt := &backendRuntime{baseURL: "http://" + backendListenAddr, backendDir: backendDir}

	externalBaseURL := strings.TrimSpace(os.Getenv("HALI_E2E_BACKEND_BASE_URL"))
	if externalBaseURL == "" {
		externalBaseURL = defaultBackendURL
	}
	if waitForHealth(externalBaseURL+"/health", 3*time.Second) == nil {
		rt.baseURL = externalBaseURL
		return rt
	}

	databaseURL := strings.TrimSpace(os.Getenv("HALI_E2E_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if databaseURL == "" {
		if err := ensureDockerCompose(); err != nil {
			t.Skipf("no DATABASE_URL and docker compose unavailable: %v", err)
		}
		if _, out, err := runCommand(backendDir, nil, 2*time.Minute, "docker", "compose", "up", "-d", "db"); err != nil {
			t.Fatalf("start backend db failed: %v\n%s", err, out)
		}
		rt.startedDB = true
		databaseURL = "postgres://hali:hali@localhost:5432/hali?sslmode=disable"
	}
	rt.databaseURL = databaseURL

	env := map[string]string{
		"DATABASE_URL":         databaseURL,
		"LISTEN_ADDR":          backendListenAddr,
		"VISIBILITY_THRESHOLD": "0",
		"LOG_LEVEL":            "debug",
	}
	apiCmd := exec.Command("go", "run", "./cmd/api")
	apiCmd.Dir = backendDir
	apiCmd.Env = mergedEnv(env)
	apiCmd.Stdout = &rt.logs
	apiCmd.Stderr = &rt.logs
	if err := apiCmd.Start(); err != nil {
		t.Fatalf("start backend api: %v", err)
	}
	rt.apiCmd = apiCmd

	if err := waitForHealth(rt.baseURL+"/health", 90*time.Second); err != nil {
		rt.stop(t)
		logs := rt.logs.String()
		if strings.Contains(strings.ToLower(logs), "address already in use") {
			t.Fatalf("backend API failed to bind %s (already in use). If backend is already running on :3000, set HALI_E2E_BACKEND_BASE_URL=%s\nlogs:\n%s", backendListenAddr, defaultBackendURL, logs)
		}
		t.Fatalf("backend health failed: %v\nlogs:\n%s", err, logs)
	}
	return rt
}

func (rt *backendRuntime) stop(t *testing.T) {
	t.Helper()
	if rt.apiCmd != nil && rt.apiCmd.Process != nil {
		if runtime.GOOS == "windows" {
			_ = rt.apiCmd.Process.Kill()
		} else {
			_ = rt.apiCmd.Process.Signal(os.Interrupt)
		}
		done := make(chan struct{})
		go func() {
			_ = rt.apiCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = rt.apiCmd.Process.Kill()
			<-done
		}
	}
	if rt.startedDB {
		_, _, _ = runCommand(rt.backendDir, nil, 60*time.Second, "docker", "compose", "down", "--remove-orphans")
	}
}

func ensureDockerCompose() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose version failed: %w (%s)", err, strings.TrimSpace(out.String()))
	}
	return nil
}

func buildHaliBinary(t *testing.T, clientRoot, base string) string {
	t.Helper()
	exe := filepath.Join(base, "hali-e2e.exe")
	if runtime.GOOS != "windows" {
		exe = filepath.Join(base, "hali-e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-tags", "oss", "-o", exe, ".")
	cmd.Dir = clientRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("build hali binary: %v\n%s", err, out.String())
	}
	return exe
}

func startDaemonProcess(t *testing.T, haliExe string, env map[string]string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	cmd := exec.Command(haliExe, "daemon", "_run")
	cmd.Env = mergedEnv(env)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon _run: %v", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if daemonProtocolReachable() {
			return cmd, &logs
		}
		select {
		case err := <-exited:
			logText := logs.String()
			if strings.Contains(strings.ToLower(logText), "only one usage") || strings.Contains(strings.ToLower(logText), "address already in use") {
				t.Skipf("IPC port 47432 occupied; stop other daemon first\nlogs:\n%s", logText)
			}
			t.Fatalf("daemon exited: %v\nlogs:\n%s", err, logText)
		default:
		}
		time.Sleep(200 * time.Millisecond)
	}
	stopDaemonProcess(cmd)
	t.Fatalf("daemon not reachable after 15s\nlogs:\n%s", logs.String())
	return nil, nil
}

func daemonProtocolReachable() bool {
	resp, err := daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil {
		return false
	}
	return resp.OK
}

func applyEnvToCurrentProcess(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func stopDaemonProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func runCommand(dir string, env map[string]string, timeout time.Duration, name string, args ...string) (int, string, error) {
	return runCommandWithInput(dir, env, timeout, "", name, args...)
}

func runCommandWithInput(dir string, env map[string]string, timeout time.Duration, stdin string, name string, args ...string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = mergedEnv(env)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return exitCode, out.String(), fmt.Errorf("command timed out after %s", timeout)
	}
	return exitCode, out.String(), err
}

func findMetadataForRepo(modelsRoot, repoID string) (*cache.Metadata, error) {
	store := cache.NewStoreAt(modelsRoot)
	entries, err := store.List()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if strings.EqualFold(strings.TrimSpace(entries[i].Meta.HFRepo), strings.TrimSpace(repoID)) {
			meta := entries[i].Meta
			return &meta, nil
		}
	}
	return nil, fmt.Errorf("metadata for repo %s not found", repoID)
}

func mergedEnv(overrides map[string]string) []string {
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

func waitForHealth(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := testHTTPClient.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("health endpoint %s not ready within %s", url, timeout)
}

func waitForSearchResult(t *testing.T, baseURL, modelID, infohash string, timeout time.Duration) map[string]any {
	t.Helper()
	query := modelID
	if idx := strings.Index(modelID, "/"); idx > 0 {
		query = modelID[:idx]
	}
	endpoint := fmt.Sprintf("%s/search?q=%s&limit=20", baseURL, url.QueryEscape(query))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := testHTTPClient.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("search read body failed: %v", readErr)
			}
			if resp.StatusCode >= http.StatusBadRequest {
				t.Fatalf("search endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			if resp.StatusCode == http.StatusOK {
				var rows []map[string]any
				if err := json.Unmarshal(body, &rows); err != nil {
					t.Fatalf("search decode failed: %v; body=%s", err, strings.TrimSpace(string(body)))
				}
				for _, row := range rows {
					if strings.EqualFold(strings.TrimSpace(asString(row["infohash"])), infohash) {
						return row
					}
				}
			}
		}
		time.Sleep(750 * time.Millisecond)
	}
	t.Fatalf("search never returned infohash %s", infohash)
	return nil
}

func waitForModelDetail(t *testing.T, baseURL, infohash string, timeout time.Duration) map[string]any {
	t.Helper()
	endpoint := fmt.Sprintf("%s/model/%s", baseURL, infohash)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := testHTTPClient.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("model detail read body failed: %v", readErr)
			}
			if resp.StatusCode >= http.StatusBadRequest {
				t.Fatalf("model detail endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			if resp.StatusCode == http.StatusOK {
				var detail map[string]any
				if err := json.Unmarshal(body, &detail); err != nil {
					t.Fatalf("model detail decode failed: %v; body=%s", err, strings.TrimSpace(string(body)))
				}
				return detail
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("model detail never available for infohash %s", infohash)
	return nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func ipcPortAvailable() bool {
	ln, err := net.Listen("tcp", "127.0.0.1:47432")
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
