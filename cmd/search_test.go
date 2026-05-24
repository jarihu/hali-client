package cmd

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPickOneEmptyList(t *testing.T) {
	_, err := pickOne("test", nil)
	if err == nil {
		t.Error("pickOne with empty list should fail")
	}
}

func TestPickOneValidSelection(t *testing.T) {
	items := []string{"alpha", "beta", "gamma"}
	for _, tc := range []struct {
		input string
		want  int
	}{
		{"1\n", 0},
		{"2\n", 1},
		{"3\n", 2},
	} {
		t.Run(fmt.Sprintf("input=%q", tc.input), func(t *testing.T) {
			withStdin(t, tc.input, func() {
				got, err := pickOne("Select", items)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Errorf("got index %d, want %d", got, tc.want)
				}
			})
		})
	}
}

func TestPickOneInvalidSelection(t *testing.T) {
	items := []string{"alpha", "beta"}
	for _, input := range []string{"0\n", "3\n", "abc\n", "\n"} {
		t.Run(fmt.Sprintf("input=%q", input), func(t *testing.T) {
			withStdin(t, input, func() {
				_, err := pickOne("Select", items)
				if err == nil {
					t.Error("expected error for invalid input")
				}
			})
		})
	}
}

// withStdin replaces os.Stdin with a pipe containing the given input for the
// duration of fn, then restores the original stdin.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := fmt.Fprint(w, strings.ReplaceAll(input, "\n", "\n")); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	w.Close()

	orig := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = orig
		r.Close()
	}()
	fn()
}
