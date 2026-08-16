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
	if config.Mode != defaults.Mode || config.Backend != defaults.Backend || config.Circles != defaults.Circles || config.Iters != defaults.Iters || config.PopSize != defaults.PopSize || config.OptimizerEpochs != 1 {
		t.Fatalf("defaults not applied: %+v", config)
	}
	if config.PolishingEnabled {
		t.Fatal("polishing must remain opt-in")
	}
	if config.PolishingStrategy != PolishingReplacement || config.PolishingActiveSetSize != 5 || config.PolishingMaxSweeps != 3 || config.PolishingEpochs != 2 || config.PolishingIters != 1000 || config.PolishingStagnationIters != 500 || config.PolishingMinImprovement != 0.001 {
		t.Fatalf("polishing defaults not applied: %+v", config)
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
		{"optimizer epochs low", func(c *JobConfig) { c.OptimizerEpochs = -1 }, "optimizerEpochs"},
		{"optimizer epochs high", func(c *JobConfig) { c.OptimizerEpochs = MaxOptimizerEpochs + 1 }, "optimizerEpochs"},
		{"batch", func(c *JobConfig) { c.Mode, c.BatchSize = ModeBatch, c.Circles+1 }, "batchSize"},
		{"polishing requires batch", func(c *JobConfig) { c.PolishingEnabled = true }, "polishingEnabled"},
		{"polishing only requires polishing", func(c *JobConfig) { c.PolishingOnly = true }, "polishingOnly"},
		{"polishing strategy", func(c *JobConfig) { c.PolishingStrategy = "invalid" }, "polishingStrategy"},
		{"polishing active set low", func(c *JobConfig) { c.PolishingActiveSetSize = -1 }, "polishingActiveSetSize"},
		{"polishing active set over circles", func(c *JobConfig) { c.PolishingActiveSetSize = c.Circles + 1 }, "polishingActiveSetSize"},
		{"polishing sweeps low", func(c *JobConfig) { c.PolishingMaxSweeps = -1 }, "polishingMaxSweeps"},
		{"polishing sweeps high", func(c *JobConfig) { c.PolishingMaxSweeps = MaxPolishingSweeps + 1 }, "polishingMaxSweeps"},
		{"polishing epochs high", func(c *JobConfig) { c.PolishingEpochs = MaxOptimizerEpochs + 1 }, "polishingEpochs"},
		{"polishing iterations high", func(c *JobConfig) { c.PolishingIters = MaxIterations + 1 }, "polishingIters"},
		{"polishing stagnation over budget", func(c *JobConfig) { c.PolishingStagnationIters = c.PolishingIters + 1 }, "polishingStagnationIters"},
		{"polishing improvement NaN", func(c *JobConfig) { c.PolishingMinImprovement = math.NaN() }, "polishingMinImprovement"},
		{"polishing improvement negative", func(c *JobConfig) { c.PolishingMinImprovement = -1 }, "polishingMinImprovement"},
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

func TestNormalizeOldConfigKeepsPolishingDisabled(t *testing.T) {
	var old JobConfig
	if err := json.Unmarshal([]byte(`{"refPath":"reference.png","mode":"batch","circles":3,"iters":100,"popSize":30,"batchSize":3}`), &old); err != nil {
		t.Fatal(err)
	}
	config, err := Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if config.PolishingEnabled {
		t.Fatal("an old configuration unexpectedly enabled polishing")
	}
	if config.PolishingStrategy != PolishingReplacement {
		t.Fatalf("old config strategy = %q, want replacement", config.PolishingStrategy)
	}
	if config.PolishingActiveSetSize != 3 {
		t.Fatalf("active set size = %d, want clamped default 3", config.PolishingActiveSetSize)
	}
}

// TestNormalizeOldConfigKeepsParallelEvaluationDisabled pins that a checkpoint
// or request written before parallel evaluation existed still decodes and
// normalizes to the serial path. The setting changes which solution a seed
// produces, so silently switching it on for an old configuration would make a
// resumed run diverge from the run it resumes.
func TestNormalizeOldConfigKeepsParallelEvaluationDisabled(t *testing.T) {
	var old JobConfig
	if err := json.Unmarshal([]byte(`{"refPath":"reference.png","mode":"joint","circles":3,"iters":100,"popSize":30}`), &old); err != nil {
		t.Fatal(err)
	}
	config, err := Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if config.ParallelEvaluation {
		t.Fatal("an old configuration unexpectedly enabled parallel evaluation")
	}
}

// TestNormalizeEvaluationWorkersFallsBackToThreads pins the documented zero
// value. Before evaluation width had its own field it was always the thread
// count, so an omitted value has to keep resolving that way for old
// checkpoints to resume with the concurrency they were written with.
func TestNormalizeEvaluationWorkersFallsBackToThreads(t *testing.T) {
	var old JobConfig
	if err := json.Unmarshal([]byte(
		`{"refPath":"reference.png","mode":"joint","circles":3,"iters":100,"popSize":30,"threads":3,"parallelEvaluation":true}`,
	), &old); err != nil {
		t.Fatal(err)
	}
	config, err := Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if config.EvaluationWorkers != 3 {
		t.Fatalf("evaluationWorkers = %d, want the thread count 3", config.EvaluationWorkers)
	}

	explicit := old
	explicit.EvaluationWorkers = 2
	config, err = Normalize(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if config.EvaluationWorkers != 2 {
		t.Fatalf("evaluationWorkers = %d, want the explicit 2 to survive", config.EvaluationWorkers)
	}
	if config.Threads != 3 {
		t.Fatalf("threads = %d, want 3; the two knobs must stay independent", config.Threads)
	}
}

func TestNormalizeAcceptsResidualRegionPolishing(t *testing.T) {
	config := DefaultConfig()
	config.RefPath = "reference.png"
	config.Mode = ModeBatch
	config.PolishingEnabled = true
	config.PolishingStrategy = PolishingResidualRegion

	normalized, err := Normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.PolishingStrategy != PolishingResidualRegion {
		t.Fatalf("polishing strategy = %q, want residual-region", normalized.PolishingStrategy)
	}
}

func TestNormalizeAcceptsContiguousWindowPolishing(t *testing.T) {
	config := DefaultConfig()
	config.RefPath = "reference.png"
	config.Mode = ModeBatch
	config.PolishingEnabled = true
	config.PolishingStrategy = PolishingContiguousWindow

	normalized, err := Normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.PolishingStrategy != PolishingContiguousWindow {
		t.Fatalf("polishing strategy = %q, want contiguous-window", normalized.PolishingStrategy)
	}
}

func TestValidateRejectsUnknownPolishingStrategy(t *testing.T) {
	config := DefaultConfig()
	config.RefPath = "reference.png"
	config.Mode = ModeBatch
	config.PolishingEnabled = true
	config.PolishingStrategy = PolishingStrategy("sliding-window")

	if _, err := Normalize(config); err == nil {
		t.Fatal("Normalize accepted an unknown polishing strategy")
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
