// Command cmaes-measurement submits and collects the evaluation-matched
// campaign described by go-cma-es PLAN.md Phase 11.
//
//nolint:cyclop,embeddedstructfieldcheck,err113,forbidigo,goconst,lll,noinlineerr,wrapcheck,wsl_v5 // A standalone campaign driver reports contextual CLI errors and Markdown to stdout.
package main

import (
	"bufio"
	"bytes"
	"cmp"
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

	"github.com/cwbudde/circlefit/internal/store"
)

const (
	defaultBudget  = 6_502_400
	defaultProject = "cmaes-phase11"
	defaultPop     = 1024
	// resultColumns is the width of the header writeResults emits.
	resultColumns = 12
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
	// The two Mayfly arms have a fixed campaign shape -- 2048 iterations,
	// however they are split across restarts -- whose cost is defaultBudget
	// evaluations. Only the CMA-ES arms derive their length from the budget,
	// so a larger one would fund them past anything the Mayfly arms ever
	// reach and the collector would still print the comparison as
	// evaluation-matched. Reject that instead of reporting it.
	if budget > defaultBudget {
		return nil, fmt.Errorf(
			"budget %d exceeds the fixed Mayfly campaign budget %d; the arms would no longer be evaluation-matched",
			budget, defaultBudget,
		)
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
			populationSpread, sigma, conditionNumber := formatDiagnostics(
				diagnostic.PopulationSpread, diagnostic.Sigma, diagnostic.ConditionNumber,
			)
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

func formatDiagnostics(spread, sigma, conditionNumber float64) (string, string, string) {
	if sigma == 0 && conditionNumber == 0 {
		return strconv.FormatFloat(spread, 'g', 17, 64), "", ""
	}

	return "", strconv.FormatFloat(sigma, 'g', 17, 64), strconv.FormatFloat(conditionNumber, 'g', 17, 64)
}

// familyAlpha is the family-wise error rate the campaign's seven paired
// contrasts are held to together. Holm's step-down procedure spends it across
// the whole family, so a contrast that clears the uncorrected 0.05 can still
// retain its null here.
const familyAlpha = 0.05

// contrast is one paired comparison of a candidate arm against a control arm,
// carried far enough to be corrected for multiplicity before it is printed.
type contrast struct {
	control   string
	candidate string
	gain      float64
	statistic float64
	pValue    float64
	wins      int
	rejected  bool
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
	for _, name := range order {
		current := byArm[name]
		if len(current) != 12 {
			return fmt.Errorf("arm %s has %d blocks, want 12", name, len(current))
		}
		slices.SortFunc(current, func(a, b resultRow) int { return a.Block - b.Block })
	}

	contrasts := make([]contrast, 0, 7)
	for _, control := range []string{"mayfly-single", "mayfly-r16"} {
		for _, candidate := range order[1:] {
			if candidate == control {
				continue
			}
			gain, statistic, wins := pairedImprovement(byArm[control], byArm[candidate])
			contrasts = append(contrasts, contrast{
				control:   control,
				candidate: candidate,
				gain:      gain,
				statistic: statistic,
				pValue:    studentTTwoSided(statistic, 11),
				wins:      wins,
			})
		}
	}
	holmReject(contrasts, familyAlpha)

	fmt.Println("| arm | mean | sd | median | best | gain vs Mayfly single | t (df=11) | p | Holm | blocks won |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |")
	for index, name := range order {
		values := costs(byArm[name])
		mean, sd := meanSD(values)
		summary := "control | control | control | control | control"
		if index > 0 {
			summary = summarize(contrasts, "mayfly-single", name)
		}

		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.2f | %s |\n",
			name, mean, sd, median(values), slices.Min(values), summary)
	}

	fmt.Println("\nAgainst Mayfly r16:")
	fmt.Println("| arm | gain vs Mayfly r16 | t (df=11) | p | Holm | blocks won |")
	fmt.Println("| --- | ---: | ---: | ---: | --- | ---: |")
	for _, name := range []string{"cmaes-single", "cmaes-ipop", "sep-cmaes-ipop"} {
		fmt.Printf("| `%s` | %s |\n", name, summarize(contrasts, "mayfly-r16", name))
	}

	fmt.Printf("\nHolm step-down over all %d paired contrasts at a family-wise alpha of %.2f;\n",
		len(contrasts), familyAlpha)
	fmt.Printf("the uncorrected two-sided threshold at df=11 is t=%.2f and the Bonferroni one is t=%.2f.\n",
		studentTCritical(familyAlpha, 11), studentTCritical(familyAlpha/float64(len(contrasts)), 11))

	return nil
}

// summarize renders one contrast's cells for the Markdown tables above.
func summarize(contrasts []contrast, control, candidate string) string {
	for _, current := range contrasts {
		if current.control != control || current.candidate != candidate {
			continue
		}
		decision := "retain"
		if current.rejected {
			decision = "reject"
		}

		return fmt.Sprintf("%+.2f | %+.2f | %.5f | %s | %d/12",
			current.gain, current.statistic, current.pValue, decision, current.wins)
	}

	return "n/a | n/a | n/a | n/a | n/a"
}

// holmReject marks the contrasts whose null hypotheses Holm's step-down
// procedure rejects at the given family-wise alpha. It stops at the first
// contrast that fails its threshold, so every larger p-value retains too.
func holmReject(contrasts []contrast, alpha float64) {
	order := make([]int, len(contrasts))
	for index := range order {
		order[index] = index
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(contrasts[a].pValue, contrasts[b].pValue)
	})

	for rank, index := range order {
		if contrasts[index].pValue >= alpha/float64(len(contrasts)-rank) {
			return
		}
		contrasts[index].rejected = true
	}
}

