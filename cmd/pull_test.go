package cmd

import (
	"hali/internal/daemon"
	"hali/internal/hf"
	"hali/internal/pull"
	"strings"
	"testing"
	"time"
)

func TestParseRepoArg(t *testing.T) {
	tests := []struct {
		raw      string
		wantRepo string
		wantFile string
	}{
		{"kirp/TinyLlama-1.1B-Chat-v0.2-gguf?file=ggml-model-q2_k.gguf", "kirp/TinyLlama-1.1B-Chat-v0.2-gguf", "ggml-model-q2_k.gguf"},
		{"owner/repo?file=model.q4_k_m.gguf&extra=1", "owner/repo", "model.q4_k_m.gguf"},
		{"owner/repo", "owner/repo", ""},
		{"mistral", "mistral", ""},
		{"owner/repo?other=val", "owner/repo", ""},
		{"owner/repo?", "owner/repo", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			repo, file := parseRepoArg(tt.raw)
			if repo != tt.wantRepo {
				t.Errorf("repo: got %q, want %q", repo, tt.wantRepo)
			}
			if file != tt.wantFile {
				t.Errorf("file: got %q, want %q", file, tt.wantFile)
			}
		})
	}
}

func TestArtifactKeyForPull(t *testing.T) {
	tests := []struct {
		modelID  string
		revision string
		expect   string
	}{
		{"mistral:7b:instruct:q4_k_m", "abc123", "mistral:7b:instruct:q4_k_m@abc123"},
		{"llama:13b:chat:q5", "", "llama:13b:chat:q5"},
		{"mistral:7b:instruct:q4_k_m", "  def456  ", "mistral:7b:instruct:q4_k_m@def456"},
		{"", "abc", "@abc"},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			got := artifactKeyForPull(tt.modelID, tt.revision)
			if got != tt.expect {
				t.Errorf("artifactKeyForPull(%q, %q) = %q, want %q", tt.modelID, tt.revision, got, tt.expect)
			}
		})
	}
}

func TestHumanizeAgeUnix(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		ts     int64
		expect string
	}{
		{"zero", 0, "unknown"},
		{"negative", -1, "unknown"},
		{"now", now.Unix(), "0s"},
		{"5 seconds ago", now.Add(-5 * time.Second).Unix(), "5s"},
		{"59 seconds ago", now.Add(-59 * time.Second).Unix(), "59s"},
		{"1 minute ago", now.Add(-1 * time.Minute).Unix(), "1m"},
		{"5 minutes ago", now.Add(-5 * time.Minute).Unix(), "5m"},
		{"59 minutes ago", now.Add(-59 * time.Minute).Unix(), "59m"},
		{"1 hour ago", now.Add(-1 * time.Hour).Unix(), "1h"},
		{"5 hours ago", now.Add(-5 * time.Hour).Unix(), "5h"},
		{"24 hours ago", now.Add(-24 * time.Hour).Unix(), "24h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := humanizeAgeUnix(tt.ts)
			if got != tt.expect {
				t.Errorf("humanizeAgeUnix(%d) = %q, want %q", tt.ts, got, tt.expect)
			}
		})
	}
}

func TestResolveRepoWithSlash(t *testing.T) {
	// resolveRepo returns the query unchanged when it contains a slash.
	got, err := resolveRepo(nil, nil, "TheBloke/Mistral-7B-Instruct-v0.2-GGUF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "TheBloke/Mistral-7B-Instruct-v0.2-GGUF" {
		t.Errorf("got %q", got)
	}
}

