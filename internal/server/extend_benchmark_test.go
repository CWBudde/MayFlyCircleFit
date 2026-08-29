package server

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

const (
	extendBenchmarkWidth      = 512
	extendBenchmarkHeight     = 512
	extendBenchmarkPopulation = 30
)

// BenchmarkSingleCircleExtendTerms keeps the inherited vector at production
// sizes while separating work paid once per extension from one optimizer
// iteration (30 candidate evaluations). Run it with -benchtime=1x when
// comparing all fixed terms: filesystem fsync latency otherwise determines a
// different b.N for the persistence cases than for renderer setup.
func BenchmarkSingleCircleExtendTerms(b *testing.B) {
	for _, circleCount := range []int{500, 2_000, 3_000} {
		b.Run(fmt.Sprintf("circles=%d", circleCount), func(b *testing.B) {
			params := extendBenchmarkParams(circleCount)
			reference := extendBenchmarkReference(params)
			config := extendBenchmarkConfig(circleCount)
			prefixRenderer := renderer.NewCPURenderer(reference, circleCount)
			prefixRenderer.SetThreads(1)
			prefixCanvas := cloneBenchmarkImage(prefixRenderer.Render(params))
			prefixCost := fit.FastMSECost(prefixCanvas, reference)

			b.Run("fixed/validation", func(b *testing.B) {
				bounds := fit.NewBounds(circleCount, extendBenchmarkWidth, extendBenchmarkHeight)

				b.ResetTimer()

				for b.Loop() {
					if !bounds.ValidVector(params) {
						b.Fatal("benchmark parameter vector is invalid")
					}
				}
			})

			b.Run("avoided/prefix-replay", func(b *testing.B) {
				for b.Loop() {
					cpu := renderer.NewCPURenderer(reference, circleCount)
					cpu.SetThreads(1)

					if cost := cpu.Cost(params); cost < 0 {
						b.Fatal("negative cost")
					}

					if image := cpu.Render(params); image == nil {
						b.Fatal("nil prefix image")
					}
				}
			})

			b.Run("fixed/append-pipeline", func(b *testing.B) {
				for b.Loop() {
					cpu := renderer.NewCPURenderer(reference, circleCount+1)
					cpu.SetThreads(1)

					result, err := renderer.OptimizeBatchAppendFromCanvasContext(
						context.Background(), cpu, fixedExtendOptimizer{candidate: extendBenchmarkTruthCandidate()}, params,
						prefixCanvas, prefixCost, circleCount+1, 1, renderer.DisabledConvergenceConfig(),
					)
					if err != nil {
						b.Fatal(err)
					}

					if result.BestImage == nil {
						b.Fatal("append returned no final image")
					}
				}
			})

			b.Run("fixed/prefix-artifact-load", func(b *testing.B) {
				fsStore, err := store.NewFSStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}

				jobID := "00000000-0000-4000-8000-000000000158"

				err = fsStore.SavePNGArtifact(jobID, store.ArtifactBest, prefixCanvas)
				if err != nil {
					b.Fatal(err)
				}

				path, err := fsStore.ArtifactPath(jobID, store.ArtifactBest)
				if err != nil {
					b.Fatal(err)
				}

				b.ResetTimer()

				for b.Loop() {
					loaded, err := loadReferenceImage(path)
					if err != nil {
						b.Fatal(err)
					}

					if cost := fit.FastMSECost(loaded, reference); cost != prefixCost {
						b.Fatalf("loaded prefix cost = %v, want %v", cost, prefixCost)
					}
				}
			})

			b.Run("fixed/checkpoint-json", func(b *testing.B) {
				fsStore, err := store.NewFSStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}

				jobID := "00000000-0000-4000-8000-000000000159"
				checkpoint := store.NewCheckpoint(jobID, params, 1, 2, 500, config)
				checkpoint.Evaluations = 15_000

				b.ResetTimer()

				for b.Loop() {
					err := fsStore.SaveCheckpoint(jobID, checkpoint)
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("fixed/trace-append", func(b *testing.B) {
				fsStore, err := store.NewFSStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}

				jobID := "00000000-0000-4000-8000-000000000160"
				entry := store.TraceEntry{Iteration: 1, Cost: 1, Timestamp: time.Unix(1, 0)}

				for b.Loop() {
					writer, err := fsStore.NewTraceWriter(jobID, false)
					if err != nil {
						b.Fatal(err)
					}

					err = writer.Write(entry)
					if err != nil {
						b.Fatal(err)
					}

					err = writer.Write(entry)
					if err != nil {
						b.Fatal(err)
					}

					err = writer.Close()
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("fixed/artifacts", func(b *testing.B) {
				fsStore, err := store.NewFSStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}

				jobID := "00000000-0000-4000-8000-000000000161"
				cpu := renderer.NewCPURenderer(reference, circleCount)
				cpu.SetThreads(1)
				b.ResetTimer()

				for b.Loop() {
					err := saveCheckpointArtifacts(fsStore, cpu, config, jobID, params, prefixCanvas)
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("candidate-render-batch/30", func(b *testing.B) {
				prefix := renderer.NewCPURenderer(reference, circleCount)
				prefix.SetThreads(1)
				canvas := cloneBenchmarkImage(prefix.Render(params))
				suffix := renderer.NewCPURendererWithCanvas(reference, canvas, 1)
				suffix.SetThreads(1)

				candidates := extendBenchmarkCandidates(extendBenchmarkPopulation)

				b.ResetTimer()

				for b.Loop() {
					for _, candidate := range candidates {
						if cost := suffix.Cost(candidate); cost < 0 {
							b.Fatal("negative cost")
						}
					}
				}
			})

			b.Run("iteration/mayfly-pop30", func(b *testing.B) {
				const measuredIterations = 50
				logger := slog.New(slog.DiscardHandler)
				var fixedElapsed, optimizedElapsed time.Duration

				for b.Loop() {
					fixedBase := renderer.NewCPURenderer(reference, circleCount+1)
					renderer.ConfigureCPUParallelism(fixedBase, 1, 12, true)

					started := time.Now()

					_, err := renderer.OptimizeBatchAppendFromCanvasContext(
						context.Background(), fixedBase, fixedExtendOptimizer{candidate: extendBenchmarkTruthCandidate()},
						params, prefixCanvas, prefixCost, circleCount+1, 1, renderer.DisabledConvergenceConfig(),
					)
					if err != nil {
						b.Fatal(err)
					}

					fixedElapsed += time.Since(started)

					optimizedBase := renderer.NewCPURenderer(reference, circleCount+1)
					renderer.ConfigureCPUParallelism(optimizedBase, 1, 12, true)

					parallel, enabled := renderer.ParallelEvaluationOption(optimizedBase, true)
					if !enabled {
						b.Fatal("parallel evaluation unavailable")
					}

					optimizer, err := opt.NewMayflyVariant(
						string(app.VariantStandard), measuredIterations, extendBenchmarkPopulation, 15_900,
						opt.WithLogger(logger), parallel,
					)
					if err != nil {
						b.Fatal(err)
					}

					started = time.Now()

					result, err := renderer.OptimizeBatchAppendFromCanvasContext(
						context.Background(), optimizedBase, optimizer,
						params, prefixCanvas, prefixCost, circleCount+1, 1, renderer.DisabledConvergenceConfig(),
					)
					if err != nil {
						b.Fatal(err)
					}

					optimizedElapsed += time.Since(started)
					extendBenchmarkCostSink += result.BestCost
				}

				netIteration := max(optimizedElapsed-fixedElapsed, 0)

				b.ReportMetric(float64(fixedElapsed.Nanoseconds())/float64(b.N), "fixed-ns/op")
				b.ReportMetric(
					float64(netIteration.Nanoseconds())/float64(b.N*measuredIterations),
					"ns/iteration",
				)
			})
		})
	}
}

// BenchmarkSingleCircleExtendWall re-measures the original Task 15.9 table
// through the complete worker path: reference and retained-artifact loading,
// a parallel Mayfly run, trace writes, the final checkpoint, and PNG artifacts.
// It is intentionally pinned to the original 2,000-circle, population-30
// shape; use -benchtime=1x because each result is one complete extension.
func BenchmarkSingleCircleExtendWall(b *testing.B) {
	const circleCount = 2_000
	params := extendBenchmarkParams(circleCount)
	reference := extendBenchmarkReference(params)
	prefixRenderer := renderer.NewCPURenderer(reference, circleCount)
	prefixRenderer.SetThreads(1)
	prefixCanvas := cloneBenchmarkImage(prefixRenderer.Render(params))
	prefixCost := fit.FastMSECost(prefixCanvas, reference)

	for _, iterations := range []int{50, 500, 1_500} {
		b.Run(fmt.Sprintf("circles=%d/iters=%d", circleCount, iterations), func(b *testing.B) {
			fsStore, err := store.NewFSStore(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}

			parentID := "00000000-0000-4000-8000-000000000166"
			referenceID := "00000000-0000-4000-8000-000000000167"

			err = fsStore.SavePNGArtifact(parentID, store.ArtifactBest, prefixCanvas)
			if err != nil {
				b.Fatal(err)
			}

			err = fsStore.SavePNGArtifact(referenceID, store.ArtifactBest, reference)
			if err != nil {
				b.Fatal(err)
			}

			referencePath, err := fsStore.ArtifactPath(referenceID, store.ArtifactBest)
			if err != nil {
				b.Fatal(err)
			}

			config := extendBenchmarkConfig(circleCount + 1)
			config.RefPath = referencePath
			config.Iters = iterations
			config.EnableTrace = true
			config.ParallelEvaluation = true
			config.EvaluationWorkers = 12

			b.ResetTimer()

			for b.Loop() {
				manager := NewJobManager()

				job := manager.CreateJob(app.DefaultProject, config)

				err := manager.UpdateJob(job.ID, func(live *Job) {
					updateBestResult(live, params, prefixCost)
					live.InitialCost = prefixCost + 1
					live.ExtendedFrom = parentID
				})
				if err != nil {
					b.Fatal(err)
				}

				err = runJob(context.Background(), manager, fsStore, job.ID)
				if err != nil {
					b.Fatal(err)
				}

				completed, ok := manager.GetJob(job.ID)
				if !ok {
					b.Fatal("completed extension job is missing")
				}

				if completed.State != StateCompleted || completed.ActualCircles != circleCount+1 {
					b.Fatalf("extension result = state %v circles %d", completed.State, completed.ActualCircles)
				}
			}
		})
	}
}

// BenchmarkSingleCircleExtendProductionCheckpoint runs the wall-clock table
// against an operator-supplied production checkpoint without making that large
// and potentially private fixture part of the repository. The source store is
// read-only; each benchmark case copies best.png into its own temporary store.
//
//	CIRCLEFIT_EXTEND_CHECKPOINT=/path/to/jobs/<uuid>/checkpoint.json \
//	  go test -run '^$' -bench '^BenchmarkSingleCircleExtendProductionCheckpoint$' \
//	  -benchtime=1x -count=1 ./internal/server
func BenchmarkSingleCircleExtendProductionCheckpoint(b *testing.B) {
	checkpointPath := os.Getenv("CIRCLEFIT_EXTEND_CHECKPOINT")
	if checkpointPath == "" {
		b.Skip("set CIRCLEFIT_EXTEND_CHECKPOINT to a production checkpoint.json")
	}

	data, err := os.ReadFile(checkpointPath)
	if err != nil {
		b.Fatal(err)
	}

	var checkpoint store.Checkpoint

	err = json.Unmarshal(data, &checkpoint)
	if err != nil {
		b.Fatal(err)
	}

	err = checkpoint.Validate()
	if err != nil {
		b.Fatal(err)
	}

	reference, err := loadReferenceImage(checkpoint.Config.RefPath)
	if err != nil {
		b.Fatal(err)
	}

	prefixCanvas, err := loadReferenceImage(filepath.Join(filepath.Dir(checkpointPath), string(store.ArtifactBest)))
	if err != nil {
		b.Fatal(err)
	}

	if cost := fit.FastMSECost(prefixCanvas, reference); cost != checkpoint.BestCost {
		b.Fatalf("production best artifact cost = %v, checkpoint = %v", cost, checkpoint.BestCost)
	}

	for _, iterations := range []int{50, 500, 1_500} {
		b.Run(fmt.Sprintf("circles=%d/iters=%d", checkpoint.ActualCircles, iterations), func(b *testing.B) {
			fsStore, err := store.NewFSStore(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}

			err = fsStore.SavePNGArtifact(checkpoint.JobID, store.ArtifactBest, prefixCanvas)
			if err != nil {
				b.Fatal(err)
			}

			config := checkpoint.Config
			config.Circles = checkpoint.ActualCircles + 1
			config.BatchSize = 1
			config.Iters = iterations
			config.OptimizerEpochs = 1
			config.PolishingEnabled = false
			config.PolishingOnly = false
			config.StopTargetCost = 0
			config.StopMinImprovement = 0
			config.StopStagnationIters = 0
			config.StopMinIters = 0
			config.EnableTrace = true
			config.CheckpointInterval = 0

			b.ResetTimer()

			for b.Loop() {
				manager := NewJobManager()

				job := manager.CreateJob(app.DefaultProject, config)

				err := manager.UpdateJob(job.ID, func(live *Job) {
					updateBestResult(live, checkpoint.BestParams, checkpoint.BestCost)
					live.InitialCost = checkpoint.InitialCost
					live.Iterations = checkpoint.Iterations
					live.Evaluations = int(checkpoint.Evaluations)
					live.ExtendedFrom = checkpoint.JobID
				})
				if err != nil {
					b.Fatal(err)
				}

				err = runJob(context.Background(), manager, fsStore, job.ID)
				if err != nil {
					b.Fatal(err)
				}

				completed, ok := manager.GetJob(job.ID)
				if !ok || completed.State != StateCompleted {
					b.Fatal("production extension did not complete")
				}
			}
		})
	}
}

// TestCheckpointRoundTripPreservesContinuationCost pins the Task 15.9
// correctness boundary: JSON persistence must preserve every float bit needed
// to reproduce the parent's renderer cost before an extension starts.
func TestCheckpointRoundTripPreservesContinuationCost(t *testing.T) {
	t.Parallel()

	const circleCount = 32
	params := extendBenchmarkParams(circleCount)
	reference := extendBenchmarkReference(params)
	cpu := renderer.NewCPURenderer(reference, circleCount)
	cpu.SetThreads(1)
	parentCost := cpu.Cost(params)

	fsStore, err := store.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	jobID := "00000000-0000-4000-8000-000000000162"

	checkpoint := store.NewCheckpoint(jobID, params, parentCost, parentCost+1, 500, extendBenchmarkConfig(circleCount))

	err = fsStore.SaveCheckpoint(jobID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := fsStore.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatal(err)
	}

	resumed := renderer.NewCPURenderer(reference, loaded.ActualCircles)
	resumed.SetThreads(1)

	if got := resumed.Cost(loaded.BestParams); got != parentCost {
		t.Fatalf("resumed parent cost = %.17g, want exact %.17g", got, parentCost)
	}
}

type fixedExtendOptimizer struct {
	candidate []float64
}

func (fixedExtendOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	panic("fixedExtendOptimizer requires lifecycle execution")
}

func (o fixedExtendOptimizer) RunContext(_ context.Context, _ opt.Problem, options opt.RunOptions) (opt.Result, error) {
	return opt.Result{
		BestParams:  append([]float64(nil), o.candidate...),
		BestCost:    options.Initial.Cost,
		Termination: opt.TerminationCompleted,
	}, nil
}

func extendBenchmarkConfig(circleCount int) app.JobConfig {
	config := app.DefaultConfig()
	config.RefPath = "benchmark-reference.png"
	config.Mode = app.ModeBatch
	config.Circles = circleCount
	config.BatchSize = 1
	config.Iters = 500
	config.PopSize = extendBenchmarkPopulation
	config.OptimizerEpochs = 1
	config.Threads = 1
	config.Seed = 15_900
	config.EffectiveSeed = 15_900
	config.DisableConvergence = true
	config.ConvergenceEnabled = false

	return config
}

func extendBenchmarkReference(prefix []float64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, extendBenchmarkWidth, extendBenchmarkHeight))
	for y := range extendBenchmarkHeight {
		for x := range extendBenchmarkWidth {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*5 + y*3) & 0xff),
				G: uint8((x*2 + y*7) & 0xff),
				B: uint8((x*11 + y) & 0xff),
				A: 0xff,
			})
		}
	}

	params := append(append([]float64(nil), prefix...), extendBenchmarkTruthCandidate()...)
	target := renderer.NewCPURenderer(img, len(params)/app.ParamsPerCircle)
	target.SetThreads(1)

	return cloneBenchmarkImage(target.Render(params))
}

