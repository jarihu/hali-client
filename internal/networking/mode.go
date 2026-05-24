package networking

import (
	"fmt"
	"strings"
)

// Mode is semantic networking intent, independent of transport implementation.
type Mode string

const ModeLANOnly Mode = "lan_only"

func DefaultMode() Mode {
	return ModeLANOnly
}

func ParseMode(raw string) (Mode, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	if normalized == "" || normalized == string(ModeLANOnly) {
		return ModeLANOnly, nil
	}
	return "", fmt.Errorf("unsupported network.mode %q: only lan_only is supported", raw)
}

func (m Mode) Valid() bool {
	return m == ModeLANOnly
}

func (m Mode) String() string {
	if !m.Valid() {
		return string(DefaultMode())
	}
	return string(m)
}
