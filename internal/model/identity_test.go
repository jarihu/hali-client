package model

import (
	"path/filepath"
	"testing"
)

func TestString(t *testing.T) {
	tests := []struct {
		id     ModelID
		expect string
	}{
		{ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}, "mistral:7b:instruct:q4_k_m"},
		{ModelID{Base: "llama", Size: "70b", Variant: "chat", Quant: "fp16"}, "llama:70b:chat:fp16"},
		{ModelID{Base: "phi", Size: "3_mini", Variant: "instruct", Quant: "q4_0"}, "phi:3_mini:instruct:q4_0"},
	}
	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			got := tt.id.String()
			if got != tt.expect {
				t.Errorf("String() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestIsZero(t *testing.T) {
	tests := []struct {
		id   ModelID
		zero bool
	}{
		{ModelID{}, true},
		{ModelID{Base: "mistral"}, false},
		{ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			if got := tt.id.IsZero(); got != tt.zero {
				t.Errorf("IsZero() = %v, want %v", got, tt.zero)
			}
		})
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		id    ModelID
		valid bool
	}{
		{ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}, true},
		{ModelID{Base: "..", Size: "7b", Variant: "instruct", Quant: "q4_k_m"}, false},
		{ModelID{Base: "mistral", Size: "../7b", Variant: "instruct", Quant: "q4_k_m"}, false},
		{ModelID{Base: "mistral", Size: "7b", Variant: "instruct/evil", Quant: "q4_k_m"}, false},
		{ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4\\traversal"}, false},
		{ModelID{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			if got := tt.id.Valid(); got != tt.valid {
				t.Errorf("Valid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	tests := []string{
		"",
		"foo",
		"foo:bar",
		"foo:bar:baz",
		"foo:bar:baz:qux:extra",
		"foo/bar:baz:qux:quux",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := Parse(input)
			if err == nil {
				t.Errorf("Parse(%q) should have rejected input", input)
			}
		})
	}
}

func TestFromHF(t *testing.T) {
	tests := []struct {
		name     string
		repoID   string
		filename string
		expect   ModelID
	}{
		{
			name:     "standard instruct",
			repoID:   "mistralai/Mistral-7B-Instruct-v0.3-GGUF",
			filename: "mistral-7b-instruct-v0.3.Q4_K_M.gguf",
			expect:   ModelID{Base: "mistral", Size: "7b", Variant: "instruct", Quant: "q4_k_m", Format: "gguf"},
		},
		{
			name:     "chat model",
			repoID:   "TheBloke/Llama-2-70B-Chat-GGUF",
			filename: "llama-2-70b-chat.Q5_K_M.gguf",
			expect:   ModelID{Base: "llama", Size: "70b", Variant: "chat", Quant: "q5_k_m", Format: "gguf"},
		},
		{
			name:     "code model",
			repoID:   "TheBloke/CodeLlama-34B-Instruct-GGUF",
			filename: "codellama-34b-instruct.Q4_0.gguf",
			expect:   ModelID{Base: "codellama", Size: "34b", Variant: "instruct", Quant: "q4_0", Format: "gguf"},
		},
		{
			name:     "fp16 quant",
			repoID:   "TheBloke/Mixtral-8x7B-Instruct-v0.1-GGUF",
			filename: "mixtral-8x7b-instruct-v0.1.F16.gguf",
			expect:   ModelID{Base: "mixtral", Size: "7b", Variant: "instruct", Quant: "f16", Format: "gguf"},
		},
		{
			name:     "bf16 quant",
			repoID:   "TheBloke/Mistral-7B-v0.1-GGUF",
			filename: "mistral-7b-v0.1.BF16.gguf",
			expect:   ModelID{Base: "mistral", Size: "7b", Variant: "base", Quant: "bf16", Format: "gguf"},
		},
		{
			name:     "size in repo name not filename",
			repoID:   "bartowski/phi-4-mini-GGUF",
			filename: "phi-4-mini-Q4_K_M.gguf",
			expect:   ModelID{},
		},
		{
			name:     "it variant suffix",
			repoID:   "unsloth/DeepSeek-R1-Distill-Qwen-1.5B-GGUF",
			filename: "deepseek-r1-distill-qwen-1.5b-q4_k_m.gguf",
			expect:   ModelID{Base: "deepseek", Size: "1.5b", Variant: "base", Quant: "q4_k_m", Format: "gguf"},
		},
		{
			name:     "sft variant",
			repoID:   "TheBloke/Tulu-3-405B-SFT-GGUF",
			filename: "tulu-3-405b-sft-q3_k_m.gguf",
			expect:   ModelID{Base: "tulu", Size: "405b", Variant: "sft", Quant: "q3_k_m", Format: "gguf"},
		},
		{
			name:     "zero — empty filename",
			repoID:   "foo/bar",
			filename: "",
			expect:   ModelID{},
		},
		{
			name:     "zero — missing quant",
			repoID:   "foo/bar-7b",
			filename: "bar-7b.gguf",
			expect:   ModelID{},
		},
		{
			name:     "zero — missing size",
			repoID:   "foo/bar",
			filename: "bar-q4_k_m.gguf",
			expect:   ModelID{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromHF(tt.repoID, tt.filename)
			if got != tt.expect {
				t.Errorf("FromHF(%q, %q) = %+v, want %+v", tt.repoID, tt.filename, got, tt.expect)
			}
		})
	}
}

func TestParseRejectsPathTraversal(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"../../../foo:bar:baz:qux"},
		{"foo:../bar:baz:qux"},
		{"foo:bar:..:qux"},
		{"foo:bar:baz:.."},
		{"foo:bar:baz:qux/extra"},
		{"foo\\windows:bar:baz:qux"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Errorf("Parse(%q) should have rejected path traversal", tt.input)
			}
		})
	}
}

func TestParseAcceptsValid(t *testing.T) {
	tests := []string{
		"mistral:7b:instruct:q4_k_m",
		"llama:70b:chat:fp16",
		"phi:3_mini:instruct:q4_0",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			id, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpectedly rejected: %v", input, err)
			}
			if !id.Valid() {
				t.Errorf("Parse(%q) produced invalid ID", input)
			}
		})
	}
}

func TestIsSafePathComponent(t *testing.T) {
	tests := []struct {
		s    string
		safe bool
	}{
		{"", false},
		{"..", false},
		{"foo/bar", false},
		{`foo\bar`, false},
		{"normal", true},
		{"q4_k_m", true},
		{"7b", true},
		{"instruct", true},
		{"mistral", true},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := isSafePathComponent(tt.s)
			if got != tt.safe {
				t.Errorf("isSafePathComponent(%q) = %v, want %v", tt.s, got, tt.safe)
			}
		})
	}
}

func TestStorePathNoEscape(t *testing.T) {
	root := filepath.Join("testdata", "root")

	safeCases := []string{
		"mistral:7b:instruct:q4_k_m",
		"llama:70b:chat:fp16",
	}
	for _, c := range safeCases {
		id, err := Parse(c)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", c, err)
		}
		sp := id.StorePath()
		full := filepath.Join(root, sp)
		// Should not contain "../" traversal
		if filepath.Base(full) == ".." {
			t.Errorf("StorePath(%q) escaped root: %s", c, full)
		}
	}
}
