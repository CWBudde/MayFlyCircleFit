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
	if config.PolishingStrategy != PolishingReplacement || config.PolishingActiveSetSize != 5 ||
		config.PolishingMaxSweeps != DefaultPolishingMaxSweeps || config.PolishingEpochs != DefaultPolishingEpochs ||
		config.PolishingIters != DefaultPolishingIters || config.PolishingPopSize != DefaultPolishingPopSize ||
		config.PolishingStagnationIters != DefaultPolishingStagnationIters || config.PolishingMinImprovement != 0.001 {
		t.Fatalf("polishing defaults not applied: %+v", config)
	}
	// The polishing budget is a measurement (docs/polishing-budget-report.md),
	// and two of its numbers only make sense together: an epoch that stagnates
	// for half its length stops, which is the ratio every default has shipped.
	if DefaultPolishingStagnationIters*2 != DefaultPolishingIters {
		t.Fatalf("stagnation %d is not half of the %d-iteration epoch it stops",
			DefaultPolishingStagnationIters, DefaultPolishingIters)
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
		{"polishing population low", func(c *JobConfig) { c.PolishingPopSize = MinPopulation - 1 }, "polishingPopSize"},
		{"polishing population high", func(c *JobConfig) { c.PolishingPopSize = MaxPopulation + 1 }, "polishingPopSize"},
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

// TestNormalizePolishingPopulationDoesNotInheritPopSize is the inverse of
// TestNormalizeEvaluationWorkersFallsBackToThreads, and deliberately so.
// Evaluation width kept inheriting for compatibility; the polishing population
// stops inheriting, because inheritance is the defect it was added to remove: a
// run sized for a whole vector spent that population on one active set. A
// checkpoint written before the field therefore polishes at the measured default
// rather than at its own popSize.
func TestNormalizePolishingPopulationDoesNotInheritPopSize(t *testing.T) {
	var old JobConfig
	if err := json.Unmarshal([]byte(
		`{"refPath":"reference.png","mode":"batch","circles":8,"iters":100,"popSize":200,"batchSize":8}`,
	), &old); err != nil {
		t.Fatal(err)
	}
	config, err := Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if config.PolishingPopSize != DefaultPolishingPopSize {
		t.Fatalf("polishingPopSize = %d, want the default %d", config.PolishingPopSize, DefaultPolishingPopSize)
	}
	if config.PopSize != 200 {
		t.Fatalf("popSize = %d, want the written 200 to survive", config.PopSize)
	}

	explicit := old
	explicit.PolishingPopSize = 50
	config, err = Normalize(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if config.PolishingPopSize != 50 {
		t.Fatalf("polishingPopSize = %d, want the explicit 50 to survive", config.PolishingPopSize)
	}
	if config.PopSize != 200 {
		t.Fatalf("popSize = %d, want 200; the two knobs must stay independent", config.PopSize)
	}
}

// TestValidateAcceptsAnOmittedPolishingPopulation covers the restore boundary.
// A checkpoint written before polishingPopSize existed carries zero, and
// jobFromCheckpoint hands that configuration out without normalizing it — so
// `status`, which validates what the API returned, must be able to display such
// a job rather than failing on it. Zero is the omitted value, not a population.
func TestValidateAcceptsAnOmittedPolishingPopulation(t *testing.T) {
	legacy := DefaultConfig()
	legacy.RefPath = "reference.png"
	legacy.PolishingPopSize = 0
	if err := legacy.Validate(); err != nil {
		t.Fatalf("Validate() rejected an omitted polishingPopSize: %v", err)
	}

	// The bounds still apply to a value that was actually written.
	legacy.PolishingPopSize = MinPopulation - 1
	if err := legacy.Validate(); err == nil {
		t.Fatalf("Validate() accepted polishingPopSize %d", MinPopulation-1)
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

// TestNormalizeRequestRefusesAWrittenDefault covers the difference a decoded
// struct cannot see: a caller who omitted a field wants the default, and a
// caller who wrote a zero wants zero. The first is filled, the second is an
// error naming the field, because filling it would run something other than
// what was asked for.
func TestNormalizeRequestRefusesAWrittenDefault(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "omitted fields are defaulted",
			body: `{"refPath": "assets/ref.png"}`,
		},
		{
			name: "an unrelated envelope key is ignored",
			body: `{"refPath": "assets/ref.png", "project": "default"}`,
		},
		{
			name:    "a written zero is not an omission",
			body:    `{"refPath": "assets/ref.png", "circles": 0}`,
			wantErr: "circles",
		},
		{
			name:    "a written zero is refused for every defaulted field",
			body:    `{"refPath": "assets/ref.png", "iters": 0}`,
			wantErr: "iters",
		},
		{
			// The reason a value that survives the defaults stays accepted: it
			// is the value the run uses, so nothing is being swallowed.
			name: "a written value the defaults keep is accepted",
			body: `{"refPath": "assets/ref.png", "circles": 32}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var config JobConfig
			if err := json.Unmarshal([]byte(test.body), &config); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			normalized, err := NormalizeRequest([]byte(test.body), config)
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("NormalizeRequest() accepted %s, circles = %d", test.body, normalized.Circles)
				}
				if !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %q, want it to name %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeRequest() error = %v", err)
			}
			if normalized.Circles < 1 || normalized.Iters < 1 || normalized.PopSize < MinPopulation {
				t.Fatalf("omitted fields were not defaulted: %+v", normalized)
			}
		})
	}
}
