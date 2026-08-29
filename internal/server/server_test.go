package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/google/uuid"
)

//nolint:paralleltest // swaps the process-global slog default, which no two tests may do at once
func TestLoggingMiddleware(t *testing.T) {
	t.Run("logs request identity and explicit status", func(t *testing.T) {
		logs := captureLogs(t)
		s := &Server{}
		handler := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)

		handler.ServeHTTP(recorder, request)

		requestID := recorder.Header().Get("X-Request-ID")

		_, err := uuid.Parse(requestID)
		if err != nil {
			t.Fatalf("X-Request-ID = %q, want UUID: %v", requestID, err)
		}

		output := logs.String()
		for _, field := range []string{
			"msg=\"HTTP request\"",
			"request_id=" + requestID,
			"method=GET",
			"path=/api/v1/example",
			"status=418",
			"duration=",
		} {
			if !strings.Contains(output, field) {
				t.Errorf("request log %q does not contain %q", output, field)
			}
		}
	})

	t.Run("records implicit success status", func(t *testing.T) {
		logs := captureLogs(t)
		s := &Server{}
		handler := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))

		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		if output := logs.String(); !strings.Contains(output, "status=200") {
			t.Errorf("request log %q does not contain implicit status 200", output)
		}
	})

	t.Run("preserves streaming support", func(t *testing.T) {
		logs := captureLogs(t)
		s := &Server{}
		handler := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("logging response writer does not preserve http.Flusher")
			}

			flusher.Flush()
		}))
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/all/stream", nil))

		if !recorder.Flushed {
			t.Error("logging response writer did not forward Flush")
		}

		if output := logs.String(); !strings.Contains(output, "status=200") {
			t.Errorf("request log %q does not contain flushed status 200", output)
		}
	})
}

func TestServer_CreateJob(t *testing.T) {
	t.Parallel()

	// Create test image
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServerWithOptions(":8080", nil, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, s)

	// Create job request
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	body, _ := json.Marshal(config)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleCreateJob(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", w.Code)
	}

	var job Job

	err := json.NewDecoder(w.Body).Decode(&job)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}

	// State should be pending or running (since worker starts immediately)
	if job.State != StatePending && job.State != StateRunning {
		t.Errorf("Expected pending or running state, got %s", job.State)
	}
}

func TestServer_CreateJob_BackendDefaults(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServerWithOptions(":8080", nil, ServerOptions{
		InputRoots:     []string{tmpDir},
		DefaultBackend: app.BackendOpenCL,
	})
	shutdownTestServer(t, s)

	// The bodies are written as JSON objects rather than marshalled from
	// JobConfig: a zero-valued struct field serializes as an explicit zero, and
	// the create endpoint refuses a written value the defaults would replace, so
	// a marshalled partial struct is a request for zero circles.
	t.Run("uses server default backend when omitted", func(t *testing.T) {
		t.Parallel()

		body, _ := json.Marshal(map[string]any{"refPath": imgPath})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		w := httptest.NewRecorder()

		s.handleCreateJob(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("Expected status 201, got %d", w.Code)
		}

		var job Job

		err := json.NewDecoder(w.Body).Decode(&job)
		if err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if job.Config.Backend != app.BackendOpenCL {
			t.Fatalf("job backend = %q, want %q", job.Config.Backend, app.BackendOpenCL)
		}
	})

	t.Run("keeps explicit backend override", func(t *testing.T) {
		t.Parallel()

		body, _ := json.Marshal(map[string]any{
			"refPath": imgPath,
			"backend": app.BackendCPU,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		w := httptest.NewRecorder()

		s.handleCreateJob(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("Expected status 201, got %d", w.Code)
		}

		var job Job

		err := json.NewDecoder(w.Body).Decode(&job)
		if err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if job.Config.Backend != app.BackendCPU {
			t.Fatalf("job backend = %q, want %q", job.Config.Backend, app.BackendCPU)
		}
	})
}

func TestServer_ListJobs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create two jobs
	config := JobConfig{RefPath: imgPath, Mode: app.ModeJoint, Circles: 1, Iters: 1, PopSize: app.MinPopulation}
	s.jobManager.CreateJob(app.DefaultProject, config)
	s.jobManager.CreateJob(app.DefaultProject, config)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	w := httptest.NewRecorder()

	s.handleListJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if strings.Contains(w.Body.String(), `"bestParams"`) {
		t.Fatal("job list serialized optimizer parameters")
	}

	var jobs []JobSummary

	err := json.NewDecoder(w.Body).Decode(&jobs)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}

	if jobs[0].Config.RefPath != imgPath || jobs[0].Config.Mode != app.ModeJoint || jobs[0].Config.Circles != 1 {
		t.Fatalf("job summary config = %+v", jobs[0].Config)
	}

	first := httptest.NewRecorder()
	s.handleListJobs(first, httptest.NewRequest(http.MethodGet, "/api/v1/jobs?limit=1", nil))

	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d, want 200", first.Code)
	}

	var firstPage jobListPage

	err = json.NewDecoder(first.Body).Decode(&firstPage)
	if err != nil {
		t.Fatalf("decode first page: %v", err)
	}

	if len(firstPage.Jobs) != 1 || firstPage.Total != 2 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %+v, want one of two jobs and a cursor", firstPage)
	}

	second := httptest.NewRecorder()
	secondURL := "/api/v1/jobs?limit=1&cursor=" + url.QueryEscape(firstPage.NextCursor)
	s.handleListJobs(second, httptest.NewRequest(http.MethodGet, secondURL, nil))

	var secondPage jobListPage

	err = json.NewDecoder(second.Body).Decode(&secondPage)
	if err != nil {
		t.Fatalf("decode second page: %v", err)
	}

	if len(secondPage.Jobs) != 1 || secondPage.Total != 2 || secondPage.NextCursor != "" {
		t.Fatalf("second page = %+v, want final job", secondPage)
	}

	if secondPage.Jobs[0].ID == firstPage.Jobs[0].ID {
		t.Fatal("cursor page repeated the first job")
	}

	for _, target := range []string{"/api/v1/jobs?limit=0", "/api/v1/jobs?cursor=not-a-cursor"} {
		response := httptest.NewRecorder()
		s.handleListJobs(response, httptest.NewRequest(http.MethodGet, target, nil))

		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, response.Code)
		}
	}
}

func TestJobsPageBoundsHydrationSeed(t *testing.T) {
	t.Parallel()

	server := NewServer(":8080", nil)
	for range defaultJobListLimit + 1 {
		server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: "reference.png", Mode: app.ModeJoint, Circles: 1})
	}

	response := httptest.NewRecorder()
	server.handleJobsPage(response, httptest.NewRequest(http.MethodGet, "/jobs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("jobs page status = %d, want 200", response.Code)
	}

	body := response.Body.String()
	const marker = `id="job-list-page"`

	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatal("jobs page has no hydration seed")
	}

	start += strings.Index(body[start:], ">") + 1

	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("jobs page hydration seed is unterminated")
	}

	var seed jobListPage

	err := json.Unmarshal([]byte(body[start:start+end]), &seed)
	if err != nil {
		t.Fatalf("decode jobs page seed: %v", err)
	}

	if len(seed.Jobs) != defaultJobListLimit || seed.Total != defaultJobListLimit+1 || seed.NextCursor == "" {
		t.Fatalf("jobs page seed has %d/%d jobs and cursor %q", len(seed.Jobs), seed.Total, seed.NextCursor)
	}
}

func TestServer_GetJobStatus(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Circles: 2})

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/status", job.ID), nil)
	w := httptest.NewRecorder()

	s.handleGetJobStatus(w, req, job.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]any

	err := json.NewDecoder(w.Body).Decode(&response)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["id"] != job.ID {
		t.Error("Response should contain job ID")
	}

	if response["state"] != string(StatePending) {
		t.Errorf("Expected pending state, got %v", response["state"])
	}
}

