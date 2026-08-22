package server

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestRendererForJobConfiguresThreads(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	rend, cleanup, err := rendererForJob(JobConfig{Backend: "cpu", Threads: 1}, ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	cpu, ok := rend.(*renderer.CPURenderer)
	if !ok {
		t.Fatalf("renderer type = %T, want *renderer.CPURenderer", rend)
	}
	if cpu.Threads() != 1 {
		t.Fatalf("renderer threads = %d, want 1", cpu.Threads())
	}
}

func TestRunJob_Success(t *testing.T) {
	// Create temporary test image
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)

	jm := NewJobManager()
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	job := jm.CreateJob(app.DefaultProject, config)

	ctx := context.Background()
	err := runJob(ctx, jm, nil, job.ID)

	if err != nil {
		t.Errorf("runJob should succeed: %v", err)
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.State != StateCompleted {
		t.Errorf("Job should be completed, got %s", updated.State)
	}

	if updated.BestCost == 0 {
		t.Error("BestCost should be set")
	}

	if len(updated.BestParams) != 14 { // 2 circles * 7 params
		t.Errorf("Expected 14 params, got %d", len(updated.BestParams))
	}

	// Note: Iterations tracking will be added in a future enhancement
	// For now, just verify the job completed successfully
}

func TestRunJobRecordsPSNRAndOptionalSSIM(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	persistence, err := store.NewFSStore(filepath.Join(tmpDir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42,
		EnableTrace: true, EnableSSIM: true,
	})
	if err := runJob(context.Background(), jm, persistence, job.ID); err != nil {
		t.Fatal(err)
	}

	completed, _ := jm.GetJob(job.ID)
	if completed.PSNR == nil || completed.PSNRInfinite || completed.SSIM == nil {
		t.Fatalf("completed metrics unavailable: PSNR=%v infinite=%v SSIM=%v", completed.PSNR, completed.PSNRInfinite, completed.SSIM)
	}
	if len(completed.MetricHistory) < 2 || completed.MetricHistory[len(completed.MetricHistory)-1].SSIM == nil {
		t.Fatalf("metric history missing initial/final samples: %+v", completed.MetricHistory)
	}

	reader, err := persistence.NewTraceReader(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 || entries[0].PSNR == nil || entries[0].SSIM == nil {
		t.Fatalf("initial trace metrics unavailable: %+v", entries)
	}
	last := entries[len(entries)-1]
	if last.PSNR == nil || last.SSIM == nil {
		t.Fatalf("final trace metrics unavailable: %+v", last)
	}
}

func TestRunJobPersistsExactFinalResultWithoutPeriodicCheckpointing(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	persistence, err := store.NewFSStore(filepath.Join(tmpDir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42,
		CheckpointInterval: 0,
	})

	if err := runJob(context.Background(), jm, persistence, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, _ := jm.GetJob(job.ID)
	checkpoint, err := persistence.LoadCheckpoint(job.ID)
	if err != nil {
		t.Fatalf("load final checkpoint: %v", err)
	}
	if checkpoint.BestCost != completed.BestCost ||
		checkpoint.Iterations != completed.Iterations ||
		checkpoint.Evaluations != int64(completed.Evaluations) ||
		checkpoint.Termination != completed.Termination ||
		!reflect.DeepEqual(checkpoint.BestParams, completed.BestParams) {
		t.Fatalf("checkpoint does not match completed job:\ncheckpoint=%+v\njob=%+v", checkpoint, completed)
	}
	for _, artifact := range []store.Artifact{store.ArtifactBest, store.ArtifactDiff} {
		path, err := persistence.ArtifactPath(job.ID, artifact)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("stat final artifact %s: %v", artifact, err)
		}
	}
}

func TestLoadRetainedPrefixCanvasVerifiesArtifactCost(t *testing.T) {
	fsStore, err := store.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parentID := "00000000-0000-4000-8000-000000000163"
	ref := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	canvas := image.NewNRGBA(ref.Bounds())
	for y := range 4 {
		for x := range 4 {
			canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 40), G: uint8(y * 50), B: uint8((x + y) * 30), A: 255})
		}
	}
	if err := fsStore.SavePNGArtifact(parentID, store.ArtifactBest, canvas); err != nil {
		t.Fatal(err)
	}
	job := &Job{
		ID:           "00000000-0000-4000-8000-000000000164",
		ExtendedFrom: parentID,
		BestCost:     fit.FastMSECost(canvas, ref),
	}
	loaded := loadRetainedPrefixCanvas(fsStore, job, ref)
	if loaded == nil || !reflect.DeepEqual(loaded.Pix, canvas.Pix) {
		t.Fatal("matching retained prefix artifact was not loaded exactly")
	}

	job.BestCost++
	if loaded := loadRetainedPrefixCanvas(fsStore, job, ref); loaded != nil {
		t.Fatal("cost-mismatched retained prefix artifact was accepted")
	}
}

