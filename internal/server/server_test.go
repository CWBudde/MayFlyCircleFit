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
	"testing"
	"time"
)

func TestServer_CreateJob(t *testing.T) {
	// Create test image
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

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
	s := NewServer("localhost:0", nil) // Use random port
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
	if !containsString(body, "Images") {
		t.Error("Response should contain images section")
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

	// Set some initial values
	job.BestParams = make([]float64, 14) // 2 circles * 7 params
	job.BestCost = 1000.0
	job.InitialCost = 2000.0
	job.Iterations = 3
	job.State = StateRunning

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
}

func TestServer_JobStream_SSE(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping SSE test in short mode")
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080", nil)

	// Create a job
	job := s.jobManager.CreateJob(JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   50,
		PopSize: 20,
		Seed:    42,
	})

	// Start worker in background
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go runJob(ctx, s.jobManager, nil, job.ID)

	// Wait a bit for job to start
	time.Sleep(100 * time.Millisecond)

	// Create SSE request
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs/%s/stream", job.ID), nil)
	w := httptest.NewRecorder()

	// Run handler in goroutine
	done := make(chan bool)
	go func() {
		s.handleJobStream(w, req, job.ID)
		done <- true
	}()

	// Wait for some data or timeout
	timeout := time.After(3 * time.Second)
	select {
	case <-done:
		// Handler completed
	case <-timeout:
		// Timeout - that's ok, we just want to check we got some events
	}

	// Check headers
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("Expected text/event-stream content type")
	}

	// Check we got some SSE data
	body := w.Body.String()
	if !containsString(body, "data:") {
		t.Error("Expected SSE data in response")
	}

	// Verify we can parse the JSON
	if containsString(body, "data: {") {
		// Good, we have JSON data
		t.Log("SSE events received successfully")
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
}

func TestServer_CreatePagePost_Success(t *testing.T) {
	// Create temp directory and test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	server := NewServer(":0", nil)

	// Create form data
	form := url.Values{}
	form.Add("refPath", testImagePath)
	form.Add("mode", "joint")
	form.Add("circles", "5")
	form.Add("iters", "50")
	form.Add("popSize", "20")
	form.Add("seed", "42")

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
	if job.Config.Mode != "joint" {
		t.Errorf("Expected mode joint, got %s", job.Config.Mode)
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
	if job.Config.Seed != 42 {
		t.Errorf("Expected seed 42, got %d", job.Config.Seed)
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

func TestServer_CreatePage_Integration(t *testing.T) {
	// Create temp directory and test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, testImagePath)

	server := NewServer(":0", nil)

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
