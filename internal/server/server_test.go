package server

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestServer_CreateJob(t *testing.T) {
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
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
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

func TestServer_ListJobs(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create two jobs
	s.jobManager.CreateJob(JobConfig{RefPath: imgPath})
	s.jobManager.CreateJob(JobConfig{RefPath: imgPath})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	w := httptest.NewRecorder()

	s.handleListJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var jobs []*Job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}
}

func TestServer_GetJobStatus(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	job := s.jobManager.CreateJob(JobConfig{RefPath: imgPath, Circles: 2})

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/status", job.ID), nil)
	w := httptest.NewRecorder()

	s.handleGetJobStatus(w, req, job.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["id"] != job.ID {
		t.Error("Response should contain job ID")
	}

	if response["state"] != string(StatePending) {
		t.Errorf("Expected pending state, got %v", response["state"])
	}
}

func TestServerJobStatusRepresentsInfinitePSNRAndOptionalSSIM(t *testing.T) {
	server := NewServer(":8080", nil)
	job := server.jobManager.CreateJob(JobConfig{RefPath: "test.png", EnableSSIM: true})
	if err := server.jobManager.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := server.jobManager.UpdateProgress(job.ID, 1, 1, []float64{1}, 0); err != nil {
		t.Fatal(err)
	}
	ssim := 1.0
	if err := server.jobManager.RecordMetrics(job.ID, qualitySample(1, 0, &ssim, time.Now())); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/status", nil)
	recorder := httptest.NewRecorder()
	server.handleGetJobStatus(recorder, req, job.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response jobStatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.PSNR != nil || !response.PSNRInfinite {
		t.Fatalf("PSNR response = (%v, %v), want (nil, true)", response.PSNR, response.PSNRInfinite)
	}
	if response.SSIM == nil || *response.SSIM != 1 {
		t.Fatalf("SSIM response = %v, want 1", response.SSIM)
	}
}

func TestServer_GetJobStatus_NotFound(t *testing.T) {
	s := NewServer(":8080", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/status", nil)
	w := httptest.NewRecorder()

	s.handleGetJobStatus(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestServer_GetBestImage(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	job := s.jobManager.CreateJob(JobConfig{RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42})

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
	json.NewDecoder(resp.Body).Decode(&job)

	// Poll status until completed
	maxAttempts := 50
	for i := 0; i < maxAttempts; i++ {
		resp, err := http.Get(srv.URL + "/api/v1/jobs/" + job.ID + "/status")
		if err != nil {
			t.Fatalf("Failed to get status: %v", err)
		}

		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

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
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 5,
		Iters:   100,
		PopSize: 30,
	})

	// Test job detail page renders successfully
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/jobs/%s", job.ID), nil)
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
	if _, _, _, err := referenceImageMetadata(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("referenceImageMetadata() error = nil for missing image")
	}
}

func TestServer_JobDetailPage_NotFound(t *testing.T) {
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
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(JobConfig{
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
	path := filepath.Join(t.TempDir(), "reference.png")
	createSimpleTestImage(t, path)

	server := NewServer(":8080", nil)
	job := server.jobManager.CreateJob(JobConfig{RefPath: path, Circles: 1, Threads: 1})
	if err := server.jobManager.StartJob(job.ID); err != nil {
		t.Fatalf("start job: %v", err)
	}
	params := []float64{25, 25, 10, 1, 0, 0, 1}
	if err := server.jobManager.UpdateProgress(job.ID, 1, 1, params, 1); err != nil {
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
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job with some test data
	job := s.jobManager.CreateJob(JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   5,
		PopSize: 10,
	})

	// Set some initial values through the manager: CreateJob returns an immutable
	// snapshot, so mutating it must not change the stored job.
	if err := s.jobManager.StartJob(job.ID); err != nil {
		t.Fatalf("Failed to start job: %v", err)
	}
	if err := s.jobManager.UpdateProgress(job.ID, 3, 3, make([]float64, 14), 1000); err != nil {
		t.Fatalf("Failed to update job progress: %v", err)
	}
	if err := s.jobManager.UpdateJob(job.ID, func(stored *Job) {
		stored.InitialCost = 2000
	}); err != nil {
		t.Fatalf("Failed to set initial cost: %v", err)
	}

	// Test that the detail page renders with job data
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/jobs/%s", job.ID), nil)
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
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   1,
		PopSize: 20,
		Seed:    42,
	})
	if err := s.jobManager.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.jobManager.CompleteJob(job.ID, 7, 20, make([]float64, 14), 12.5, 100, "completed"); err != nil {
		t.Fatal(err)
	}
	ssim := 0.75
	if err := s.jobManager.RecordMetrics(job.ID, qualitySample(7, 12.5, &ssim, time.Now())); err != nil {
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
	s := NewServer(":8080", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/stream", nil)
	w := httptest.NewRecorder()

	s.handleJobStream(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestEventBroadcaster(t *testing.T) {
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
	img := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	white := color.NRGBA{255, 255, 255, 255}
	red := color.NRGBA{255, 0, 0, 255}

	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
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

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}
}

func TestServer_CreatePageGet(t *testing.T) {
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
	if !containsString(body, "polishingEnabled") || !containsString(body, "polishingActiveSetSize") {
		t.Error("Expected page to expose active-set polishing controls")
	}
}

func TestServer_CreatePagePost_Success(t *testing.T) {
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

func TestServer_CreatePagePost_ValidationErrors(t *testing.T) {
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
			errMsg: "Circles must be between 1 and 1000",
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
			errMsg: "Iterations must be between 1 and 10000",
		},
		{
			name: "invalid popSize",
			formData: map[string]string{
				"refPath": "test.png",
				"mode":    "joint",
				"circles": "10",
				"iters":   "100",
				"popSize": "1",
				"seed":    "0",
			},
			errMsg: "Population size must be between 2 and 200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestPlannedOptimizerIterationsIncludesStagesRefillsAndPolishing(t *testing.T) {
	tests := []struct {
		name   string
		config JobConfig
		want   int
	}{
		{name: "joint", config: JobConfig{Mode: app.ModeJoint, Iters: 100, OptimizerEpochs: 2}, want: 200},
		{name: "sequential", config: JobConfig{Mode: app.ModeSequential, Circles: 3, Iters: 100, OptimizerEpochs: 2}, want: 600},
		{
			name: "batch with refill budget and polishing",
			config: JobConfig{
				Mode: app.ModeBatch, Circles: 30, BatchSize: 30, Iters: 2000, OptimizerEpochs: 4,
				PolishingEnabled: true, PolishingMaxSweeps: 3, PolishingEpochs: 2, PolishingIters: 1000,
			},
			want: 38_000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := plannedOptimizerIterations(test.config); got != test.want {
				t.Fatalf("plannedOptimizerIterations() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPolishEndpointCreatesCheckpointContinuation(t *testing.T) {
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
	source := server.jobManager.CreateJob(config)
	params := []float64{1, 1, 1, 1, 0, 0, 1}
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
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
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
		continuation.Config.PopSize != 40 || continuation.Config.Seed != 99 || continuation.Config.EffectiveSeed != 99 {
		t.Fatalf("polishing continuation overrides = %+v", continuation.Config)
	}
	if continuation.Iterations != 8000 || continuation.Evaluations != 900000 || !reflect.DeepEqual(continuation.BestParams, params) {
		t.Fatalf("polishing continuation state = %+v", continuation)
	}
}

func TestExtendEndpointCreatesOrderedBatchContinuation(t *testing.T) {
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
	source := server.jobManager.CreateJob(config)
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+source.ID+"/extend", strings.NewReader(`{
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
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
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

func TestServer_CreatePage_Integration(t *testing.T) {
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

	job := server.jobManager.CreateJob(config)

	// Start worker in background
	go runJob(server.ctx, server.jobManager, store, job.ID)

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

	if _, err := os.Stat(bestPngPath); os.IsNotExist(err) {
		t.Error("best.png artifact should exist")
	}

	if _, err := os.Stat(diffPngPath); os.IsNotExist(err) {
		t.Error("diff.png artifact should exist")
	}
}

// createTestStore creates a filesystem store for testing
func createTestStore(baseDir string) (*store.FSStore, error) {
	return store.NewFSStore(baseDir)
}

func shutdownTestServer(t *testing.T, server *Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Failed to shut down test server: %v", err)
		}
	})
}

// TestServer_CreatePagePost_EarlyStopDefersToAppValidation proves the form
// parses the optimizer-level stopping fields but leaves their bounds to
// app.Normalize, so the HTML and JSON entry points cannot drift apart.
func TestServer_CreatePagePost_EarlyStopDefersToAppValidation(t *testing.T) {
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
