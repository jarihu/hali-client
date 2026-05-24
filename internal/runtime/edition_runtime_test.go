package runtime

import (
	"context"
	"errors"
	"testing"

	"hali/editionapi"
)

type testPolicy struct{ err error }

func (t testPolicy) ApplyPolicies(context.Context) error { return t.err }

type testFleet struct{ err error }

func (t testFleet) ReportNode() error { return t.err }

type testAudit struct{ err error }

func (t testAudit) LogEvent(string) error { return t.err }

func TestNewEditionRuntimeWiresCapabilities(t *testing.T) {
	expectedErr := errors.New("boom")
	cap := editionapi.Capabilities{
		Policy: testPolicy{err: expectedErr},
		Fleet:  testFleet{err: expectedErr},
		Audit:  testAudit{err: expectedErr},
	}
	rt := newEditionRuntime(cap)
	if rt == nil {
		t.Fatal("newEditionRuntime returned nil")
	}
	if rt.Cap.Policy == nil || rt.Cap.Fleet == nil || rt.Cap.Audit == nil {
		t.Fatal("newEditionRuntime returned nil capability provider")
	}
	if err := rt.Cap.Policy.ApplyPolicies(context.Background()); !errors.Is(err, expectedErr) {
		t.Fatalf("Policy.ApplyPolicies error = %v, want %v", err, expectedErr)
	}
	if err := rt.Cap.Fleet.ReportNode(); !errors.Is(err, expectedErr) {
		t.Fatalf("Fleet.ReportNode error = %v, want %v", err, expectedErr)
	}
	if err := rt.Cap.Audit.LogEvent("event"); !errors.Is(err, expectedErr) {
		t.Fatalf("Audit.LogEvent error = %v, want %v", err, expectedErr)
	}
}