// studentTTwoSided returns the two-sided p-value of a t statistic on the given
// degrees of freedom. An infinite statistic comes from a zero-variance paired
// difference and is reported as p=0.
func studentTTwoSided(statistic float64, degrees int) float64 {
	if math.IsInf(statistic, 0) {
		return 0
	}
	freedom := float64(degrees)

	return regularizedIncompleteBeta(freedom/(freedom+statistic*statistic), freedom/2, 0.5)
}

// studentTCritical inverts studentTTwoSided by bisection, returning the
// two-sided critical t for the given alpha and degrees of freedom.
func studentTCritical(alpha float64, degrees int) float64 {
	low, high := 0.0, 1e3
	for range 200 {
		middle := (low + high) / 2
		if studentTTwoSided(middle, degrees) > alpha {
			low = middle
		} else {
			high = middle
		}
	}

	return (low + high) / 2
}

// regularizedIncompleteBeta evaluates I_x(a, b) by the continued fraction of
// Numerical Recipes section 6.4, switching branches where it converges fastest.
func regularizedIncompleteBeta(x, a, b float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}

	logA, _ := math.Lgamma(a)
	logB, _ := math.Lgamma(b)
	logSum, _ := math.Lgamma(a + b)
	front := math.Exp(logSum - logA - logB + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}

	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

// betaContinuedFraction evaluates the continued fraction of the incomplete beta
// function by the modified Lentz algorithm.
func betaContinuedFraction(x, a, b float64) float64 {
	const (
		maxIterations = 300
		epsilon       = 1e-15
		tiny          = 1e-300
	)

	guard := func(value float64) float64 {
		if math.Abs(value) < tiny {
			return tiny
		}

		return value
	}

	sum, plus, minus := a+b, a+1, a-1
	numerator := 1.0
	denominator := 1 / guard(1-sum*x/plus)
	fraction := denominator
	for step := 1; step <= maxIterations; step++ {
		index := float64(step)
		doubled := 2 * index

		term := index * (b - index) * x / ((minus + doubled) * (a + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		fraction *= denominator * numerator

		term = -(a + index) * (sum + index) * x / ((a + doubled) * (plus + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		delta := denominator * numerator
		fraction *= delta

		if math.Abs(delta-1) < epsilon {
			break
		}
	}

	return fraction
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
	for index, record := range records[1:] {
		line := index + 2
		if len(record) != resultColumns {
			return nil, fmt.Errorf("results line %d has %d columns, want %d", line, len(record), resultColumns)
		}

		parser := rowParser{record: record, line: line}
		row := resultRow{
			manifestRow: manifestRow{
				Arm: record[0], Block: parser.integer(1), Seed: parser.integer64(2), JobID: record[3],
			},
			State: record[4], Termination: record[5], OptimizerVersion: record[6],
			Score:             parser.float(7),
			ScoredEvaluations: parser.integer(8),
			FinalEvaluations:  parser.integer(9),
			Iterations:        parser.integer(10),
			ElapsedSeconds:    parser.float(11),
		}
		if parser.err != nil {
			return nil, parser.err
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// rowParser reads the numeric columns of one result record and keeps the first
// failure. Discarding the strconv errors instead would let a hand-edited or
// truncated CSV parse as zeros and be reported as a statistic, which is the
// one failure mode a measurement collector must not have.
type rowParser struct {
	record []string
	err    error
	line   int
}

func (p *rowParser) fail(column int, err error) {
	if p.err == nil {
		p.err = fmt.Errorf("results line %d column %d (%q): %w", p.line, column+1, p.record[column], err)
	}
}

func (p *rowParser) integer(column int) int {
	value, err := strconv.Atoi(p.record[column])
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) integer64(column int) int64 {
	value, err := strconv.ParseInt(p.record[column], 10, 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) float(column int) float64 {
	value, err := strconv.ParseFloat(p.record[column], 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
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
