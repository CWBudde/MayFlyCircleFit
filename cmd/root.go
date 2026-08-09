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
			return fmt.Errorf("invalid log level %q: use debug, info, warn, or error", logLevel)
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
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}