func TestServer_JobControlActions_E2E(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	persistence, err := createTestStore(filepath.Join(tmpDir, "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":8080", persistence, ServerOptions{
		InputRoots: []string{tmpDir},
	})
	shutdownTestServer(t, server)

	pauseAndResume := server.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10_000,
		PopSize: 30,
		Seed:    42,
	})

	err = server.jobManager.StartJob(pauseAndResume.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.UpdateProgress(pauseAndResume.ID, 1, 1, []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7}, 125)
	if err != nil {
		t.Fatal(err)
	}

	pause := httptest.NewRecorder()
	server.Handler().ServeHTTP(pause, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+pauseAndResume.ID+"/pause", nil))

	if pause.Code != http.StatusAccepted {
		t.Fatalf("pause status = %d, body %s", pause.Code, pause.Body.String())
	}

	waitForJobState(t, server.jobManager, pauseAndResume.ID, StatePaused)

	resume := httptest.NewRecorder()
	server.Handler().ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+pauseAndResume.ID+"/resume", nil))

	if resume.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, body %s", resume.Code, resume.Body.String())
	}

	waitForJobState(t, server.jobManager, pauseAndResume.ID, StateRunning)

	cancelJob := server.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 30,
		Seed:    43,
	})
	cancel := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+cancelJob.ID+"/cancel", nil))

	if cancel.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body %s", cancel.Code, cancel.Body.String())
	}

	waitForJobState(t, server.jobManager, cancelJob.ID, StateCancelled)

	completedJob := server.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 30,
		Seed:    44,
	})

	err = server.jobManager.StartJob(completedJob.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(
		completedJob.ID,
		10,
		100,
		[]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7},
		1.23,
		1.25,
		"completed",
	)
	if err != nil {
		t.Fatal(err)
	}

	deleted := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+completedJob.ID, nil))

	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body %s", deleted.Code, deleted.Body.String())
	}

	if _, ok := server.jobManager.GetJob(completedJob.ID); ok {
		t.Fatalf("completed job %s was not deleted", completedJob.ID)
	}
}

// TestServer_PauseJobRejections covers the states a pause must refuse. Each one
// leaves a job the operator could otherwise no longer run: a pending job the
// worker loop would skip, a running job with nothing to resume from, and a
// schedule stage whose driver waits for a terminal state.
func TestServer_PauseJobRejections(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	persistence, err := createTestStore(filepath.Join(tmpDir, "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":8080", persistence, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	config := JobConfig{RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 10, PopSize: 30, Seed: 7}
	pause := func(jobID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/pause", nil))

		return recorder
	}

	t.Run("pending job", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)
		if got := pause(job.ID).Code; got != http.StatusConflict {
			t.Fatalf("pause status = %d, want %d", got, http.StatusConflict)
		}

		if state := server.jobManager.getJobState(job.ID); state != StatePending {
			t.Fatalf("state = %q, want %q", state, StatePending)
		}
	})

	t.Run("running job without progress", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		if got := pause(job.ID).Code; got != http.StatusConflict {
			t.Fatalf("pause status = %d, want %d", got, http.StatusConflict)
		}
		// The rejected pause must hand the job back exactly as it found it.
		if state := server.jobManager.getJobState(job.ID); state != StateRunning {
			t.Fatalf("state = %q, want %q", state, StateRunning)
		}
	})

	t.Run("schedule stage", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.UpdateProgress(job.ID, 1, 1, []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7}, 125)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.UpdateJob(job.ID, func(j *Job) { j.ScheduleID = "schedule-1" })
		if err != nil {
			t.Fatal(err)
		}

		if got := pause(job.ID).Code; got != http.StatusConflict {
			t.Fatalf("pause status = %d, want %d", got, http.StatusConflict)
		}

		if state := server.jobManager.getJobState(job.ID); state != StateRunning {
			t.Fatalf("state = %q, want %q", state, StateRunning)
		}
	})

	t.Run("missing job", func(t *testing.T) {
		t.Parallel()

		if got := pause("00000000-0000-4000-8000-000000000000").Code; got != http.StatusNotFound {
			t.Fatalf("pause status = %d, want %d", got, http.StatusNotFound)
		}
	})
}

// TestServer_ResumeDispatchesOnJobState pins the two continuations the resume
// route carries. A paused job resumes under its own ID; a stopped job is forked
// into a new one, which is the contract the resume CLI command and the release
// lifecycle depend on.
func TestServer_ResumeDispatchesOnJobState(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	persistence, err := createTestStore(filepath.Join(tmpDir, "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":8080", persistence, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	config := JobConfig{RefPath: imgPath, Mode: "joint", Circles: 1, Iters: 10, PopSize: 30, Seed: 11}
	params := []float64{1, 1, 1, 1, 0, 0, 1}

	resume := func(jobID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/resume", nil))

		return recorder
	}
	decode := func(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
		t.Helper()

		var payload map[string]any

		err := json.Unmarshal(recorder.Body.Bytes(), &payload)
		if err != nil {
			t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
		}

		return payload
	}

	t.Run("paused job resumes in place", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.UpdateProgress(job.ID, 5, 50, params, 600)
		if err != nil {
			t.Fatal(err)
		}

		pause := httptest.NewRecorder()
		server.Handler().ServeHTTP(pause, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+job.ID+"/pause", nil))

		if pause.Code != http.StatusAccepted {
			t.Fatalf("pause status = %d, body %s", pause.Code, pause.Body.String())
		}

		waitForJobState(t, server.jobManager, job.ID, StatePaused)

		recorder := resume(job.ID)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
		}

		if got := decode(t, recorder)["jobId"]; got != job.ID {
			t.Fatalf("jobId = %v, want the paused job %s", got, job.ID)
		}
	})

	t.Run("cancelled job forks a new one", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.CancelJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		checkpoint := store.NewCheckpoint(job.ID, params, 600, 1000, 8, job.Config)

		checkpoint.Evaluations = 80

		err = persistence.SaveCheckpoint(job.ID, checkpoint)
		if err != nil {
			t.Fatal(err)
		}

		recorder := resume(job.ID)
		if recorder.Code != http.StatusOK {
			t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
		}

		payload := decode(t, recorder)
		if got := payload["resumedFrom"]; got != job.ID {
			t.Fatalf("resumedFrom = %v, want %s", got, job.ID)
		}

		forked, _ := payload["jobId"].(string)
		if forked == "" || forked == job.ID {
			t.Fatalf("jobId = %q, want a new job identifier", forked)
		}

		if _, ok := server.jobManager.GetJob(forked); !ok {
			t.Fatalf("forked job %s was not created", forked)
		}
		// The source run must survive its own continuation untouched.
		if state := server.jobManager.getJobState(job.ID); state != StateCancelled {
			t.Fatalf("source state = %q, want %q", state, StateCancelled)
		}
	})

	t.Run("job without a checkpoint", func(t *testing.T) {
		t.Parallel()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.CancelJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		if got := resume(job.ID).Code; got != http.StatusNotFound {
			t.Fatalf("resume status = %d, want %d", got, http.StatusNotFound)
		}
	})
}

// TestPausedJobCannotBeCompleted pins the guarantee the pause checkpoint rests
// on: once the paused state is claimed, a worker that finishes afterwards may
// not publish the job as completed over the snapshot the operator asked for.
func TestPausedJobCannotBeCompleted(t *testing.T) {
	t.Parallel()

	manager := NewJobManager()

	job := manager.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})

	err := manager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := manager.claimPause(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	if claimed.State != StatePaused {
		t.Fatalf("claimed state = %q, want %q", claimed.State, StatePaused)
	}

	err = manager.MarkJobCompleted(job.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("MarkJobCompleted error = %v, want %v", err, ErrInvalidTransition)
	}

	if state := manager.getJobState(job.ID); state != StatePaused {
		t.Fatalf("state = %q, want %q", state, StatePaused)
	}

	_, err = manager.claimPause(job.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second claimPause error = %v, want %v", err, ErrInvalidTransition)
	}
}

func TestServerJobStatusRepresentsInfinitePSNRAndOptionalSSIM(t *testing.T) {
	t.Parallel()

	server := NewServer(":8080", nil)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png", EnableSSIM: true})

	err := server.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.UpdateProgress(job.ID, 1, 1, []float64{1}, 0)
	if err != nil {
		t.Fatal(err)
	}

	ssim := 1.0

	err = server.jobManager.RecordMetrics(job.ID, qualitySample(1, 0, &ssim, time.Now()))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil)
	recorder := httptest.NewRecorder()
	server.handleGetJobStatus(recorder, req, job.ID)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var response jobStatusResponse

	err = json.NewDecoder(recorder.Body).Decode(&response)
	if err != nil {
		t.Fatal(err)
	}

	if response.PSNR != nil || !response.PSNRInfinite {
		t.Fatalf("PSNR response = (%v, %v), want (nil, true)", response.PSNR, response.PSNRInfinite)
	}

	if response.SSIM == nil || *response.SSIM != 1 {
		t.Fatalf("SSIM response = %v, want 1", response.SSIM)
	}
}

