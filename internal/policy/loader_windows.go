//go:build windows

package policy

import (
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const policyRoot = `SOFTWARE\Policies\Hali`

// HKLMStore implements Store backed by HKLM\SOFTWARE\Policies\Hali.
type HKLMStore struct{}

// ReadDWORD reads a DWORD value from HKLM\SOFTWARE\Policies\Hali\<subkey>\<name>.
// An absent key or value returns (0, false, nil) — not an error.
func (HKLMStore) ReadDWORD(subkey, name string) (uint32, bool, error) {
	path := policyRoot
	if subkey != "" {
		path = policyRoot + `\` + subkey
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false, nil
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false, nil
	}
	return uint32(val), true, nil
}

// DefaultStore returns an HKLMStore for reading system policy.
func DefaultStore() Store { return HKLMStore{} }

// Watch monitors HKLM\SOFTWARE\Policies\Hali for changes using
// RegNotifyChangeKeyValue. onChange is debounced (300 ms) and fires after
// a quiet period following any change. Returns a stop function for cleanup.
func Watch(store Store, onChange func()) (stop func()) {
	stopCh := make(chan struct{})
	var once sync.Once
	go watchLoop(stopCh, onChange)
	return func() { once.Do(func() { close(stopCh) }) }
}

func watchLoop(stopCh <-chan struct{}, onChange func()) {
	const debounce = 300 * time.Millisecond

	for {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, policyRoot,
			registry.NOTIFY|registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			// Policy key does not exist yet; poll until it appears.
			select {
			case <-stopCh:
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}

		fired := waitForChange(k, stopCh)
		k.Close()
		if !fired {
			return // stop was called
		}

		// Debounce: wait for registry writes to settle before reloading.
		timer := time.NewTimer(debounce)
		select {
		case <-stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}

		onChange()
	}
}

// waitForChange blocks until the registry key changes or stopCh is closed.
// Returns true if a change was detected, false if stop was requested.
func waitForChange(k registry.Key, stopCh <-chan struct{}) bool {
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		// Fallback: sleep and signal a "change" so we reload anyway.
		select {
		case <-stopCh:
			return false
		case <-time.After(30 * time.Second):
			return true
		}
	}
	defer windows.CloseHandle(event)

	const notifyFilter = 0x00000001 | // REG_NOTIFY_CHANGE_NAME
		0x00000004 // REG_NOTIFY_CHANGE_LAST_SET

	if err := windows.RegNotifyChangeKeyValue(
		windows.Handle(k), true, notifyFilter, event, true,
	); err != nil {
		select {
		case <-stopCh:
			return false
		case <-time.After(5 * time.Second):
			return true
		}
	}

	// Poll WaitForSingleObject in 500 ms ticks so we can also check stopCh.
	for {
		result, _ := windows.WaitForSingleObject(event, 500)
		select {
		case <-stopCh:
			return false
		default:
		}
		if result == windows.WAIT_OBJECT_0 {
			return true
		}
		// WAIT_TIMEOUT: loop and check stop again.
	}
}