type artifactRenderProbe struct {
	reference *image.NRGBA
	renders   int
}

func (p *artifactRenderProbe) Render([]float64) *image.NRGBA {
	p.renders++
	return image.NewNRGBA(p.reference.Bounds())
}

func (p *artifactRenderProbe) Cost([]float64) float64 { return 0 }
func (p *artifactRenderProbe) Dim() int               { return 7 }
func (p *artifactRenderProbe) Bounds() ([]float64, []float64) {
	return make([]float64, 7), make([]float64, 7)
}
func (p *artifactRenderProbe) Reference() *image.NRGBA { return p.reference }

func TestSaveCheckpointArtifactsReusesFinalImage(t *testing.T) {
	fsStore, err := store.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	best := image.NewNRGBA(ref.Bounds())
	probe := &artifactRenderProbe{reference: ref}
	jobID := "00000000-0000-4000-8000-000000000165"
	if err := saveCheckpointArtifacts(
		fsStore,
		probe,
		JobConfig{Backend: app.BackendCPU, Threads: 1},
		jobID,
		[]float64{2, 2, 1, 0, 0, 0, 1},
		best,
	); err != nil {
		t.Fatal(err)
	}
	if probe.renders != 0 {
		t.Fatalf("artifact persistence replayed final parameters %d times", probe.renders)
	}
}

// observingWorkerStore reports what the job manager said about a job at the
// instant its final checkpoint was written.
type observingWorkerStore struct {
	*store.FSStore
	jm            *JobManager
	stateAtSave   JobState
	sawCheckpoint bool
}

func (s *observingWorkerStore) SaveCheckpoint(jobID string, checkpoint *store.Checkpoint) error {
	if job, ok := s.jm.GetJob(jobID); ok {
		s.stateAtSave = job.State
		s.sawCheckpoint = true
	}
	return s.FSStore.SaveCheckpoint(jobID, checkpoint)
}

// TestRunJobPublishesCompletionOnlyAfterTheCheckpointIsDurable pins the
// ordering every continuation depends on. `completed` is the state the extend
// and polish endpoints and the schedule executor read as "there is a checkpoint
// to continue from", so it must not be published while the checkpoint write is
// still outstanding. Announcing it early cost a campaign its next stage on a
// loaded host, which reported the parent's checkpoint as missing.
func TestRunJobPublishesCompletionOnlyAfterTheCheckpointIsDurable(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	fsStore, err := store.NewFSStore(filepath.Join(tmpDir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	jm := NewJobManager()
	persistence := &observingWorkerStore{FSStore: fsStore, jm: jm}
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath, Mode: "batch", Circles: 1, BatchSize: 1, Iters: 3, PopSize: 20, Seed: 42,
	})

	if err := runJob(context.Background(), jm, persistence, job.ID); err != nil {
		t.Fatal(err)
	}
	if !persistence.sawCheckpoint {
		t.Fatal("the job never wrote a final checkpoint")
	}
	if persistence.stateAtSave == StateCompleted {
		t.Fatal("the job was already completed while its checkpoint was still being written")
	}
	if persistence.stateAtSave != StateRunning {
		t.Fatalf("job state during the final checkpoint = %q, want running", persistence.stateAtSave)
	}
	settled, _ := jm.GetJob(job.ID)
	if settled.State != StateCompleted {
		t.Fatalf("job state after runJob = %q, want completed", settled.State)
	}
	if _, err := persistence.LoadCheckpoint(job.ID); err != nil {
		t.Fatalf("a completed job has no checkpoint to continue from: %v", err)
	}
}

func TestRunJobRefusesToRecordAFinalResultForASettledJob(t *testing.T) {
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "ref.png", Mode: "batch", Circles: 1})
	if err := jm.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jm.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	err := jm.RecordFinalResult(job.ID, 1, 1, []float64{1, 2, 3, 4, 5, 6, 7}, 1, 2, "completed")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("RecordFinalResult on a cancelled job = %v, want ErrInvalidTransition", err)
	}
	if err := jm.MarkJobCompleted(job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("MarkJobCompleted on a cancelled job = %v, want ErrInvalidTransition", err)
	}
	settled, _ := jm.GetJob(job.ID)
	if settled.State != StateCancelled {
		t.Fatalf("job state = %q, want cancelled", settled.State)
	}
}

type faultingWorkerStore struct {
	*store.FSStore
	checkpointErr error
	artifactErrs  map[store.Artifact]error
	artifactMu    sync.Mutex
	artifactCalls []store.Artifact
}

func (s *faultingWorkerStore) SaveCheckpoint(jobID string, checkpoint *store.Checkpoint) error {
	if s.checkpointErr != nil {
		return s.checkpointErr
	}
	return s.FSStore.SaveCheckpoint(jobID, checkpoint)
}

