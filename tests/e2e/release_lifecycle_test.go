package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	requestTimeout  = 5 * time.Second
	startupTimeout  = 10 * time.Second
	progressTimeout = 25 * time.Second
	stateTimeout    = 10 * time.Second
	shutdownTimeout = 12 * time.Second
)

type jobResponse struct {
	ID          string    `json:"id"`
	State       string    `json:"state"`
	BestCost    float64   `json:"bestCost"`
	Iterations  int       `json:"iterations"`
	Evaluations int       `json:"evaluations"`
	Error       string    `json:"error"`
	Config      jobConfig `json:"config"`
}

type jobConfig struct {
	RefPath            string `json:"refPath"`
	Mode               string `json:"mode"`
	Backend            string `json:"backend"`
	Variant            string `json:"variant"`
	Circles            int    `json:"circles"`
	Iters              int    `json:"iters"`
	PopSize            int    `json:"popSize"`
	Seed               int64  `json:"seed"`
	EffectiveSeed      int64  `json:"effectiveSeed"`
	ResumeCount        int    `json:"resumeCount"`
	CheckpointInterval int    `json:"checkpointInterval"`
}

type progressEvent struct {
	JobID      string  `json:"jobId"`
	State      string  `json:"state"`
	Iterations int     `json:"iterations"`
	BestCost   float64 `json:"bestCost"`
}

type checkpointFile struct {
	SchemaVersion    int       `json:"schemaVersion"`
	JobID            string    `json:"jobId"`
	BestParams       []float64 `json:"bestParams"`
	BestCost         float64   `json:"bestCost"`
	InitialCost      float64   `json:"initialCost"`
	RequestedCircles int       `json:"requestedCircles"`
	ActualCircles    int       `json:"actualCircles"`
	EffectiveSeed    int64     `json:"effectiveSeed"`
	ResumeCount      int       `json:"resumeCount"`
	Iterations       int       `json:"iterations"`
	Evaluations      int64     `json:"evaluations"`
	Termination      string    `json:"termination"`
	Timestamp        time.Time `json:"timestamp"`
	Config           jobConfig `json:"config"`
}

type resumeResponse struct {
	JobID         string  `json:"jobId"`
	ResumedFrom   string  `json:"resumedFrom"`
	State         string  `json:"state"`
	PreviousCost  float64 `json:"previousCost"`
	PreviousIters int     `json:"previousIters"`
}

