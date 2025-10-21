package main

import (
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
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
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
			level = slog.LevelInfo
		}

		opts := &slog.HandlerOptions{Level: level}
		handler := slog.NewJSONHandler(os.Stdout, opts)
		logger = slog.New(handler)
		slog.SetDefault(logger)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
}
