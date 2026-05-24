package runtime

import (
	"context"
	"hali/editionapi"
)

// Runtime describes a passive local runtime installation.
type Runtime interface {
	Name() string
	Detect() bool
	ModelsPath() (string, error)
}

func newEditionRuntime(cap editionapi.Capabilities) *editionapi.Runtime {
	return &editionapi.Runtime{Cap: cap}
}

type noOpPolicy struct{}

func (noOpPolicy) ApplyPolicies(_ context.Context) error { return nil }

type noOpFleet struct{}

func (noOpFleet) ReportNode() error { return nil }

type noOpAudit struct{}

func (noOpAudit) LogEvent(_ string) error { return nil }