// TestReleaseLifecycle exercises the release-critical workflow through the
// compiled executable and its public HTTP boundary. It is intentionally opt-in
// because it builds a binary, starts real processes, and runs an optimizer long
// enough to persist live progress.
func TestReleaseLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("release E2E test is not part of the short suite")
	}

	if os.Getenv("MAYFLY_RUN_E2E") != "1" {
		t.Skip("set MAYFLY_RUN_E2E=1 to run the release E2E test")
	}

	tempDir := t.TempDir()
	repoRoot := findRepositoryRoot(t)
	binaryPath := filepath.Join(tempDir, "mayflycirclefit")
	buildBinary(t, repoRoot, binaryPath)

	inputRoot := filepath.Join(tempDir, "input")
	dataRoot := filepath.Join(tempDir, "data")

	err := os.MkdirAll(inputRoot, 0o755)
	if err != nil {
		t.Fatalf("create input root: %v", err)
	}

	refPath := filepath.Join(inputRoot, "reference.png")
	writeReferenceImage(t, refPath, 64, 64)

	client := &http.Client{Timeout: progressTimeout + requestTimeout}
	server := startServer(t, client, binaryPath, inputRoot, dataRoot)
	t.Cleanup(func() {
		err := server.stop()
		if err != nil {
			t.Logf("cleanup server: %v\nlogs:\n%s", err, server.logs.String())
		}
	})

	createBody := map[string]any{
		"refPath":            refPath,
		"mode":               "joint",
		"backend":            "cpu",
		"variant":            "standard",
		"circles":            48,
		"iters":              10000,
		"popSize":            200,
		"seed":               424242,
		"checkpointInterval": 1,
		"disableTrace":       true,
		"disableConvergence": true,
	}
	var created jobResponse
	doJSON(t, client, http.MethodPost, server.baseURL+"/api/v1/jobs", createBody, http.StatusCreated, &created)
	assertCanonicalUUID(t, created.ID)

	if created.State != "pending" && created.State != "running" {
		t.Fatalf("created job state = %q, want pending or running", created.State)
	}

	firstProgress := waitForProgress(t, client, server.baseURL, created.ID, func(event progressEvent) bool {
		return event.State == "running" && event.Iterations > 0
	})
	if firstProgress.BestCost <= 0 {
		t.Fatalf("first progress best cost = %v, want positive", firstProgress.BestCost)
	}

	checkpointPath := filepath.Join(dataRoot, "jobs", created.ID, "checkpoint.json")
	checkpoint := waitForCheckpoint(t, checkpointPath)
	validateCheckpoint(t, checkpoint, created.ID, refPath, 48)

	cancelJob(t, client, server.baseURL, created.ID)

	cancelled := waitForState(t, client, server.baseURL, created.ID, "cancelled")
	if cancelled.Iterations < checkpoint.Iterations || cancelled.Evaluations < int(checkpoint.Evaluations) {
		t.Fatalf("cancelled counters (%d, %d) precede checkpoint counters (%d, %d)", cancelled.Iterations, cancelled.Evaluations, checkpoint.Iterations, checkpoint.Evaluations)
	}

	err = server.stop()
	if err != nil {
		t.Fatalf("stop first server: %v\nlogs:\n%s", err, server.logs.String())
	}

	restarted := startServer(t, client, binaryPath, inputRoot, dataRoot)
	server = restarted

	var resumed resumeResponse
	doJSON(t, client, http.MethodPost, restarted.baseURL+"/api/v1/jobs/"+created.ID+"/resume", nil, http.StatusOK, &resumed)
	assertCanonicalUUID(t, resumed.JobID)

	if resumed.JobID == created.ID {
		t.Fatal("resume reused the source job UUID")
	}

	if resumed.ResumedFrom != created.ID {
		t.Fatalf("resumedFrom = %q, want %q", resumed.ResumedFrom, created.ID)
	}

	if resumed.State != "pending" && resumed.State != "running" {
		t.Fatalf("resumed state = %q, want pending or running", resumed.State)
	}

	if resumed.PreviousIters != checkpoint.Iterations || resumed.PreviousCost != checkpoint.BestCost {
		t.Fatalf("resume baseline = (%d, %v), want checkpoint (%d, %v)", resumed.PreviousIters, resumed.PreviousCost, checkpoint.Iterations, checkpoint.BestCost)
	}

	waitForProgress(t, client, restarted.baseURL, resumed.JobID, func(event progressEvent) bool {
		return event.State == "running" && event.Iterations > checkpoint.Iterations
	})

	advanced := getStatus(t, client, restarted.baseURL, resumed.JobID)
	if advanced.Iterations <= checkpoint.Iterations {
		t.Fatalf("resumed iterations = %d, want greater than checkpoint %d", advanced.Iterations, checkpoint.Iterations)
	}

	if advanced.Config.ResumeCount != checkpoint.ResumeCount+1 {
		t.Fatalf("resume count = %d, want %d", advanced.Config.ResumeCount, checkpoint.ResumeCount+1)
	}

	for _, artifact := range []string{"ref.png", "best.png", "diff.png"} {
		assertPNGArtifact(t, client, restarted.baseURL+"/api/v1/jobs/"+resumed.JobID+"/"+artifact, 64, 64)
	}

	cancelJob(t, client, restarted.baseURL, resumed.JobID)
	waitForState(t, client, restarted.baseURL, resumed.JobID, "cancelled")

	err = restarted.stop()
	if err != nil {
		t.Fatalf("stop restarted server: %v\nlogs:\n%s", err, restarted.logs.String())
	}
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate E2E test source")
	}

	dir, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		t.Fatalf("resolve test directory: %v", err)
	}

	for {
		if info, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && !info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("find repository root from %s", filename)
		}

		dir = parent
	}
}

func buildBinary(t *testing.T, repoRoot, output string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", output, ".")
	command.Dir = repoRoot

	combined, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build executable: %v\n%s", err, combined)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.b.String()
}

type serverProcess struct {
	cmd     *exec.Cmd
	wait    chan error
	logs    *lockedBuffer
	baseURL string
	mu      sync.Mutex
	stopped bool
}

