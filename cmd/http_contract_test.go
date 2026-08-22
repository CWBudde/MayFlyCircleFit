package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/spf13/cobra"
)

func validJobResponse(t *testing.T, id string) jobResponse {
	t.Helper()

	config := app.DefaultConfig()
	config.RefPath = "assets/reference.png"

	config.Seed = 42
	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("apply config defaults: %v", err)
	}

	bestCost := 4.0
	initialCost := 10.0
	iterations := 2
	evaluations := 40
	elapsed := 1.5
	cps := 100.0
	startTime := time.Now().UTC()

	return jobResponse{
		ID:          id,
		State:       "running",
		Config:      &config,
		BestCost:    &bestCost,
		InitialCost: &initialCost,
		Iterations:  &iterations,
		Evaluations: &evaluations,
		Elapsed:     &elapsed,
		CPS:         &cps,
		StartTime:   &startTime,
	}
}

func TestJobResponseAcceptsCompactCollectionConfig(t *testing.T) {
	response := validJobResponse(t, "job-1")

	response.Config = &app.JobConfig{RefPath: "assets/reference.png", Mode: app.ModeBatch, Circles: 8}
	err := response.validate(false)
	if err != nil {
		t.Fatalf("compact collection response rejected: %v", err)
	}

	err := response.validate(true)
	if err == nil {
		t.Fatal("compact config was accepted for the detailed status response")
	}
}

func testCommand(ctx context.Context, output io.Writer) *cobra.Command {
	command := &cobra.Command{}
	command.SetContext(ctx)
	command.SetOut(output)

	return command
}

func TestRunStatusEscapesJobIDAndUsesTypedResponse(t *testing.T) {
	jobID := "job/with ?#"
	response := validJobResponse(t, jobID)
	requestURI := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestURI <- request.RequestURI

		writer.Header().Set("Content-Type", "application/json")

		err := json.NewEncoder(writer).Encode(response)
		if err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	previousURL := serverURL
	serverURL = server.URL + "/"

	t.Cleanup(func() { serverURL = previousURL })

	var output bytes.Buffer
	err := runStatus(testCommand(context.Background(), &output), []string{jobID})
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	if got := <-requestURI; got != "/api/v1/jobs/job%2Fwith%20%3F%23/status" {
		t.Errorf("RequestURI = %q, want escaped job ID", got)
	}

	if !strings.Contains(output.String(), "Job: "+jobID) || !strings.Contains(output.String(), "Improvement: 6.00 (60.0%)") {
		t.Errorf("unexpected status output:\n%s", output.String())
	}
}

func TestListJobsRejectsMalformedOrSkewedResponses(t *testing.T) {
	valid, err := json.Marshal([]jobResponse{validJobResponse(t, "job-1")})
	if err != nil {
		t.Fatalf("marshal valid response: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "null instead of array", body: `null`},
		{name: "wrong config type", body: `[{"id":"job-1","state":"running","config":"old"}]`},
		{name: "missing required fields", body: `[{"id":"job-1","state":"running","config":{}}]`},
		{name: "unknown top-level field", body: strings.TrimSuffix(string(valid), "}]") + `,"apiVersion":99}]`},
		{name: "wrong cost type", body: strings.Replace(string(valid), `"bestCost":4`, `"bestCost":"four"`, 1)},
		{name: "unknown state", body: strings.Replace(string(valid), `"state":"running"`, `"state":"paused"`, 1)},
		{name: "trailing JSON", body: string(valid) + `{}`},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, testCase.body)
			}))
			defer server.Close()

			var output bytes.Buffer
			err := listJobs(context.Background(), &output, server.URL)
			if err == nil {
				t.Fatalf("listJobs accepted malformed response %s", testCase.body)
			}
		})
	}
}

