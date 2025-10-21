package cmd

import (
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/spf13/cobra"
)

var (
	refPath string
	outPath string
	mode    string
	circles int
	iters   int
	popSize int
	seed    int64
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run single-shot optimization",
	Long:  `Runs circle fitting optimization and writes output image and parameters.`,
	RunE:  runOptimization,
}

func init() {
	runCmd.Flags().StringVar(&refPath, "ref", "", "Reference image path (required)")
	runCmd.Flags().StringVar(&outPath, "out", "out.png", "Output image path")
	runCmd.Flags().StringVar(&mode, "mode", "joint", "Optimization mode: joint, sequential, batch")
	runCmd.Flags().IntVar(&circles, "circles", 10, "Number of circles")
	runCmd.Flags().IntVar(&iters, "iters", 100, "Max iterations")
	runCmd.Flags().IntVar(&popSize, "pop", 30, "Population size")
	runCmd.Flags().Int64Var(&seed, "seed", 42, "Random seed")

	runCmd.MarkFlagRequired("ref")
	rootCmd.AddCommand(runCmd)
}

func runOptimization(cmd *cobra.Command, args []string) error {
	slog.Info("Starting optimization", "mode", mode, "circles", circles, "iters", iters)

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
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	slog.Info("Loaded reference", "width", bounds.Dx(), "height", bounds.Dy())

	// Create renderer
	renderer := fit.NewCPURenderer(ref, circles)

	// Create optimizer
	optimizer := opt.NewMayfly(iters, popSize, seed)

	// Run optimization
	start := time.Now()
	var result *fit.OptimizationResult

	switch mode {
	case "joint":
		result = fit.OptimizeJoint(renderer, optimizer, circles)
	case "sequential":
		result = fit.OptimizeSequential(renderer, optimizer, circles)
	case "batch":
		batchSize := 5
		passes := circles / batchSize
		if circles%batchSize != 0 {
			passes++
		}
		result = fit.OptimizeBatch(renderer, optimizer, batchSize, passes)
	default:
		return fmt.Errorf("unknown mode: %s", mode)
	}

	elapsed := time.Since(start)

	// Render final image
	finalRenderer := fit.NewCPURenderer(ref, circles)
	output := finalRenderer.Render(result.BestParams)

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
	totalEvals := iters * popSize
	totalCircles := totalEvals * circles
	cps := float64(totalCircles) / elapsed.Seconds()

	slog.Info("Optimization complete",
		"elapsed", elapsed,
		"initial_cost", result.InitialCost,
		"final_cost", result.BestCost,
		"improvement", result.InitialCost-result.BestCost,
		"circles_per_second", fmt.Sprintf("%.0f", cps),
	)

	fmt.Printf("Wrote %s (cost: %.2f -> %.2f, %.0f circles/sec)\n", outPath, result.InitialCost, result.BestCost, cps)

	return nil
}
