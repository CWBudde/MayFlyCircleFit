package cmd

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"math"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/spf13/cobra"
)

var (
	refPath                  string
	canvasPath               string
	outPath                  string
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
	enableSSIM               bool

	stopTargetCost      float64
	stopMinImprovement  float64
	stopStagnationIters int
	stopMinIters        int
	cpuProfile          string
	memProfile          string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run single-shot optimization",
	Long:  `Runs circle fitting optimization and writes output image and parameters.`,
	RunE:  runOptimization,
}

func init() {
	runCmd.Flags().StringVar(&refPath, "ref", "", "Reference image path (required)")
	runCmd.Flags().StringVar(&canvasPath, "canvas", "", "Canvas image path (optional: start from existing result)")
	runCmd.Flags().StringVar(&outPath, "out", "out.png", "Output image path")
	runCmd.Flags().StringVar(&mode, "mode", "joint", "Optimization mode: joint, sequential, batch")
	runCmd.Flags().StringVar(&backendName, "backend", "cpu", "Renderer backend to use (cpu, opencl)")
	runCmd.Flags().StringVar(&variantName, "variant", "standard", "MayFly algorithm variant: standard, desma, olce")
	runCmd.Flags().IntVar(&circles, "circles", 10, "Number of circles")
	runCmd.Flags().IntVar(&iters, "iters", 100, "Max iterations")
	runCmd.Flags().IntVar(&popSize, "pop", 30, "Population size")
	runCmd.Flags().IntVar(&optimizerEpochs, "optimizer-epochs", 1, "Optimizer runs per stage, reseeding each continuation from the best result")
	runCmd.Flags().IntVar(&batchSize, "batch-size", 0, "Circles optimized together in batch mode (0 selects the automatic default)")
	runCmd.Flags().BoolVar(&polishingEnabled, "polishing", false, "Polish weak circles transactionally after a batch run")
	runCmd.Flags().StringVar(&polishingStrategy, "polishing-strategy", "replacement", "Polishing strategy: replacement, hybrid-overlap, residual-region, or contiguous-window")
	runCmd.Flags().IntVar(&polishingActiveSetSize, "polishing-active-set-size", 5, "Circles optimized together in each polishing sweep")
	runCmd.Flags().IntVar(&polishingMaxSweeps, "polishing-max-sweeps", app.DefaultPolishingMaxSweeps, "Maximum transactional polishing sweeps")
	runCmd.Flags().IntVar(&polishingEpochs, "polishing-epochs", app.DefaultPolishingEpochs, "Optimizer epochs per polishing sweep")
	runCmd.Flags().IntVar(&polishingIters, "polishing-iters", app.DefaultPolishingIters, "Iterations per polishing epoch")
	runCmd.Flags().IntVar(&polishingPopSize, "polishing-pop", app.DefaultPolishingPopSize, "Population a polishing sweep optimizes its active set with (--pop sizes the whole vector instead)")
	runCmd.Flags().IntVar(&polishingStagnationIters, "polishing-stagnation-iters", app.DefaultPolishingStagnationIters, "Stop a polishing epoch after this many iterations without sufficient progress")
	runCmd.Flags().Float64Var(&polishingMinImprovement, "polishing-min-improvement", 0.001, "Absolute optimizer cost reduction counted as progress during polishing")
	runCmd.Flags().IntVar(&threads, "threads", runtime.GOMAXPROCS(0), "CPU rendering threads (capped at GOMAXPROCS)")
	runCmd.Flags().BoolVar(&parallelEvaluation, "parallel-evaluation", false, "Evaluate optimizer population members concurrently over independent renderer sessions (reproducible per seed, but not identical to a serial run of the same seed)")
	runCmd.Flags().IntVar(&evaluationWorkers, "evaluation-workers", 0, "Concurrent cost evaluations when --parallel-evaluation is set, capped at GOMAXPROCS (0 uses --threads). Each worker holds its own full-size canvas")
	runCmd.Flags().BoolVar(&fastCompositing, "fast-compositing", false, "Use the reduced-precision float32 SIMD span compositor (output may differ by 1 per channel)")
	runCmd.Flags().Int64Var(&seed, "seed", 0, "Random seed (0 chooses and reports a random seed)")
	runCmd.Flags().BoolVar(&enableSSIM, "enable-ssim", false, "Calculate the optional final structural similarity metric")

	// Stage-level convergence detection (only used for sequential/batch modes)
	runCmd.Flags().BoolVar(&convergenceEnable, "convergence", true, "Enable adaptive convergence detection")
	runCmd.Flags().IntVar(&patience, "patience", 3, "Stage-level: stop after N circles/batches with no significant improvement (sequential/batch only)")
	runCmd.Flags().Float64Var(&threshold, "threshold", 0.001, "Stage-level: minimum RELATIVE improvement ratio per circle/batch (0.001 = 0.1%)")

	// Optimizer-level early stopping, evaluated per iteration within one
	// optimizer run. Disabled by default so runs stay reproducible.
	runCmd.Flags().Float64Var(&stopTargetCost, "stop-target-cost", 0, "Stop the optimizer once the best cost reaches this absolute value (0 disables)")
	runCmd.Flags().Float64Var(&stopMinImprovement, "stop-min-improvement", 0, "Optimizer-level: ABSOLUTE cost reduction per iteration counted as progress (0 accepts any improvement)")
	runCmd.Flags().IntVar(&stopStagnationIters, "stop-stagnation-iters", 0, "Optimizer-level: stop after N consecutive iterations without progress (0 disables)")
	runCmd.Flags().IntVar(&stopMinIters, "stop-min-iters", 0, "Optimizer-level: minimum iterations before any early stop can fire")

	// Profiling flags
	runCmd.Flags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file")
	runCmd.Flags().StringVar(&memProfile, "memprofile", "", "Write memory profile to file")

	runCmd.MarkFlagRequired("ref")
	rootCmd.AddCommand(runCmd)
}

