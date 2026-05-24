package cmd

import (
	"encoding/json"
	"os"
)

type cliResult struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func emitJSONError(err error) {
	_ = json.NewEncoder(os.Stdout).Encode(cliResult{OK: false, ExitCode: 1, Error: err.Error()})
}
