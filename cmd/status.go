package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Query server status (coming in Phase 6)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("status command not yet implemented (Phase 6)")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