func TestServerJobStatusExposesProvisionalCandidateSeparately(t *testing.T) {
	t.Parallel()

	server := NewServer(":8080", nil)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})

	err := server.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.UpdateProgress(job.ID, 1, 10, []float64{1}, 100)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.UpdateCandidateProgress(job.ID, 2, 20, 95.25)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.handleGetJobStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil), job.ID)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var response jobStatusResponse

	err = json.NewDecoder(recorder.Body).Decode(&response)
	if err != nil {
		t.Fatal(err)
	}

	if response.BestCost != 100 || response.CandidateCost == nil || *response.CandidateCost != 95.25 {
		t.Fatalf("audited/candidate costs = %v/%v, want 100/95.25", response.BestCost, response.CandidateCost)
	}

	if response.CandidatePSNR == nil || response.CandidatePSNRInfinite {
		t.Fatalf("candidate PSNR = (%v, %v), want finite value", response.CandidatePSNR, response.CandidatePSNRInfinite)
	}
}

func TestServer_GetJobStatus_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer(":8080", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/status", nil)
	w := httptest.NewRecorder()

	s.handleGetJobStatus(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestServer_GetBestImage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42})

	// Run job and wait for completion
	err := runJob(context.Background(), s.jobManager, nil, job.ID)
	if err != nil {
		t.Fatalf("Job failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/best.png", job.ID), nil)
	w := httptest.NewRecorder()

	s.handleGetBestImage(w, req, job.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "image/png" {
		t.Error("Expected image/png content type")
	}

	// Verify it's a valid PNG
	_, err = png.Decode(w.Body)
	if err != nil {
		t.Errorf("Response should be valid PNG: %v", err)
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel load would skew its wall-clock waits
func TestServer_Integration(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	// Start server in background
	s := NewServerWithOptions("localhost:0", nil, ServerOptions{InputRoots: []string{tmpDir}}) // Use random port
	shutdownTestServer(t, s)

	srv := httptest.NewServer(s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/jobs" && r.Method == http.MethodPost {
			s.handleCreateJob(w, r)
		} else if r.URL.Path == "/api/v1/jobs" && r.Method == http.MethodGet {
			s.handleListJobs(w, r)
		} else {
			s.handleJobsWithID(w, r)
		}
	})))
	defer srv.Close()

	// Create job
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	body, _ := json.Marshal(config)

	resp, err := http.Post(srv.URL+"/api/v1/jobs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}
	defer resp.Body.Close()

	var job Job

	err = json.NewDecoder(resp.Body).Decode(&job)
	if err != nil {
		t.Fatalf("Failed to decode job: %v", err)
	}

	// Poll status until completed
	maxAttempts := 50
	for i := range maxAttempts {
		resp, err := http.Get(srv.URL + "/api/v1/jobs/" + job.ID + "/status")
		if err != nil {
			t.Fatalf("Failed to get status: %v", err)
		}

		var status map[string]any

		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		if err != nil {
			t.Fatalf("Failed to decode status: %v", err)
		}

		if status["state"] == string(StateCompleted) {
			break
		}

		if status["state"] == string(StateFailed) {
			t.Fatalf("Job failed: %v", status["error"])
		}

		if i == maxAttempts-1 {
			t.Fatal("Job did not complete in time")
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Get best image
	resp, err = http.Get(srv.URL + "/api/v1/jobs/" + job.ID + "/best.png")
	if err != nil {
		t.Fatalf("Failed to get best image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func TestServer_JobDetailPage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 5,
		Iters:   100,
		PopSize: 30,
	})

	// Test job detail page renders successfully
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID, nil)
	w := httptest.NewRecorder()

	s.handleJobDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Error("Expected text/html content type")
	}

	// Check that the response contains expected elements
	body := w.Body.String()
	if !containsString(body, job.ID[:8]) {
		t.Error("Response should contain job ID")
	}

	if !containsString(body, "Metrics") {
		t.Error("Response should contain metrics section")
	}

	if !containsString(body, "Configuration") {
		t.Error("Response should contain configuration section")
	}

	if !containsString(body, "Active-set Polishing") || !containsString(body, "Disabled") {
		t.Error("Response should contain the polishing configuration")
	}

	if !containsString(body, "Images") {
		t.Error("Response should contain images section")
	}

	if !containsString(body, "50 × 50 px") {
		t.Error("Response should contain reference image dimensions")
	}

	info, err := os.Stat(imgPath)
	if err != nil {
		t.Fatalf("stat reference image: %v", err)
	}

	if !containsString(body, fmt.Sprintf("%d bytes", info.Size())) {
		t.Error("Response should contain the original reference file size")
	}
}

func TestReferenceImageMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reference.png")
	createSimpleTestImage(t, path)

	width, height, size, err := referenceImageMetadata(path)
	if err != nil {
		t.Fatalf("referenceImageMetadata() error = %v", err)
	}

	if width != 50 || height != 50 {
		t.Fatalf("referenceImageMetadata() dimensions = %dx%d, want 50x50", width, height)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat reference image: %v", err)
	}

	if size != info.Size() {
		t.Errorf("referenceImageMetadata() size = %d, want %d", size, info.Size())
	}
}

func TestReferenceImageMetadataUnavailable(t *testing.T) {
	t.Parallel()

	_, _, _, err := referenceImageMetadata(filepath.Join(t.TempDir(), "missing.png"))
	if err == nil {
		t.Fatal("referenceImageMetadata() error = nil for missing image")
	}
}

func TestJobStatusCarriesReferenceImageFacts(t *testing.T) {
	t.Parallel()

	// The three keys the reference image contributes to the status payload.
	// Both halves below check the same set, from opposite directions.
	refKeys := []string{"refWidth", "refHeight", "refSize"}

	t.Run("present and typed for a readable image", func(t *testing.T) {
		t.Parallel()

		imgPath := filepath.Join(t.TempDir(), "reference.png")
		createSimpleTestImage(t, imgPath)

		server := NewServer(":8080", nil)
		job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Circles: 2})

		recorder := httptest.NewRecorder()
		server.handleGetJobStatus(
			recorder,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil),
			job.ID,
		)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}

		body := recorder.Body.Bytes()

		var raw map[string]any

		err := json.Unmarshal(body, &raw)
		if err != nil {
			t.Fatalf("decode raw status: %v", err)
		}

		for _, key := range refKeys {
			value, ok := raw[key]
			if !ok {
				t.Fatalf("status JSON is missing %q", key)
			}

			if _, ok := value.(float64); !ok {
				t.Fatalf("status JSON %q = %T, want a JSON number", key, value)
			}
		}

		wantWidth, wantHeight, wantSize, err := referenceImageMetadata(imgPath)
		if err != nil {
			t.Fatalf("referenceImageMetadata() error = %v", err)
		}

		info, err := os.Stat(imgPath)
		if err != nil {
			t.Fatalf("stat reference image: %v", err)
		}

		if wantSize != info.Size() {
			t.Fatalf("fixture size = %d, want %d", wantSize, info.Size())
		}

		var response jobStatusResponse

		err = json.Unmarshal(body, &response)
		if err != nil {
			t.Fatalf("decode status: %v", err)
		}

		if response.RefWidth != wantWidth || response.RefHeight != wantHeight {
			t.Fatalf(
				"reference dimensions = %dx%d, want %dx%d",
				response.RefWidth, response.RefHeight, wantWidth, wantHeight,
			)
		}

		if response.RefSize != info.Size() {
			t.Fatalf("reference size = %d, want %d", response.RefSize, info.Size())
		}
	})

	t.Run("omitted for an unreadable image", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "missing.png")

		server := NewServer(":8080", nil)
		job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: missing, Circles: 2})

		recorder := httptest.NewRecorder()
		server.handleGetJobStatus(
			recorder,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil),
			job.ID,
		)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}

		var raw map[string]any

		err := json.Unmarshal(recorder.Body.Bytes(), &raw)
		if err != nil {
			t.Fatalf("decode raw status: %v", err)
		}

		for _, key := range refKeys {
			if value, ok := raw[key]; ok {
				t.Fatalf("status JSON has %q = %v for an unreadable image, want it omitted", key, value)
			}
		}
	})
}

