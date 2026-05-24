package cmd

import (
	"fmt"

	"hali/internal/buildinfo"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit hash, build mode, and edition",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("hali %s (%s) %s [%s]\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildMode, buildinfo.Edition)
	},
}
