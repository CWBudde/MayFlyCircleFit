// Command cmaes-measurement submits and collects the evaluation-matched
// campaign described by go-cma-es PLAN.md Phase 11.
//
//nolint:cyclop,embeddedstructfieldcheck,err113,forbidigo,goconst,lll,noinlineerr,wrapcheck,wsl_v5 // A standalone campaign driver reports contextual CLI errors and Markdown to stdout.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
)

const (
	defaultBudget  = 6_502_400
	defaultProject = "cmaes-phase11"
	defaultPop     = 1024
)

type arm struct {
	name              string
	optimizer         string
	covariance        string
	restartStrategy   string
	iters             int
	optimizerRestarts int
}

type manifestRow struct {
	Arm   string
	Block int
	Seed  int64
	JobID string
}

type jobStatus struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Termination string  `json:"termination"`
	BestCost    float64 `json:"bestCost"`
	Elapsed     float64 `json:"elapsed"`
	Iterations  int     `json:"iterations"`
	Evaluations int     `json:"evaluations"`
}

type resultRow struct {
	manifestRow
	State             string
	Termination       string
	OptimizerVersion  string
	Score             float64
	ScoredEvaluations int
	FinalEvaluations  int
	Iterations        int
	ElapsedSeconds    float64
}

type checkpoint struct {
	OptimizerVersion string `json:"optimizerVersion"`
}

type checkpointInfo struct {
	Termination      string `json:"termination"`
	OptimizerVersion string `json:"optimizerVersion"`
	Iteration        int    `json:"iteration"`
	Evaluations      int    `json:"evaluations"`
}

type settings struct {
	server       string
	dataRoot     string
	reference    string
	manifestPath string
	resultsPath  string
	trajectory   string
	project      string
	action       string
	blocks       int
	budget       int
	workers      int
	seedBase     int64
}

