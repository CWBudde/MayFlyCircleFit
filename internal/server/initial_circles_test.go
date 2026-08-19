package server

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
)

// seededJobConfig is a one-circle batch job whose single circle is placed on
// the red square createTestImage draws, so the seeded cost is far better than
// anything a two-iteration run would find on its own.
func seededJobConfig(refPath string) JobConfig {
	return JobConfig{
		RefPath:            refPath,
		Mode:               app.ModeBatch,
		Backend:            app.BackendCPU,
		Variant:            app.VariantStandard,
		Circles:            1,
		BatchSize:          1,
		Iters:              2,
		OptimizerEpochs:    1,
		PopSize:            20,
		Threads:            1,
		Seed:               42,
		EffectiveSeed:      42,
		DisableConvergence: true,
		InitialCircles: app.CircleSpecs{
			{X: 25, Y: 25, R: 5, Color: "#ff0000", Opacity: 1},
		},
	}
}

// TestRunJobStartsFromTheAuthoredArrangement is the whole point of the field: a
// job with no parent must begin at the cost of the circles it was handed, and
// must never finish worse than it started.
func TestRunJobStartsFromTheAuthoredArrangement(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "test.png")
	createTestImage(t, imgPath)
	config := seededJobConfig(imgPath)

	ref, err := loadReferenceImage(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	params, err := config.InitialCircles.ToParams()
	if err != nil {
		t.Fatal(err)
	}
	seededCost := renderer.NewCPURenderer(ref, config.Circles).Cost(params)

	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, config)
	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("completed job not found")
	}
	if completed.State != StateCompleted {
		t.Fatalf("state = %s, want completed", completed.State)
	}
	if completed.BestCost > seededCost {
		t.Fatalf("best cost = %f, want no worse than the seeded %f", completed.BestCost, seededCost)
	}
	// The blank canvas is the improvement baseline for a seeded run exactly as
	// it is for a random one, so a continuation of this job compares against the
	// same number its parent did.
	blankCost := renderer.NewCPURenderer(ref, config.Circles).Cost(make([]float64, len(params)))
	if math.Abs(completed.InitialCost-blankCost) > 1e-9 {
		t.Fatalf("initial cost = %f, want the blank canvas cost %f", completed.InitialCost, blankCost)
	}

	// The control. Without the arrangement the same two-iteration budget lands
	// somewhere random, so this is what makes the assertion above mean
	// something rather than merely hold.
	unseeded := config
	unseeded.InitialCircles = nil
	control := jm.CreateJob(app.DefaultProject, unseeded)
	if err := runJob(context.Background(), jm, nil, control.ID); err != nil {
		t.Fatal(err)
	}
	finished, ok := jm.GetJob(control.ID)
	if !ok {
		t.Fatal("control job not found")
	}
	if finished.BestCost <= completed.BestCost {
		t.Fatalf("unseeded cost = %f beat the seeded %f; the arrangement is not being used",
			finished.BestCost, completed.BestCost)
	}
}

// TestRunJobPrefersAParentsParametersOverAnAuthoredArrangement covers the guard
// that makes the field safe inside a campaign: a continuation arrives carrying
// its parent's result, and a spec that rode along on the copied configuration
// must not displace it.
func TestRunJobPrefersAParentsParametersOverAnAuthoredArrangement(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "test.png")
	createTestImage(t, imgPath)
	config := seededJobConfig(imgPath)

	ref, err := loadReferenceImage(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	// A deliberately poor parent result: a black circle in the corner. If the
	// authored red circle won, the job would start far better than this.
	parentParams := []float64{5, 5, 4, 0, 0, 0, 1}
	parentCost := renderer.NewCPURenderer(ref, config.Circles).Cost(parentParams)

	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, config)
	if err := jm.UpdateJob(job.ID, func(live *Job) {
		updateBestResult(live, parentParams, parentCost)
		live.InitialCost = parentCost
	}); err != nil {
		t.Fatal(err)
	}
	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("completed job not found")
	}
	if math.Abs(completed.InitialCost-parentCost) > 1e-9 {
		t.Fatalf("initial cost = %f, want the parent's %f", completed.InitialCost, parentCost)
	}
}

// TestRunJobRefusesAnArrangementTheCanvasCannotHold proves the refusal is real
// rather than a silent clamp: a circle far outside the bounds fails the job
// instead of being pulled inside and scored as if it had been authored there.
func TestRunJobRefusesAnArrangementTheCanvasCannotHold(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "test.png")
	createTestImage(t, imgPath)
	config := seededJobConfig(imgPath)
	config.InitialCircles = app.CircleSpecs{{X: 5000, Y: 25, R: 5, Color: "#ff0000", Opacity: 1}}

	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, config)
	if err := runJob(context.Background(), jm, nil, job.ID); err == nil {
		t.Fatal("runJob accepted a circle outside the canvas bounds")
	}
	failed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("failed job not found")
	}
	if failed.State != StateFailed {
		t.Fatalf("state = %s, want failed", failed.State)
	}
}
