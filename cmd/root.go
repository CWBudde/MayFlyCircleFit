package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	logLevel string
	logger   *slog.Logger
)

var rootCmd = &cobra.Command{
	Use:   "mayflycirclefit",
	Short: "High-performance circle fitting with mayfly optimization",
	Long: `MayFlyCircleFit uses evolutionary algorithms to approximate images
with colored circles, featuring CPU/GPU backends and live visualization.`,
	// CLI output should be normalized in main for consistent user-facing messages
	// and explicit exit status handling.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Setup logger
		var level slog.Level
		switch logLevel {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			return NewUsageError(fmt.Errorf("invalid log level %q: use debug, info, warn, or error", logLevel))
		}

		opts := &slog.HandlerOptions{Level: level}
		handler := slog.NewJSONHandler(os.Stdout, opts)
		logger = slog.New(handler)
		slog.SetDefault(logger)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	// Subcommands inherit this from the root, so every flag parsing failure
	// arrives at main already typed as an invocation error.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return NewUsageError(err)
	})
}

// Execute runs the root command. Invocation failures come back wrapped in
// UsageError so the caller can pick the exit status.
func Execute() error {
	return classifyExecuteError(rootCmd.Execute())
}