func main() {
	config := parseFlags()
	var err error

	switch config.action {
	case "submit":
		err = submit(config)
	case "collect":
		err = collect(config)
	case "preliminary":
		err = collectPreliminary(config)
	case "analyze":
		err = analyze(config.resultsPath)
	default:
		err = fmt.Errorf("unknown action %q (want submit, collect, preliminary, or analyze)", config.action)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() settings {
	var config settings
	flag.StringVar(&config.action, "action", "collect", "submit, collect, preliminary, or analyze")
	flag.StringVar(&config.server, "server", "http://localhost:8085", "serve base URL")
	flag.StringVar(&config.dataRoot, "data-root", "./data/cmaes-phase11", "serve data root")
	flag.StringVar(&config.reference, "ref", "example/MayFly-512.png", "reference image")
	flag.StringVar(&config.manifestPath, "manifest", "./data/cmaes-phase11/manifest.csv", "job manifest")
	flag.StringVar(&config.resultsPath, "results", "docs/cmaes-measurement.csv", "collected result CSV")
	flag.StringVar(&config.trajectory, "trajectories", "docs/cmaes-trajectories.csv", "diagnostic trajectory CSV")
	flag.StringVar(&config.project, "project", defaultProject, "server project")
	flag.IntVar(&config.blocks, "blocks", 12, "paired blocks")
	flag.IntVar(&config.budget, "budget", defaultBudget, "optimizer evaluation cap")
	flag.IntVar(&config.workers, "workers", 8, "parallel evaluation workers")
	flag.Int64Var(&config.seedBase, "seed-base", 111_000, "first block seed prefix")
	flag.Parse()

	return config
}

func campaignArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%defaultPop != 0 {
		return nil, fmt.Errorf("budget %d must be positive and divisible by population %d", budget, defaultPop)
	}

	return []arm{
		{name: "mayfly-single", optimizer: "mayfly", iters: 2048, optimizerRestarts: 1},
		{name: "mayfly-r16", optimizer: "mayfly", iters: 128, optimizerRestarts: 16},
		{name: "cmaes-single", optimizer: "cmaes", covariance: "full", restartStrategy: "none", iters: budget / defaultPop, optimizerRestarts: 1},
		{name: "cmaes-ipop", optimizer: "cmaes", covariance: "full", restartStrategy: "ipop", iters: budget / defaultPop, optimizerRestarts: 1},
		{name: "sep-cmaes-ipop", optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop", iters: budget / defaultPop, optimizerRestarts: 1},
	}, nil
}

func submit(config settings) error {
	arms, err := campaignArms(config.budget)
	if err != nil {
		return err
	}

	if config.blocks != 12 {
		return fmt.Errorf("phase 11 requires exactly 12 paired blocks, got %d", config.blocks)
	}

	if err := os.MkdirAll(filepath.Dir(config.manifestPath), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	file, err := os.OpenFile(config.manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh manifest (refusing to duplicate a campaign): %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "block", "seed", "jobId"}); err != nil {
		return err
	}
	writer.Flush()

	client := &http.Client{Timeout: 30 * time.Second}
	for block := 1; block <= config.blocks; block++ {
		seed := config.seedBase + int64(block)
		for _, current := range arms {
			jobID, submitErr := submitJob(client, config, current, seed)
			if submitErr != nil {
				return fmt.Errorf("submit %s block %d: %w", current.name, block, submitErr)
			}

			record := []string{current.name, strconv.Itoa(block), strconv.FormatInt(seed, 10), jobID}
			if err := writer.Write(record); err != nil {
				return fmt.Errorf("record submitted job: %w", err)
			}
			writer.Flush()
			if err := writer.Error(); err != nil {
				return fmt.Errorf("flush manifest: %w", err)
			}
			fmt.Printf("submitted block %02d %-16s %s\n", block, current.name, jobID)
		}
	}

	return nil
}

func submitJob(client *http.Client, config settings, current arm, seed int64) (string, error) {
	payload := map[string]any{
		"project": config.project, "refPath": config.reference,
		"mode": "batch", "backend": "cpu", "optimizer": current.optimizer,
		"circles": 8, "batchSize": 8, "iters": current.iters, "popSize": defaultPop,
		"optimizerEpochs": 1, "optimizerRestarts": current.optimizerRestarts,
		"seed": seed, "threads": 1, "parallelEvaluation": true,
		"evaluationWorkers": config.workers, "disableConvergence": true,
		"enableTrace": true, "enableOptimizerDiagnostics": true,
	}
	if current.optimizer == "mayfly" {
		payload["variant"] = "standard"
	} else {
		payload["covarianceMode"] = current.covariance
		payload["restartStrategy"] = current.restartStrategy
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, config.server+"/api/v1/jobs", bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("server returned %s: %s", response.Status, responseBody)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("create response has no job id")
	}

	return created.ID, nil
}

func collect(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]resultRow, 0, len(manifest))
	counts := make(map[string]int)

	for _, record := range manifest {
		status, statusErr := fetchStatus(client, config.server, record.JobID)
		if statusErr != nil {
			return fmt.Errorf("status %s: %w", record.JobID, statusErr)
		}
		counts[status.State]++
		if status.State != "completed" {
			continue
		}

		result, collectErr := collectJob(config, record, status)
		if collectErr != nil {
			return collectErr
		}
		results = append(results, result)
	}

	fmt.Printf("campaign status:")
	for _, state := range []string{"pending", "running", "completed", "failed", "cancelled"} {
		if counts[state] > 0 {
			fmt.Printf(" %s=%d", state, counts[state])
		}
	}
	fmt.Println()

	if len(results) != len(manifest) {
		return fmt.Errorf("campaign is not complete: collected %d of %d jobs", len(results), len(manifest))
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}
	if err := writeTrajectories(config, manifest); err != nil {
		return err
	}

	return analyze(config.resultsPath)
}

func collectPreliminary(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	results := make([]resultRow, 0, len(manifest))
	available := make([]manifestRow, 0, len(manifest))
	for _, record := range manifest {
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)
		body, readErr := os.ReadFile(filepath.Join(jobDir, "checkpoint-info.json"))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("read %s checkpoint info: %w", record.JobID, readErr)
		}

		var saved checkpointInfo
		if err := json.Unmarshal(body, &saved); err != nil {
			return fmt.Errorf("decode %s checkpoint info: %w", record.JobID, err)
		}
		state := "interrupted"
		if saved.Termination == "completed" {
			state = "completed"
		}
		status := jobStatus{
			State: state, Termination: saved.Termination,
			Iterations: saved.Iteration, Evaluations: saved.Evaluations,
		}
		result, collectErr := collectJob(config, record, status)
		if collectErr != nil {
			return collectErr
		}
		result.OptimizerVersion = saved.OptimizerVersion
		results = append(results, result)
		available = append(available, record)
	}
	if len(results) == 0 {
		return errors.New("campaign has no persisted job results")
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}
	if err := writeTrajectories(config, available); err != nil {
		return err
	}

	fmt.Printf("wrote %d preliminary results from %d planned jobs; no inferential statistics were calculated\n", len(results), len(manifest))

	return nil
}

