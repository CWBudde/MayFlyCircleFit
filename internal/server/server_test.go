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

	s := NewServer(":8080")

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

	if job.State != StatePending {
		t.Errorf("Expected pending state, got %s", job.State)
	}
}

func TestServer_ListJobs(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	s := NewServer(":8080")

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

	s := NewServer(":8080")

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
	s := NewServer(":8080")

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

	s := NewServer(":8080")

	job := s.jobManager.CreateJob(JobConfig{RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42})

	// Run job and wait for completion
	err := runJob(context.Background(), s.jobManager, job.ID)
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
	s := NewServer("localhost:0") // Use random port
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
