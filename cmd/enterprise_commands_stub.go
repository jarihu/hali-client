package cmd

import (
	"hali/editionapi"

	"github.com/spf13/cobra"
)

// RegisterEnterpriseCommands is a no-op in the OSS build.
func RegisterEnterpriseCommands(_ *cobra.Command, _ *editionapi.Runtime) {}
