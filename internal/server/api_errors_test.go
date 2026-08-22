package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// TestAPIErrorsUseTheJSONEnvelope pins the machine-readable error format the
// dashboard and the CLI both parse. A plain-text body here would leave the
// browser guessing why a request failed.
func TestAPIErrorsUseTheJSONEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing job id",
			method:     http.MethodGet,
			target:     "/api/v1/jobs/",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_job_id",
		},
		{
			name:       "unknown job status",
			method:     http.MethodGet,
			target:     "/api/v1/jobs/2b6f0cc9-04d4-4a1b-9f6a-6ec1f9a3c0d1",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unknown job best image",
			method:     http.MethodGet,
			target:     "/api/v1/jobs/2b6f0cc9-04d4-4a1b-9f6a-6ec1f9a3c0d1/best.png",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unknown job diff image",
			method:     http.MethodGet,
			target:     "/api/v1/jobs/2b6f0cc9-04d4-4a1b-9f6a-6ec1f9a3c0d1/diff.png",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unknown job reference image",
			method:     http.MethodGet,
			target:     "/api/v1/jobs/2b6f0cc9-04d4-4a1b-9f6a-6ec1f9a3c0d1/ref.png",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unrouted API path",
			method:     http.MethodGet,
			target:     "/api/v1/typo",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unrouted API subtree path",
			method:     http.MethodPost,
			target:     "/api/v1/jobs-not-a-route/17/best.png",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "wrong method",
			method:     http.MethodDelete,
			target:     "/api/v1/jobs/2b6f0cc9-04d4-4a1b-9f6a-6ec1f9a3c0d1/ref.png",
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   "method_not_allowed",
		},
	}

	server := NewServer(":0", nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", response.Code, test.wantStatus, response.Body.String())
			}

			if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}

			var decoded apiErrorResponse
			err := json.Unmarshal(response.Body.Bytes(), &decoded)
			if err != nil {
				t.Fatalf("body %q is not the JSON error envelope: %v", response.Body.String(), err)
			}

			if decoded.Error.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", decoded.Error.Code, test.wantCode)
			}

			if decoded.Error.Message == "" {
				t.Errorf("error message is empty in %q", response.Body.String())
			}
		})
	}
}

// TestAPIErrorsHideInternalDetail keeps reference-loading failures from leaking
// filesystem paths to the client; the operator still gets them through the log.
func TestAPIErrorsHideInternalDetail(t *testing.T) {
	server := NewServer(":0", nil)
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{
		RefPath: "/does/not/exist/secret-reference.png",
		Mode:    "joint",
		Circles: 1,
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/ref.png", nil)
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body %q)", response.Code, http.StatusInternalServerError, response.Body.String())
	}

	var decoded apiErrorResponse
	err := json.Unmarshal(response.Body.Bytes(), &decoded)
	if err != nil {
		t.Fatalf("body %q is not the JSON error envelope: %v", response.Body.String(), err)
	}

	if decoded.Error.Code != "reference_load_failed" {
		t.Errorf("error code = %q, want reference_load_failed", decoded.Error.Code)
	}

	if body := response.Body.String(); strings.Contains(body, "secret-reference.png") {
		t.Errorf("error body %q leaks the reference path", body)
	}
}
