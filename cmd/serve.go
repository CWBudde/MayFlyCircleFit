package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server (coming in Phase 6)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("serve command not yet implemented (Phase 6)")
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
