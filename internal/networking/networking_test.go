package networking

import (
	"testing"
)

func TestParseModeDefaultsToLanOnly(t *testing.T) {
	got, err := ParseMode("")
	if err != nil {
		t.Fatalf("ParseMode(\"\"): %v", err)
	}
	if got != ModeLANOnly {
		t.Fatalf("ParseMode default = %q, want %q", got, ModeLANOnly)
	}
}

func TestParseModeRejectsUnknown(t *testing.T) {
	if _, err := ParseMode("internet"); err == nil {
		t.Fatal("ParseMode(internet) expected error")
	}
	if _, err := ParseMode("hybrid"); err == nil {
		t.Fatal("ParseMode(hybrid) expected error")
	}
}

func TestResolveCapabilitiesLANOnly(t *testing.T) {
	caps := ResolveCapabilities(ModeLANOnly)
	if !caps.EnableLSD {
		t.Fatalf("lan_only caps missing LSD: %+v", caps)
	}
}

func TestPublishRequiresConfirmation(t *testing.T) {
	policy := PublishReachabilityPolicy{RequiresInternetReachability: true}
	if PublishRequiresConfirmation(policy) {
		t.Fatal("did not expect confirmation for internet-reachable policy in lan-only mode")
	}
	noPolicy := PublishReachabilityPolicy{RequiresInternetReachability: false}
	if PublishRequiresConfirmation(noPolicy) {
		t.Fatal("did not expect confirmation for local policy")
	}
}