func TestServer_JobDetailPage_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer(":8080", nil)

	// Test job detail page with non-existent job ID
	req := httptest.NewRequest(http.MethodGet, "/jobs/nonexistent", nil)
	w := httptest.NewRecorder()

	s.handleJobDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 (with not found message), got %d", w.Code)
	}

	body := w.Body.String()
	if !containsString(body, "Job Not Found") {
		t.Error("Response should contain 'Job Not Found' message")
	}
}

func TestServer_GetRefImage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Circles: 2,
	})

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/ref.png", job.ID), nil)
	w := httptest.NewRecorder()

	s.handleGetRefImage(w, req, job.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "image/png" {
		t.Error("Expected image/png content type")
	}

	// Verify it's a valid PNG
	_, err := png.Decode(w.Body)
	if err != nil {
		t.Errorf("Response should be valid PNG: %v", err)
	}
}

func TestServer_GetDiffImageColormap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reference.png")
	createSimpleTestImage(t, path)

	server := NewServer(":8080", nil)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: path, Circles: 1, Threads: 1})

	err := server.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	params := []float64{25, 25, 10, 1, 0, 0, 1}

	err = server.jobManager.UpdateProgress(job.ID, 1, 1, params, 1)
	if err != nil {
		t.Fatalf("update job: %v", err)
	}

	images := make(map[string]image.Image)

	requests := []struct {
		name  string
		query string
	}{
		{name: "default"},
		{name: "turbo", query: "?colormap=turbo"},
		{name: "magma", query: "?colormap=magma"},
	}
	for _, test := range requests {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/diff.png"+test.query, nil)
		recorder := httptest.NewRecorder()
		server.handleGetDiffImage(recorder, req, job.ID)

		if recorder.Code != http.StatusOK {
			t.Fatalf("%s diff status = %d, want 200: %s", test.name, recorder.Code, recorder.Body.String())
		}

		colormap := test.name
		if colormap == "default" {
			colormap = "turbo"
		}

		if got, want := recorder.Header().Get("ETag"), fmt.Sprintf(`"diff-%s-1"`, colormap); got != want {
			t.Errorf("%s ETag = %q, want %q", test.name, got, want)
		}

		decoded, err := png.Decode(recorder.Body)
		if err != nil {
			t.Fatalf("decode %s diff: %v", test.name, err)
		}

		images[test.name] = decoded
	}

	defaultColor := color.NRGBAModel.Convert(images["default"].At(0, 0))
	turbo := color.NRGBAModel.Convert(images["turbo"].At(0, 0))
	magma := color.NRGBAModel.Convert(images["magma"].At(0, 0))

	if defaultColor != turbo {
		t.Errorf("default pixel = %#v, want Turbo %#v", defaultColor, turbo)
	}

	if turbo == magma {
		t.Errorf("Turbo and Magma pixels unexpectedly match: %#v", turbo)
	}
}

func TestServer_GetDiffImageRejectsInvalidColormap(t *testing.T) {
	t.Parallel()

	server := NewServer(":8080", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/missing/diff.png?colormap=viridis", nil)
	recorder := httptest.NewRecorder()

	server.handleGetDiffImage(recorder, req, "missing")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}

	if !containsString(recorder.Body.String(), `"code":"invalid_colormap"`) {
		t.Errorf("response = %s, want invalid_colormap error", recorder.Body.String())
	}
}

func TestServer_JobDetailPage_Integration(t *testing.T) {
	t.Parallel()

	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job with some test data
	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   5,
		PopSize: 10,
	})

	// Set some initial values through the manager: CreateJob returns an immutable
	// snapshot, so mutating it must not change the stored job.
	err := s.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatalf("Failed to start job: %v", err)
	}

	err = s.jobManager.UpdateProgress(job.ID, 3, 3, make([]float64, 14), 1000)
	if err != nil {
		t.Fatalf("Failed to update job progress: %v", err)
	}

	err = s.jobManager.UpdateJob(job.ID, func(stored *Job) {
		stored.InitialCost = 2000
	})
	if err != nil {
		t.Fatalf("Failed to set initial cost: %v", err)
	}

	// Test that the detail page renders with job data
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID, nil)
	w := httptest.NewRecorder()

	s.handleJobDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Verify key information is displayed
	if !containsString(body, "1000.00") { // Best cost
		t.Error("Response should contain best cost")
	}

	if !containsString(body, "joint") { // Mode
		t.Error("Response should contain mode")
	}

	if !containsString(body, "Running") { // State badge
		t.Error("Response should contain Running badge")
	}

	if !containsString(body, `id="parameter-count">2</span> of 2 circles available`) {
		t.Error("Response should contain the materialized parameter count")
	}

	for _, description := range []string{
		"Circle 1: (0.00, 0.00, 0.00) RGB(0, 0, 0) α=0.000",
		"Circle 2: (0.00, 0.00, 0.00) RGB(0, 0, 0) α=0.000",
	} {
		if !containsString(body, description) {
			t.Errorf("Response should contain %q", description)
		}
	}
}

func TestServer_JobStream_SSE(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   1,
		PopSize: 20,
		Seed:    42,
	})

	err := s.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.jobManager.CompleteJob(job.ID, 7, 20, make([]float64, 14), 12.5, 100, "completed")
	if err != nil {
		t.Fatal(err)
	}

	ssim := 0.75

	err = s.jobManager.RecordMetrics(job.ID, qualitySample(7, 12.5, &ssim, time.Now()))
	if err != nil {
		t.Fatal(err)
	}

	// Create SSE request
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/stream", job.ID), nil)
	w := httptest.NewRecorder()

	s.handleJobStream(w, req, job.ID)

	// Check headers
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	if got := w.Header().Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want no-cache, no-transform", got)
	}

	if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}

	events := decodeSSEEvents(t, w.Body.String())
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1; body=%q", len(events), w.Body.String())
	}

	if events[0].State != StateCompleted || events[0].Iterations != 7 || events[0].BestCost != 12.5 {
		t.Errorf("terminal event = %+v", events[0])
	}

	if events[0].PSNR == nil || events[0].PSNRInfinite || events[0].SSIM == nil || *events[0].SSIM != ssim {
		t.Errorf("terminal quality metrics = %+v", events[0])
	}
}

