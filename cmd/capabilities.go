package cmd

import (
	"encoding/json"

	versionapp "github.com/samsaffron/term-llm/internal/app/version"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{
		Use: "capabilities", Short: "Print native feature capability versions", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(versionapp.Capabilities())
		},
	}
	command.Flags().Bool("json", false, "Print machine-readable JSON (the default)")
	rootCmd.AddCommand(command)
}
