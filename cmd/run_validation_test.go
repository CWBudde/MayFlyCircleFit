package cmd

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

type runCommandState struct {
	refPath                  string
	canvasPath               string
	mode                     string
	backendName              string
	variantName              string
	circles                  int
	iters                    int
	popSize                  int
	optimizerEpochs          int
	batchSize                int
	polishingEnabled         bool
	polishingStrategy        string
	polishingActiveSetSize   int
	polishingMaxSweeps       int
	polishingEpochs          int
	polishingIters           int
	polishingPopSize         int
	polishingStagnationIters int
	polishingMinImprovement  float64
	threads                  int
	parallelEvaluation       bool
	evaluationWorkers        int
	fastCompositing          bool
	seed                     int64
	convergenceEnable        bool
	patience                 int
	threshold                float64
	stopTargetCost           float64
	stopMinImprovement       float64
	stopStagnationIters      int
	stopMinIters             int
	enableSSIM               bool
}

func captureRunCommandState() runCommandState {
	return runCommandState{
		refPath:                  refPath,
		canvasPath:               canvasPath,
		mode:                     mode,
		backendName:              backendName,
		variantName:              variantName,
		circles:                  circles,
		iters:                    iters,
		popSize:                  popSize,
		optimizerEpochs:          optimizerEpochs,
		batchSize:                batchSize,
		polishingEnabled:         polishingEnabled,
		polishingStrategy:        polishingStrategy,
		polishingActiveSetSize:   polishingActiveSetSize,
		polishingMaxSweeps:       polishingMaxSweeps,
		polishingEpochs:          polishingEpochs,
		polishingIters:           polishingIters,
		polishingPopSize:         polishingPopSize,
		polishingStagnationIters: polishingStagnationIters,
		polishingMinImprovement:  polishingMinImprovement,
		threads:                  threads,
		parallelEvaluation:       parallelEvaluation,
		evaluationWorkers:        evaluationWorkers,
		fastCompositing:          fastCompositing,
		seed:                     seed,
		convergenceEnable:        convergenceEnable,
		patience:                 patience,
		threshold:                threshold,
		stopTargetCost:           stopTargetCost,
		stopMinImprovement:       stopMinImprovement,
		stopStagnationIters:      stopStagnationIters,
		stopMinIters:             stopMinIters,
		enableSSIM:               enableSSIM,
	}
}

func restoreRunCommandState(state runCommandState) {
	refPath = state.refPath
	canvasPath = state.canvasPath
	mode = state.mode
	backendName = state.backendName
	variantName = state.variantName
	circles = state.circles
	iters = state.iters
	popSize = state.popSize
	optimizerEpochs = state.optimizerEpochs
	batchSize = state.batchSize
	polishingEnabled = state.polishingEnabled
	polishingStrategy = state.polishingStrategy
	polishingActiveSetSize = state.polishingActiveSetSize
	polishingMaxSweeps = state.polishingMaxSweeps
	polishingEpochs = state.polishingEpochs
	polishingIters = state.polishingIters
	polishingPopSize = state.polishingPopSize
	polishingStagnationIters = state.polishingStagnationIters
	polishingMinImprovement = state.polishingMinImprovement
	threads = state.threads
	parallelEvaluation = state.parallelEvaluation
	evaluationWorkers = state.evaluationWorkers
	fastCompositing = state.fastCompositing
	seed = state.seed
	convergenceEnable = state.convergenceEnable
	patience = state.patience
	threshold = state.threshold
	stopTargetCost = state.stopTargetCost
	stopMinImprovement = state.stopMinImprovement
	stopStagnationIters = state.stopStagnationIters
	stopMinIters = state.stopMinIters
	enableSSIM = state.enableSSIM
}

func setRunValidationDefaults(referencePath string) {
	refPath = referencePath
	canvasPath = ""
	mode = string(app.ModeJoint)
	backendName = string(app.BackendCPU)
	variantName = string(app.VariantStandard)
	circles = 10
	iters = 100
	popSize = 30
	optimizerEpochs = 1
	batchSize = 0
	polishingEnabled = false
	polishingStrategy = string(app.PolishingReplacement)
	polishingActiveSetSize = 5
	polishingMaxSweeps = app.DefaultPolishingMaxSweeps
	polishingEpochs = app.DefaultPolishingEpochs
	polishingIters = app.DefaultPolishingIters
	polishingPopSize = app.DefaultPolishingPopSize
	polishingStagnationIters = app.DefaultPolishingStagnationIters
	polishingMinImprovement = 0.001
	threads = runtime.GOMAXPROCS(0)
	parallelEvaluation = false
	evaluationWorkers = 0
	fastCompositing = false
	seed = 1
	convergenceEnable = true
	patience = 3
	threshold = 0.001
	stopTargetCost = 0
	stopMinImprovement = 0
	stopStagnationIters = 0
	stopMinIters = 0
	enableSSIM = false
}

func createSimpleRunImage(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image file: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode test image: %v", err)
	}
}

func TestRunOptimizationRejectsInvalidInputs(t *testing.T) {
	tmpDir := t.TempDir()
	validRefPath := filepath.Join(tmpDir, "reference.png")
	createSimpleRunImage(t, validRefPath)

	restore := captureRunCommandState()
	t.Cleanup(func() { restoreRunCommandState(restore) })

	tests := []struct {
		name       string
		mutate     func()
		wantErrMsg string
	}{
		{
			name:       "invalid backend",
			mutate:     func() { backendName = "quantum" },
			wantErrMsg: "backend must be one of: cpu, opencl",
		},
		{
			name:       "missing refPath",
			mutate:     func() { refPath = "" },
			wantErrMsg: "refPath is required",
		},
		{
			name:       "invalid mode",
			mutate:     func() { mode = "invalid" },
			wantErrMsg: "mode must be joint, sequential, or batch",
		},
		{
			name:       "invalid circles",
			mutate:     func() { circles = 0 },
			wantErrMsg: "circles must be between 1 and",
		},
		{
			name:       "invalid iters",
			mutate:     func() { iters = 0 },
			wantErrMsg: "iters must be between 1 and",
		},
		{
			// A flag's zero is typed, never absent, so the configuration
			// defaults must not fill it. Without that, `--threads 0` runs on
			// every core the host has instead of failing.
			name:       "invalid threads",
			mutate:     func() { threads = 0 },
			wantErrMsg: "threads must be positive",
		},
		{
			name:       "invalid optimizer epochs",
			mutate:     func() { optimizerEpochs = 0 },
			wantErrMsg: "optimizerEpochs must be between 1 and",
		},
		{
			name:   "invalid popSize",
			mutate: func() { popSize = 1 },
			// The message names the flag's configuration field, as every other
			// case here does: the API error contract spells fields with their
			// JSON names, and internal/server asserts the same "popSize".
			wantErrMsg: "popSize must be between 20 and 200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRunValidationDefaults(validRefPath)
			tt.mutate()
			err := runOptimization(nil, nil)
			if err == nil {
				t.Fatalf("runOptimization() = nil")
			}
			if !IsUsageError(err) {
				t.Fatalf("runOptimization() = %v, want usage error", err)
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErrMsg)
			}
		})
	}
}