func TestListJobsEmptyAndValidResponses(t *testing.T) {
	tests := []struct {
		name       string
		response   any
		wantOutput string
	}{
		{name: "empty", response: []jobResponse{}, wantOutput: "No jobs found"},
		{name: "one job", response: []jobResponse{validJobResponse(t, "job-1")}, wantOutput: "Found 1 job(s)"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				err := json.NewEncoder(writer).Encode(testCase.response)
				if err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()

			var output bytes.Buffer
			err := listJobs(context.Background(), &output, server.URL)
			if err != nil {
				t.Fatalf("listJobs: %v", err)
			}

			if !strings.Contains(output.String(), testCase.wantOutput) {
				t.Errorf("output %q does not contain %q", output.String(), testCase.wantOutput)
			}
		})
	}
}

func TestListJobsReadsBoundedPages(t *testing.T) {
	first := validJobResponse(t, "job-1")
	second := validJobResponse(t, "job-2")
	total := 2
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++

		if got := request.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q, want 100", got)
		}

		writer.Header().Set("Content-Type", "application/json")

		if request.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(writer).Encode(jobListPageResponse{Jobs: []jobResponse{first}, NextCursor: "page-2", Total: &total})
			return
		}

		if got := request.URL.Query().Get("cursor"); got != "page-2" {
			t.Errorf("cursor = %q, want page-2", got)
		}

		_ = json.NewEncoder(writer).Encode(jobListPageResponse{Jobs: []jobResponse{second}, Total: &total})
	}))
	defer server.Close()

	var output bytes.Buffer
	err := listJobs(context.Background(), &output, server.URL)
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}

	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}

	for _, want := range []string{"Found 2 job(s)", "Job ID: job-1", "Job ID: job-2"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output %q does not contain %q", output.String(), want)
		}
	}
}

func TestGetJobStatusRejectsMissingMetricsAndMismatchedID(t *testing.T) {
	response, err := json.Marshal(validJobResponse(t, "job-1"))
	if err != nil {
		t.Fatalf("marshal valid response: %v", err)
	}

	tests := []struct {
		name      string
		body      string
		requested string
	}{
		{name: "missing elapsed", body: strings.Replace(string(response), `"elapsed":1.5,`, "", 1), requested: "job-1"},
		{name: "mismatched ID", body: string(response), requested: "another-job"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, testCase.body)
			}))
			defer server.Close()

			err := getJobStatus(context.Background(), io.Discard, server.URL, testCase.requested)
			if err == nil {
				t.Fatal("getJobStatus accepted malformed status response")
			}
		})
	}
}

func TestCLIRequestsHandleStructuredLegacyAndPlainErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "structured", body: `{"error":{"code":"invalid_config","message":"circles too large"}}`, want: "invalid_config: circles too large"},
		{name: "legacy JSON", body: `{"error":"legacy failure"}`, want: "legacy failure"},
		{name: "plain text", body: "plain failure\n", want: "plain failure"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, testCase.body)
			}))
			defer server.Close()

			_, err := requestCLI(context.Background(), http.MethodGet, server.URL)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want text %q", err, testCase.want)
			}

			var apiErr *cliAPIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("error = %#v, want *cliAPIError with status 400", err)
			}
		})
	}
}

func TestCLIRequestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("x", maxCLIResponseBytes+1))
	}))
	defer server.Close()

	_, err := requestCLI(context.Background(), http.MethodGet, server.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want oversized response error", err)
	}
}

func TestRunStatusUsesCommandContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	previousURL := serverURL
	serverURL = server.URL

	t.Cleanup(func() { serverURL = previousURL })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runStatus(testCommand(ctx, io.Discard), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestRunResumeServerEscapesIDAndValidatesResponse(t *testing.T) {
	jobID := "checkpoint/with ?#"
	requestDetails := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestDetails <- fmt.Sprintf("%s %s %s", request.Method, request.RequestURI, request.Header.Get("Content-Type"))

		_, _ = fmt.Fprintf(writer, `{"jobId":"new-job","resumedFrom":%q,"state":"pending","previousCost":2.5,"previousIters":40,"message":"resumed"}`, jobID)
	}))
	defer server.Close()

	previousURL := resumeServerURL
	resumeServerURL = server.URL + "/"

	t.Cleanup(func() { resumeServerURL = previousURL })

	var output bytes.Buffer
	err := runResumeServer(context.Background(), &output, jobID)
	if err != nil {
		t.Fatalf("runResumeServer: %v", err)
	}

	if got := <-requestDetails; got != "POST /api/v1/jobs/checkpoint%2Fwith%20%3F%23/resume application/json" {
		t.Errorf("request = %q, want escaped POST with JSON content type", got)
	}

	if !strings.Contains(output.String(), "Job ID: new-job") {
		t.Errorf("unexpected output: %s", output.String())
	}
}

func TestRunResumeServerRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing job ID", body: `{"resumedFrom":"source","state":"pending","previousCost":2,"previousIters":1}`},
		{name: "wrong source", body: `{"jobId":"new","resumedFrom":"other","state":"pending","previousCost":2,"previousIters":1}`},
		{name: "terminal state", body: `{"jobId":"new","resumedFrom":"source","state":"completed","previousCost":2,"previousIters":1}`},
		{name: "negative iterations", body: `{"jobId":"new","resumedFrom":"source","state":"pending","previousCost":2,"previousIters":-1}`},
		{name: "missing previous cost", body: `{"jobId":"new","resumedFrom":"source","state":"pending","previousIters":1}`},
		{name: "unknown field", body: `{"jobId":"new","resumedFrom":"source","state":"pending","previousCost":2,"previousIters":1,"version":2}`},
		{name: "wrong type", body: `{"jobId":3,"resumedFrom":"source","state":"pending","previousCost":2,"previousIters":1}`},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, testCase.body)
			}))
			defer server.Close()

			previousURL := resumeServerURL
			resumeServerURL = server.URL

			t.Cleanup(func() { resumeServerURL = previousURL })

			err := runResumeServer(context.Background(), io.Discard, "source")
			if err == nil {
				t.Fatalf("runResumeServer accepted malformed response %s", testCase.body)
			}
		})
	}
}

func TestRunResumeServerReportsCheckpointNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(writer, `{"error":{"code":"not_found","message":"checkpoint is gone"}}`)
	}))
	defer server.Close()

	previousURL := resumeServerURL
	resumeServerURL = server.URL

	t.Cleanup(func() { resumeServerURL = previousURL })

	err := runResumeServer(context.Background(), io.Discard, "source")
	if err == nil || !strings.Contains(err.Error(), "checkpoint not found") || !strings.Contains(err.Error(), "checkpoint is gone") {
		t.Fatalf("error = %v, want checkpoint and API details", err)
	}
}

func TestCLIHTTPClientHasBoundedTimeout(t *testing.T) {
	if cliHTTPClient.Timeout != 10*time.Second {
		t.Errorf("CLI HTTP timeout = %s, want 10s", cliHTTPClient.Timeout)
	}
}

// TestGetJobStatusPrintsTermination covers the reason a run stopped now that
// the server reports the optimizer's actual termination instead of a hardcoded
// "completed".
func TestGetJobStatusPrintsTermination(t *testing.T) {
	tests := []struct {
		name        string
		termination string
		want        string
		absent      bool
	}{
		{name: "stagnation", termination: "stagnation", want: "Termination: stagnation"},
		{name: "target cost", termination: "target_cost", want: "Termination: target_cost"},
		{name: "legacy sentinel", termination: "legacy", want: "Termination: legacy"},
		{name: "omitted", termination: "", want: "Termination:", absent: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := validJobResponse(t, "job-1")
			response.Termination = testCase.termination

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")

				err := json.NewEncoder(writer).Encode(response)
				if err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()

			var output bytes.Buffer
			err := getJobStatus(context.Background(), &output, server.URL, "job-1")
			if err != nil {
				t.Fatalf("getJobStatus: %v", err)
			}

			if got := strings.Contains(output.String(), testCase.want); got == testCase.absent {
				t.Errorf("output contains %q = %v, want %v:\n%s",
					testCase.want, got, !testCase.absent, output.String())
			}
		})
	}
}
