package crypto

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Canonicalize serializes v as canonical JSON:
// keys sorted lexicographically, no whitespace, HTML escaping disabled,
// "signature" field removed.
func Canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var m map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}

	delete(m, "signature")

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		var compacted bytes.Buffer
		if err := json.Compact(&compacted, m[k]); err != nil {
			return nil, err
		}
		buf.Write(compacted.Bytes())
	}
	buf.WriteByte('}')

	return buf.Bytes(), nil
}
