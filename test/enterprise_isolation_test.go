//go:build oss_ci

package test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestEnterpriseImplNotInOSSGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "oss", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}

	graph := string(out)
	if strings.Contains(graph, "hali/internal/enterprise") {
		t.Fatalf("enterprise package leaked into OSS dependency graph:\n%s", graph)
	}
}

func TestEnterprisePackageNotInOSSPackageList(t *testing.T) {
	out, err := exec.Command("go", "list", "-tags", "oss", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, out)
	}

	pkgList := string(out)
	if strings.Contains(pkgList, "hali/internal/enterprise") {
		t.Fatalf("enterprise package present in OSS package list:\n%s", pkgList)
	}
}
