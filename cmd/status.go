package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var (
	serverURL string
)

var statusCmd = &cobra.Command{
	Use:   "status [job-id]",
	Short: "Query server status or specific job",
	Long: `Queries the server for job status information.
If no job-id is provided, lists all jobs.
If job-id is provided, shows detailed status for that job.`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	var url string

	if len(args) == 0 {
		// List all jobs
		url = fmt.Sprintf("%s/api/v1/jobs", serverURL)
		return listJobs(url)
	} else {
		// Get specific job status
		jobID := args[0]
		url = fmt.Sprintf("%s/api/v1/jobs/%s/status", serverURL, jobID)
		return getJobStatus(url, jobID)
	}
}

func listJobs(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error: %s", string(body))
	}

	var jobs []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if len(jobs) == 0 {
		fmt.Println("No jobs found")
		return nil
	}

	fmt.Printf("Found %d job(s):\n\n", len(jobs))
	for _, job := range jobs {
		fmt.Printf("Job ID: %s\n", job["id"])
		fmt.Printf("  State: %s\n", job["state"])
		fmt.Printf("  Circles: %v\n", job["config"].(map[string]interface{})["circles"])
		fmt.Printf("  Mode: %s\n", job["config"].(map[string]interface{})["mode"])
		if job["bestCost"] != nil && job["bestCost"].(float64) > 0 {
			fmt.Printf("  Cost: %.2f -> %.2f\n", job["initialCost"], job["bestCost"])
		}
		fmt.Println()
	}

	return nil
}

func getJobStatus(url, jobID string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error: %s", string(body))
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Display status
	fmt.Printf("Job: %s\n", status["id"])
	fmt.Printf("State: %s\n", status["state"])
	fmt.Println()

	config := status["config"].(map[string]interface{})
	fmt.Println("Configuration:")
	fmt.Printf("  Reference: %s\n", config["refPath"])
	fmt.Printf("  Mode: %s\n", config["mode"])
	fmt.Printf("  Circles: %v\n", config["circles"])
	fmt.Printf("  Iterations: %v\n", config["iters"])
	fmt.Printf("  Population: %v\n", config["popSize"])
	fmt.Println()

	fmt.Println("Progress:")
	if status["initialCost"] != nil && status["initialCost"].(float64) > 0 {
		fmt.Printf("  Initial Cost: %.2f\n", status["initialCost"])
	}
	if status["bestCost"] != nil && status["bestCost"].(float64) > 0 {
		fmt.Printf("  Best Cost: %.2f\n", status["bestCost"])
		improvement := status["initialCost"].(float64) - status["bestCost"].(float64)
		improvementPct := (improvement / status["initialCost"].(float64)) * 100
		fmt.Printf("  Improvement: %.2f (%.1f%%)\n", improvement, improvementPct)
	}

	if status["elapsed"] != nil {
		elapsed := time.Duration(status["elapsed"].(float64) * float64(time.Second))
		fmt.Printf("  Elapsed: %s\n", elapsed.Round(time.Millisecond))
	}

	if status["cps"] != nil && status["cps"].(float64) > 0 {
		fmt.Printf("  Throughput: %.0f circles/sec\n", status["cps"])
	}

	if status["error"] != nil && status["error"].(string) != "" {
		fmt.Printf("\nError: %s\n", status["error"])
	}

	return nil
}
