package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var resumeCmd = &cobra.Command{
	Use:   "resume [job-id]",
	Short: "Resume from checkpoint (coming in Phase 7)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("resume command not yet implemented (Phase 7)")
	},
}

func init() {
	rootCmd.AddCommand(resumeCmd)
}
