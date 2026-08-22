package server

import (
	"encoding/base64"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestArtifactDownloadHeaders(t *testing.T) {
	server, jobID := downloadTestJob(t, false)

	tests := []struct {
		name        string
		path        string
		contentType string
		filename    string
		attachment  bool
	}{
		{name: "best inline", path: "best.png", contentType: "image/png"},
		{name: "best download", path: "best.png?download=1", contentType: "image/png", filename: artifactFilename(jobID, "best.png"), attachment: true},
		{name: "difference download", path: "diff.png?colormap=magma&download=1", contentType: "image/png", filename: artifactFilename(jobID, "diff.png"), attachment: true},
		{name: "parameters download", path: "params.json", contentType: "application/json; charset=utf-8", filename: artifactFilename(jobID, "params.json"), attachment: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/"+test.path, nil)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}

			if got := recorder.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}

			contentDisposition := recorder.Header().Get("Content-Disposition")
			if !test.attachment {
				if contentDisposition != "" {
					t.Errorf("inline Content-Disposition = %q, want empty", contentDisposition)
				}

				return
			}

			mediaType, params, err := mime.ParseMediaType(contentDisposition)
			if err != nil || mediaType != "attachment" || params["filename"] != test.filename {
				t.Errorf("Content-Disposition = %q, error = %v", contentDisposition, err)
			}
		})
	}
}

func TestServerReportIsSelfContained(t *testing.T) {
	server, jobID := downloadTestJob(t, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/report.html?colormap=magma", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil || mediaType != "attachment" || params["filename"] != artifactFilename(jobID, "report.html") {
		t.Errorf("Content-Disposition = %q, error = %v", recorder.Header().Get("Content-Disposition"), err)
	}

	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}

	body := recorder.Body.String()
	for _, marker := range []string{
		"<!doctype html>", "MayFlyCircleFit Report", jobID, "Metrics", "Parameters",
		"Circle", "magma", "12.500000", "0.8765", `class="metrics-table"`,
		"Elapsed time", "page-break-before: always", "Self-contained report generated",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("report missing %q", marker)
		}
	}

	if strings.Contains(body, `<script`) || strings.Contains(body, `<link`) || strings.Contains(body, `src="http`) {
		t.Error("report contains an external or executable dependency")
	}

	dataURIs := regexp.MustCompile(`data:image/png;base64,([^" ]+)`).FindAllStringSubmatch(body, -1)
	if len(dataURIs) != 3 {
		t.Fatalf("embedded PNG count = %d, want 3", len(dataURIs))
	}

	for i, match := range dataURIs {
		decoded, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("decode embedded PNG %d: %v", i, err)
		}

		if len(decoded) < 8 || string(decoded[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Errorf("embedded image %d is not a PNG", i)
		}
	}
}

func TestServerReportErrors(t *testing.T) {
	server := NewServer(":8080", nil)
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{Circles: 1})

	tests := []struct {
		name   string
		method string
		jobID  string
		query  string
		want   int
	}{
		{name: "method", method: http.MethodPost, jobID: job.ID, want: http.StatusMethodNotAllowed},
		{name: "no results", method: http.MethodGet, jobID: job.ID, want: http.StatusNotFound},
		{name: "invalid colormap", method: http.MethodGet, jobID: job.ID, query: "?colormap=viridis", want: http.StatusBadRequest},
		{name: "missing", method: http.MethodGet, jobID: "905aa491-a159-4d80-bd23-913734053a92", want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/v1/jobs/"+test.jobID+"/report.html"+test.query, nil)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestServerReportSnapshotFailures(t *testing.T) {
	tests := []struct {
		name    string
		refPath string
		params  []float64
	}{
		{name: "unavailable reference", refPath: filepath.Join(t.TempDir(), "missing.png"), params: []float64{25, 25, 10, 1, 0.5, 0, 0.75}},
		{name: "malformed parameters", params: []float64{25}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			refPath := test.refPath
			if refPath == "" {
				refPath = filepath.Join(t.TempDir(), "reference.png")
				createSimpleTestImage(t, refPath)
			}

			server := NewServer(":8080", nil)

			job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: refPath, Mode: "joint", Circles: 1})

			err := server.jobManager.StartJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}

			err = server.jobManager.UpdateProgress(job.ID, 1, 1, test.params, 12.5)
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/report.html", nil)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
			}

			if !strings.Contains(recorder.Body.String(), `"code":"report_failed"`) {
				t.Errorf("response = %s, want report_failed error", recorder.Body.String())
			}
		})
	}
}

func downloadTestJob(t *testing.T, enableSSIM bool) (*Server, string) {
	t.Helper()
	imagePath := filepath.Join(t.TempDir(), "reference.png")
	createSimpleTestImage(t, imagePath)

	server := NewServer(":8080", nil)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imagePath, Mode: "joint", Circles: 1, EnableSSIM: enableSSIM})

	err := server.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	params := []float64{25, 25, 10, 1, 0.5, 0, 0.75}

	err = server.jobManager.UpdateProgress(job.ID, 17, 23, params, 12.5)
	if err != nil {
		t.Fatal(err)
	}

	if enableSSIM {
		ssim := 0.8765

		err = server.jobManager.RecordMetrics(job.ID, qualitySample(17, 12.5, &ssim, time.Now()))
		if err != nil {
			t.Fatal(err)
		}
	}

	return server, job.ID
}
