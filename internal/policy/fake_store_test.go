package policy

// FakeStore is an in-memory Store for unit tests.
// It does not touch the Windows registry or require elevated privileges.
// Label any test using FakeStore as a unit test only — not as evidence that
// actual system policy deployment works end-to-end.
type FakeStore struct {
	values map[string]uint32
}

func (f *FakeStore) ReadDWORD(subkey, name string) (uint32, bool, error) {
	key := subkey + `\` + name
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *FakeStore) set(subkey, name string, val uint32) {
	if f.values == nil {
		f.values = make(map[string]uint32)
	}
	f.values[subkey+`\`+name] = val
}

func (f *FakeStore) del(subkey, name string) {
	delete(f.values, subkey+`\`+name)
}
