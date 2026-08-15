package server

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestDecodeParameterCircles(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "one", count: 1},
		{name: "several", count: 7},
		{name: "large job", count: 1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := make([]float64, test.count*parametersPerCircle)
			for i := range test.count {
				offset := i * parametersPerCircle
				params[offset] = float64(i) + 0.25
				params[offset+1] = float64(i) + 0.5
				params[offset+2] = float64(i) + 0.75
				params[offset+3] = 0.1
				params[offset+4] = 0.2
				params[offset+5] = 0.3
				params[offset+6] = 0.4
			}

			circles, err := decodeParameterCircles(params)
			if err != nil {
				t.Fatalf("decodeParameterCircles() error = %v", err)
			}
			if len(circles) != test.count {
				t.Fatalf("circle count = %d, want %d", len(circles), test.count)
			}
			if got := circles[test.count-1]; got.Number != test.count || got.Opacity != 0.4 {
				t.Fatalf("last circle = %+v", got)
			}
		})
	}
}

func TestDecodeParameterCirclesRejectsPartialCircle(t *testing.T) {
	if _, err := decodeParameterCircles(make([]float64, parametersPerCircle+1)); err == nil {
		t.Fatal("decodeParameterCircles() error = nil for partial circle")
	}
}

func TestServerGetParameters(t *testing.T) {
	server := NewServer(":8080", nil)
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{Circles: 2})
	if err := server.jobManager.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	params := []float64{
		10.25, 20.5, 3.75, 1, 0.5, 0, 0.8,
		30, 40, 8, 0.1, 0.2, 0.3, 0.4,
	}
	if err := server.jobManager.UpdateProgress(job.ID, 17, 23, params, 12.5); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/params.json", nil)
	recorder := httptest.NewRecorder()
	before := time.Now().UTC()
	server.Handler().ServeHTTP(recorder, request)
	after := time.Now().UTC()

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	mediaType, disposition, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil || mediaType != "attachment" || disposition["filename"] != artifactFilename(job.ID, "params.json") {
		t.Errorf("Content-Disposition = %q, error = %v", recorder.Header().Get("Content-Disposition"), err)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}

	var exported parameterExport
	if err := json.NewDecoder(recorder.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if exported.JobID != job.ID || exported.Cost != 12.5 || exported.Iterations != 17 {
		t.Errorf("metadata = %+v", exported)
	}
	if exported.Timestamp.Before(before) || exported.Timestamp.After(after) {
		t.Errorf("timestamp = %s, want between %s and %s", exported.Timestamp, before, after)
	}
	if !reflect.DeepEqual(exported.Params, params) {
		t.Errorf("params = %v, want %v", exported.Params, params)
	}
	if len(exported.Circles) != 2 {
		t.Fatalf("circle count = %d, want 2", len(exported.Circles))
	}
	first := exported.Circles[0]
	if first.Number != 1 || first.X != 10.25 || first.Radius != 3.75 || first.Green != 0.5 || first.Opacity != 0.8 {
		t.Errorf("first circle = %+v", first)
	}
}

func TestServerGetParametersErrors(t *testing.T) {
	server := NewServer(":8080", nil)
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{Circles: 1})

	tests := []struct {
		name   string
		method string
		jobID  string
		want   int
	}{
		{name: "method", method: http.MethodPost, jobID: job.ID, want: http.StatusMethodNotAllowed},
		{name: "no results", method: http.MethodGet, jobID: job.ID, want: http.StatusNotFound},
		{name: "missing job", method: http.MethodGet, jobID: "902b471e-4aca-4f19-b3a5-d35250a1df5f", want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/v1/jobs/"+test.jobID+"/params.json", nil)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}
