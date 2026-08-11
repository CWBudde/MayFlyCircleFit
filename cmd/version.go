package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func versionInfo() string {
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "mayflycirclefit version %s\n", versionInfo())
	},
}

func init() {
	rootCmd.Version = versionInfo()
	rootCmd.SetVersionTemplate("mayflycirclefit version {{.Version}}\n")
	rootCmd.AddCommand(versionCmd)
}