func extendBenchmarkParams(circleCount int) []float64 {
	rng := rand.New(rand.NewPCG(15_900, uint64(circleCount)))
	params := make([]float64, circleCount*app.ParamsPerCircle)

	vector := fit.ParamVector{Data: params, K: circleCount, Width: extendBenchmarkWidth, Height: extendBenchmarkHeight}
	for i := range circleCount {
		var radius float64

		switch bucket := rng.Float64(); {
		case bucket < 0.7:
			radius = 1 + rng.Float64()*3
		case bucket < 0.9:
			radius = 4 + rng.Float64()*60
		default:
			radius = 64 + rng.Float64()*320
		}

		vector.EncodeCircle(i, fit.Circle{
			X:       rng.Float64() * (extendBenchmarkWidth - 1),
			Y:       rng.Float64() * (extendBenchmarkHeight - 1),
			R:       radius,
			CR:      rng.Float64(),
			CG:      rng.Float64(),
			CB:      rng.Float64(),
			Opacity: fit.MinCircleOpacity + rng.Float64()*0.35,
		})
	}

	return params
}

func extendBenchmarkCandidates(count int) [][]float64 {
	candidates := make([][]float64, count)
	for i := range candidates {
		candidates[i] = []float64{
			float64((i * 37) % extendBenchmarkWidth),
			float64((i * 61) % extendBenchmarkHeight),
			1 + float64(i%24),
			float64((i*17)%255) / 255,
			float64((i*29)%255) / 255,
			float64((i*43)%255) / 255,
			0.5,
		}
	}

	return candidates
}

func extendBenchmarkTruthCandidate() []float64 {
	return []float64{257, 251, 32, 0.91, 0.13, 0.67, 0.8}
}

func cloneBenchmarkImage(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)

	return dst
}

var _ opt.LifecycleOptimizer = fixedExtendOptimizer{}

var extendBenchmarkCostSink float64