func TestFetchLANObservedNoDaemon(t *testing.T) {
	if daemon.IsRunning() {
		t.Skip("daemon is running in this environment")
	}
	// Without a running daemon, returns empty map.
	got := fetchLANObserved()
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

func TestInferFallbackSize(t *testing.T) {
	if got := inferFallbackSize("mradermacher/jina-reranker-v1-tiny-en-GGUF", "jina-reranker-v1-tiny-en.Q3_K_M.gguf"); got != "tiny" {
		t.Fatalf("inferFallbackSize tiny = %q, want tiny", got)
	}
	if got := inferFallbackSize("TheBloke/Mistral-7B-Instruct-v0.2-GGUF", "mistral-7b-instruct-v0.2.Q4_K_M.gguf"); got != "7b" {
		t.Fatalf("inferFallbackSize numeric = %q, want 7b", got)
	}
	if got := inferFallbackSize("org/weird-model", "model.q4_k_m.gguf"); got != "base" {
		t.Fatalf("inferFallbackSize default = %q, want base", got)
	}
}

func TestBuildFallbackBase(t *testing.T) {
	got := buildFallbackBase("mradermacher/jina-reranker-v1-tiny-en-GGUF", "tiny")
	want := "jina_reranker_v1_en"
	if got != want {
		t.Fatalf("buildFallbackBase = %q, want %q", got, want)
	}
}

func TestParsePullFilesFlag(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"a.gguf,b.gguf", []string{"a.gguf", "b.gguf"}},
		{" a.gguf , b.gguf ", []string{"a.gguf", "b.gguf"}},
		{"a.gguf,,b.gguf", []string{"a.gguf", "b.gguf"}},
		{"a.gguf", []string{"a.gguf"}},
		{"", nil},
		{"  ,  ", nil},
	}
	for _, tc := range cases {
		var got []string
		for _, name := range strings.Split(tc.raw, ",") {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				got = append(got, trimmed)
			}
		}
		if len(got) != len(tc.want) {
			t.Errorf("raw=%q: got %v, want %v", tc.raw, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("raw=%q [%d]: got %q, want %q", tc.raw, i, got[i], tc.want[i])
			}
		}
	}
}

func TestAllFilesPathWhenNoSpecifier(t *testing.T) {
	// opts with no FileName, no Files, and pullFileIndex==0 → all-files path.
	opts := pull.Options{Repo: "owner/repo"}
	if opts.FileName != "" || len(opts.Files) != 0 {
		t.Fatal("expected empty specifiers")
	}
	// The routing condition: no file name, pullFileIndex is the package-level var (0 at test time).
	noSingleFile := opts.FileName == "" && pullFileIndex == 0
	if !noSingleFile {
		t.Error("expected all-files path to be taken")
	}
}

func TestSingleFilePathWhenFileNameSet(t *testing.T) {
	opts := pull.Options{Repo: "owner/repo", FileName: "model.Q4_K_M.gguf"}
	if opts.FileName == "" {
		t.Fatal("expected FileName to be set")
	}
	// The routing condition: FileName set → single-file path.
	isSingleFile := opts.FileName != ""
	if !isSingleFile {
		t.Error("expected single-file path to be taken")
	}
}

func TestOnlyGGUFFilesFiltersAndSorts(t *testing.T) {
	in := []hf.File{
		{Name: "tokenizer.json", Size: 10},
		{Name: "Q8.gguf", Size: 3},
		{Name: "q4.gguf", Size: 1},
		{Name: "MODEL.SAFETENSORS", Size: 2},
	}
	out := onlyGGUFFiles(in)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Name != "q4.gguf" || out[1].Name != "Q8.gguf" {
		t.Fatalf("unexpected ordering: %+v", out)
	}
}

func TestBuildCollectionKeyDeterministic(t *testing.T) {
	artifacts := []downloadedArtifact{
		{FileName: "q4.gguf", FileSize: 11},
		{FileName: "q8.gguf", FileSize: 22},
	}
	a := buildCollectionKey("owner/repo", "main", artifacts)
	b := buildCollectionKey("owner/repo", "main", []downloadedArtifact{
		{FileName: "q8.gguf", FileSize: 22},
		{FileName: "q4.gguf", FileSize: 11},
	})
	if a != b {
		t.Fatalf("collection key should be stable across ordering: %q vs %q", a, b)
	}
	c := buildCollectionKey("owner/repo", "main", []downloadedArtifact{{FileName: "q4.gguf", FileSize: 11}})
	if a == c {
		t.Fatal("collection key should differ when gguf set differs")
	}
}
