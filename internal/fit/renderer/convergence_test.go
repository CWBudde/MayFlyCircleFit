package renderer

import (
	"math"
	"testing"
)

func TestConvergenceTracker_BasicConvergence(t *testing.T) {
	config := ConvergenceConfig{
		Enabled:   true,
		Patience:  3,
		Threshold: 0.01, // 1% improvement required
	}
	tracker := NewConvergenceTracker(config)

	// Test initial state
	if tracker.BestCost() != math.Inf(1) {
		t.Errorf("Expected initial best cost to be Inf, got %v", tracker.BestCost())
	}

	// First update - no convergence
	if tracker.Update(1.0) {
		t.Error("Should not converge on first update")
	}
	if tracker.BestCost() != 1.0 {
		t.Errorf("Expected best cost 1.0, got %v", tracker.BestCost())
	}

	// Significant improvement - reset stale counter
	if tracker.Update(0.8) { // 20% improvement
		t.Error("Should not converge after improvement")
	}
	if tracker.StaleCount() != 0 {
		t.Errorf("Expected stale count 0 after improvement, got %v", tracker.StaleCount())
	}

	// Small improvements (below threshold) - increment stale counter
	// Last significant was 0.8, so we need < 1% improvement from 0.8
	if tracker.Update(0.795) { // (0.8-0.795)/0.8 = 0.625% < 1%
		t.Error("Should not converge yet (1/3)")
	}
	if tracker.StaleCount() != 1 {
		t.Errorf("Expected stale count 1, got %v", tracker.StaleCount())
	}

	if tracker.Update(0.796) { // (0.8-0.796)/0.8 = 0.5% < 1%
		t.Error("Should not converge yet (2/3)")
	}
	if tracker.StaleCount() != 2 {
		t.Errorf("Expected stale count 2, got %v", tracker.StaleCount())
	}

	// Third small improvement - should trigger convergence
	if !tracker.Update(0.797) { // (0.8-0.797)/0.8 = 0.375% < 1%
		t.Error("Should converge after patience exceeded (3/3)")
	}
	if tracker.StaleCount() != 3 {
		t.Errorf("Expected stale count 3, got %v", tracker.StaleCount())
	}
}

func TestConvergenceTracker_ImprovementResetsStaleCount(t *testing.T) {
	config := ConvergenceConfig{
		Enabled:   true,
		Patience:  2,
		Threshold: 0.05, // 5% improvement required
	}
	tracker := NewConvergenceTracker(config)

	tracker.Update(1.0)  // Initial
	tracker.Update(0.99) // Stale (1% < 5%)
	if tracker.StaleCount() != 1 {
		t.Errorf("Expected stale count 1, got %v", tracker.StaleCount())
	}

	tracker.Update(0.94) // Significant improvement (5.05% from best)
	if tracker.StaleCount() != 0 {
		t.Errorf("Expected stale count reset to 0, got %v", tracker.StaleCount())
	}
	if tracker.BestCost() != 0.94 {
		t.Errorf("Expected best cost 0.94, got %v", tracker.BestCost())
	}
}

func TestConvergenceTracker_Disabled(t *testing.T) {
	config := DisabledConvergenceConfig()
	tracker := NewConvergenceTracker(config)

	// Should never converge when disabled
	for i := 0; i < 100; i++ {
		if tracker.Update(1.0) {
			t.Error("Should never converge when disabled")
		}
	}
}

func TestConvergenceTracker_History(t *testing.T) {
	config := DefaultConvergenceConfig()
	tracker := NewConvergenceTracker(config)

	costs := []float64{1.0, 0.9, 0.85, 0.82}
	for _, cost := range costs {
		tracker.Update(cost)
	}

	history := tracker.History()
	if len(history) != len(costs) {
		t.Errorf("Expected history length %d, got %d", len(costs), len(history))
	}

	for i, cost := range costs {
		if history[i] != cost {
			t.Errorf("Expected history[%d] = %v, got %v", i, cost, history[i])
		}
	}

	// Verify it's a copy (mutation shouldn't affect original)
	history[0] = 999.0
	if tracker.History()[0] == 999.0 {
		t.Error("History() should return a copy, not a reference")
	}
}

func TestConvergenceTracker_Reset(t *testing.T) {
	config := DefaultConvergenceConfig()
	tracker := NewConvergenceTracker(config)

	tracker.Update(1.0)
	tracker.Update(0.99)
	tracker.Update(0.98)

	if len(tracker.History()) != 3 {
		t.Error("Expected history before reset")
	}

	tracker.Reset()

	if len(tracker.History()) != 0 {
		t.Error("Expected empty history after reset")
	}
	if tracker.BestCost() != math.Inf(1) {
		t.Error("Expected best cost reset to Inf")
	}
	if tracker.StaleCount() != 0 {
		t.Error("Expected stale count reset to 0")
	}
}

func TestConvergenceTracker_ZeroThreshold(t *testing.T) {
	config := ConvergenceConfig{
		Enabled:   true,
		Patience:  2,
		Threshold: 0.0, // Any improvement counts
	}
	tracker := NewConvergenceTracker(config)

	tracker.Update(1.0)
	tracker.Update(0.999) // Tiny improvement
	if tracker.StaleCount() != 0 {
		t.Error("Even tiny improvement should reset stale count with zero threshold")
	}

	tracker.Update(0.999) // No improvement
	tracker.Update(1.0)   // Worse (no improvement)
	if !tracker.Update(1.1) { // Still no improvement - should converge
		t.Error("Should converge after patience with no improvement")
	}
}

func TestDefaultConvergenceConfig(t *testing.T) {
	config := DefaultConvergenceConfig()

	if !config.Enabled {
		t.Error("Expected default config to be enabled")
	}
	if config.Patience <= 0 {
		t.Error("Expected default patience > 0")
	}
	if config.Threshold < 0 {
		t.Error("Expected default threshold >= 0")
	}
}

func TestDisabledConvergenceConfig(t *testing.T) {
	config := DisabledConvergenceConfig()

	if config.Enabled {
		t.Error("Expected disabled config to not be enabled")
	}
}