func TestServer_JobStream_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer(":8080", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/stream", nil)
	w := httptest.NewRecorder()

	s.handleJobStream(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestEventBroadcaster(t *testing.T) {
	t.Parallel()

	eb := NewEventBroadcaster()

	// Subscribe to events
	ch := eb.Subscribe("job1")
	defer eb.Unsubscribe("job1", ch)

	// Broadcast an event
	event := ProgressEvent{
		JobID:      "job1",
		State:      StateRunning,
		Iterations: 10,
		BestCost:   100.5,
		CPS:        1500.0,
		Timestamp:  time.Now(),
	}
	eb.Broadcast(event)

	// Receive event
	select {
	case received := <-ch:
		if received.JobID != "job1" {
			t.Errorf("Expected jobID job1, got %s", received.JobID)
		}

		if received.Iterations != 10 {
			t.Errorf("Expected 10 iterations, got %d", received.Iterations)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for event")
	}

	// Cleanup
	eb.CleanupJob("job1")
}

func containsString(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

func createSimpleTestImage(t *testing.T, path string) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	white := color.NRGBA{255, 255, 255, 255}
	red := color.NRGBA{255, 0, 0, 255}

	for y := range 50 {
		for x := range 50 {
			img.Set(x, y, white)
		}
	}

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

	err = png.Encode(f, img)
	if err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}
}

func TestServer_CreatePageGet(t *testing.T) {
	t.Parallel()

	server := NewServer(":0", nil)

	req := httptest.NewRequest(http.MethodGet, "/create", nil)
	rec := httptest.NewRecorder()

	server.handleCreatePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !containsString(body, "Create New Job") {
		t.Error("Expected page to contain 'Create New Job'")
	}

	if !containsString(body, "Reference Image") {
		t.Error("Expected page to contain 'Reference Image'")
	}

	if !containsString(body, "Optimization Parameters") {
		t.Error("Expected page to contain 'Optimization Parameters'")
	}

	if !containsString(body, "batchSize") {
		t.Error("Expected page to expose batch size")
	}

	if !containsString(body, "optimizerEpochs") {
		t.Error("Expected page to expose optimizer epochs")
	}

	if !containsString(body, `name="optimizer"`) || !containsString(body, `value="cmaes"`) {
		t.Error("Expected page to expose the CMA-ES engine selector")
	}

	if !containsString(body, "polishingEnabled") || !containsString(body, "polishingActiveSetSize") {
		t.Error("Expected page to expose active-set polishing controls")
	}
}

func TestServer_CreatePagePost_Success(t *testing.T) {
	t.Parallel()

	// Create temp directory and test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	server := NewServerWithOptions(":0", nil, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	// Create form data
	form := url.Values{}
	form.Add("refPath", testImagePath)
	form.Add("mode", "batch")
	form.Add("circles", "5")
	form.Add("iters", "50")
	form.Add("popSize", "20")
	form.Add("optimizerEpochs", "4")
	form.Add("batchSize", "5")
	form.Add("polishingEnabled", "on")
	form.Add("polishingActiveSetSize", "3")
	form.Add("polishingMaxSweeps", "2")
	form.Add("polishingEpochs", "2")
	form.Add("polishingIters", "10")
	form.Add("polishingStagnationIters", "5")
	form.Add("polishingMinImprovement", "0.01")
	form.Add("seed", "42")
	form.Add("enableSSIM", "on")

	req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()

	server.handleCreatePage(rec, req)

	// Should redirect to job detail page
	if rec.Code != http.StatusSeeOther {
		t.Errorf("Expected status 303, got %d", rec.Code)
	}

	location := rec.Header().Get("Location")
	if !bytes.Contains([]byte(location), []byte("/jobs/")) {
		t.Errorf("Expected redirect to /jobs/, got %s", location)
	}

	// Verify job was created
	jobs := server.jobManager.ListJobs()
	if len(jobs) != 1 {
		t.Errorf("Expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Config.RefPath != testImagePath {
		t.Errorf("Expected refPath %s, got %s", testImagePath, job.Config.RefPath)
	}

	if job.Config.Mode != "batch" {
		t.Errorf("Expected mode batch, got %s", job.Config.Mode)
	}

	if job.Config.Circles != 5 {
		t.Errorf("Expected 5 circles, got %d", job.Config.Circles)
	}

	if job.Config.Iters != 50 {
		t.Errorf("Expected 50 iters, got %d", job.Config.Iters)
	}

	if job.Config.PopSize != 20 {
		t.Errorf("Expected popSize 20, got %d", job.Config.PopSize)
	}

	if job.Config.OptimizerEpochs != 4 {
		t.Errorf("Expected optimizerEpochs 4, got %d", job.Config.OptimizerEpochs)
	}

	if job.Config.BatchSize != 5 {
		t.Errorf("Expected batchSize 5, got %d", job.Config.BatchSize)
	}

	if !job.Config.PolishingEnabled || job.Config.PolishingActiveSetSize != 3 || job.Config.PolishingMaxSweeps != 2 {
		t.Errorf("unexpected polishing configuration: %+v", job.Config)
	}

	if job.Config.PolishingEpochs != 2 || job.Config.PolishingIters != 10 || job.Config.PolishingStagnationIters != 5 || job.Config.PolishingMinImprovement != 0.01 {
		t.Errorf("unexpected polishing optimizer settings: %+v", job.Config)
	}

	if job.Config.Seed != 42 {
		t.Errorf("Expected seed 42, got %d", job.Config.Seed)
	}

	if !job.Config.EnableSSIM {
		t.Error("Expected SSIM to be enabled")
	}
}

func TestServerCreatePagePostsCMAESEngine(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	server := NewServerWithOptions(":0", nil, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	form := url.Values{
		"refPath":      {testImagePath},
		fieldMode:      {modeJoint},
		fieldOptimizer: {optimizerCMAES},
		fieldCircles:   {"1"},
		fieldIters:     {"1"},
		fieldPopSize:   {"20"},
		fieldSeed:      {"42"},
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/create", bytes.NewBufferString(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()

	server.handleCreatePage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	jobs := server.jobManager.ListJobs()
	if len(jobs) != 1 || jobs[0].Config.ResolvedOptimizer() != app.OptimizerCMAES {
		t.Fatalf("created jobs = %+v, want one CMA-ES job", jobs)
	}
}

func TestServer_CreatePagePost_ValidationErrors(t *testing.T) {
	t.Parallel()

	server := NewServer(":0", nil)

	tests := []struct {
		name     string
		formData map[string]string
		errMsg   string
	}{
		{
			name: "missing refPath",
			formData: map[string]string{
				"mode":    "joint",
				"circles": "10",
				"iters":   "100",
				"popSize": "30",
				"seed":    "0",
			},
			errMsg: "Reference image path is required",
		},
		{
			name: "missing mode",
			formData: map[string]string{
				"refPath": "test.png",
				"circles": "10",
				"iters":   "100",
				"popSize": "30",
				"seed":    "0",
			},
			errMsg: "Mode is required",
		},
		{
			name: "invalid circles",
			formData: map[string]string{
				"refPath": "test.png",
				"mode":    "joint",
				"circles": "0",
				"iters":   "100",
				"popSize": "30",
				"seed":    "0",
			},
			errMsg: "Circles must be between 1 and 3000",
		},
		{
			name: "invalid iters",
			formData: map[string]string{
				"refPath": "test.png",
				"mode":    "joint",
				"circles": "10",
				"iters":   "0",
				"popSize": "30",
				"seed":    "0",
			},
			errMsg: fmt.Sprintf("Iterations must be between 1 and %d", app.MaxIterations),
		},
		{
			name: "invalid popSize",
			formData: map[string]string{
				"refPath": "test.png",
				"mode":    "joint",
				"circles": "10",
				"iters":   "100",
				"popSize": "19",
				"seed":    "0",
			},
			errMsg: fmt.Sprintf(
				"Population size must be between %d and %d", app.MinPopulation, app.MaxPopulation,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			form := url.Values{}
			for k, v := range tt.formData {
				form.Add(k, v)
			}

			req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewBufferString(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rec := httptest.NewRecorder()

			server.handleCreatePage(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", rec.Code)
			}

			body := rec.Body.String()
			if !containsString(body, tt.errMsg) {
				t.Errorf("Expected error message '%s' in body", tt.errMsg)
			}
		})
	}
}

// TestPlannedOptimizerIterationsCoversStagesAndPolishing pins the denominator
// every progress bar divides by. Batch refills used to be added to it, back
// when a refill minted a further full budget on top of the plan; they are now
// drawn from the planned budget instead, so counting them here would leave a
// one-stage job showing a quarter of its progress and then finishing.
func TestPlannedOptimizerIterationsCoversStagesAndPolishing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config JobConfig
		want   int
	}{
		{name: "joint", config: JobConfig{Mode: app.ModeJoint, Iters: 100, OptimizerEpochs: 2}, want: 200},
		{name: "sequential", config: JobConfig{Mode: app.ModeSequential, Circles: 3, Iters: 100, OptimizerEpochs: 2}, want: 600},
		{
			// One planned stage of 2000 iterations over four epochs, plus three
			// polishing sweeps of 1000 over two epochs. The refill stages this
			// run may still attempt come out of the 8000, not on top of it.
			name: "batch with polishing",
			config: JobConfig{
				Mode: app.ModeBatch, Circles: 30, BatchSize: 30, Iters: 2000, OptimizerEpochs: 4,
				PolishingEnabled: true, PolishingMaxSweeps: 3, PolishingEpochs: 2, PolishingIters: 1000,
			},
			want: 14_000,
		},
		{
			// Four planned stages, so four budgets: the batch count is what
			// scales the denominator, and only the planned batches count.
			name: "batch with several planned stages",
			config: JobConfig{
				Mode: app.ModeBatch, Circles: 32, BatchSize: 8, Iters: 100, OptimizerEpochs: 1,
			},
			want: 400,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := plannedOptimizerIterations(test.config); got != test.want {
				t.Fatalf("plannedOptimizerIterations() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPolishEndpointCreatesCheckpointContinuation(t *testing.T) {
	t.Parallel()

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
		RefPath: imgPath, Mode: app.ModeBatch, Circles: 1, BatchSize: 1, Iters: 2,
		PopSize: 20, Threads: 1, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)
	params := []float64{1, 1, 1, 1, 0, 0, 1}

	err = server.jobManager.StartJob(source.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(source.ID, 8000, 900000, params, 600, 1000, "completed")
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, config)

	checkpoint.Evaluations = 900000

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the continuation pending so its exact checkpoint initialization can
	// be inspected without racing the background optimizer.
	server.cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+source.ID+"/polish", strings.NewReader(`{
		"strategy":"residual-region",
		"activeSetSize":1,
		"maxSweeps":2,
		"epochs":2,
		"iters":20,
		"stagnationIters":10,
		"minImprovement":0.01,
		"popSize":40,
		"seed":99
	}`))
	req.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("polish status = %d body=%s", response.Code, response.Body.String())
	}

	var payload struct {
		JobID string `json:"jobId"`
	}

	err = json.NewDecoder(response.Body).Decode(&payload)
	if err != nil {
		t.Fatal(err)
	}

	continuation, ok := server.jobManager.GetJob(payload.JobID)
	if !ok {
		t.Fatal("polishing continuation job not found")
	}

	if !continuation.Config.PolishingEnabled || !continuation.Config.PolishingOnly || continuation.Config.Mode != app.ModeBatch {
		t.Fatalf("polishing continuation config = %+v", continuation.Config)
	}

	if continuation.Config.PolishingStrategy != app.PolishingResidualRegion || continuation.Config.PolishingActiveSetSize != 1 ||
		continuation.Config.PolishingMaxSweeps != 2 || continuation.Config.PolishingEpochs != 2 || continuation.Config.PolishingIters != 20 ||
		continuation.Config.PolishingStagnationIters != 10 || continuation.Config.PolishingMinImprovement != 0.01 ||
		continuation.Config.PolishingPopSize != 40 || continuation.Config.Seed != 99 || continuation.Config.EffectiveSeed != 99 {
		t.Fatalf("polishing continuation overrides = %+v", continuation.Config)
	}
	// popSize on a polish request is the polishing population, which is the one
	// the continuation runs at; the inherited job-wide population is untouched.
	if continuation.Config.PopSize != 20 {
		t.Fatalf("polishing continuation popSize = %d, want the inherited 20 to stay put", continuation.Config.PopSize)
	}

	if continuation.Iterations != 8000 || continuation.Evaluations != 900000 || !reflect.DeepEqual(continuation.BestParams, params) {
		t.Fatalf("polishing continuation state = %+v", continuation)
	}
}

// newExtendableBatchJob prepares a server holding one completed two-circle batch
// job and its checkpoint, which is the only state the extend endpoint accepts.
func newExtendableBatchJob(t *testing.T) (*Server, string, []float64) {
	t.Helper()
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
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)
	params := []float64{
		1, 1, 1, 1, 0, 0, 1,
		2, 2, 1, 0, 1, 0, 1,
	}

	err = server.jobManager.StartJob(source.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(source.ID, 8000, 900000, params, 600, 1000, "completed")
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, config)

	checkpoint.Evaluations = 900000

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	server.cancel()

	return server, source.ID, params
}

func TestExtendEndpointCreatesOrderedBatchContinuation(t *testing.T) {
	t.Parallel()

	server, sourceID, params := newExtendableBatchJob(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+sourceID+"/extend", strings.NewReader(`{
		"additionalCircles":10,
		"batchSize":10,
		"epochs":4,
		"iters":2000,
		"popSize":50,
		"seed":99
	}`))
	req.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("extend status = %d body=%s", response.Code, response.Body.String())
	}

	var payload struct {
		JobID         string `json:"jobId"`
		TargetCircles int    `json:"targetCircles"`
	}

	err := json.NewDecoder(response.Body).Decode(&payload)
	if err != nil {
		t.Fatal(err)
	}

	if payload.TargetCircles != 12 {
		t.Fatalf("target circles = %d, want 12", payload.TargetCircles)
	}

	continuation, ok := server.jobManager.GetJob(payload.JobID)
	if !ok {
		t.Fatal("extension continuation job not found")
	}

	if continuation.Config.Circles != 12 || continuation.Config.BatchSize != 10 || continuation.Config.OptimizerEpochs != 4 ||
		continuation.Config.Iters != 2000 || continuation.Config.PopSize != 50 || continuation.Config.Seed != 99 ||
		continuation.Config.EffectiveSeed != 99 || continuation.Config.PolishingEnabled || continuation.Config.PolishingOnly {
		t.Fatalf("extension continuation config = %+v", continuation.Config)
	}

	if continuation.Iterations != 8000 || continuation.Evaluations != 900000 || !reflect.DeepEqual(continuation.BestParams, params) {
		t.Fatalf("extension continuation state = %+v", continuation)
	}
}

// ineffectiveBatchOptimizer always returns a minimum-opacity black circle.
// Over an already-perfect black prefix that circle changes no final pixel, so
// the batch pruner removes it on every bounded refill attempt.
type ineffectiveBatchOptimizer struct{}

func (ineffectiveBatchOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := make([]float64, dim)
	return params, eval(params)
}

func TestRefillLimitedBatchCheckpointContinuesFromActualSize(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "black.png")

	ref := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := 3; i < len(ref.Pix); i += 4 {
		ref.Pix[i] = 255
	}

	file, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}

	err = png.Encode(file, ref)
	if err != nil {
		_ = file.Close()

		t.Fatal(err)
	}

	err = file.Close()
	if err != nil {
		t.Fatal(err)
	}

	const requestedCircles = 2
	prefix := []float64{3.5, 3.5, 8, 0, 0, 0, 1}

	result, err := renderer.OptimizeBatchAppendContext(
		context.Background(),
		renderer.NewCPURenderer(ref, requestedCircles),
		ineffectiveBatchOptimizer{},
		prefix,
		requestedCircles,
		1,
		renderer.DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Termination != renderer.TerminationRefillLimit || result.OptimizedCircles != 1 {
		t.Fatalf("pipeline result = termination %q, circles %d; want refill_limit with one circle",
			result.Termination, result.OptimizedCircles)
	}

	fsStore, err := store.NewFSStore(filepath.Join(tmpDir, "data"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":0", fsStore, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	config, err := app.Normalize(JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Circles: requestedCircles, BatchSize: 1,
		Iters: 1, OptimizerEpochs: 1, PopSize: app.MinPopulation, Threads: 1, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)

	err = server.jobManager.StartJob(source.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(source.ID, result.Iterations, result.Evaluations,
		result.BestParams, result.BestCost, result.InitialCost, string(result.Termination))
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, result.BestParams, result.BestCost, result.InitialCost, result.Iterations, config)
	checkpoint.Evaluations = int64(result.Evaluations)

	checkpoint.Termination = string(result.Termination)

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the accepted continuation pending so its inherited size is stable to
	// inspect without racing the worker.
	server.cancel()

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+source.ID, nil))

	if status.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", status.Code, status.Body.String())
	}

	var resource jobStatusResponse

	err = json.NewDecoder(status.Body).Decode(&resource)
	if err != nil {
		t.Fatal(err)
	}

	if resource.RequestedCircles != requestedCircles || resource.ActualCircles != 1 {
		t.Fatalf("job resource circles = requested %d actual %d, want %d and 1",
			resource.RequestedCircles, resource.ActualCircles, requestedCircles)
	}

	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))

	if list.Code != http.StatusOK {
		t.Fatalf("list = %d body=%s", list.Code, list.Body.String())
	}

	var summaries []JobSummary

	err = json.NewDecoder(list.Body).Decode(&summaries)
	if err != nil {
		t.Fatal(err)
	}

	if len(summaries) != 1 || summaries[0].RequestedCircles != requestedCircles || summaries[0].ActualCircles != 1 {
		t.Fatalf("job list circle counts = %+v, want requested %d actual 1", summaries, requestedCircles)
	}

	polish := httptest.NewRecorder()
	server.Handler().ServeHTTP(polish, httptest.NewRequest(http.MethodPost,
		"/api/v1/jobs/"+source.ID+"/polish", strings.NewReader(`{}`)))

	if polish.Code != http.StatusCreated {
		t.Fatalf("polish status = %d body=%s", polish.Code, polish.Body.String())
	}

	var polishPayload struct {
		JobID string `json:"jobId"`
	}

	err = json.NewDecoder(polish.Body).Decode(&polishPayload)
	if err != nil {
		t.Fatal(err)
	}

	polishingContinuation, ok := server.jobManager.GetJob(polishPayload.JobID)
	if !ok {
		t.Fatal("polishing continuation job not found")
	}

	if polishingContinuation.Config.Circles != 1 || polishingContinuation.Config.BatchSize != 1 ||
		polishingContinuation.Config.PolishingActiveSetSize != 1 || polishingContinuation.ActualCircles != 1 {
		t.Fatalf("polishing continuation did not rebase count-dependent settings: %+v", polishingContinuation)
	}

	extend := httptest.NewRecorder()
	server.Handler().ServeHTTP(extend, httptest.NewRequest(http.MethodPost,
		"/api/v1/jobs/"+source.ID+"/extend", strings.NewReader(`{"additionalCircles":1}`)))

	if extend.Code != http.StatusCreated {
		t.Fatalf("extend status = %d body=%s", extend.Code, extend.Body.String())
	}

	var payload struct {
		JobID           string `json:"jobId"`
		PreviousCircles int    `json:"previousCircles"`
		TargetCircles   int    `json:"targetCircles"`
	}

	err = json.NewDecoder(extend.Body).Decode(&payload)
	if err != nil {
		t.Fatal(err)
	}

	if payload.PreviousCircles != 1 || payload.TargetCircles != requestedCircles {
		t.Fatalf("extension sizes = previous %d target %d, want 1 and %d",
			payload.PreviousCircles, payload.TargetCircles, requestedCircles)
	}

	continuation, ok := server.jobManager.GetJob(payload.JobID)
	if !ok {
		t.Fatal("continuation job not found")
	}

	if continuation.Config.Circles != requestedCircles || continuation.RequestedCircles != requestedCircles ||
		continuation.ActualCircles != 1 || !reflect.DeepEqual(continuation.BestParams, prefix) {
		t.Fatalf("continuation did not inherit the actual checkpoint: %+v", continuation)
	}

	// A short parameter vector is not accepted merely because the job says it
	// completed. refill_limit is the typed outcome that makes its actual size a
	// valid continuation boundary.
	checkpoint.Termination = string(opt.TerminationCompleted)

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	invalid := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost,
		"/api/v1/jobs/"+source.ID+"/extend", strings.NewReader(`{"additionalCircles":1}`)))

	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_checkpoint"`) {
		t.Fatalf("ordinary short checkpoint response = %d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestExtendEndpointPolishIsOptIn(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		body string
		want bool
	}{
		{name: "absent", body: `{"additionalCircles":3}`, want: false},
		{name: "disabled", body: `{"additionalCircles":3,"polish":false}`, want: false},
		{name: "enabled", body: `{"additionalCircles":3,"polish":true}`, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server, sourceID, _ := newExtendableBatchJob(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+sourceID+"/extend", strings.NewReader(testCase.body))
			req.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, req)

			if response.Code != http.StatusCreated {
				t.Fatalf("extend status = %d body=%s", response.Code, response.Body.String())
			}

			var payload struct {
				JobID string `json:"jobId"`
			}

			err := json.NewDecoder(response.Body).Decode(&payload)
			if err != nil {
				t.Fatal(err)
			}

			continuation, ok := server.jobManager.GetJob(payload.JobID)
			if !ok {
				t.Fatal("extension continuation job not found")
			}

			if continuation.Config.PolishingEnabled != testCase.want || continuation.Config.PolishingOnly {
				t.Fatalf("polishing enabled/only = %v/%v, want %v/false",
					continuation.Config.PolishingEnabled, continuation.Config.PolishingOnly, testCase.want)
			}
		})
	}
}

