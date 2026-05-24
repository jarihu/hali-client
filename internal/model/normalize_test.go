package model

import (
	"testing"
)

func TestNormalizeOnePart(t *testing.T) {
	id, err := Normalize("mistral")
	if err != nil {
		t.Fatalf("Normalize(\"mistral\") error: %v", err)
	}
	if id.Base != "mistral" {
		t.Errorf("Base = %q, want %q", id.Base, "mistral")
	}
	if id.Size != "" || id.Variant != "" || id.Quant != "" {
		t.Errorf("partial ID should only set Base, got %+v", id)
	}
}

func TestNormalizeTwoPart(t *testing.T) {
	id, err := Normalize("mistral:7b")
	if err != nil {
		t.Fatalf("Normalize(\"mistral:7b\") error: %v", err)
	}
	if id.Base != "mistral" {
		t.Errorf("Base = %q, want %q", id.Base, "mistral")
	}
	if id.Size != "7b" {
		t.Errorf("Size = %q, want %q", id.Size, "7b")
	}
	if id.Variant != "" || id.Quant != "" {
		t.Errorf("two-part ID should only set Base+Size, got %+v", id)
	}
}

func TestNormalizeThreePart(t *testing.T) {
	id, err := Normalize("mistral:7b:instruct")
	if err != nil {
		t.Fatalf("Normalize(\"mistral:7b:instruct\") error: %v", err)
	}
	if id.Base != "mistral" {
		t.Errorf("Base = %q, want %q", id.Base, "mistral")
	}
	if id.Size != "7b" {
		t.Errorf("Size = %q, want %q", id.Size, "7b")
	}
	if id.Variant != "instruct" {
		t.Errorf("Variant = %q, want %q", id.Variant, "instruct")
	}
	if id.Quant != "" {
		t.Errorf("three-part ID should not set Quant, got %q", id.Quant)
	}
}

func TestNormalizeFourPart(t *testing.T) {
	id, err := Normalize("mistral:7b:instruct:q4_k_m")
	if err != nil {
		t.Fatalf("Normalize four-part error: %v", err)
	}
	if id.Base != "mistral" || id.Size != "7b" || id.Variant != "instruct" || id.Quant != "q4_k_m" {
		t.Errorf("four-part = %+v, want mistral:7b:instruct:q4_k_m", id)
	}
}

func TestNormalizeInvalid(t *testing.T) {
	tests := []string{
		"",
		":::::",
		"../../etc/passwd",
		"foo:../bar:baz:qux",
		"foo:bar:baz:qux:extra",
		":",
		"::",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := Normalize(input)
			if err == nil {
				t.Errorf("Normalize(%q) should have returned an error", input)
			}
		})
	}
}

func TestNormalizeLowercases(t *testing.T) {
	tests := []struct {
		input    string
		wantBase string
	}{
		{"MISTRAL", "mistral"},
		{"MISTRAL:7B", "mistral"},
		{"MISTRAL:7B:INSTRUCT", "mistral"},
		{"MISTRAL:7B:INSTRUCT:Q4_K_M", "mistral"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			id, err := Normalize(tt.input)
			if err != nil {
				t.Fatalf("Normalize(%q) error: %v", tt.input, err)
			}
			if id.Base != tt.wantBase {
				t.Errorf("Base = %q, want %q", id.Base, tt.wantBase)
			}
		})
	}
}

func TestNormalizeHFRepoFormat(t *testing.T) {
	tests := []struct {
		input      string
		wantBase   string
		wantFormat string
	}{
		{
			input:      "TheBloke/Mistral-7B-Instruct-v0.2-GGUF",
			wantBase:   "mistral",
			wantFormat: "gguf",
		},
		{
			input:      "bartowski/Llama-3-8B-Instruct-GGUF",
			wantBase:   "llama",
			wantFormat: "gguf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			id, err := Normalize(tt.input)
			if err != nil {
				t.Fatalf("Normalize(%q) error: %v", tt.input, err)
			}
			if id.Base != tt.wantBase {
				t.Errorf("Base = %q, want %q", id.Base, tt.wantBase)
			}
			if id.Format != tt.wantFormat {
				t.Errorf("Format = %q, want %q", id.Format, tt.wantFormat)
			}
		})
	}
}

func TestNormalizePathTraversalInHFRepo(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"org/../secret/model",
		"foo/../../etc/passwd",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := Normalize(c)
			if err == nil {
				t.Errorf("Normalize(%q) should have rejected path traversal", c)
			}
		})
	}
}
