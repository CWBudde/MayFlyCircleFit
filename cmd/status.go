package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/spf13/cobra"
)

// maxCLIResponseBytes is the shared limit; the server side of the contract is
// app.MaxCLIResponseBytes, which every endpoint the CLI reads must stay under.
const maxCLIResponseBytes = app.MaxCLIResponseBytes

var (
	serverURL = "http://localhost:8080"

	cliHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

var statusCmd = &cobra.Command{
	Use:   "status [job-id]",
	Short: "Query server status or specific job",
	Long: `Queries the server for job status information.
If no job-id is provided, lists all jobs.
If job-id is provided, shows detailed status for that job.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	rootCmd.AddCommand(statusCmd)
}

type jobResponse struct {
	ID          string         `json:"id"`
	State       string         `json:"state"`
	Config      *app.JobConfig `json:"config"`
	BestParams  []float64      `json:"bestParams,omitempty"`
	BestCost    *float64       `json:"bestCost"`
	InitialCost *float64       `json:"initialCost"`
	Iterations  *int           `json:"iterations"`
	Evaluations *int           `json:"evaluations"`
	// EvaluationWidth is the concurrency the run actually used, which the server
	// records from the renderer. Config.EvaluationWorkers is only the request,
	// and differs from it whenever the backend declined or the clamp applied.
	EvaluationWidth *int       `json:"evaluationWidth,omitempty"`
	Termination     string     `json:"termination,omitempty"`
	Elapsed         *float64   `json:"elapsed,omitempty"`
	CPS             *float64   `json:"cps,omitempty"`
	StartTime       *time.Time `json:"startTime"`
	EndTime         *time.Time `json:"endTime,omitempty"`
	Error           string     `json:"error,omitempty"`
}

type jobListPageResponse struct {
	Jobs       []jobResponse `json:"jobs"`
	NextCursor string        `json:"nextCursor,omitempty"`
	Total      *int          `json:"total"`
}

type apiErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type cliAPIError struct {
	StatusCode int
	Status     string
	Code       string
	Message    string
}

func (e *cliAPIError) Error() string {
	detail := e.Message
	if e.Code != "" {
		detail = fmt.Sprintf("%s: %s", e.Code, detail)
	}

	if detail == "" {
		return "server returned " + e.Status
	}

	return fmt.Sprintf("server returned %s: %s", e.Status, detail)
}

func runStatus(cmd *cobra.Command, args []string) error {
	baseURL := strings.TrimRight(serverURL, "/")
	if len(args) == 0 {
		return listJobs(cmd.Context(), cmd.OutOrStdout(), baseURL+"/api/v1/jobs")
	}

	jobID := args[0]
	endpoint := fmt.Sprintf("%s/api/v1/jobs/%s/status", baseURL, url.PathEscape(jobID))

	return getJobStatus(cmd.Context(), cmd.OutOrStdout(), endpoint, jobID)
}

func listJobs(ctx context.Context, output io.Writer, endpoint string) error {
	pageEndpoint, err := jobListPageEndpoint(endpoint, "")
	if err != nil {
		return fmt.Errorf("query jobs: %w", err)
	}

	body, err := requestCLI(ctx, http.MethodGet, pageEndpoint)
	if err != nil {
		return fmt.Errorf("query jobs: %w", err)
	}

	// Servers predating pagination return a bare array even when the query
	// parameters are present. Keep accepting that response during upgrades.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		var jobs []jobResponse
		err := decodeCLIResponse(body, &jobs)
		if err != nil {
			return fmt.Errorf("decode jobs response: %w", err)
		}

		if jobs == nil {
			return errors.New("invalid jobs response: expected a JSON array")
		}

		err := validateJobListPage(jobs, 0)
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			fmt.Fprintln(output, "No jobs found")
			return nil
		}

		fmt.Fprintf(output, "Found %d job(s):\n\n", len(jobs))
		writeJobList(output, jobs)

		return nil
	}

	var page jobListPageResponse
	if err := decodeCLIResponse(body, &page); err != nil {
		return fmt.Errorf("decode jobs response: %w", err)
	}

	if page.Jobs == nil || page.Total == nil || *page.Total < len(page.Jobs) || *page.Total < 0 {
		return errors.New("invalid jobs response: expected a paginated job collection")
	}

	if err := validateJobListPage(page.Jobs, 0); err != nil {
		return err
	}

	if *page.Total == 0 {
		fmt.Fprintln(output, "No jobs found")
		return nil
	}

	fmt.Fprintf(output, "Found %d job(s):\n\n", *page.Total)
	writeJobList(output, page.Jobs)

	seenCursors := make(map[string]struct{})

	seenJobs := len(page.Jobs)
	for page.NextCursor != "" {
		if len(page.Jobs) == 0 {
			return errors.New("invalid jobs response: empty page has a continuation cursor")
		}

		if _, exists := seenCursors[page.NextCursor]; exists {
			return errors.New("invalid jobs response: repeated continuation cursor")
		}

		seenCursors[page.NextCursor] = struct{}{}

		pageEndpoint, err = jobListPageEndpoint(endpoint, page.NextCursor)
		if err != nil {
			return fmt.Errorf("query jobs: %w", err)
		}

		body, err = requestCLI(ctx, http.MethodGet, pageEndpoint)
		if err != nil {
			return fmt.Errorf("query jobs: %w", err)
		}

		page = jobListPageResponse{}
		err := decodeCLIResponse(body, &page)
		if err != nil {
			return fmt.Errorf("decode jobs response: %w", err)
		}

		if page.Jobs == nil || page.Total == nil || *page.Total < len(page.Jobs) || *page.Total < 0 {
			return errors.New("invalid jobs response: expected a paginated job collection")
		}

		err := validateJobListPage(page.Jobs, seenJobs)
		if err != nil {
			return err
		}

		writeJobList(output, page.Jobs)
		seenJobs += len(page.Jobs)
	}

	return nil
}

func jobListPageEndpoint(endpoint, cursor string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}

	query := parsed.Query()
	query.Set("limit", "100")

	if cursor == "" {
		query.Del("cursor")
	} else {
		query.Set("cursor", cursor)
	}

	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

func validateJobListPage(jobs []jobResponse, offset int) error {
	for i := range jobs {
		err := jobs[i].validate(false)
		if err != nil {
			return fmt.Errorf("invalid jobs response at index %d: %w", offset+i, err)
		}
	}

	return nil
}

func writeJobList(output io.Writer, jobs []jobResponse) {
	for _, job := range jobs {
		fmt.Fprintf(output, "Job ID: %s\n", job.ID)
		fmt.Fprintf(output, "  State: %s\n", job.State)
		fmt.Fprintf(output, "  Circles: %d\n", job.Config.Circles)
		fmt.Fprintf(output, "  Mode: %s\n", job.Config.Mode)

		if *job.BestCost > 0 {
			fmt.Fprintf(output, "  Cost: %.2f -> %.2f\n", *job.InitialCost, *job.BestCost)
		}

		fmt.Fprintln(output)
	}
}

func getJobStatus(ctx context.Context, output io.Writer, endpoint, jobID string) error {
	body, err := requestCLI(ctx, http.MethodGet, endpoint)
	if err != nil {
		var apiErr *cliAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("job not found %q: %w", jobID, err)
		}

		return fmt.Errorf("query job %q: %w", jobID, err)
	}

	var status jobResponse
	if err := decodeCLIResponse(body, &status); err != nil {
		return fmt.Errorf("decode status response: %w", err)
	}

	if err := status.validate(true); err != nil {
		return fmt.Errorf("invalid status response: %w", err)
	}

	if status.ID != jobID {
		return fmt.Errorf("invalid status response: job ID %q does not match requested ID %q", status.ID, jobID)
	}

	fmt.Fprintf(output, "Job: %s\n", status.ID)
	fmt.Fprintf(output, "State: %s\n\n", status.State)

	fmt.Fprintln(output, "Configuration:")
	fmt.Fprintf(output, "  Reference: %s\n", status.Config.RefPath)
	fmt.Fprintf(output, "  Mode: %s\n", status.Config.Mode)
	fmt.Fprintf(output, "  Circles: %d\n", status.Config.Circles)
	fmt.Fprintf(output, "  Iterations: %d\n", status.Config.Iters)
	fmt.Fprintf(output, "  Population: %d\n", status.Config.PopSize)
	// Parallel evaluation changes which solution a seed produces, so a run is
	// only comparable to another run with the same setting. Report the width the
	// server measured from the renderer rather than the configured request: the
	// request is not what ran when the backend declined it or the clamp applied.
	// A server that reports no width leaves this line out entirely instead of
	// falling back to the request, because a wrong width is worse than none.
	if status.EvaluationWidth != nil && *status.EvaluationWidth > 1 {
		fmt.Fprintf(output, "  Parallel evaluation: %d workers\n", *status.EvaluationWidth)
	}

	fmt.Fprintln(output)

	fmt.Fprintln(output, "Progress:")

	if *status.InitialCost > 0 {
		fmt.Fprintf(output, "  Initial Cost: %.2f\n", *status.InitialCost)
	}

	if *status.BestCost > 0 {
		fmt.Fprintf(output, "  Best Cost: %.2f\n", *status.BestCost)

		if *status.InitialCost > 0 {
			improvement := *status.InitialCost - *status.BestCost
			improvementPct := improvement / *status.InitialCost * 100
			fmt.Fprintf(output, "  Improvement: %.2f (%.1f%%)\n", improvement, improvementPct)
		}
	}

	elapsed := time.Duration(*status.Elapsed * float64(time.Second))
	fmt.Fprintf(output, "  Elapsed: %s\n", elapsed.Round(time.Millisecond))

	if *status.CPS > 0 {
		fmt.Fprintf(output, "  Throughput: %.0f circles/sec\n", *status.CPS)
	}

	if status.Termination != "" {
		fmt.Fprintf(output, "  Termination: %s\n", status.Termination)
	}

	if status.Error != "" {
		fmt.Fprintf(output, "\nError: %s\n", status.Error)
	}

	return nil
}

func (job jobResponse) validate(statusResponse bool) error {
	if strings.TrimSpace(job.ID) == "" {
		return errors.New("id is required")
	}

	switch job.State {
	case "pending", "running", "completed", "failed", "cancelled":
	default:
		return fmt.Errorf("unknown state %q", job.State)
	}

	if job.Config == nil {
		return errors.New("config is required")
	}

	if statusResponse {
		err := job.Config.Validate()
		if err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
	} else {
		if strings.TrimSpace(job.Config.RefPath) == "" {
			return errors.New("config.refPath is required")
		}

		switch job.Config.Mode {
		case app.ModeJoint, app.ModeSequential, app.ModeBatch:
		default:
			return fmt.Errorf("invalid config mode %q", job.Config.Mode)
		}

		if job.Config.Circles < 1 || job.Config.Circles > app.MaxCircles {
			return fmt.Errorf("config.circles must be between 1 and %d", app.MaxCircles)
		}
	}

	if job.Iterations == nil || *job.Iterations < 0 {
		return errors.New("iterations must be present and non-negative")
	}

	if job.Evaluations == nil || *job.Evaluations < 0 {
		return errors.New("evaluations must be present and non-negative")
	}

	if job.InitialCost == nil || job.BestCost == nil {
		return errors.New("costs are required")
	}

	if !finiteNonnegative(*job.InitialCost) || !finiteNonnegative(*job.BestCost) {
		return errors.New("costs must be finite and non-negative")
	}

	if job.StartTime == nil || job.StartTime.IsZero() {
		return errors.New("startTime is required")
	}

	if statusResponse {
		maxSeconds := float64(math.MaxInt64) / float64(time.Second)
		if job.Elapsed == nil || !finiteNonnegative(*job.Elapsed) || *job.Elapsed > maxSeconds {
			return errors.New("elapsed must be a representable, finite, non-negative duration")
		}

		if job.CPS == nil || !finiteNonnegative(*job.CPS) {
			return errors.New("cps must be finite and non-negative")
		}
	}

	return nil
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func requestCLI(ctx context.Context, method, endpoint string) ([]byte, error) {
	return requestCLIBody(ctx, method, endpoint, nil)
}

// requestCLIBody is requestCLI with a request payload, which the schedule
// commands need: a campaign is created by posting its document.
func requestCLIBody(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
	var requestBody io.Reader
	if payload != nil {
		requestBody = bytes.NewReader(payload)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := cliHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("connect to server: %w", err)
	}
	defer response.Body.Close()

	body, err := readBoundedResponse(response.Body)
	if err != nil {
		return nil, err
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, parseCLIAPIError(response.StatusCode, response.Status, body)
	}

	return body, nil
}

func readBoundedResponse(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxCLIResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read server response: %w", err)
	}

	if len(body) > maxCLIResponseBytes {
		return nil, fmt.Errorf("server response exceeds %d bytes", maxCLIResponseBytes)
	}

	return body, nil
}

func decodeCLIResponse(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(destination)
	if err != nil {
		return err
	}

	err := decoder.Decode(&struct{}{})
	if err != io.EOF {
		if err == nil {
			return errors.New("response contains more than one JSON value")
		}

		return fmt.Errorf("decode trailing response data: %w", err)
	}

	return nil
}

func parseCLIAPIError(statusCode int, status string, body []byte) error {
	var response apiErrorResponse
	err := json.Unmarshal(body, &response)
	if err == nil && response.Error.Message != "" {
		return &cliAPIError{
			StatusCode: statusCode,
			Status:     status,
			Code:       response.Error.Code,
			Message:    response.Error.Message,
		}
	}

	var legacy struct {
		Error string `json:"error"`
	}
	err := json.Unmarshal(body, &legacy)
	if err == nil && legacy.Error != "" {
		return &cliAPIError{StatusCode: statusCode, Status: status, Message: legacy.Error}
	}

	return &cliAPIError{
		StatusCode: statusCode,
		Status:     status,
		Message:    strings.TrimSpace(string(body)),
	}
}