func startServer(t *testing.T, client *http.Client, binaryPath, inputRoot, dataRoot string) *serverProcess {
	t.Helper()
	var failures []string

	for attempt := 1; attempt <= 5; attempt++ {
		port := availablePort(t)
		logs := &lockedBuffer{}
		command := exec.Command(
			binaryPath, "serve",
			"--addr", "127.0.0.1",
			"--port", strconv.Itoa(port),
			"--input-root", inputRoot,
			"--data-root", dataRoot,
			"--max-jobs", "1",
			"--queue-size", "4",
		)
		command.Stdout = logs

		command.Stderr = logs
		err := command.Start()
		if err != nil {
			failures = append(failures, fmt.Sprintf("attempt %d start: %v", attempt, err))
			continue
		}

		process := &serverProcess{
			cmd: command, wait: make(chan error, 1), logs: logs,
			baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		}
		go func() { process.wait <- command.Wait() }()

		deadline := time.Now().Add(startupTimeout)
		for time.Now().Before(deadline) {
			select {
			case err := <-process.wait:
				process.stopped = true

				failures = append(failures, fmt.Sprintf("attempt %d exited: %v\n%s", attempt, err, logs.String()))

				goto nextAttempt
			default:
			}

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)

			request, err := http.NewRequestWithContext(ctx, http.MethodGet, process.baseURL+"/api/v1/jobs", nil)
			if err == nil {
				response, requestErr := client.Do(request)
				if requestErr == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))

					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK {
						cancel()
						return process
					}
				}
			}

			cancel()
			time.Sleep(50 * time.Millisecond)
		}

		_ = command.Process.Kill()

		<-process.wait
		process.stopped = true

		failures = append(failures, fmt.Sprintf("attempt %d readiness timeout\n%s", attempt, logs.String()))

	nextAttempt:
	}

	t.Fatalf("start server after retries:\n%s", strings.Join(failures, "\n"))

	return nil
}

func availablePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate loopback port: %v", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}

	return port
}

func (p *serverProcess) stop() error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}

	p.stopped = true
	p.mu.Unlock()

	err := p.cmd.Process.Signal(os.Interrupt)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal interrupt: %w", err)
	}

	select {
	case err := <-p.wait:
		if err != nil {
			return fmt.Errorf("wait after interrupt: %w", err)
		}

		return nil
	case <-time.After(shutdownTimeout):
		err := p.cmd.Process.Kill()
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("force kill after shutdown timeout: %w", err)
		}

		select {
		case <-p.wait:
			return fmt.Errorf("graceful shutdown exceeded %s; process was killed", shutdownTimeout)
		case <-time.After(3 * time.Second):
			return errors.New("process did not exit after force kill")
		}
	}
}

func doJSON(t *testing.T, client *http.Client, method, endpoint string, requestBody any, wantStatus int, responseBody any) {
	t.Helper()
	var body io.Reader

	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			t.Fatalf("encode %s %s: %v", method, endpoint, err)
		}

		body = bytes.NewReader(encoded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		t.Fatalf("create %s %s: %v", method, endpoint, err)
	}

	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, endpoint, err)
	}

	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body: %s", method, endpoint, response.StatusCode, wantStatus, payload)
	}

	if responseBody != nil {
		err := json.Unmarshal(payload, responseBody)
		if err != nil {
			t.Fatalf("decode %s %s response: %v; body: %s", method, endpoint, err, payload)
		}
	}
}

func getStatus(t *testing.T, client *http.Client, baseURL, jobID string) jobResponse {
	t.Helper()
	var status jobResponse
	doJSON(t, client, http.MethodGet, baseURL+"/api/v1/jobs/"+jobID+"/status", nil, http.StatusOK, &status)

	return status
}

func waitForState(t *testing.T, client *http.Client, baseURL, jobID, want string) jobResponse {
	t.Helper()

	deadline := time.Now().Add(stateTimeout)

	var last jobResponse
	for time.Now().Before(deadline) {
		last = getStatus(t, client, baseURL, jobID)
		if last.State == want {
			return last
		}

		if last.State == "completed" || last.State == "failed" {
			t.Fatalf("job reached %q while waiting for %q: %s", last.State, want, last.Error)
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("job state = %q after %s, want %q", last.State, stateTimeout, want)

	return jobResponse{}
}

func waitForProgress(t *testing.T, client *http.Client, baseURL, jobID string, accept func(progressEvent) bool) progressEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), progressTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/jobs/"+jobID+"/stream", nil)
	if err != nil {
		t.Fatalf("create SSE request: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		t.Fatalf("open SSE stream status = %d; body: %s", response.StatusCode, payload)
	}

	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("SSE content type = %q", contentType)
	}

	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		var event progressEvent
		err := json.Unmarshal(bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:"))), &event)
		if err != nil {
			t.Fatalf("decode SSE event: %v; line: %s", err, line)
		}

		if event.JobID != jobID {
			t.Fatalf("SSE job ID = %q, want %q", event.JobID, jobID)
		}

		if accept(event) {
			return event
		}

		if event.State == "completed" || event.State == "failed" || event.State == "cancelled" {
			t.Fatalf("job reached %q before required SSE progress", event.State)
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE stream: %v", err)
	}

	t.Fatal("SSE stream closed before required progress")

	return progressEvent{}
}

func waitForCheckpoint(t *testing.T, path string) checkpointFile {
	t.Helper()

	deadline := time.Now().Add(progressTimeout)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			var checkpoint checkpointFile
			err := json.Unmarshal(payload, &checkpoint)
			if err != nil {
				t.Fatalf("decode checkpoint %s: %v", path, err)
			}

			return checkpoint
		}

		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read checkpoint %s: %v", path, err)
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("checkpoint did not appear within %s: %s", progressTimeout, path)

	return checkpointFile{}
}

