//go:build !oss

package cmd

import (
	"fmt"
	"hali/internal/runtime"
	"os"
)

func Execute() {
	rt := runtime.NewOSS()
	root := NewRootCmd(rt)
	root.SilenceErrors = true
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		if jsonOutput {
			emitJSONError(err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
