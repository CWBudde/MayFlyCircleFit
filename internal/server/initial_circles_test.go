package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/store"
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
	err := runJob(context.Background(), jm, nil, job.ID)
	if err == nil {
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

// TestSeededBatchRunsWhenTheBatchSizeIsDefaulted covers the path a seeded job
// reaches at dispatch. The authored vector is full length, so the batch switch
// treats it as a resume, and every resume branch but the full-size one refuses.
// Leaving batchSize at the stock five would therefore validate, queue, and then
// fail the run -- so the default follows the seed and the job actually starts.
func TestSeededBatchRunsWhenTheBatchSizeIsDefaulted(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "test.png")
	createTestImage(t, imgPath)

	config := JobConfig{
		RefPath:            imgPath,
		Mode:               app.ModeBatch,
		Circles:            8,
		Iters:              2,
		OptimizerEpochs:    1,
		PopSize:            20,
		Threads:            1,
		Seed:               42,
		DisableConvergence: true,
		InitialCircles:     make(app.CircleSpecs, 8),
	}
	for i := range config.InitialCircles {
		config.InitialCircles[i] = app.CircleSpec{X: 25, Y: 25, R: 5, Color: "#ff0000"}
	}

	normalized, err := app.Normalize(config)
	if err != nil {
		t.Fatalf("Normalize() = %v, want nil", err)
	}

	if normalized.BatchSize != normalized.Circles {
		t.Fatalf("batchSize = %d, want %d", normalized.BatchSize, normalized.Circles)
	}

	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, normalized)
	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatalf("runJob() = %v, want a seeded batch run to complete", err)
	}

	completed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}

	if completed.State != StateCompleted {
		t.Fatalf("state = %s (%s), want completed", completed.State, completed.Error)
	}
}

// TestExtendClearsTheAuthoredArrangement keeps the ordinary continuation
// endpoint working on a seeded job. The extension raises the circle count, so a
// retained list -- still holding the parent's count -- would fail the exact
// count check and reject every extend. Schedule expansion clears the field for
// the same reason.
func TestExtendClearsTheAuthoredArrangement(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, imgPath)

	fsStore, err := store.NewFSStore(filepath.Join(tmpDir, "data"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":0", fsStore, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	config, err := app.Normalize(JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Circles: 2, BatchSize: 2, Iters: 2,
		OptimizerEpochs: 1, PopSize: 20, Threads: 1, Seed: 42,
		InitialCircles: app.CircleSpecs{
			{X: 1, Y: 1, R: 1, Color: "#ff0000"},
			{X: 2, Y: 2, R: 1, Color: "#00ff00"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)
	params := []float64{
		1, 1, 1, 1, 0, 0, 1,
		2, 2, 1, 0, 1, 0, 1,
	}

	if err := server.jobManager.StartJob(source.ID); err != nil {
		t.Fatal(err)
	}

	if err := server.jobManager.CompleteJob(source.ID, 8000, 900000, params, 600, 1000, "completed"); err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, config)

	checkpoint.Evaluations = 900000
	if err := fsStore.SaveCheckpoint(source.ID, checkpoint); err != nil {
		t.Fatal(err)
	}

	server.cancel()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+source.ID+"/extend",
		strings.NewReader(`{"additionalCircles":2}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("extend status = %d body=%s", response.Code, response.Body.String())
	}

	var payload struct {
		JobID string `json:"jobId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	continuation, ok := server.jobManager.GetJob(payload.JobID)
	if !ok {
		t.Fatal("extension continuation job not found")
	}

	if len(continuation.Config.InitialCircles) != 0 {
		t.Fatalf("continuation carries %d authored circles, want none",
			len(continuation.Config.InitialCircles))
	}

	if continuation.Config.Circles != 4 {
		t.Fatalf("continuation circles = %d, want 4", continuation.Config.Circles)
	}
}

// TestJobConfigsDoNotShareAuthoredCircles keeps the arrangement out of the
// aliasing that a by-value Config copy would otherwise leave: everything the
// manager hands out must be safe to write without touching live job state.
func TestJobConfigsDoNotShareAuthoredCircles(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "test.png")
	createTestImage(t, imgPath)
	config := seededJobConfig(imgPath)

	jm := NewJobManager()
	created := jm.CreateJob(app.DefaultProject, config)

	// The caller's own slice must not be the job's.
	config.InitialCircles[0].R = 999

	fetched, ok := jm.GetJob(created.ID)
	if !ok {
		t.Fatal("job not found")
	}

	if fetched.Config.InitialCircles[0].R == 999 {
		t.Fatal("the manager aliased the caller's arrangement")
	}

	// Neither must one clone's slice be another's.
	created.Config.InitialCircles[0].R = 777
	fetched.Config.InitialCircles[0].R = 555

	again, _ := jm.GetJob(created.ID)
	if again.Config.InitialCircles[0].R != seededJobConfig(imgPath).InitialCircles[0].R {
		t.Fatalf("radius = %v, want the stored arrangement unchanged",
			again.Config.InitialCircles[0].R)
	}

	if listed := jm.ListJobs(); len(listed) == 1 &&
		listed[0].Config.InitialCircles[0].R != seededJobConfig(imgPath).InitialCircles[0].R {
		t.Fatal("ListJobs handed out the live arrangement")
	}
}
