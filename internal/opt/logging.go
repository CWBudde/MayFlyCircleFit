package opt

import (
	"context"
	"log/slog"
)

// Mayfly event names. The library emits all three at info level.
const (
	eventOptimizationStarted   = "optimization_started"
	eventIterationCompleted    = "iteration_completed"
	eventOptimizationCompleted = "optimization_completed"
)

// mayflyLogger adapts *slog.Logger to the Mayfly logger interface and remaps
// event levels so a long run cannot flood the default info log.
//
// Mayfly logs optimization_started, iteration_completed, and
// optimization_completed at info. One iteration record per iteration is far too
// much for this application: a joint run may perform thousands of iterations,
// and staged runs multiply that by the circle or batch count. Per-iteration
// history already has a home in the job trace, so only the completion event,
// which carries the measured work and the termination reason, stays at info.
type mayflyLogger struct {
	logger *slog.Logger
}

// Log implements the Mayfly logger interface.
func (l mayflyLogger) Log(ctx context.Context, level slog.Level, message string, args ...any) {
	if l.logger == nil {
		return
	}
	level = remapEventLevel(level, args)
	if !l.logger.Enabled(ctx, level) {
		return
	}
	l.logger.Log(ctx, level, message, args...)
}

// remapEventLevel demotes high-frequency and redundant Mayfly events. Events
// this package does not recognize keep the level Mayfly chose, so a new
// upstream event is never silently hidden.
func remapEventLevel(level slog.Level, args []any) slog.Level {
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			return level
		}
		if key != "event" {
			continue
		}
		value, ok := args[i+1].(string)
		if !ok {
			return level
		}
		switch value {
		case eventIterationCompleted:
			// One record per iteration; only useful when debugging.
			return slog.LevelDebug
		case eventOptimizationStarted:
			// Redundant: the CLI and the pipeline already log run parameters.
			return slog.LevelDebug
		}
		return level
	}
	return level
}
