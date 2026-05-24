//go:build !windows

package policy

// emptyStore is a no-op Store for non-Windows platforms where
// Group Policy / registry-based configuration is not available.
type emptyStore struct{}

func (emptyStore) ReadDWORD(_, _ string) (uint32, bool, error) { return 0, false, nil }

// DefaultStore returns a no-op store on non-Windows platforms.
// Policy is only enforced via Windows Group Policy / Intune.
func DefaultStore() Store { return emptyStore{} }

// Watch is a no-op on non-Windows platforms. It returns immediately with a
// no-op stop function since there is no registry to watch.
func Watch(_ Store, _ func()) (stop func()) { return func() {} }
