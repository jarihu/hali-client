package export

import (
	"fmt"
	"hali/internal/runtime"
	"strings"
)

type ExportResult struct {
	Runtime string
	Skipped bool
	Reason  string
	Err     error
}

type ExportEngine struct {
	exporters     []Exporter
	runtime       *runtime.RuntimeRegistry
	modelResolver func(string) (Model, error) // nil → uses ResolveModel; set in tests
}

func NewEngine(runtimeRegistry *runtime.RuntimeRegistry, exporters ...Exporter) *ExportEngine {
	if runtimeRegistry == nil {
		runtimeRegistry = runtime.NewRegistry()
	}
	return &ExportEngine{exporters: exporters, runtime: runtimeRegistry}
}

func (e *ExportEngine) ExportAll(modelID string) error {
	results, err := e.Export(modelID, nil, false)
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

func (e *ExportEngine) resolveModel(id string) (Model, error) {
	if e.modelResolver != nil {
		return e.modelResolver(id)
	}
	return ResolveModel(id)
}

func (e *ExportEngine) Export(modelID string, targets []string, strict bool) ([]ExportResult, error) {
	m, err := e.resolveModel(modelID)
	if err != nil {
		return nil, err
	}

	runtimes := e.runtime.All()
	if len(targets) > 0 {
		picked := make([]runtime.Runtime, 0, len(targets))
		for _, name := range targets {
			rt, ok := e.runtime.Get(name)
			if !ok {
				return nil, fmt.Errorf("unknown runtime: %s", name)
			}
			picked = append(picked, rt)
		}
		runtimes = picked
	}

	results := make([]ExportResult, 0, len(runtimes))
	for _, rt := range runtimes {
		if !rt.Detect() {
			res := ExportResult{Runtime: rt.Name(), Skipped: true, Reason: "not detected"}
			if strict {
				res.Err = fmt.Errorf("runtime %s not detected", rt.Name())
			}
			results = append(results, res)
			continue
		}

		exp, ok := e.findExporter(rt.Name())
		if !ok {
			res := ExportResult{Runtime: rt.Name(), Skipped: true, Reason: "no exporter"}
			if strict {
				res.Err = fmt.Errorf("no exporter registered for runtime %s", rt.Name())
			}
			results = append(results, res)
			continue
		}
		if !exp.Supports(m) {
			results = append(results, ExportResult{Runtime: rt.Name(), Skipped: true, Reason: "unsupported model"})
			continue
		}
		res := ExportResult{Runtime: rt.Name()}
		if err := exp.Export(m, rt); err != nil {
			res.Err = err
		}
		results = append(results, res)
	}

	return results, nil
}

func (e *ExportEngine) findExporter(name string) (Exporter, bool) {
	for _, ex := range e.exporters {
		if strings.EqualFold(ex.Name(), name) {
			return ex, true
		}
	}
	return nil, false
}
