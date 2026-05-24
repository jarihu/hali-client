package cmd

import "testing"

func TestFmtDownloads(t *testing.T) {
	tests := []struct {
		n      int
		expect string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{9999, "10.0K"},
		{10000, "10.0K"},
		{12345, "12.3K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{1000000000, "1000.0M"},
	}
	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			got := fmtDownloads(tt.n)
			if got != tt.expect {
				t.Errorf("fmtDownloads(%d) = %q, want %q", tt.n, got, tt.expect)
			}
		})
	}
}
