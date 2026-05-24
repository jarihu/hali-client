package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"hali/editionapi"
)

func TestRegisterEnterpriseCommandsNoPanic(t *testing.T) {
	root := &cobra.Command{Use: "hali"}
	RegisterEnterpriseCommands(root, &editionapi.Runtime{})
}
