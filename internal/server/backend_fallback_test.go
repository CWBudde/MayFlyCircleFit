//go:build !gpu

//nolint:testpackage // drives the unexported renderer factory and the unexported HTTP handlers
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
)

// A build without the gpu tag is the one deployment where "the backend is
// unavailable" is a certainty rather than a device question, so it is where the
// fallback policy can be pinned without a prepared OpenCL runner.

// Request field names this file writes more than once. The rest come from
// optimizer_engine_test.go, which owns the shared set.
const (
	fieldBackend         = "backend"
	fieldBackendFallback = "backendFallback"
)

func TestRendererForJobFailsAnUnavailableBackendByDefault(t *testing.T) {
	t.Parallel()

	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))

	rend, backend, cleanup, err := rendererForJob(JobConfig{Backend: app.BackendOpenCL}, ref, 2)
	if cleanup != nil {
		cleanup() // Must be safe on the failure path.
	}

	if err == nil {
		t.Fatal("rendererForJob() = nil error for an unavailable backend with no fallback configured")
	}

	// Failing is the default on purpose. A cost from the device is not
	// comparable with a cost from the CPU, so producing CPU numbers under a
	// GPU label would be worse than producing none.
	if !errors.Is(err, renderer.ErrBackendUnavailable) {
		t.Fatalf("error = %v, want ErrBackendUnavailable", err)
	}

	if rend != nil || backend != "" {
		t.Fatalf("rendererForJob() = (%T, %q), want no renderer and no backend alongside the error", rend, backend)
	}
}

func TestRendererForJobFallsBackWhenAsked(t *testing.T) {
	t.Parallel()

	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))

	config := JobConfig{Backend: app.BackendOpenCL, BackendFallback: app.BackendCPU, Threads: 1}

	rend, backend, cleanup, err := rendererForJob(config, ref, 2)
	if err != nil {
		t.Fatalf("rendererForJob() = %v, want a cpu fallback", err)
	}
	defer cleanup()

	// The reported backend is the one that will actually answer, not the one
	// the configuration asked for. Nothing on the Renderer interface says which
	// implementation this is, which is why the value is returned at all.
	if backend != app.BackendCPU {
		t.Fatalf("effective backend = %q, want %q", backend, app.BackendCPU)
	}

	cpu, ok := rend.(*renderer.CPURenderer)
	if !ok {
		t.Fatalf("renderer type = %T, want *renderer.CPURenderer", rend)
	}

	// The fallback is a fully configured job renderer, not a bare default: a
	// run that fell back still has to honour the parallelism it was given.
	if cpu.Threads() != 1 {
		t.Fatalf("fallback renderer threads = %d, want 1", cpu.Threads())
	}
}

func TestRendererForJobDoesNotFallBackForOtherFailures(t *testing.T) {
	t.Parallel()

	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))

	config := JobConfig{Backend: "vulkan", BackendFallback: app.BackendCPU}

	_, _, cleanup, err := rendererForJob(config, ref, 2)
	if cleanup != nil {
		cleanup()
	}

	// A misspelled backend is a request error. Running it on the CPU would hide
	// the mistake behind a result that looks fine.
	if !errors.Is(err, renderer.ErrUnknownBackend) {
		t.Fatalf("error = %v, want ErrUnknownBackend rather than a silent fallback", err)
	}
}

func TestCreateJobRejectsAnUnavailableBackendAtSubmit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, imgPath)

	srv := NewServerWithOptions(":8080", nil, ServerOptions{
		InputRoots: []string{tmpDir},
		DataRoot:   t.TempDir(),
	})
	shutdownTestServer(t, srv)

	t.Run("an explicit request is refused", func(t *testing.T) {
		t.Parallel()

		body, _ := json.Marshal(map[string]any{fieldRefPath: imgPath, fieldBackend: app.BackendOpenCL})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		w := httptest.NewRecorder()

		srv.handleCreateJob(w, req)

		// Accepting it would defer the same failure to a worker minutes later,
		// where it reads as a job that broke rather than one that could never
		// have run here.
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
		}

		if !strings.Contains(w.Body.String(), "build") {
			t.Fatalf("body = %s, want it to name the build as the reason", w.Body.String())
		}
	})

	t.Run("a configured fallback is accepted", func(t *testing.T) {
		t.Parallel()

		body, _ := json.Marshal(map[string]any{
			fieldRefPath:         imgPath,
			fieldBackend:         app.BackendOpenCL,
			fieldBackendFallback: app.BackendCPU,
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		w := httptest.NewRecorder()

		srv.handleCreateJob(w, req)

		// The fallback is the caller stating that this backend is not required,
		// so refusing the job would defeat the field.
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
		}
	})
}

