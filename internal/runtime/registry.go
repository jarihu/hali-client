package runtime

import "strings"

type RuntimeRegistry struct {
	runtimes []Runtime
}

func NewRegistry(runtimes ...Runtime) *RuntimeRegistry {
	if len(runtimes) == 0 {
		runtimes = []Runtime{
			OllamaRuntime{},
			LMStudioRuntime{},
		}
	}
	return &RuntimeRegistry{runtimes: runtimes}
}

func (r *RuntimeRegistry) DetectAll() []Runtime {
	out := make([]Runtime, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		if rt.Detect() {
			out = append(out, rt)
		}
	}
	return out
}

func (r *RuntimeRegistry) Active() []Runtime {
	return r.DetectAll()
}

func (r *RuntimeRegistry) Get(name string) (Runtime, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, rt := range r.runtimes {
		if strings.EqualFold(rt.Name(), name) {
			return rt, true
		}
	}
	return nil, false
}

func (r *RuntimeRegistry) All() []Runtime {
	out := make([]Runtime, len(r.runtimes))
	copy(out, r.runtimes)
	return out
}