// parallelEvaluationOption derives the optimizer option from what the renderer
// can actually deliver and warns when an explicit request cannot be honored,
// which is the case for every backend without independent sessions.
func parallelEvaluationOption(config app.JobConfig, rend renderer.Renderer) opt.MayflyOption {
	option, enabled := renderer.ParallelEvaluationOption(rend, config.ParallelEvaluation)
	if config.ParallelEvaluation && !enabled {
		slog.Warn("Parallel evaluation requested but unavailable; evaluating serially",
			"backend", config.Backend, "evaluationWorkers", config.EvaluationWorkers)
	}
	return option
}

// earlyStopFromConfig maps the optimizer-level stopping fields onto the adapter
// option. A configuration that sets none of them yields a zero Stop, which
// leaves the optimizer unchanged.
func earlyStopFromConfig(config app.JobConfig) opt.Stop {
	return opt.Stop{
		TargetCost:      config.StopTargetCost,
		MinImprovement:  config.StopMinImprovement,
		StagnationIters: config.StopStagnationIters,
		MinIters:        config.StopMinIters,
	}
}

func runOptimization(cmd *cobra.Command, args []string) error {
	config, err := app.Normalize(app.JobConfig{
		RefPath:                  refPath,
		CanvasPath:               canvasPath,
		Mode:                     app.Mode(mode),
		Backend:                  app.Backend(backendName),
		Variant:                  app.Variant(variantName),
		Circles:                  circles,
		Iters:                    iters,
		PopSize:                  popSize,
		OptimizerEpochs:          optimizerEpochs,
		BatchSize:                batchSize,
		PolishingEnabled:         polishingEnabled,
		PolishingStrategy:        app.PolishingStrategy(polishingStrategy),
		PolishingActiveSetSize:   polishingActiveSetSize,
		PolishingMaxSweeps:       polishingMaxSweeps,
		PolishingEpochs:          polishingEpochs,
		PolishingIters:           polishingIters,
		PolishingPopSize:         polishingPopSize,
		PolishingStagnationIters: polishingStagnationIters,
		PolishingMinImprovement:  polishingMinImprovement,
		Threads:                  threads,
		ParallelEvaluation:       parallelEvaluation,
		EvaluationWorkers:        evaluationWorkers,
		FastCompositing:          fastCompositing,
		Seed:                     seed,
		EnableSSIM:               enableSSIM,
		ConvergenceEnabled:       convergenceEnable,
		DisableConvergence:       !convergenceEnable,
		ConvergencePatience:      patience,
		ConvergenceThreshold:     threshold,
		StopTargetCost:           stopTargetCost,
		StopMinImprovement:       stopMinImprovement,
		StopStagnationIters:      stopStagnationIters,
		StopMinIters:             stopMinIters,
	})
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Start CPU profiling if requested
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			return fmt.Errorf("failed to create CPU profile: %w", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("failed to start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()
		slog.Info("CPU profiling enabled", "output", cpuProfile)
	}

	slog.Info("Starting optimization", "mode", config.Mode, "circles", config.Circles, "iters", config.Iters, "backend", config.Backend, "seed", config.EffectiveSeed)

	// Load reference image
	f, err := os.Open(refPath)
	if err != nil {
		return fmt.Errorf("failed to open reference: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Convert to NRGBA
	bounds := img.Bounds()
	if err := app.ValidateImageDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return err
	}
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	slog.Info("Loaded reference", "width", bounds.Dx(), "height", bounds.Dy())

	// Load canvas image if specified
	var canvas *image.NRGBA
	if canvasPath != "" {
		slog.Info("Loading canvas", "path", canvasPath)

		canvasFile, err := os.Open(canvasPath)
		if err != nil {
			return fmt.Errorf("failed to open canvas: %w", err)
		}
		defer canvasFile.Close()

		canvasImg, _, err := image.Decode(canvasFile)
		if err != nil {
			return fmt.Errorf("failed to decode canvas: %w", err)
		}
		if canvasImg.Bounds().Dx() != bounds.Dx() || canvasImg.Bounds().Dy() != bounds.Dy() {
			return fmt.Errorf("canvas dimensions %dx%d do not match reference %dx%d", canvasImg.Bounds().Dx(), canvasImg.Bounds().Dy(), bounds.Dx(), bounds.Dy())
		}

		// Convert canvas to NRGBA
		canvas = image.NewNRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				canvas.Set(x, y, canvasImg.At(x, y))
			}
		}

		slog.Info("Loaded canvas", "width", canvas.Bounds().Dx(), "height", canvas.Bounds().Dy())
	}

	// Create renderer (with or without canvas)
	var rend renderer.Renderer
	var cleanup func()

	if config.Backend == app.BackendCPU {
		// CPU renderer supports canvas
		if canvas != nil {
			rend = renderer.NewCPURendererWithCanvas(ref, canvas, config.Circles)
		} else {
			rend = renderer.NewCPURenderer(ref, config.Circles)
		}
		cpuRenderer := rend.(*renderer.CPURenderer)
		renderer.ConfigureCPUParallelism(cpuRenderer, config.Threads, config.EvaluationWorkers, config.ParallelEvaluation)
		renderer.ConfigureCPUCompositing(cpuRenderer, config.FastCompositing)
		renderer.LogCPURendererConfiguration(cpuRenderer)
		cleanup = func() {} // No cleanup needed for CPU renderer
	} else {
		// Other backends don't support canvas yet
		if canvas != nil {
			return fmt.Errorf("canvas loading only supported with CPU backend")
		}
		// The compositing and parallelism knobs are CPU-renderer settings. A
		// non-CPU backend ignores them, which is fine, but silently ignoring a
		// flag the user set is not.
		if config.FastCompositing {
			slog.Warn("--fast-compositing applies to the CPU backend only and is ignored here",
				"backend", config.Backend)
		}
		var err error
		rend, cleanup, err = renderer.NewRendererForBackend(string(config.Backend), ref, config.Circles)
		if err != nil {
			return fmt.Errorf("failed to create renderer: %w", err)
		}
	}
	defer cleanup()

	// Create optimizer
	optimizer, err := opt.NewMayflyVariant(string(config.Variant), config.Iters, config.PopSize, config.EffectiveSeed,
		opt.WithLogger(slog.Default()), opt.WithEarlyStop(earlyStopFromConfig(config)),
		parallelEvaluationOption(config, rend))
	if err != nil {
		return fmt.Errorf("create optimizer: %w", err)
	}
	optimizer = opt.WithEpochs(optimizer, config.OptimizerEpochs)

	// Create convergence config
	convergenceConfig := renderer.ConvergenceConfig{
		Enabled:   config.ConvergenceEnabled,
		Patience:  config.ConvergencePatience,
		Threshold: config.ConvergenceThreshold,
	}

	if config.Mode == app.ModeJoint && config.ConvergenceEnabled {
		slog.Info("Convergence detection not applicable to joint mode (ignored)")
	}

	// Run optimization
	start := time.Now()
	var result *renderer.OptimizationResult

	switch config.Mode {
	case app.ModeJoint:
		result, err = renderer.OptimizeJoint(rend, optimizer, config.Circles, convergenceConfig)
	case app.ModeSequential:
		result, err = renderer.OptimizeSequential(rend, optimizer, config.Circles, convergenceConfig, nil)
	case app.ModeBatch:
		result, err = renderer.OptimizeBatch(rend, optimizer, config.Circles, config.BatchSize, convergenceConfig)
		if err == nil && config.PolishingEnabled && result.OptimizedCircles == config.Circles {
			var polishOptimizer opt.Optimizer
			polishOptimizer, err = opt.NewMayflyVariant(string(app.VariantStandard), config.PolishingIters, config.PolishingPopSize, config.EffectiveSeed,
				opt.WithLogger(slog.Default()),
				opt.WithEarlyStop(opt.Stop{
					MinImprovement:  config.PolishingMinImprovement,
					StagnationIters: config.PolishingStagnationIters,
				}),
			)
			if err == nil {
				polishOptimizer = opt.WithEpochs(polishOptimizer, config.PolishingEpochs)
				var polished *renderer.BatchPolishResult
				polished, err = renderer.PolishCircleBatchContext(cmd.Context(), rend, polishOptimizer, result.BestParams, renderer.BatchPolishOptions{
					ActiveSetSize: config.PolishingActiveSetSize,
					MaxSweeps:     config.PolishingMaxSweeps,
					Strategy:      renderer.BatchPolishStrategy(config.PolishingStrategy),
				})
				if err == nil {
					result.BestParams = polished.BestParams
					result.BestCost = polished.BestCost
					result.BestImage = polished.BestImage
					result.Iterations += polished.Iterations
					result.Evaluations += polished.Evaluations
					result.Stages += polished.Sweeps
					slog.Info("Batch polishing complete",
						"sweeps", polished.Sweeps,
						"accepted_sweeps", polished.AcceptedSweeps,
						"population", config.PolishingPopSize,
						"best_cost", polished.BestCost)
				}
			}
		}
	default:
		return fmt.Errorf("unknown mode: %s", config.Mode)
	}
	if err != nil {
		return fmt.Errorf("optimization failed: %w", err)
	}

	elapsed := time.Since(start)

	// BestImage is rendered by the same backend session and base canvas used by
	// the objective, so the saved output cannot diverge from the reported cost.
	actualCircles := len(result.BestParams) / 7
	output := result.BestImage
	if output == nil {
		return fmt.Errorf("optimizer returned no final image")
	}

	// Save output
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create output: %w", err)
	}
	defer outFile.Close()

	if err := png.Encode(outFile, output); err != nil {
		return fmt.Errorf("failed to encode output: %w", err)
	}

	// Compute throughput (circles rendered per second) from the measured
	// evaluation count. An iters*popSize estimate overstates the work whenever a
	// run stops before its iteration budget.
	totalCircles := result.Evaluations * actualCircles
	cps := float64(totalCircles) / elapsed.Seconds()
	psnr := fit.PSNR(result.BestCost)
	var ssim *float64
	if config.EnableSSIM {
		value, err := fit.SSIM(output, ref)
		if err != nil {
			return fmt.Errorf("calculate final SSIM: %w", err)
		}
		ssim = &value
	}

	logAttrs := []any{
		"elapsed", elapsed,
		"initial_cost", result.InitialCost,
		"final_cost", result.BestCost,
		"improvement", result.InitialCost - result.BestCost,
		"circles_used", actualCircles,
		"circles_requested", config.Circles,
		"seed", config.EffectiveSeed,
		"evaluations", result.Evaluations,
		"termination", result.Termination,
		"circles_per_second", fmt.Sprintf("%.0f", cps),
		"psnr_db", psnr,
	}
	if ssim != nil {
		logAttrs = append(logAttrs, "ssim", *ssim)
	}
	slog.Info("Optimization complete", logAttrs...)

	qualitySummary := fmt.Sprintf(", PSNR %.2f dB", psnr)
	if math.IsInf(psnr, 1) {
		qualitySummary = ", PSNR ∞ dB"
	}
	if ssim != nil {
		qualitySummary += fmt.Sprintf(", SSIM %.4f", *ssim)
	}

	if actualCircles < config.Circles {
		fmt.Printf("Wrote %s (cost: %.2f -> %.2f%s, %d/%d circles, %.0f circles/sec) - Converged early!\n",
			outPath, result.InitialCost, result.BestCost, qualitySummary, actualCircles, config.Circles, cps)
	} else {
		fmt.Printf("Wrote %s (cost: %.2f -> %.2f%s, %d circles, %.0f circles/sec)\n",
			outPath, result.InitialCost, result.BestCost, qualitySummary, actualCircles, cps)
	}

	// Write memory profile if requested
	if memProfile != "" {
		f, err := os.Create(memProfile)
		if err != nil {
			return fmt.Errorf("failed to create memory profile: %w", err)
		}
		defer f.Close()
		runtime.GC() // Run GC to get accurate heap stats
		if err := pprof.WriteHeapProfile(f); err != nil {
			return fmt.Errorf("failed to write memory profile: %w", err)
		}
		slog.Info("Memory profile written", "output", memProfile)
	}

	return nil
}
