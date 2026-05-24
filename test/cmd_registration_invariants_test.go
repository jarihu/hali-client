package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoCommandRegistrationInitFunctions(t *testing.T) {
	cmdDir := filepath.Clean(filepath.Join("..", "cmd"))
	var offenders []string

	err := filepath.WalkDir(cmdDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"tray"+string(filepath.Separator)) {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		if strings.Contains(s, "func init()") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking cmd tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("found forbidden init() in command files: %v", offenders)
	}
}
