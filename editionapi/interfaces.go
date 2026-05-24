package editionapi

import "context"

type PolicyProvider interface {
	ApplyPolicies(ctx context.Context) error
}

type FleetProvider interface {
	ReportNode() error
}

type AuditProvider interface {
	LogEvent(event string) error
}
