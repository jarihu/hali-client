//go:build !oss

package runtime

import (
	"context"
	"testing"
)

func TestNewOSSDefaultBuildCapabilities(t *testing.T) {
	rt := NewOSS()
	if rt == nil {
		t.Fatal("NewOSS returned nil")
	}
	if rt.Cap.Policy == nil || rt.Cap.Fleet == nil || rt.Cap.Audit == nil {
		t.Fatal("NewOSS returned nil capability provider")
	}
	if err := rt.Cap.Policy.ApplyPolicies(context.Background()); err != nil {
		t.Fatalf("Policy.ApplyPolicies error = %v, want nil", err)
	}
	if err := rt.Cap.Fleet.ReportNode(); err != nil {
		t.Fatalf("Fleet.ReportNode error = %v, want nil", err)
	}
	if err := rt.Cap.Audit.LogEvent("default"); err != nil {
		t.Fatalf("Audit.LogEvent error = %v, want nil", err)
	}
}
