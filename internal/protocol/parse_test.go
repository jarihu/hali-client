package protocol

import (
	"strings"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		raw       string
		namespace string
		name      string
		version   string
		file      string
		all       bool
		repoID    string
		revision  string
	}{
		{
			raw:       "hali://model/Qwen/Qwen3-32B",
			namespace: "Qwen", name: "Qwen3-32B", version: "",
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
		{
			raw:       "hali://model/Qwen/Qwen3-32B?version=latest",
			namespace: "Qwen", name: "Qwen3-32B", version: "latest",
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
		{
			raw:       "hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?file=Qwen3.6-35B-A3B-UD-Q4_K_M.gguf",
			namespace: "unsloth", name: "Qwen3.6-35B-A3B-MTP-GGUF", version: "",
			file:   "Qwen3.6-35B-A3B-UD-Q4_K_M.gguf",
			repoID: "unsloth/Qwen3.6-35B-A3B-MTP-GGUF", revision: "main",
		},
		{
			raw:       "hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?version=latest&file=BF16%2FQwen3.6-35B-A3B-BF16-00001-of-00002.gguf",
			namespace: "unsloth", name: "Qwen3.6-35B-A3B-MTP-GGUF", version: "latest",
			file:   "BF16/Qwen3.6-35B-A3B-BF16-00001-of-00002.gguf",
			repoID: "unsloth/Qwen3.6-35B-A3B-MTP-GGUF", revision: "main",
		},
		{
			raw:       "hali://model/TheBloke/Mistral-7B-Instruct-v0.2-GGUF?version=abc1234567890123456789012345678901234567",
			namespace: "TheBloke", name: "Mistral-7B-Instruct-v0.2-GGUF",
			version:  "abc1234567890123456789012345678901234567",
			repoID:   "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
			revision: "abc1234567890123456789012345678901234567",
		},
		{
			raw:       "hali://model/meta-llama/Meta-Llama-3-8B?version=v1.0",
			namespace: "meta-llama", name: "Meta-Llama-3-8B", version: "v1.0",
			repoID: "meta-llama/Meta-Llama-3-8B", revision: "v1.0",
		},
		{
			// Scheme case-insensitive
			raw:       "HALI://model/Qwen/Qwen3-32B",
			namespace: "Qwen", name: "Qwen3-32B", version: "",
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
		{
			// Host case-insensitive
			raw:       "hali://MODEL/Qwen/Qwen3-32B",
			namespace: "Qwen", name: "Qwen3-32B", version: "",
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
		{
			raw:       "hali://model/Qwen/Qwen3-32B?all=1",
			namespace: "Qwen", name: "Qwen3-32B", version: "", all: true,
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
		{
			// Unknown query params are accepted
			raw:       "hali://model/Qwen/Qwen3-32B?version=latest&unknown=ignored",
			namespace: "Qwen", name: "Qwen3-32B", version: "latest",
			repoID: "Qwen/Qwen3-32B", revision: "main",
		},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			h, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if h.Namespace != tc.namespace {
				t.Errorf("namespace: got %q, want %q", h.Namespace, tc.namespace)
			}
			if h.Name != tc.name {
				t.Errorf("name: got %q, want %q", h.Name, tc.name)
			}
			if h.Version != tc.version {
				t.Errorf("version: got %q, want %q", h.Version, tc.version)
			}
			if h.File != tc.file {
				t.Errorf("file: got %q, want %q", h.File, tc.file)
			}
			if h.All != tc.all {
				t.Errorf("all: got %t, want %t", h.All, tc.all)
			}
			if h.RepositoryID() != tc.repoID {
				t.Errorf("RepositoryID: got %q, want %q", h.RepositoryID(), tc.repoID)
			}
			if h.Revision() != tc.revision {
				t.Errorf("Revision: got %q, want %q", h.Revision(), tc.revision)
			}
		})
	}
}

func TestParse_InvalidAll(t *testing.T) {
	cases := []string{
		"hali://model/Qwen/Qwen3-32B?all=true",
		"hali://model/Qwen/Qwen3-32B?all=0",
		"hali://model/Qwen/Qwen3-32B?all=yes",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("expected error for invalid all parameter in %q", raw)
			}
		})
	}
}

func TestParse_FileAndAllBothPresent(t *testing.T) {
	h, err := Parse("hali://model/Qwen/Qwen3-32B?file=q4.gguf&all=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.File != "q4.gguf" {
		t.Fatalf("file = %q, want q4.gguf", h.File)
	}
	if !h.All {
		t.Fatal("expected all=true when all=1 is present")
	}
}

func TestParse_InvalidScheme(t *testing.T) {
	cases := []string{
		"http://model/Qwen/Qwen3-32B",
		"https://model/Qwen/Qwen3-32B",
		"hali2://model/Qwen/Qwen3-32B",
		"ftp://model/Qwen/Qwen3-32B",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatal("expected error for invalid scheme")
			}
		})
	}
}

func TestParse_InvalidAction(t *testing.T) {
	cases := []string{
		"hali://exec/cmd",
		"hali://run/script",
		"hali://download/foo/bar",
		"hali://install/foo/bar",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatal("expected error for invalid action")
			}
		})
	}
}

func TestParse_PathTraversal(t *testing.T) {
	cases := []string{
		"hali://model/foo/%2e%2e/bar",
		"hali://model/../evil",
		"hali://model/foo/%2F%2e%2e%2Fbar/baz",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatal("expected error for path traversal")
			}
		})
	}
}

func TestParse_EmptySegments(t *testing.T) {
	cases := []string{
		"hali://model//bar",
		"hali://model/foo/",
		"hali://model/",
		"hali://model",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("expected error for empty segment in %q", raw)
			}
		})
	}
}

func TestParse_BadCharset(t *testing.T) {
	cases := []string{
		"hali://model/foo bar/baz",
		"hali://model/foo;rm/bar",
		"hali://model/foo|bar/baz",
		"hali://model/foo&bar/baz",
		"hali://model/foo`bar/baz",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("expected error for bad charset in %q", raw)
			}
		})
	}
}

func TestParse_TooLong(t *testing.T) {
	raw := "hali://model/Qwen/" + strings.Repeat("A", 2000)
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected error for URL > 2048 chars")
	}
}

func TestParse_InvalidVersion(t *testing.T) {
	cases := []string{
		"hali://model/Qwen/Qwen3-32B?version=foo bar",
		"hali://model/Qwen/Qwen3-32B?version=foo;rm",
		"hali://model/Qwen/Qwen3-32B?version=" + strings.Repeat("a", 81),
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("expected error for invalid version in %q", raw)
			}
		})
	}
}

func TestParse_InvalidFileParameter(t *testing.T) {
	cases := []string{
		"hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?file=..%2Fsecret.gguf",
		"hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?file=%2Fabsolute.gguf",
		"hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?file=bad\\path.gguf",
		"hali://model/unsloth/Qwen3.6-35B-A3B-MTP-GGUF?file=folder%2F.%2Ffile.gguf",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("expected error for invalid file parameter in %q", raw)
			}
		})
	}
}
