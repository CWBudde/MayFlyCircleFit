package opt

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// captureHandler records every emitted record so event levels can be asserted.
type captureHandler struct {
	level slog.Level

	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, record.Clone())

	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// countEvent returns how many captured records carry the given event name, and
// how many of those were emitted at the given level.
func (h *captureHandler) countEvent(event string) (total int, byLevel map[slog.Level]int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	byLevel = map[slog.Level]int{}

	for _, record := range h.records {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "event" && attr.Value.String() == event {
				total++
				byLevel[record.Level]++

				return false
			}

			return true
		})
	}

	return total, byLevel
}

func runLoggedOptimization(t *testing.T, handler *captureHandler, maxIters int) {
	t.Helper()

	optimizer := NewMayfly(maxIters, 20, 42, WithLogger(slog.New(handler)))

	_, err := optimizer.(LifecycleOptimizer).RunContext(context.Background(), Problem{
		Eval: sphere, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}
}

// TestMayflyLoggerDemotesHighFrequencyEvents pins the flood protection: at info
// level a run must emit exactly one optimizer record regardless of iteration
// count.
func TestMayflyLoggerDemotesHighFrequencyEvents(t *testing.T) {
	handler := &captureHandler{level: slog.LevelInfo}
	runLoggedOptimization(t, handler, 25)

	if total, _ := handler.countEvent(eventIterationCompleted); total != 0 {
		t.Fatalf("iteration_completed records at info = %d, want 0", total)
	}

	if total, _ := handler.countEvent(eventOptimizationStarted); total != 0 {
		t.Fatalf("optimization_started records at info = %d, want 0", total)
	}

	total, byLevel := handler.countEvent(eventOptimizationCompleted)
	if total != 1 {
		t.Fatalf("optimization_completed records = %d, want 1", total)
	}

	if byLevel[slog.LevelInfo] != 1 {
		t.Fatalf("optimization_completed at info = %d, want 1", byLevel[slog.LevelInfo])
	}
}

// TestMayflyLoggerEmitsIterationDetailAtDebug covers the escape hatch: debug
// level still exposes the full per-iteration history.
func TestMayflyLoggerEmitsIterationDetailAtDebug(t *testing.T) {
	const iterations = 12
	handler := &captureHandler{level: slog.LevelDebug}
	runLoggedOptimization(t, handler, iterations)

	total, byLevel := handler.countEvent(eventIterationCompleted)
	if total != iterations {
		t.Fatalf("iteration_completed records = %d, want %d", total, iterations)
	}

	if byLevel[slog.LevelDebug] != iterations {
		t.Fatalf("iteration_completed at debug = %d, want %d", byLevel[slog.LevelDebug], iterations)
	}

	if started, startedByLevel := handler.countEvent(eventOptimizationStarted); started != 1 || startedByLevel[slog.LevelDebug] != 1 {
		t.Fatalf("optimization_started at debug = %d/%d, want 1/1", startedByLevel[slog.LevelDebug], started)
	}
}

// TestMayflyLoggerNilIsSafe keeps WithLogger(nil) a real off switch.
func TestMayflyLoggerNilIsSafe(t *testing.T) {
	optimizer := NewMayfly(5, 20, 42, WithLogger(nil)).(*MayflyAdapter)
	if optimizer.logger != nil {
		t.Fatal("WithLogger(nil) stored a logger")
	}

	if _, err := optimizer.RunContext(context.Background(), Problem{
		Eval: sphere, Lower: []float64{-1, -1}, Upper: []float64{1, 1}, Dim: 2,
	}, RunOptions{}); err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	var logger mayflyLogger
	logger.Log(context.Background(), slog.LevelInfo, "ignored", "event", eventIterationCompleted)
}

// TestRemapEventLevelKeepsUnknownEvents ensures a new upstream event is never
// silently hidden by this adapter.
func TestRemapEventLevelKeepsUnknownEvents(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want slog.Level
	}{
		{name: "iteration", args: []any{"event", eventIterationCompleted}, want: slog.LevelDebug},
		{name: "started", args: []any{"event", eventOptimizationStarted}, want: slog.LevelDebug},
		{name: "completed", args: []any{"event", eventOptimizationCompleted}, want: slog.LevelInfo},
		{name: "unknown event", args: []any{"event", "something_new"}, want: slog.LevelInfo},
		{name: "no event key", args: []any{"iteration", 3}, want: slog.LevelInfo},
		{name: "no args", args: nil, want: slog.LevelInfo},
		{name: "non-string key", args: []any{7, "value"}, want: slog.LevelInfo},
		{name: "non-string value", args: []any{"event", 7}, want: slog.LevelInfo},
		{name: "odd args", args: []any{"event"}, want: slog.LevelInfo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := remapEventLevel(slog.LevelInfo, test.args); got != test.want {
				t.Fatalf("remapEventLevel() = %v, want %v", got, test.want)
			}
		})
	}
}