func (s *faultingWorkerStore) SavePNGArtifact(jobID string, artifact store.Artifact, img image.Image) error {
	s.artifactMu.Lock()
	s.artifactCalls = append(s.artifactCalls, artifact)
	s.artifactMu.Unlock()
	if err := s.artifactErrs[artifact]; err != nil {
		return err
	}
	return s.FSStore.SavePNGArtifact(jobID, artifact, img)
}

func TestRunJobReportsFinalPersistenceFailuresAndAttemptsBothArtifacts(t *testing.T) {
	tests := []struct {
		name          string
		checkpointErr error
		artifactErrs  map[store.Artifact]error
		wantErrors    []string
	}{
		{
			name:          "checkpoint",
			checkpointErr: errors.New("injected checkpoint failure"),
			wantErrors:    []string{"save checkpoint", "injected checkpoint failure"},
		},
		{
			name: "both artifacts",
			artifactErrs: map[store.Artifact]error{
				store.ArtifactBest: errors.New("injected best failure"),
				store.ArtifactDiff: errors.New("injected diff failure"),
			},
			wantErrors: []string{"save best artifact", "injected best failure", "save diff artifact", "injected diff failure"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			imgPath := filepath.Join(tmpDir, "test.png")
			createTestImage(t, imgPath)
			fsStore, err := store.NewFSStore(filepath.Join(tmpDir, "artifacts"))
			if err != nil {
				t.Fatal(err)
			}
			persistence := &faultingWorkerStore{
				FSStore: fsStore, checkpointErr: test.checkpointErr, artifactErrs: test.artifactErrs,
			}
			jm := NewJobManager()
			job := jm.CreateJob(app.DefaultProject, JobConfig{
				RefPath: imgPath, Mode: "joint", Circles: 1, Iters: 2, PopSize: 20, Seed: 42,
			})

			runErr := runJob(context.Background(), jm, persistence, job.ID)
			if runErr == nil {
				t.Fatal("runJob returned nil after final persistence failure")
			}
			for _, want := range test.wantErrors {
				if !strings.Contains(runErr.Error(), want) {
					t.Errorf("runJob error %q does not contain %q", runErr, want)
				}
			}
			completed, _ := jm.GetJob(job.ID)
			if completed.State != StateCompleted || completed.Error != "failed to persist final result" {
				t.Fatalf("completed job did not expose persistence failure: state=%s error=%q", completed.State, completed.Error)
			}
			persistence.artifactMu.Lock()
			calls := append([]store.Artifact(nil), persistence.artifactCalls...)
			persistence.artifactMu.Unlock()
			counts := make(map[store.Artifact]int, len(calls))
			for _, artifact := range calls {
				counts[artifact]++
			}
			if counts[store.ArtifactBest] != 1 || counts[store.ArtifactDiff] != 1 || len(calls) != 2 {
				t.Fatalf("artifact calls = %v, want best.png and diff.png once each", calls)
			}
		})
	}
}

func TestRunJobExecutesConfiguredBatchPolishing(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath:                  imgPath,
		Mode:                     app.ModeBatch,
		Backend:                  app.BackendCPU,
		Variant:                  app.VariantStandard,
		Circles:                  1,
		BatchSize:                1,
		Iters:                    2,
		OptimizerEpochs:          1,
		PopSize:                  20,
		Threads:                  1,
		Seed:                     42,
		EffectiveSeed:            42,
		PolishingEnabled:         true,
		PolishingActiveSetSize:   1,
		PolishingMaxSweeps:       1,
		PolishingEpochs:          1,
		PolishingIters:           2,
		PolishingPopSize:         20,
		PolishingStagnationIters: 1,
		PolishingMinImprovement:  0.001,
		DisableConvergence:       true,
	})

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("completed job not found")
	}
	if completed.State != StateCompleted || len(completed.BestParams) != 7 {
		t.Fatalf("completed polished job = state %s params %d", completed.State, len(completed.BestParams))
	}
	if completed.Iterations <= job.Config.Iters {
		t.Fatalf("iterations = %d, want work from initial stage plus polishing", completed.Iterations)
	}
}