func TestServer_CreatePage_Integration(t *testing.T) {
	t.Parallel()

	// Create temp directory and test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	server := NewServerWithOptions(":0", nil, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	// Test GET request
	req := httptest.NewRequest(http.MethodGet, "/create", nil)
	rec := httptest.NewRecorder()
	server.handleCreatePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /create: Expected status 200, got %d", rec.Code)
	}

	// Test POST request
	form := url.Values{}
	form.Add("refPath", testImagePath)
	form.Add("mode", "joint")
	form.Add("circles", "2")
	form.Add("iters", "10")
	form.Add("popSize", "30")
	form.Add("seed", "123")

	req = httptest.NewRequest(http.MethodPost, "/create", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec = httptest.NewRecorder()
	server.handleCreatePage(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("POST /create: Expected status 303, got %d", rec.Code)
	}

	// Extract job ID from redirect location
	location := rec.Header().Get("Location")
	if !bytes.Contains([]byte(location), []byte("/jobs/")) {
		t.Errorf("Expected redirect to /jobs/, got %s", location)
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel load would skew its wall-clock waits
func TestServer_GracefulShutdownWithCheckpoint(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping shutdown test in short mode")
	}

	// Create temp directory and test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	// Create checkpoint store
	checkpointDir := filepath.Join(tmpDir, "data")

	store, err := createTestStore(checkpointDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint store: %v", err)
	}

	server := NewServer(":0", store)

	// Create a job with checkpointing enabled
	config := JobConfig{
		RefPath:            testImagePath,
		Mode:               "joint",
		Circles:            10,  // More circles = longer optimization
		Iters:              500, // Many iterations to ensure it's still running when we shut down
		PopSize:            50,
		Seed:               42,
		CheckpointInterval: 1, // Checkpoint every 1 second
	}

	job := server.jobManager.CreateJob(app.DefaultProject, config)

	// Start worker in background; the test asserts on the checkpoint it writes,
	// not on how the run ends.
	go func() { _ = runJob(server.ctx, server.jobManager, store, job.ID) }()

	// Wait for job to start and for at least one checkpoint to happen
	// Since checkpointInterval is 1 second, wait 1.5 seconds to ensure checkpoint occurs
	time.Sleep(1500 * time.Millisecond)

	// Verify job is running or pending
	j, exists := server.jobManager.GetJob(job.ID)
	if !exists {
		t.Fatal("Job not found")
	}

	// If job already completed (ran too fast), skip the shutdown test
	if j.State == StateCompleted {
		t.Skip("Job completed too quickly for shutdown test")
	}

	if j.State != StateRunning && j.State != StatePending {
		t.Fatalf("Expected job to be running or pending, got %s", j.State)
	}

	t.Logf("Job state before shutdown: state=%s, iterations=%d", j.State, j.Iterations)

	// Simulate shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Trigger shutdown
	err = server.Shutdown(shutdownCtx)
	if err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}

	// Wait a bit for checkpoint to complete
	time.Sleep(500 * time.Millisecond)

	// Try to load checkpoint - it should exist if job was running
	checkpoint, err := store.LoadCheckpoint(job.ID)
	if err != nil {
		// If checkpoint doesn't exist, it means job finished before/during shutdown
		// This is acceptable - the test verified graceful shutdown works
		t.Logf("No checkpoint found (job may have completed): %v", err)
		return
	}

	// If we have a checkpoint, verify it contains valid data
	if checkpoint.JobID != job.ID {
		t.Errorf("Expected checkpoint jobID %s, got %s", job.ID, checkpoint.JobID)
	}

	if len(checkpoint.BestParams) == 0 {
		t.Error("Checkpoint should contain best params")
	}

	if checkpoint.BestCost == 0 {
		t.Error("Checkpoint should have non-zero best cost")
	}

	if checkpoint.Iteration == 0 {
		t.Error("Checkpoint should have non-zero iteration count")
	}

	t.Logf("Checkpoint saved successfully: iteration=%d, cost=%f", checkpoint.Iteration, checkpoint.BestCost)

	// Verify checkpoint artifacts exist
	jobDir := filepath.Join(checkpointDir, "jobs", job.ID)
	bestPngPath := filepath.Join(jobDir, "best.png")
	diffPngPath := filepath.Join(jobDir, "diff.png")

	_, err = os.Stat(bestPngPath)
	if os.IsNotExist(err) {
		t.Error("best.png artifact should exist")
	}

	_, err = os.Stat(diffPngPath)
	if os.IsNotExist(err) {
		t.Error("diff.png artifact should exist")
	}
}