func validateCheckpoint(t *testing.T, checkpoint checkpointFile, jobID, refPath string, circles int) {
	t.Helper()

	if checkpoint.SchemaVersion != 2 {
		t.Errorf("checkpoint schema = %d, want 2", checkpoint.SchemaVersion)
	}

	if checkpoint.JobID != jobID {
		t.Errorf("checkpoint job ID = %q, want %q", checkpoint.JobID, jobID)
	}

	assertCanonicalUUID(t, checkpoint.JobID)

	if len(checkpoint.BestParams) != circles*7 {
		t.Errorf("checkpoint parameter count = %d, want %d", len(checkpoint.BestParams), circles*7)
	}

	if checkpoint.BestCost <= 0 || checkpoint.InitialCost <= 0 {
		t.Errorf("checkpoint costs = best %v, initial %v; want positive", checkpoint.BestCost, checkpoint.InitialCost)
	}

	if checkpoint.Iterations <= 0 || checkpoint.Evaluations <= 0 {
		t.Errorf("checkpoint counters = (%d, %d), want positive", checkpoint.Iterations, checkpoint.Evaluations)
	}

	if checkpoint.RequestedCircles != circles || checkpoint.ActualCircles != circles {
		t.Errorf("checkpoint circles = requested %d, actual %d; want %d", checkpoint.RequestedCircles, checkpoint.ActualCircles, circles)
	}

	if checkpoint.EffectiveSeed != 424242 || checkpoint.ResumeCount != 0 {
		t.Errorf("checkpoint continuation = seed %d, resumes %d", checkpoint.EffectiveSeed, checkpoint.ResumeCount)
	}

	if checkpoint.Termination != "unknown" {
		t.Errorf("live checkpoint termination = %q, want unknown", checkpoint.Termination)
	}

	if checkpoint.Timestamp.IsZero() {
		t.Error("checkpoint timestamp is zero")
	}

	if checkpoint.Config.RefPath != refPath || checkpoint.Config.Mode != "joint" || checkpoint.Config.Backend != "cpu" {
		t.Errorf("checkpoint config = %+v", checkpoint.Config)
	}

	if checkpoint.Config.Circles != circles || checkpoint.Config.Iters != 10000 || checkpoint.Config.PopSize != 200 || checkpoint.Config.CheckpointInterval != 1 {
		t.Errorf("checkpoint optimizer config = %+v", checkpoint.Config)
	}
}

func assertCanonicalUUID(t *testing.T, value string) {
	t.Helper()

	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		t.Fatalf("value %q is not a canonical non-nil UUID", value)
	}
}

func cancelJob(t *testing.T, client *http.Client, baseURL, jobID string) {
	t.Helper()
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/jobs/"+jobID+"/cancel", nil, http.StatusAccepted, nil)
}

func assertPNGArtifact(t *testing.T, client *http.Client, endpoint string, width, height int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create artifact request: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("fetch artifact %s: %v", endpoint, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		t.Fatalf("fetch artifact %s status = %d; body: %s", endpoint, response.StatusCode, payload)
	}

	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("artifact %s content type = %q", endpoint, contentType)
	}

	decoded, err := png.Decode(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		t.Fatalf("decode artifact %s: %v", endpoint, err)
	}

	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		t.Fatalf("artifact %s dimensions = %dx%d, want %dx%d", endpoint, decoded.Bounds().Dx(), decoded.Bounds().Dy(), width, height)
	}
}

func writeReferenceImage(t *testing.T, path string, width, height int) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*3 + y) % 256),
				G: uint8((x + y*5) % 256),
				B: uint8((x*x + y*7) % 256),
				A: 255,
			})
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create reference image: %v", err)
	}

	if err := png.Encode(file, img); err != nil {
		_ = file.Close()

		t.Fatalf("encode reference image: %v", err)
	}

	if err := file.Close(); err != nil {
		t.Fatalf("close reference image: %v", err)
	}
}