func fetchStatus(client *http.Client, server, jobID string) (jobStatus, error) {
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, server+"/api/v1/jobs/"+jobID+"/status", nil,
	)
	if err != nil {
		return jobStatus{}, fmt.Errorf("build status request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return jobStatus{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return jobStatus{}, fmt.Errorf("server returned %s: %s", response.Status, body)
	}

	var status jobStatus
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return jobStatus{}, err
	}

	return status, nil
}

func collectJob(config settings, record manifestRow, status jobStatus) (resultRow, error) {
	jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)
	trace, err := readTrace(filepath.Join(jobDir, "trace.jsonl"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s trace: %w", record.JobID, err)
	}

	score := math.Inf(1)
	scoredEvaluations := 0
	for _, entry := range trace {
		if entry.Evaluations <= config.budget && entry.Cost < score {
			score = entry.Cost
			scoredEvaluations = entry.Evaluations
		}
	}
	if math.IsInf(score, 1) {
		return resultRow{}, fmt.Errorf("job %s has no trace sample within budget", record.JobID)
	}
	if status.Elapsed == 0 && len(trace) > 1 {
		status.Elapsed = trace[len(trace)-1].Timestamp.Sub(trace[0].Timestamp).Seconds()
	}

	var saved checkpoint
	checkpointBody, err := os.ReadFile(filepath.Join(jobDir, "checkpoint.json"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s checkpoint: %w", record.JobID, err)
	}
	if err := json.Unmarshal(checkpointBody, &saved); err != nil {
		return resultRow{}, fmt.Errorf("decode %s checkpoint: %w", record.JobID, err)
	}

	return resultRow{
		manifestRow: record, State: status.State, Termination: status.Termination,
		OptimizerVersion: saved.OptimizerVersion, Score: score,
		ScoredEvaluations: scoredEvaluations, FinalEvaluations: status.Evaluations,
		Iterations: status.Iterations, ElapsedSeconds: status.Elapsed,
	}, nil
}

func readTrace(path string) ([]store.TraceEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []store.TraceEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		var entry store.TraceEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

func readManifest(path string) ([]manifestRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 || !slices.Equal(records[0], []string{"arm", "block", "seed", "jobId"}) {
		return nil, errors.New("manifest has an unexpected header or no jobs")
	}

	rows := make([]manifestRow, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != 4 {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}
		block, blockErr := strconv.Atoi(record[1])
		seed, seedErr := strconv.ParseInt(record[2], 10, 64)
		if blockErr != nil || seedErr != nil {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}
		rows = append(rows, manifestRow{Arm: record[0], Block: block, Seed: seed, JobID: record[3]})
	}

	return rows, nil
}

func writeResults(path string, results []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	header := []string{"arm", "block", "seed", "jobId", "state", "termination", "optimizerVersion", "bestCost", "scoredEvaluations", "finalEvaluations", "iterations", "elapsedSeconds"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, result := range results {
		record := []string{
			result.Arm, strconv.Itoa(result.Block), strconv.FormatInt(result.Seed, 10), result.JobID,
			result.State, result.Termination, result.OptimizerVersion,
			strconv.FormatFloat(result.Score, 'g', 17, 64), strconv.Itoa(result.ScoredEvaluations),
			strconv.Itoa(result.FinalEvaluations), strconv.Itoa(result.Iterations),
			strconv.FormatFloat(result.ElapsedSeconds, 'f', 6, 64),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()

	return writer.Error()
}

func writeTrajectories(config settings, manifest []manifestRow) error {
	if err := os.MkdirAll(filepath.Dir(config.trajectory), 0o755); err != nil {
		return err
	}
	file, err := os.Create(config.trajectory)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "block", "seed", "iteration", "evaluations", "bestCost", "populationSpread", "sigma", "conditionNumber"}); err != nil {
		return err
	}

	early := map[int]bool{1: true, 5: true, 10: true, 20: true, 40: true, 80: true, 160: true, 255: true}
	for _, record := range manifest {
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)
		entries, readErr := readTrace(filepath.Join(jobDir, "trace.jsonl"))
		if readErr != nil {
			return readErr
		}
		lastEligible := -1
		for index, entry := range entries {
			if entry.OptimizerDiagnostics != nil && entry.Evaluations <= config.budget {
				lastEligible = index
			}
		}
		lastBucket := -1
		for index, entry := range entries {
			if entry.OptimizerDiagnostics == nil || entry.Evaluations > config.budget {
				continue
			}
			bucket := entry.Evaluations * 256 / config.budget
			isLast := index == lastEligible
			if !isLast && !early[entry.Iteration] && bucket == lastBucket {
				continue
			}
			lastBucket = bucket
			diagnostic := entry.OptimizerDiagnostics
			populationSpread := ""
			sigma := ""
			conditionNumber := ""
			if diagnostic.Sigma == 0 && diagnostic.ConditionNumber == 0 {
				populationSpread = strconv.FormatFloat(diagnostic.PopulationSpread, 'g', 17, 64)
			} else {
				sigma = strconv.FormatFloat(diagnostic.Sigma, 'g', 17, 64)
				conditionNumber = strconv.FormatFloat(diagnostic.ConditionNumber, 'g', 17, 64)
			}
			record := []string{
				record.Arm, strconv.Itoa(record.Block), strconv.FormatInt(record.Seed, 10),
				strconv.Itoa(entry.Iteration), strconv.Itoa(entry.Evaluations),
				strconv.FormatFloat(entry.Cost, 'g', 17, 64),
				populationSpread, sigma, conditionNumber,
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
	}
	writer.Flush()

	return writer.Error()
}

func analyze(path string) error {
	rows, err := readResults(path)
	if err != nil {
		return err
	}

	byArm := make(map[string][]resultRow)
	for _, row := range rows {
		byArm[row.Arm] = append(byArm[row.Arm], row)
	}
	order := []string{"mayfly-single", "mayfly-r16", "cmaes-single", "cmaes-ipop", "sep-cmaes-ipop"}
	baseline := byArm[order[0]]
	if len(baseline) != 12 {
		return fmt.Errorf("baseline has %d blocks, want 12", len(baseline))
	}

	fmt.Println("| arm | mean | sd | median | best | gain vs Mayfly single | t (df=11) | blocks won |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, name := range order {
		current := byArm[name]
		if len(current) != 12 {
			return fmt.Errorf("arm %s has %d blocks, want 12", name, len(current))
		}
		slices.SortFunc(current, func(a, b resultRow) int { return a.Block - b.Block })
		values := costs(current)
		mean, sd := meanSD(values)
		gain, statistic, wins := pairedImprovement(baseline, current)
		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.2f | %+.2f | %+.2f | %d/12 |\n",
			name, mean, sd, median(values), slices.Min(values), gain, statistic, wins)
	}

	fmt.Println("\nAgainst Mayfly r16:")
	r16 := byArm["mayfly-r16"]
	for _, name := range []string{"cmaes-single", "cmaes-ipop", "sep-cmaes-ipop"} {
		gain, statistic, wins := pairedImprovement(r16, byArm[name])
		fmt.Printf("- `%s`: gain %+.2f, t=%+.2f, blocks won %d/12\n", name, gain, statistic, wins)
	}

	return nil
}

func readResults(path string) ([]resultRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) != 61 {
		return nil, fmt.Errorf("results contain %d jobs, want 60", len(records)-1)
	}

	rows := make([]resultRow, 0, 60)
	for _, record := range records[1:] {
		block, _ := strconv.Atoi(record[1])
		seed, _ := strconv.ParseInt(record[2], 10, 64)
		score, _ := strconv.ParseFloat(record[7], 64)
		scored, _ := strconv.Atoi(record[8])
		final, _ := strconv.Atoi(record[9])
		iterations, _ := strconv.Atoi(record[10])
		elapsed, _ := strconv.ParseFloat(record[11], 64)
		rows = append(rows, resultRow{
			manifestRow: manifestRow{Arm: record[0], Block: block, Seed: seed, JobID: record[3]},
			State:       record[4], Termination: record[5], OptimizerVersion: record[6], Score: score,
			ScoredEvaluations: scored, FinalEvaluations: final, Iterations: iterations, ElapsedSeconds: elapsed,
		})
	}

	return rows, nil
}

func costs(rows []resultRow) []float64 {
	values := make([]float64, len(rows))
	for index, row := range rows {
		values[index] = row.Score
	}

	return values
}

func meanSD(values []float64) (float64, float64) {
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))

	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(values) - 1)

	return mean, math.Sqrt(variance)
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	middle := len(ordered) / 2

	return (ordered[middle-1] + ordered[middle]) / 2
}

func pairedImprovement(control, candidate []resultRow) (float64, float64, int) {
	controlByBlock := make(map[int]float64, len(control))
	for _, row := range control {
		controlByBlock[row.Block] = row.Score
	}

	differences := make([]float64, 0, len(candidate))
	wins := 0
	for _, row := range candidate {
		difference := controlByBlock[row.Block] - row.Score
		differences = append(differences, difference)
		if difference > 0 {
			wins++
		}
	}
	mean, sd := meanSD(differences)
	if sd == 0 {
		return mean, math.Copysign(math.Inf(1), mean), wins
	}

	return mean, mean / (sd / math.Sqrt(float64(len(differences)))), wins
}