// TestRunJobPolishesAtTheConfiguredEvaluationWidth covers the path that used to
// be impossible: a job configured for parallel evaluation now polishes at the
// same width as its main optimizer instead of dropping to a serial polisher.
// Polishing refuses a concurrent optimizer it cannot pool sessions for, so this
// also proves the server hands it a renderer that can.
func TestRunJobPolishesAtTheConfiguredEvaluationWidth(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs at least two processors to enable parallel evaluation")
	}
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	logs := captureLogs(t)
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath:                  imgPath,
		Mode:                     app.ModeBatch,
		Backend:                  app.BackendCPU,
		Variant:                  app.VariantStandard,
		Circles:                  2,
		BatchSize:                1,
		Iters:                    2,
		OptimizerEpochs:          1,
		PopSize:                  20,
		Threads:                  1,
		ParallelEvaluation:       true,
		EvaluationWorkers:        4,
		Seed:                     42,
		EffectiveSeed:            42,
		PolishingEnabled:         true,
		PolishingActiveSetSize:   1,
		PolishingMaxSweeps:       2,
		PolishingEpochs:          1,
		PolishingIters:           2,
		PolishingPopSize:         20,
		PolishingStagnationIters: 1,
		PolishingMinImprovement:  0.001,
		DisableConvergence:       true,
	})

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatalf("parallel polishing job failed: %v", err)
	}
	completed, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("completed job not found")
	}
	if completed.State != StateCompleted || len(completed.BestParams) != 14 {
		t.Fatalf("completed polished job = state %s params %d, error %q",
			completed.State, len(completed.BestParams), completed.Error)
	}
	// Completion alone proves nothing: the serial polisher completed this job
	// too. What has to hold is the width polishing actually ran at, which the
	// completion record reports.
	width := polishingWidthFromLogs(t, logs.String())
	if width < 2 {
		t.Fatalf("polishing evaluation width = %d, want more than one worker", width)
	}
}

// TestRunJobPolishesAtItsOwnPopulation pins that polishing runs at
// PolishingPopSize and not at the job-wide PopSize. The two are deliberately
// far apart here, because before the polishing population existed the job-wide
// one was what a sweep spent on its active set.
func TestRunJobPolishesAtItsOwnPopulation(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	logs := captureLogs(t)
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath:                  imgPath,
		Mode:                     app.ModeBatch,
		Backend:                  app.BackendCPU,
		Variant:                  app.VariantStandard,
		Circles:                  1,
		BatchSize:                1,
		Iters:                    2,
		OptimizerEpochs:          1,
		PopSize:                  200,
		Threads:                  1,
		Seed:                     42,
		EffectiveSeed:            42,
		PolishingEnabled:         true,
		PolishingActiveSetSize:   1,
		PolishingMaxSweeps:       1,
		PolishingEpochs:          1,
		PolishingIters:           2,
		PolishingPopSize:         20,
		PolishingStagnationIters: 1,
		PolishingMinImprovement:  0.001,
		DisableConvergence:       true,
	})

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	if population := polishingPopulationFromLogs(t, logs.String()); population != 20 {
		t.Fatalf("polishing population = %d, want the configured 20 rather than the job's 200", population)
	}
}

// polishingPopulationFromLogs reads the population off the same completion
// record polishingWidthFromLogs reads the width from, for the same reason: the
// polisher is built inside runJob and its budget is not otherwise observable.
func polishingPopulationFromLogs(t *testing.T, logs string) int {
	t.Helper()
	return polishingRecordField(t, logs, "population=")
}

// polishingWidthFromLogs reads the evaluation width off the record polishing
// writes when it finishes. It is the only place the width polishing really used
// is observable from outside, since the polisher is built inside runJob.
func polishingWidthFromLogs(t *testing.T, logs string) int {
	t.Helper()
	return polishingRecordField(t, logs, "evaluation_workers=")
}

// polishingRecordField pulls one integer attribute off the polishing completion
// record.
func polishingRecordField(t *testing.T, logs, key string) int {
	t.Helper()
	for line := range strings.SplitSeq(logs, "\n") {
		if !strings.Contains(line, "Batch polishing complete") {
			continue
		}
		_, rest, ok := strings.Cut(line, key)
		if !ok {
			t.Fatalf("polishing completion record has no %s: %s", key, line)
		}
		value, err := strconv.Atoi(strings.Fields(rest)[0])
		if err != nil {
			t.Fatalf("%s is not a number in %q: %v", key, line, err)
		}
		return value
	}
	t.Fatalf("no polishing completion record in logs: %s", logs)
	return 0
}

func TestRunJobPolishingOnlyContinuesCompleteBatch(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	jm := NewJobManager()
	config := JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Backend: app.BackendCPU, Variant: app.VariantStandard,
		Circles: 1, BatchSize: 1, Iters: 2, OptimizerEpochs: 1, PopSize: 20, Threads: 1,
		Seed: 42, EffectiveSeed: 42, PolishingEnabled: true, PolishingOnly: true,
		PolishingActiveSetSize: 1, PolishingMaxSweeps: 1, PolishingEpochs: 1,
		PolishingIters: 2, PolishingPopSize: 20, PolishingStagnationIters: 1, PolishingMinImprovement: 0.001,
		DisableConvergence: true,
	}
	job := jm.CreateJob(app.DefaultProject, config)
	params := []float64{25, 25, 10, 1, 0, 0, 1}
	if err := jm.UpdateJob(job.ID, func(live *Job) {
		live.BestParams = append([]float64(nil), params...)
		live.BestCost = 1000
		live.InitialCost = 2000
		live.Iterations = 8000
		live.Evaluations = 900000
	}); err != nil {
		t.Fatal(err)
	}

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, _ := jm.GetJob(job.ID)
	if completed.State != StateCompleted || len(completed.BestParams) != 7 {
		t.Fatalf("polishing continuation = state %s params %d", completed.State, len(completed.BestParams))
	}
	if completed.Iterations <= 8000 || completed.Evaluations <= 900000 {
		t.Fatalf("continuation work = %d/%d, want counters beyond checkpoint", completed.Iterations, completed.Evaluations)
	}
}

