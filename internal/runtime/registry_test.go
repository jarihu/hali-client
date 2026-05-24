package runtime

import (
	"testing"
)

type mockRuntime struct {
	name      string
	detect    bool
	modelPath string
	pathErr   error
}

func (m mockRuntime) Name() string                { return m.name }
func (m mockRuntime) Detect() bool                { return m.detect }
func (m mockRuntime) ModelsPath() (string, error) { return m.modelPath, m.pathErr }

func TestNewRegistryDefaults(t *testing.T) {
	r := NewRegistry()
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("default registry has %d runtimes, want 2", len(all))
	}
	if all[0].Name() != "ollama" {
		t.Errorf("first default runtime = %q, want ollama", all[0].Name())
	}
	if all[1].Name() != "lmstudio" {
		t.Errorf("second default runtime = %q, want lmstudio", all[1].Name())
	}
}

func TestNewRegistryCustom(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "foo", detect: true},
		mockRuntime{name: "bar", detect: false},
	)
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("registry has %d runtimes, want 2", len(all))
	}
	if all[0].Name() != "foo" {
		t.Errorf("all[0].Name() = %q, want foo", all[0].Name())
	}
	if all[1].Name() != "bar" {
		t.Errorf("all[1].Name() = %q, want bar", all[1].Name())
	}
}

func TestNewRegistryEmpty(t *testing.T) {
	r := NewRegistry() // no args → defaults
	all := r.All()
	if len(all) == 0 {
		t.Error("empty args should still produce default runtimes, not nil")
	}
}

func TestDetectAllFilters(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "a", detect: true},
		mockRuntime{name: "b", detect: false},
		mockRuntime{name: "c", detect: true},
		mockRuntime{name: "d", detect: false},
	)
	detected := r.DetectAll()
	if len(detected) != 2 {
		t.Fatalf("DetectAll returned %d runtimes, want 2", len(detected))
	}
	if detected[0].Name() != "a" {
		t.Errorf("first = %q, want a", detected[0].Name())
	}
	if detected[1].Name() != "c" {
		t.Errorf("second = %q, want c", detected[1].Name())
	}
}

func TestDetectAllNone(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "a", detect: false},
		mockRuntime{name: "b", detect: false},
	)
	detected := r.DetectAll()
	if len(detected) != 0 {
		t.Errorf("DetectAll returned %d runtimes, want 0", len(detected))
	}
}

func TestDetectAllEmptyRegistry(t *testing.T) {
	r := &RuntimeRegistry{runtimes: nil}
	detected := r.DetectAll()
	if detected == nil || len(detected) != 0 {
		t.Errorf("DetectAll on empty registry should return empty slice, got %v", detected)
	}
}

func TestActiveDelegatesToDetectAll(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "a", detect: true},
		mockRuntime{name: "b", detect: false},
	)
	active := r.Active()
	detected := r.DetectAll()
	if len(active) != len(detected) {
		t.Fatalf("Active returned %d, DetectAll returned %d", len(active), len(detected))
	}
	for i := range active {
		if active[i].Name() != detected[i].Name() {
			t.Errorf("Active[%d] = %q, want %q", i, active[i].Name(), detected[i].Name())
		}
	}
}

func TestGetExactMatch(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "ollama", detect: true},
		mockRuntime{name: "lmstudio", detect: false},
	)
	rt, ok := r.Get("ollama")
	if !ok {
		t.Fatal("Get(ollama) not found")
	}
	if rt.Name() != "ollama" {
		t.Errorf("Name() = %q, want ollama", rt.Name())
	}
}

func TestGetCaseInsensitive(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "ollama", detect: true},
	)
	tests := []string{"Ollama", "OLLAMA", "oLlAmA", "ollama"}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			rt, ok := r.Get(name)
			if !ok {
				t.Fatalf("Get(%q) not found", name)
			}
			if rt.Name() != "ollama" {
				t.Errorf("Name() = %q, want ollama", rt.Name())
			}
		})
	}
}

func TestGetWhitespaceTrimming(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "ollama", detect: true},
	)
	tests := []string{" ollama", "ollama ", "  ollama  ", "\tollama\n"}
	for _, name := range tests {
		rt, ok := r.Get(name)
		if !ok {
			t.Errorf("Get(%q) not found", name)
			continue
		}
		if rt.Name() != "ollama" {
			t.Errorf("Name() = %q, want ollama", rt.Name())
		}
	}
}

func TestGetNotFound(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "ollama", detect: true},
	)
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}

func TestGetEmptyName(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "ollama", detect: true},
	)
	_, ok := r.Get("")
	if ok {
		t.Error("Get(\"\") should return false")
	}
}

func TestAllDefensiveCopy(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "a", detect: true},
		mockRuntime{name: "b", detect: false},
	)
	all1 := r.All()
	all2 := r.All()
	if &all1[0] == &all2[0] {
		t.Error("All() should return a new slice each call")
	}

	all1[0] = mockRuntime{name: "modified"}
	all3 := r.All()
	if all3[0].Name() != "a" {
		t.Errorf("modifying All() result mutated internal state: got %q, want a", all3[0].Name())
	}
}

func TestRegistryPreservesOrder(t *testing.T) {
	r := NewRegistry(
		mockRuntime{name: "z", detect: true},
		mockRuntime{name: "y", detect: true},
		mockRuntime{name: "x", detect: true},
	)
	all := r.All()
	if all[0].Name() != "z" || all[1].Name() != "y" || all[2].Name() != "x" {
		t.Error("All() should preserve insertion order")
	}

	detected := r.DetectAll()
	if detected[0].Name() != "z" || detected[1].Name() != "y" || detected[2].Name() != "x" {
		t.Error("DetectAll() should preserve order of detected runtimes")
	}
}
