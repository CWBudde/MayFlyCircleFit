package app

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNormalizeAppliesCanonicalDefaults(t *testing.T) {
	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}
	defaults := DefaultConfig()
	if config.Mode != defaults.Mode || config.Backend != defaults.Backend || config.Circles != defaults.Circles || config.Iters != defaults.Iters || config.PopSize != defaults.PopSize {
		t.Fatalf("defaults not applied: %+v", config)
	}
	if config.Threads != defaults.Threads || config.Threads < 1 {
		t.Fatalf("threads = %d, want default %d", config.Threads, defaults.Threads)
	}
	if config.EffectiveSeed == 0 {
		t.Fatal("zero seed was not resolved")
	}
}

func TestNormalizePreservesExplicitSeedAndDisables(t *testing.T) {
	config, err := Normalize(JobConfig{
		RefPath:            "reference.png",
		Seed:               42,
		DisableTrace:       true,
		DisableConvergence: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.EffectiveSeed != 42 {
		t.Fatalf("effective seed = %d, want 42", config.EffectiveSeed)
	}
	if config.EnableTrace || config.ConvergenceEnabled {
		t.Fatal("explicit disable flags were ignored")
	}
}

func TestValidateBoundaries(t *testing.T) {
	valid := DefaultConfig()
	valid.RefPath = "reference.png"
	valid.EffectiveSeed = 1

	tests := []struct {
		name   string
		mutate func(*JobConfig)
		field  string
	}{
		{"mode", func(c *JobConfig) { c.Mode = "invalid" }, "mode"},
		{"backend", func(c *JobConfig) { c.Backend = "invalid" }, "backend"},
		{"circles low", func(c *JobConfig) { c.Circles = -1 }, "circles"},
		{"circles high", func(c *JobConfig) { c.Circles = MaxCircles + 1 }, "circles"},
		{"iterations", func(c *JobConfig) { c.Iters = MaxIterations + 1 }, "iters"},
		{"population low", func(c *JobConfig) { c.PopSize = MinPopulation - 1 }, "popSize"},
		{"population high", func(c *JobConfig) { c.PopSize = MaxPopulation + 1 }, "popSize"},
		{"batch", func(c *JobConfig) { c.Mode, c.BatchSize = ModeBatch, c.Circles+1 }, "batchSize"},
		{"threads", func(c *JobConfig) { c.Threads = -1 }, "threads"},
		{"patience", func(c *JobConfig) { c.ConvergencePatience = 101 }, "convergencePatience"},
		{"threshold", func(c *JobConfig) { c.ConvergenceThreshold = 2 }, "convergenceThreshold"},
		{"stop target cost NaN", func(c *JobConfig) { c.StopTargetCost = math.NaN() }, "stopTargetCost"},
		{"stop target cost infinite", func(c *JobConfig) { c.StopTargetCost = math.Inf(1) }, "stopTargetCost"},
		{"stop target cost negative", func(c *JobConfig) { c.StopTargetCost = -1 }, "stopTargetCost"},
		{"stop min improvement infinite", func(c *JobConfig) { c.StopMinImprovement = math.Inf(1) }, "stopMinImprovement"},
		{"stop min improvement negative", func(c *JobConfig) { c.StopMinImprovement = -1 }, "stopMinImprovement"},
		{"stop min improvement without window", func(c *JobConfig) { c.StopMinImprovement = 1 }, "stopMinImprovement"},
		{"stop stagnation negative", func(c *JobConfig) { c.StopStagnationIters = -1 }, "stopStagnationIters"},
		{"stop stagnation over budget", func(c *JobConfig) { c.StopStagnationIters = c.Iters + 1 }, "stopStagnationIters"},
		{"stop min iters negative", func(c *JobConfig) { c.StopMinIters = -1 }, "stopMinIters"},
		{"stop min iters over budget", func(c *JobConfig) { c.StopMinIters = c.Iters + 1 }, "stopMinIters"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			err := config.Validate()
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != test.field {
				t.Fatalf("got %v, want validation error for %s", err, test.field)
			}
		})
	}
}

func TestValidateImageDimensions(t *testing.T) {
	for _, test := range []struct {
		width, height int
		wantErr       bool
	}{
		{1, 1, false},
		{4096, 4096, false},
		{0, 1, true},
		{1, 0, true},
		{MaxImagePixels, 2, true},
	} {
		if got := ValidateImageDimensions(test.width, test.height); (got != nil) != test.wantErr {
			t.Errorf("ValidateImageDimensions(%d, %d) = %v", test.width, test.height, got)
		}
	}
}

// TestNormalizeLeavesEarlyStopDisabled is the reproducibility contract for the
// optimizer-level stopping fields: ApplyDefaults must never fill them in, so an
// unconfigured run behaves exactly as it did before they existed.
func TestNormalizeLeavesEarlyStopDisabled(t *testing.T) {
	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}
	if config.StopTargetCost != 0 || config.StopMinImprovement != 0 {
		t.Fatalf("early-stop costs defaulted to non-zero: %+v", config)
	}
	if config.StopStagnationIters != 0 || config.StopMinIters != 0 {
		t.Fatalf("early-stop windows defaulted to non-zero: %+v", config)
	}
	if config.EarlyStopEnabled() {
		t.Fatal("early stopping is enabled by default")
	}
}

func TestSSIMIsOptInAndSerializedWhenEnabled(t *testing.T) {
	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if config.EnableSSIM || strings.Contains(string(data), "enableSSIM") {
		t.Fatalf("SSIM should be disabled and omitted by default: %s", data)
	}
	config.EnableSSIM = true
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"enableSSIM":true`) {
		t.Fatalf("enabled SSIM was not serialized: %s", data)
	}
}

// TestDefaultConfigJSONOmitsEarlyStopFields proves the persisted bytes for a
// default job are unchanged, so existing checkpoints round-trip identically.
func TestDefaultConfigJSONOmitsEarlyStopFields(t *testing.T) {
	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"stopTargetCost", "stopMinImprovement", "stopStagnationIters", "stopMinIters"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("default config serialized %s: %s", key, data)
		}
	}
}

func TestEarlyStopEnabledAndValidCombinations(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*JobConfig)
		enabled bool
	}{
		{"target cost only", func(c *JobConfig) { c.StopTargetCost = 0.5 }, true},
		{"stagnation only", func(c *JobConfig) { c.StopStagnationIters = 5 }, true},
		{"min iters only", func(c *JobConfig) { c.StopMinIters = 5 }, false},
		{"full configuration", func(c *JobConfig) {
			c.StopTargetCost = 1
			c.StopMinImprovement = 0.5
			c.StopStagnationIters = 5
			c.StopMinIters = 10
		}, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.RefPath = "reference.png"
			config.EffectiveSeed = 1
			test.mutate(&config)

			if err := config.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if got := config.EarlyStopEnabled(); got != test.enabled {
				t.Fatalf("EarlyStopEnabled() = %v, want %v", got, test.enabled)
			}
		})
	}
}