func TestInheritedContiguousWindowVisitCountsReplaysPolishLineage(t *testing.T) {
	tmpDir := t.TempDir()
	persistence, err := store.NewFSStore(filepath.Join(tmpDir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	baseConfig, err := app.Normalize(JobConfig{
		RefPath: "ref.png", Mode: app.ModeBatch, Circles: 10, BatchSize: 10,
		Iters: 2, OptimizerEpochs: 1, PopSize: 20, Seed: 42,
		PolishingEnabled: true, PolishingOnly: true,
		PolishingStrategy: app.PolishingContiguousWindow, PolishingActiveSetSize: 3,
		PolishingMaxSweeps: 2, PolishingEpochs: 1, PolishingIters: 2,
		PolishingPopSize: 20, PolishingStagnationIters: 1, DisableConvergence: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := make([]float64, baseConfig.Circles*app.ParamsPerCircle)
	rootID := "00000000-0000-4000-8000-000000000170"
	parentID := "00000000-0000-4000-8000-000000000171"
	saveParent := func(jobID, polishedFrom string, config JobConfig) {
		t.Helper()
		checkpoint := store.NewCheckpoint(jobID, params, 100, 200, 10, config)
		checkpoint.Evaluations = 100
		checkpoint.Termination = "completed"
		checkpoint.PolishedFrom = polishedFrom
		if err := persistence.SaveCheckpoint(jobID, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	saveParent(rootID, "", baseConfig)
	parentConfig := baseConfig
	parentConfig.PolishingMaxSweeps = 1
	saveParent(parentID, rootID, parentConfig)

	job := &Job{
		ID:           "00000000-0000-4000-8000-000000000172",
		Config:       parentConfig,
		BestParams:   params,
		PolishedFrom: parentID,
	}
	got, err := inheritedContiguousWindowVisitCounts(persistence, job)
	if err != nil {
		t.Fatal(err)
	}
	_, want, err := renderer.PlanContiguousWindows(10, 3, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, want, err = renderer.PlanContiguousWindows(10, 3, 1, want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inherited visits = %v, want replayed lineage %v", got, want)
	}
}

func TestInheritedContiguousWindowVisitCountsRejectsBrokenLineage(t *testing.T) {
	persistence, err := store.NewFSStore(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := app.Normalize(JobConfig{
		RefPath: "ref.png", Mode: app.ModeBatch, Circles: 2, BatchSize: 2,
		Iters: 2, PopSize: 20, Seed: 42, PolishingEnabled: true, PolishingOnly: true,
		PolishingStrategy: app.PolishingContiguousWindow, PolishingActiveSetSize: 1,
		PolishingMaxSweeps: 1, PolishingEpochs: 1, PolishingIters: 2,
		PolishingPopSize: 20, PolishingStagnationIters: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := &Job{
		ID:           "00000000-0000-4000-8000-000000000173",
		Config:       config,
		BestParams:   make([]float64, 2*app.ParamsPerCircle),
		PolishedFrom: "00000000-0000-4000-8000-000000000174",
	}
	if _, err := inheritedContiguousWindowVisitCounts(persistence, job); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing parent error = %v, want store.ErrNotFound", err)
	}

	firstID := "00000000-0000-4000-8000-000000000176"
	secondID := "00000000-0000-4000-8000-000000000177"
	save := func(jobID, polishedFrom string, checkpointConfig JobConfig) {
		t.Helper()
		checkpoint := store.NewCheckpoint(jobID, job.BestParams, 100, 200, 10, checkpointConfig)
		checkpoint.Termination = "completed"
		checkpoint.PolishedFrom = polishedFrom
		if err := persistence.SaveCheckpoint(jobID, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	save(firstID, secondID, config)
	save(secondID, firstID, config)
	job.PolishedFrom = firstID
	if _, err := inheritedContiguousWindowVisitCounts(persistence, job); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cyclic lineage error = %v, want an explicit cycle error", err)
	}

	incompatibleID := "00000000-0000-4000-8000-000000000178"
	incompatible := config
	incompatible.PolishingStrategy = app.PolishingResidualRegion
	save(incompatibleID, "00000000-0000-4000-8000-000000000179", incompatible)
	job.PolishedFrom = incompatibleID
	visits, err := inheritedContiguousWindowVisitCounts(persistence, job)
	if err != nil {
		t.Fatalf("incompatible strategy should end coverage history: %v", err)
	}
	if visits != nil {
		t.Fatalf("incompatible strategy visits = %v, want a fresh history", visits)
	}
}

func TestRunJobContiguousWindowContinuationIsDeterministicForSameParentAndSeed(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	persistence, err := store.NewFSStore(filepath.Join(tmpDir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := app.Normalize(JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Backend: app.BackendCPU,
		Circles: 2, BatchSize: 2, Iters: 2, OptimizerEpochs: 1, PopSize: 20,
		Threads: 1, Seed: 42, EffectiveSeed: 42, DisableConvergence: true,
		PolishingEnabled: true, PolishingOnly: true,
		PolishingStrategy: app.PolishingContiguousWindow, PolishingActiveSetSize: 1,
		PolishingMaxSweeps: 1, PolishingEpochs: 1, PolishingIters: 3,
		PolishingPopSize: 20, PolishingStagnationIters: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := []float64{
		15, 15, 10, 1, 0, 0, 1,
		35, 35, 8, 0, 0, 1, 1,
	}
	parentID := "00000000-0000-4000-8000-000000000175"
	parent := store.NewCheckpoint(parentID, params, 1000, 2000, 10, config)
	parent.Termination = "completed"
	if err := persistence.SaveCheckpoint(parentID, parent); err != nil {
		t.Fatal(err)
	}

	jm := NewJobManager()
	run := func() *Job {
		t.Helper()
		job := jm.CreateJob(app.DefaultProject, config)
		if err := jm.UpdateJob(job.ID, func(live *Job) {
			updateBestResult(live, params, parent.BestCost)
			live.InitialCost = parent.InitialCost
			live.Iterations = parent.Iterations
			live.Evaluations = int(parent.Evaluations)
			live.PolishedFrom = parentID
		}); err != nil {
			t.Fatal(err)
		}
		if err := runJob(context.Background(), jm, persistence, job.ID); err != nil {
			t.Fatal(err)
		}
		completed, _ := jm.GetJob(job.ID)
		return completed
	}
	first, second := run(), run()
	if first.BestCost != second.BestCost || !reflect.DeepEqual(first.BestParams, second.BestParams) {
		t.Fatalf("same parent/config/seed diverged: first cost=%v params=%v second cost=%v params=%v",
			first.BestCost, first.BestParams, second.BestCost, second.BestParams)
	}
}

func TestRunJobResumesSingleStageBatch(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Backend: app.BackendCPU, Variant: app.VariantStandard,
		Circles: 1, BatchSize: 1, Iters: 2, OptimizerEpochs: 1, PopSize: 20, Threads: 1,
		Seed: 42, EffectiveSeed: 42, ResumeCount: 1, DisableConvergence: true,
	})
	params := []float64{25, 25, 10, 1, 0, 0, 1}
	if err := jm.UpdateJob(job.ID, func(live *Job) {
		updateBestResult(live, params, 1000)
		live.InitialCost = 2000
		live.Iterations = 10
		live.Evaluations = 100
	}); err != nil {
		t.Fatal(err)
	}

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, _ := jm.GetJob(job.ID)
	if completed.State != StateCompleted || len(completed.BestParams) != 7 {
		t.Fatalf("resumed batch = state %s params %d", completed.State, len(completed.BestParams))
	}
	if completed.Iterations <= 10 || completed.Evaluations <= 100 {
		t.Fatalf("resumed work = %d/%d, want counters beyond checkpoint", completed.Iterations, completed.Evaluations)
	}
}

func TestRunJobAppendsBatchSuffix(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Backend: app.BackendCPU, Variant: app.VariantStandard,
		Circles: 2, BatchSize: 1, Iters: 5, OptimizerEpochs: 1, PopSize: 20, Threads: 1,
		Seed: 42, EffectiveSeed: 42, ResumeCount: 1, DisableConvergence: true,
	})
	prefix := []float64{25, 25, 10, 1, 0, 0, 1}
	if err := jm.UpdateJob(job.ID, func(live *Job) {
		updateBestResult(live, prefix, 1e9)
		live.InitialCost = 2000
		live.Iterations = 10
		live.Evaluations = 100
	}); err != nil {
		t.Fatal(err)
	}

	if err := runJob(context.Background(), jm, nil, job.ID); err != nil {
		t.Fatal(err)
	}
	completed, _ := jm.GetJob(job.ID)
	if completed.State != StateCompleted || len(completed.BestParams) != 14 {
		t.Fatalf("extended batch = state %s params %d", completed.State, len(completed.BestParams))
	}
	if !reflect.DeepEqual(completed.BestParams[:len(prefix)], prefix) {
		t.Fatalf("prefix changed during append: got %v want %v", completed.BestParams[:len(prefix)], prefix)
	}
	if completed.Iterations <= 10 || completed.Evaluations <= 100 {
		t.Fatalf("extension work = %d/%d, want counters beyond checkpoint", completed.Iterations, completed.Evaluations)
	}
}

func TestRunJob_InvalidImage(t *testing.T) {
	jm := NewJobManager()
	config := JobConfig{
		RefPath: "/nonexistent/image.png",
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	job := jm.CreateJob(app.DefaultProject, config)

	ctx := context.Background()
	err := runJob(ctx, jm, nil, job.ID)

	if err == nil {
		t.Error("runJob should fail with invalid image path")
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.State != StateFailed {
		t.Errorf("Job should be failed, got %s", updated.State)
	}

	if updated.Error == "" {
		t.Error("Error message should be set")
	}
}

func TestRunJob_Cancellation(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)

	jm := NewJobManager()
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 5,
		Iters:   1000, // Long-running job
		PopSize: 30,
		Seed:    42,
	}

	job := jm.CreateJob(app.DefaultProject, config)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- runJob(ctx, jm, nil, job.ID)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the job
	cancel()

	// Wait for completion
	err := <-done

	if err == nil {
		t.Error("runJob should return error when cancelled")
	}

	updated, _ := jm.GetJob(job.ID)
	// State could be running or cancelled depending on timing
	if updated.State != StateRunning && updated.State != StateCancelled {
		t.Errorf("Job should be running or cancelled, got %s", updated.State)
	}
}

// Helper function to create a simple test image
func createTestImage(t *testing.T, path string) {
	img := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	white := color.NRGBA{255, 255, 255, 255}
	red := color.NRGBA{255, 0, 0, 255}

	// Fill with white
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img.Set(x, y, white)
		}
	}

	// Add red square
	for y := 20; y < 30; y++ {
		for x := 20; x < 30; x++ {
			img.Set(x, y, red)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create test image: %v", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}
}

// terminationOptimizer returns a fixed termination reason so the wrapper's
// pass-through behavior can be checked without running a real optimizer.
type terminationOptimizer struct {
	reason opt.Termination
}

func (t terminationOptimizer) Run(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	return make([]float64, dim), 0
}

type optionsCaptureOptimizer struct {
	options opt.RunOptions
}

type epochCallbackOptimizer struct{}

func (o *optionsCaptureOptimizer) Run(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	return make([]float64, dim), 0
}

func (o *optionsCaptureOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.options = options
	return opt.Result{BestParams: append([]float64(nil), options.Initial.Params...), BestCost: options.Initial.Cost}, nil
}

func (epochCallbackOptimizer) Run(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	return make([]float64, dim), 0
}

func (epochCallbackOptimizer) RunContext(_ context.Context, _ opt.Problem, options opt.RunOptions) (opt.Result, error) {
	progress := opt.Progress{Iterations: 2, Evaluations: 3, BestParams: []float64{4}, BestCost: 5}
	if options.ProgressMapper != nil {
		progress = options.ProgressMapper(progress)
	}
	if err := options.EpochObserver(opt.EpochBoundary{Epoch: 1, Progress: progress, Termination: opt.TerminationCompleted}); err != nil {
		return opt.Result{}, err
	}
	return opt.Result{BestParams: []float64{4}, BestCost: 5, Iterations: 2, Evaluations: 3, Termination: opt.TerminationCompleted}, nil
}

func (t terminationOptimizer) RunContext(_ context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	return opt.Result{
		BestParams:  make([]float64, problem.Dim),
		BestCost:    1,
		Iterations:  5,
		Evaluations: 9,
		Termination: t.reason,
	}, nil
}

// TestProgressOptimizerPreservesTermination pins the wrapper that rebuilds
// RunOptions: it must still return the base optimizer's termination reason,
// because the worker now reports that reason instead of a hardcoded value.
func TestProgressOptimizerPreservesTermination(t *testing.T) {
	reasons := []opt.Termination{
		opt.TerminationCompleted,
		opt.TerminationTargetCost,
		opt.TerminationStagnation,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			wrapped := &progressOptimizer{base: terminationOptimizer{reason: reason}}
			result, err := wrapped.RunContext(context.Background(), opt.Problem{
				Eval:  func([]float64) float64 { return 1 },
				Lower: []float64{0},
				Upper: []float64{1},
				Dim:   1,
			}, opt.RunOptions{})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}
			if result.Termination != reason {
				t.Fatalf("Termination = %q, want %q", result.Termination, reason)
			}
		})
	}
}

func TestProgressOptimizerForwardsPipelineInitialSeed(t *testing.T) {
	base := &optionsCaptureOptimizer{}
	wrapped := &progressOptimizer{base: base}
	initial := &opt.Candidate{Params: []float64{0.75}, Cost: 2}
	if _, err := wrapped.RunContext(context.Background(), opt.Problem{
		Eval: func([]float64) float64 { return 1 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, opt.RunOptions{Initial: initial, ResumeCount: 3}); err != nil {
		t.Fatal(err)
	}
	if base.options.Initial != initial || base.options.ResumeCount != 3 {
		t.Fatalf("forwarded options = %+v, want initial seed and resume count 3", base.options)
	}
}

func TestProgressOptimizerMapsAndOffsetsEpochBoundary(t *testing.T) {
	var boundary opt.EpochBoundary
	wrapped := &progressOptimizer{
		base: epochCallbackOptimizer{},
		epochObserver: func(sample opt.EpochBoundary) error {
			boundary = sample
			return nil
		},
		iterations:  10,
		evaluations: 20,
	}
	_, err := wrapped.RunContext(context.Background(), opt.Problem{
		Eval: func([]float64) float64 { return 1 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, opt.RunOptions{ProgressMapper: func(progress opt.Progress) opt.Progress {
		progress.BestParams = append([]float64{9}, progress.BestParams...)
		return progress
	}})
	if err != nil {
		t.Fatal(err)
	}
	if boundary.Progress.Iterations != 12 || boundary.Progress.Evaluations != 23 {
		t.Fatalf("boundary work = %+v, want offsets 12/23", boundary.Progress)
	}
	if !reflect.DeepEqual(boundary.Progress.BestParams, []float64{9, 4}) {
		t.Fatalf("boundary params = %v, want complete mapped vector", boundary.Progress.BestParams)
	}
}

func TestSafeJobError(t *testing.T) {
	t.Run("staged optimization unsupported", func(t *testing.T) {
		got := safeJobError(renderer.ErrStagedOptimizationUnsupported)
		want := "selected backend does not support this optimization mode"
		if got != want {
			t.Fatalf("safeJobError() = %q, want %q", got, want)
		}
	})
	t.Run("invalid optimization input", func(t *testing.T) {
		got := safeJobError(renderer.ErrInvalidOptimizationInput)
		want := "optimizer produced an invalid result"
		if got != want {
			t.Fatalf("safeJobError() = %q, want %q", got, want)
		}
	})
	t.Run("backend unavailable", func(t *testing.T) {
		got := safeJobError(fmt.Errorf("%w: no devices", renderer.ErrBackendUnavailable))
		if !strings.Contains(got, "renderer backend unavailable") {
			t.Fatalf("safeJobError() = %q, want backend unavailable message", got)
		}
	})
	t.Run("generic", func(t *testing.T) {
		got := safeJobError(errors.New("boom"))
		want := "job execution failed"
		if got != want {
			t.Fatalf("safeJobError() = %q, want %q", got, want)
		}
	})
}

// TestEvaluationWidthReportsWhatRanNotWhatWasAsked is the regression test for
// reporting Config.EvaluationWorkers as if it were the concurrency a job used.
// The configured value is only the request. A CPU request above GOMAXPROCS is
// clamped, and a backend without independent sessions declines the request and
// evaluates serially, so echoing the request back would claim a concurrency the
// job never had -- in a field whose whole purpose is telling two runs apart.
func TestEvaluationWidthReportsWhatRanNotWhatWasAsked(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	oversized := runtime.GOMAXPROCS(0) * 100

	rend, cleanup, err := rendererForJob(JobConfig{
		Backend:            "cpu",
		Threads:            1,
		ParallelEvaluation: true,
		EvaluationWorkers:  oversized,
	}, ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	width := renderer.EvaluationWidth(rend)
	if width != runtime.GOMAXPROCS(0) {
		t.Fatalf("effective width = %d, want the GOMAXPROCS clamp %d", width, runtime.GOMAXPROCS(0))
	}
	if width >= oversized {
		t.Fatalf("effective width %d still reflects the unclamped request %d", width, oversized)
	}
}

// TestEvaluationWidthIsZeroWithoutParallelEvaluation pins that a job which did
// not opt in reports nothing rather than a worker count, so the detail page and
// status output stay silent instead of showing a concurrency of one as if it
// were a deliberate setting.
func TestEvaluationWidthIsZeroWithoutParallelEvaluation(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	rend, cleanup, err := rendererForJob(JobConfig{
		Backend:           "cpu",
		Threads:           2,
		EvaluationWorkers: 8,
	}, ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if width := renderer.EvaluationWidth(rend); width != 1 {
		t.Fatalf("effective width = %d, want 1 without the opt-in", width)
	}
}
