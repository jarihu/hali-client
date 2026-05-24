package cmd

import (
	"testing"
)

func TestPickOne(t *testing.T) {
	// pickOne reads from stdin via fmt.Scan — not directly testable
	// without replacing os.Stdin. But we can verify it errors on empty list.
	_, err := pickOne("test", nil)
	if err == nil {
		t.Error("pickOne with empty list should fail")
	}
}
