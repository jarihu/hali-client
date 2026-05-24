package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AcquireLock creates an exclusive lock file for the given repo, preventing
// duplicate concurrent downloads when the user double-clicks "Open in Hali".
// The returned cleanup function removes the lock file and must be called (via defer) on exit.
func AcquireLock(repo string) (func(), error) {
	safe := strings.NewReplacer("/", "-", "\\", "-").Replace(repo)
	lockPath := filepath.Join(os.TempDir(), "hali-"+safe+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return nil, fmt.Errorf("download already in progress for %s", repo)
	}
	f.Close()
	return func() { os.Remove(lockPath) }, nil
}
