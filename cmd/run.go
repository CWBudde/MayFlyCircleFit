package cmd

import (
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/spf13/cobra"
)

var (
	refPath           string
	canvasPath        string
	outPath           string
	mode              string
	backendName       string
	circles           int
	iters             int
	popSize           int
	seed              int64
	convergenceEnable bool
	patience          int
	threshold         float64
	cpuProfile        string
	memProfile        string
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
	runCmd.Flags().IntVar(&circles, "circles", 10, "Number of circles")
	runCmd.Flags().IntVar(&iters, "iters", 100, "Max iterations")
	runCmd.Flags().IntVar(&popSize, "pop", 30, "Population size")
	runCmd.Flags().Int64Var(&seed, "seed", 0, "Random seed (0 chooses and reports a random seed)")

	// Convergence detection flags (only used for sequential/batch modes)
	runCmd.Flags().BoolVar(&convergenceEnable, "convergence", true, "Enable adaptive convergence detection")
	runCmd.Flags().IntVar(&patience, "patience", 3, "Stop after N circles/batches with no significant improvement")
	runCmd.Flags().Float64Var(&threshold, "threshold", 0.001, "Minimum relative improvement required (0.001 = 0.1%)")

	// Profiling flags
	runCmd.Flags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file")
	runCmd.Flags().StringVar(&memProfile, "memprofile", "", "Write memory profile to file")

	runCmd.MarkFlagRequired("ref")
	rootCmd.AddCommand(runCmd)
}

func runOptimization(cmd *cobra.Command, args []string) error {
	config, err := app.Normalize(app.JobConfig{
		RefPath:              refPath,
		CanvasPath:           canvasPath,
		Mode:                 app.Mode(mode),
		Backend:              app.Backend(backendName),
		Circles:              circles,
		Iters:                iters,
		PopSize:              popSize,
		Seed:                 seed,
		ConvergenceEnabled:   convergenceEnable,
		DisableConvergence:   !convergenceEnable,
		ConvergencePatience:  patience,
		ConvergenceThreshold: threshold,
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
		cleanup = func() {} // No cleanup needed for CPU renderer
	} else {
		// Other backends don't support canvas yet
		if canvas != nil {
			return fmt.Errorf("canvas loading only supported with CPU backend")
		}
		var err error
		rend, cleanup, err = renderer.NewRendererForBackend(string(config.Backend), ref, config.Circles)
		if err != nil {
			return fmt.Errorf("failed to create renderer: %w", err)
		}
	}
	defer cleanup()

	// Create optimizer
	optimizer := opt.NewMayfly(config.Iters, config.PopSize, config.EffectiveSeed)

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

	// Compute throughput (circles rendered per second)
	// Each eval renders K circles, estimate total evals ~ iters * popSize
	totalEvals := config.Iters * config.PopSize
	totalCircles := totalEvals * actualCircles
	cps := float64(totalCircles) / elapsed.Seconds()

	slog.Info("Optimization complete",
		"elapsed", elapsed,
		"initial_cost", result.InitialCost,
		"final_cost", result.BestCost,
		"improvement", result.InitialCost-result.BestCost,
		"circles_used", actualCircles,
		"circles_requested", config.Circles,
		"seed", config.EffectiveSeed,
		"circles_per_second", fmt.Sprintf("%.0f", cps),
	)

	if actualCircles < config.Circles {
		fmt.Printf("Wrote %s (cost: %.2f -> %.2f, %d/%d circles, %.0f circles/sec) - Converged early!\n",
			outPath, result.InitialCost, result.BestCost, actualCircles, config.Circles, cps)
	} else {
		fmt.Printf("Wrote %s (cost: %.2f -> %.2f, %d circles, %.0f circles/sec)\n",
			outPath, result.InitialCost, result.BestCost, actualCircles, cps)
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
