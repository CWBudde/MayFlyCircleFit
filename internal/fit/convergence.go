package fit

import (
	"log/slog"
	"math"
)

// ConvergenceConfig defines parameters for detecting optimization convergence
type ConvergenceConfig struct {
	// Enabled controls whether convergence detection is active
	Enabled bool

	// Patience is the number of circles/batches with no improvement before stopping
	// For sequential mode: number of circles with no improvement
	// For batch mode: number of batches with no improvement
	Patience int

	// Threshold is the minimum relative improvement required to count as progress
	// Example: 0.001 = 0.1% improvement required
	// Relative improvement = (oldCost - newCost) / oldCost
	Threshold float64
}

// DefaultConvergenceConfig returns sensible defaults for convergence detection
func DefaultConvergenceConfig() ConvergenceConfig {
	return ConvergenceConfig{
		Enabled:   true,
		Patience:  3,
		Threshold: 0.001, // 0.1% improvement
	}
}

// DisabledConvergenceConfig returns a config with convergence detection disabled
func DisabledConvergenceConfig() ConvergenceConfig {
	return ConvergenceConfig{
		Enabled: false,
	}
}

// ConvergenceTracker tracks cost history and detects when optimization has converged
type ConvergenceTracker struct {
	config           ConvergenceConfig
	costHistory      []float64
	bestCost         float64 // Best cost ever seen
	lastSignificant  float64 // Last cost that was a significant improvement
	staleCount       int     // Number of iterations without significant improvement
}

// NewConvergenceTracker creates a new convergence tracker with the given config
func NewConvergenceTracker(config ConvergenceConfig) *ConvergenceTracker {
	return &ConvergenceTracker{
		config:          config,
		costHistory:     []float64{},
		bestCost:        math.Inf(1), // Start with infinity
		lastSignificant: math.Inf(1), // Start with infinity
		staleCount:      0,
	}
}

// Update records a new cost value and returns true if convergence is detected
func (c *ConvergenceTracker) Update(cost float64) bool {
	if !c.config.Enabled {
		return false // Never converge if disabled
	}

	c.costHistory = append(c.costHistory, cost)

	// Update best cost if this is better
	if cost < c.bestCost {
		c.bestCost = cost
	}

	// First cost - initialize lastSignificant
	if len(c.costHistory) == 1 {
		c.lastSignificant = cost
		return false
	}

	// Check if this is a significant improvement from last significant point
	relativeImprovement := (c.lastSignificant - cost) / c.lastSignificant

	if relativeImprovement >= c.config.Threshold {
		// Significant improvement - reset stale counter
		c.lastSignificant = cost
		c.staleCount = 0
		slog.Debug("Cost improvement detected",
			"cost", cost,
			"relative_improvement", relativeImprovement,
			"stale_count", c.staleCount,
		)
	} else {
		// No significant improvement
		c.staleCount++
		slog.Debug("No significant cost improvement",
			"cost", cost,
			"last_significant", c.lastSignificant,
			"relative_improvement", relativeImprovement,
			"stale_count", c.staleCount,
			"patience", c.config.Patience,
		)

		// Check if we've exceeded patience
		if c.staleCount >= c.config.Patience {
			slog.Info("Convergence detected - stopping early",
				"stale_count", c.staleCount,
				"patience", c.config.Patience,
				"best_cost", c.bestCost,
			)
			return true
		}
	}

	return false
}

// BestCost returns the best cost seen so far
func (c *ConvergenceTracker) BestCost() float64 {
	return c.bestCost
}

// History returns the full cost history
func (c *ConvergenceTracker) History() []float64 {
	return append([]float64{}, c.costHistory...) // Return copy
}

// StaleCount returns the current number of iterations without improvement
func (c *ConvergenceTracker) StaleCount() int {
	return c.staleCount
}

// Reset clears the tracker's state
func (c *ConvergenceTracker) Reset() {
	c.costHistory = []float64{}
	c.bestCost = math.Inf(1)
	c.lastSignificant = math.Inf(1)
	c.staleCount = 0
}
