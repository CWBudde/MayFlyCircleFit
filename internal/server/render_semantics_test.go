package server

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestBestImagePreservesConfiguredCanvas(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.png")
	canvasPath := filepath.Join(root, "canvas.png")
	writeSolidPNG(t, referencePath, color.NRGBA{R: 255, A: 255})
	canvasColor := color.NRGBA{B: 255, A: 255}
	writeSolidPNG(t, canvasPath, canvasColor)

	server := NewServerWithOptions(":0", nil, ServerOptions{InputRoots: []string{root}})
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: referencePath, CanvasPath: canvasPath, Mode: "joint", Backend: "cpu",
		Circles: 1, Iters: 1, PopSize: 20, BatchSize: 1,
	})
	if err := server.jobManager.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	// A zero-opacity circle is a no-op, so the endpoint must return the canvas.
	if err := server.jobManager.CompleteJob(job.ID, 1, 1, make([]float64, 7), 1, 2, "completed"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/best.png", nil)
	response := httptest.NewRecorder()
	server.handleGetBestImage(response, request, job.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("ETag"); got != `"best-1"` {
		t.Fatalf("ETag = %q, want %q", got, `"best-1"`)
	}
	decoded, err := png.Decode(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	if got != canvasColor {
		t.Fatalf("best image pixel = %v, want canvas %v", got, canvasColor)
	}

	conditional := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/best.png?v=1", nil)
	conditional.Header.Set("If-None-Match", `"best-1"`)
	notModified := httptest.NewRecorder()
	server.handleGetBestImage(notModified, conditional, job.ID)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional response = %d with %d bytes, want 304 with empty body", notModified.Code, notModified.Body.Len())
	}
}

func writeSolidPNG(t *testing.T, path string, value color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.SetNRGBA(x, y, value)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}
