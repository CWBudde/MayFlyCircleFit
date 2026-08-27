package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/cwbudde/circlefit/internal/server"
	"github.com/spf13/cobra"
)

var (
	version        = "dev"
	commit         = "unknown"
	buildDate      = "unknown"
	versionVerbose bool
)

func versionInfo() string {
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Args:  cobra.NoArgs,
	RunE:  runVersion,
}

func init() {
	rootCmd.Version = versionInfo()
	rootCmd.SetVersionTemplate("circlefit version {{.Version}}\n")
	versionCmd.Flags().BoolVar(&versionVerbose, "verbose", false, "Print host and build details")
	rootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) error {
	if versionVerbose {
		facts, err := json.MarshalIndent(server.HostFactsFromMetadata(server.BuildMetadata{
			Version:   version,
			Commit:    commit,
			BuildDate: buildDate,
		}), "", "  ")
		if err != nil {
			return fmt.Errorf("encode version facts: %w", err)
		}

		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(facts))

		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "circlefit version %s\n", versionInfo())

	return nil
}
