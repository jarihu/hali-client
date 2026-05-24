package export

import (
	"errors"
	"sync"
	"testing"

	"hali/internal/runtime"
)

type mockExporter struct {
	name     string
	supports bool
	exportFn func(Model, runtime.Runtime) error
}

func (m mockExporter) Name() string              { return m.name }
func (m mockExporter) Supports(model Model) bool { return m.supports }
func (m mockExporter) Export(model Model, rt runtime.Runtime) error {
	if m.exportFn != nil {
		return m.exportFn(model, rt)
	}
	return nil
}

func TestFindExporter(t *testing.T) {
	e := &ExportEngine{
		exporters: []Exporter{
			mockExporter{name: "ollama"},
			mockExporter{name: "lmstudio"},
		},
	}

	t.Run("exact match", func(t *testing.T) {
		exp, ok := e.findExporter("ollama")
		if !ok {
			t.Fatal("findExporter(ollama) not found")
		}
		if exp.Name() != "ollama" {
			t.Errorf("Name() = %q, want ollama", exp.Name())
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		exp, ok := e.findExporter("OLLAMA")
		if !ok {
			t.Fatal("findExporter(OLLAMA) not found")
		}
		if exp.Name() != "ollama" {
			t.Errorf("Name() = %q, want ollama", exp.Name())
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := e.findExporter("nonexistent")
		if ok {
			t.Error("findExporter(nonexistent) should return false")
		}
	})

	t.Run("first match wins", func(t *testing.T) {
		e2 := &ExportEngine{
			exporters: []Exporter{
				mockExporter{name: "dupe", supports: true},
				mockExporter{name: "dupe", supports: false},
			},
		}
		exp, ok := e2.findExporter("dupe")
		if !ok {
			t.Fatal("first dupe not found")
		}
		if !exp.(mockExporter).supports {
			t.Error("should return first matching exporter")
		}
	})
}

func TestNewEngine(t *testing.T) {
	t.Run("with nil registry", func(t *testing.T) {
		e := NewEngine(nil)
		if e.runtime == nil {
			t.Fatal("runtime registry should not be nil")
		}
		if len(e.exporters) != 0 {
			t.Errorf("exporters = %d, want 0", len(e.exporters))
		}
	})

	t.Run("with custom registry", func(t *testing.T) {
		reg := runtime.NewRegistry()
		e := NewEngine(reg)
		if e.runtime != reg {
			t.Error("should use provided registry")
		}
	})
}

func TestExportInvalidModel(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Export("", nil, false)
	if err == nil {
		t.Error("expected error for invalid model ID")
	}
}

func TestExportUnknownRuntime(t *testing.T) {
	reg := runtime.NewRegistry()
	e := NewEngine(reg)
	_, err := e.Export("mistral:7b:instruct:q4_k_m", []string{"nonexistent-runtime"}, false)
	if err == nil {
		t.Error("expected error for unknown runtime target")
	}
	if err != nil && !errors.Is(err, errors.New("unknown runtime: nonexistent-runtime")) {
		// Just check it's an error about unknown runtime
		if err.Error() == "" {
			t.Error("error should have message")
		}
	}
}

func TestExportAllNoDetected(t *testing.T) {
	reg := runtime.NewRegistry()
	e := NewEngine(reg, mockExporter{name: "ollama", supports: true})
	err := e.ExportAll("mistral:7b:instruct:q4_k_m")
	if err == nil {
		t.Error("expected error because no model is cached")
	}
}

// mockRuntime is a runtime.Runtime that always detects itself.
type mockRuntime struct{ name string }

func (m mockRuntime) Name() string                { return m.name }
func (m mockRuntime) Detect() bool                { return true }
func (m mockRuntime) ModelsPath() (string, error) { return "", nil }

func TestExportUnsupportedFormat(t *testing.T) {
	// When the exporter does not support the model's format, the result must be
	// Skipped=true, not an error. No panic, no partial write.
	reg := runtime.NewRegistry(mockRuntime{name: "ollama"})
	e := NewEngine(reg, mockExporter{name: "ollama", supports: false})
	e.modelResolver = func(id string) (Model, error) {
		return Model{
			ID:       id,
			Metadata: map[string]any{"format": "safetensors"},
		}, nil
	}

	results, err := e.Export("any:1b:base:q4_0", nil, false)
	if err != nil {
		t.Fatalf("Export returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if !results[0].Skipped {
		t.Error("unsupported format should produce Skipped=true result")
	}
	if results[0].Err != nil {
		t.Errorf("unsupported format should not set Err; got: %v", results[0].Err)
	}
}

func TestConcurrentExportsIdempotent(t *testing.T) {
	// 5 goroutines calling Export simultaneously must not race or panic.
	// The exporter is a no-op so no file writes occur — this guards engine state.
	reg := runtime.NewRegistry(mockRuntime{name: "ollama"})
	e := NewEngine(reg, mockExporter{name: "ollama", supports: true, exportFn: func(Model, runtime.Runtime) error {
		return nil
	}})
	e.modelResolver = func(id string) (Model, error) {
		return Model{
			ID:       id,
			GGUFPath: "/tmp/model.gguf",
			Metadata: map[string]any{"format": "gguf"},
		}, nil
	}

	const goroutines = 5
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = e.Export("concurrent:7b:instruct:q4_k_m", nil, false)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Export returned error: %v", i, err)
		}
	}
}