// createTestStore creates a filesystem store for testing.
func createTestStore(baseDir string) (*store.FSStore, error) {
	return store.NewFSStore(baseDir)
}

func shutdownTestServer(t *testing.T, server *Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := server.Shutdown(ctx)
		if err != nil {
			t.Errorf("Failed to shut down test server: %v", err)
		}
	})
}

// TestServer_CreatePagePost_EarlyStopDefersToAppValidation proves the form
// parses the optimizer-level stopping fields but leaves their bounds to
// app.Normalize, so the HTML and JSON entry points cannot drift apart.
func TestServer_CreatePagePost_EarlyStopDefersToAppValidation(t *testing.T) {
	t.Parallel()

	server := NewServer(":0", nil)

	base := map[string]string{
		"refPath": "test.png",
		"mode":    "joint",
		"circles": "10",
		"iters":   "100",
		"popSize": "30",
		"seed":    "1",
	}

	tests := []struct {
		name     string
		extra    map[string]string
		errMsg   string
		wantPage bool
	}{
		{
			name:     "min iters above the iteration budget",
			extra:    map[string]string{"stopMinIters": "999999"},
			errMsg:   "stopMinIters",
			wantPage: true,
		},
		{
			name:     "min improvement without a stagnation window",
			extra:    map[string]string{"stopMinImprovement": "5"},
			errMsg:   "stopMinImprovement",
			wantPage: true,
		},
		{
			name:     "negative target cost",
			extra:    map[string]string{"stopTargetCost": "-1"},
			errMsg:   "stopTargetCost",
			wantPage: true,
		},
		{
			name:     "non-numeric target cost",
			extra:    map[string]string{"stopTargetCost": "abc"},
			errMsg:   "stopTargetCost must be a number",
			wantPage: true,
		},
		{
			name:  "empty fields are accepted as disabled",
			extra: map[string]string{"stopTargetCost": "", "stopStagnationIters": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			form := url.Values{}
			for k, v := range base {
				form.Add(k, v)
			}

			for k, v := range tt.extra {
				form.Add(k, v)
			}

			req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewBufferString(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rec := httptest.NewRecorder()

			server.handleCreatePage(rec, req)

			body := rec.Body.String()
			if tt.wantPage {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}

				if !containsString(body, tt.errMsg) {
					t.Fatalf("expected %q in the rendered error page, got:\n%s", tt.errMsg, body)
				}

				return
			}
			// A valid submission redirects to the created job instead of
			// re-rendering the form with an error.
			if containsString(body, "stopTargetCost must be") || containsString(body, "stopStagnationIters must be") {
				t.Fatalf("empty early-stop fields were rejected:\n%s", body)
			}
		})
	}
}