// A completed job has to be able to say which backend produced its cost. The
// value is recorded from the renderer rather than copied from the request,
// because the two differ whenever a fallback resolved an unavailable backend.
func TestRunJobRecordsTheBackendItActuallyRanOn(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)

	manager := NewJobManager()
	job := manager.CreateJob(app.DefaultProject, JobConfig{
		RefPath:         imgPath,
		Mode:            modeJoint,
		Circles:         2,
		Iters:           5,
		PopSize:         10,
		Seed:            42,
		Backend:         app.BackendOpenCL,
		BackendFallback: app.BackendCPU,
	})

	err := runJob(t.Context(), manager, nil, job.ID)
	if err != nil {
		t.Fatalf("runJob() = %v, want the cpu fallback to carry the run", err)
	}

	updated, ok := manager.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found after runJob")
	}

	if updated.State != StateCompleted {
		t.Fatalf("state = %s, want %s", updated.State, StateCompleted)
	}

	if updated.EffectiveBackend != app.BackendCPU {
		t.Fatalf("effective backend = %q, want %q", updated.EffectiveBackend, app.BackendCPU)
	}

	// The request is kept as it was. A run that fell back has to record both
	// halves, or the record cannot say that a fallback happened at all.
	if updated.Config.Backend != app.BackendOpenCL {
		t.Fatalf("configured backend = %q, want the request preserved as %q", updated.Config.Backend, app.BackendOpenCL)
	}

	// The CPU renderer never degrades; that flag is reserved for a device that
	// started and then gave up.
	if updated.BackendDegraded {
		t.Fatal("BackendDegraded = true for a run that never reached a device")
	}
}

// The two fields have to reach a client, or recording them changes nothing that
// anyone can act on. This walks the status projection and the list projection,
// which are the two shapes a client reads a finished run through.
func TestBackendFactsReachTheAPIProjections(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, imgPath)

	srv := NewServerWithOptions(":8080", nil, ServerOptions{
		InputRoots: []string{tmpDir},
		DataRoot:   t.TempDir(),
	})
	shutdownTestServer(t, srv)

	job := srv.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Circles: 2, Iters: 2, PopSize: 10})

	err := srv.jobManager.UpdateJob(job.ID, func(j *Job) {
		j.EffectiveBackend = app.BackendOpenCL
		j.BackendDegraded = true
	})
	if err != nil {
		t.Fatalf("UpdateJob() = %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil)
	w := httptest.NewRecorder()

	srv.handleGetJobStatus(w, req, job.ID)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var status struct {
		EffectiveBackend app.Backend `json:"effectiveBackend"`
		BackendDegraded  bool        `json:"backendDegraded"`
	}

	err = json.NewDecoder(w.Body).Decode(&status)
	if err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if status.EffectiveBackend != app.BackendOpenCL || !status.BackendDegraded {
		t.Fatalf("status projection = %+v, want opencl and degraded", status)
	}

	summaries := srv.jobManager.ListJobSummaries()

	index := slices.IndexFunc(summaries, func(summary JobSummary) bool { return summary.ID == job.ID })
	if index < 0 {
		t.Fatalf("job %s missing from the summary listing", job.ID)
	}

	if summaries[index].EffectiveBackend != app.BackendOpenCL || !summaries[index].BackendDegraded {
		t.Fatalf("summary = %+v, want opencl and degraded", summaries[index])
	}
}