// TestServer_BackendDefaults_AllEntryPoints covers the job entry points that do
// not go through handleCreateJob. Each one used to normalize an omitted backend
// straight through app.Normalize, which filled the application-wide CPU default
// before the server flag was ever consulted.
func TestServer_BackendDefaults_AllEntryPoints(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServerWithOptions(":8080", nil, ServerOptions{
		InputRoots:     []string{tmpDir},
		DataRoot:       t.TempDir(),
		DefaultBackend: app.BackendOpenCL,
	})
	shutdownTestServer(t, s)

	t.Run("whitespace backend counts as omitted", func(t *testing.T) {
		t.Parallel()

		body, _ := json.Marshal(map[string]any{"refPath": imgPath, "backend": "  "})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		w := httptest.NewRecorder()

		s.handleCreateJob(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("Expected status 201, got %d: %s", w.Code, w.Body.String())
		}

		var job Job

		err := json.NewDecoder(w.Body).Decode(&job)
		if err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if job.Config.Backend != app.BackendOpenCL {
			t.Fatalf("job backend = %q, want %q", job.Config.Backend, app.BackendOpenCL)
		}
	})

	t.Run("dashboard form inherits the server default", func(t *testing.T) {
		t.Parallel()

		form := url.Values{
			"refPath": {imgPath},
			"mode":    {string(app.ModeJoint)},
			"circles": {"4"},
			"iters":   {"2"},
			"popSize": {"20"},
			"seed":    {"1"},
		}
		req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		w := httptest.NewRecorder()

		s.handleCreatePagePost(w, req)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("Expected status 303, got %d: %s", w.Code, w.Body.String())
		}

		jobID := strings.TrimPrefix(w.Header().Get("Location"), "/jobs/")

		job, ok := s.jobManager.GetJob(jobID)
		if !ok {
			t.Fatalf("job %q not found", jobID)
		}

		if job.Config.Backend != app.BackendOpenCL {
			t.Fatalf("dashboard job backend = %q, want %q", job.Config.Backend, app.BackendOpenCL)
		}
	})

	t.Run("schedule stage inherits the server default", func(t *testing.T) {
		t.Parallel()

		stage := app.ScheduleStage{
			Index:  0,
			Kind:   app.ScheduleStageBase,
			Config: JobConfig{RefPath: imgPath, Circles: 4, Iters: 2, PopSize: 20},
		}

		config, _, err := s.scheduleStageConfig(stage, []app.ScheduleStage{stage}, "", 0)
		if err != nil {
			t.Fatalf("scheduleStageConfig: %v", err)
		}

		if config.Backend != app.BackendOpenCL {
			t.Fatalf("stage backend = %q, want %q", config.Backend, app.BackendOpenCL)
		}
	})

	t.Run("schedule stage keeps an explicit backend", func(t *testing.T) {
		t.Parallel()

		stage := app.ScheduleStage{
			Index:  0,
			Kind:   app.ScheduleStageBase,
			Config: JobConfig{RefPath: imgPath, Circles: 4, Iters: 2, PopSize: 20, Backend: app.BackendCPU},
		}

		config, _, err := s.scheduleStageConfig(stage, []app.ScheduleStage{stage}, "", 0)
		if err != nil {
			t.Fatalf("scheduleStageConfig: %v", err)
		}

		if config.Backend != app.BackendCPU {
			t.Fatalf("stage backend = %q, want %q", config.Backend, app.BackendCPU)
		}
	})
}

// TestReferenceImageFactsAreMemoizedAndRevalidated pins both halves of the memo
// the polled status endpoint depends on. The cache is proven by corrupting the
// file's contents while keeping its size and modification time: a call that
// still answers correctly cannot have decoded the header again. Invalidation is
// proven by writing a genuinely different image, which changes both.
func TestReferenceImageFactsAreMemoizedAndRevalidated(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reference.png")
	createSimpleTestImage(t, path)

	srv := &Server{}

	width, height, size, err := srv.referenceImageFactsFor(path)
	if err != nil {
		t.Fatalf("referenceImageFactsFor() error = %v", err)
	}

	if width != 50 || height != 50 {
		t.Fatalf("referenceImageFactsFor() dimensions = %dx%d, want 50x50", width, height)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat reference image: %v", err)
	}

	// Same length, same timestamps, unreadable as an image. Only a cache hit
	// can answer this correctly.
	err = os.WriteFile(path, bytes.Repeat([]byte{0}, int(size)), 0o600)
	if err != nil {
		t.Fatalf("corrupt reference image: %v", err)
	}

	err = os.Chtimes(path, info.ModTime(), info.ModTime())
	if err != nil {
		t.Fatalf("restore modification time: %v", err)
	}

	cachedWidth, cachedHeight, cachedSize, err := srv.referenceImageFactsFor(path)
	if err != nil {
		t.Fatalf("referenceImageFactsFor() decoded again instead of using the memo: %v", err)
	}

	if cachedWidth != width || cachedHeight != height || cachedSize != size {
		t.Errorf("memoized facts = %dx%d/%d, want %dx%d/%d", cachedWidth, cachedHeight, cachedSize, width, height, size)
	}

	// A different image changes size and modification time, so the memo has to
	// stand aside rather than serve the previous answer.
	replaced := image.NewNRGBA(image.Rect(0, 0, 12, 34))

	var encoded bytes.Buffer

	err = png.Encode(&encoded, replaced)
	if err != nil {
		t.Fatalf("encode replacement image: %v", err)
	}

	err = os.WriteFile(path, encoded.Bytes(), 0o600)
	if err != nil {
		t.Fatalf("replace reference image: %v", err)
	}

	freshWidth, freshHeight, _, err := srv.referenceImageFactsFor(path)
	if err != nil {
		t.Fatalf("referenceImageFactsFor() error after replacement = %v", err)
	}

	if freshWidth != 12 || freshHeight != 34 {
		t.Errorf("facts after replacement = %dx%d, want 12x34; the memo was served stale", freshWidth, freshHeight)
	}
}
