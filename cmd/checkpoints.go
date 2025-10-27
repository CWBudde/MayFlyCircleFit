package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/spf13/cobra"
)

var (
	checkpointDataDir string
	keepLast          int
	olderThanDays     int
	forceClean        bool
)

var checkpointsCmd = &cobra.Command{
	Use:   "checkpoints",
	Short: "Manage optimization checkpoints",
	Long: `Manage optimization checkpoints including listing and cleaning old checkpoints.
Checkpoints allow resuming long-running optimizations from saved state.`,
}

var listCheckpointsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available checkpoints",
	Long:  `Display all checkpoints with metadata including job ID, timestamp, iterations, cost, and file sizes.`,
	RunE:  runListCheckpoints,
}

var cleanCheckpointsCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean old checkpoints",
	Long: `Delete old checkpoints based on retention policy.
You can specify how many checkpoints to keep per job or delete checkpoints older than N days.`,
	RunE: runCleanCheckpoints,
}

func init() {
	// Add checkpoints command to root
	rootCmd.AddCommand(checkpointsCmd)

	// Add subcommands
	checkpointsCmd.AddCommand(listCheckpointsCmd)
	checkpointsCmd.AddCommand(cleanCheckpointsCmd)

	// Global flags for checkpoints command
	checkpointsCmd.PersistentFlags().StringVar(&checkpointDataDir, "data-dir", "./data", "Base directory for checkpoint storage")

	// Clean command flags
	cleanCheckpointsCmd.Flags().IntVar(&keepLast, "keep-last", 0, "Keep only the last N checkpoints per job (0 = keep all)")
	cleanCheckpointsCmd.Flags().IntVar(&olderThanDays, "older-than", 0, "Delete checkpoints older than N days (0 = no age limit)")
	cleanCheckpointsCmd.Flags().BoolVarP(&forceClean, "force", "f", false, "Skip confirmation prompt")
}

func runListCheckpoints(cmd *cobra.Command, args []string) error {
	// Create store
	checkpointStore, err := store.NewFSStore(checkpointDataDir)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint store: %w", err)
	}

	// List all checkpoints
	infos, err := checkpointStore.ListCheckpoints()
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(infos) == 0 {
		fmt.Println("No checkpoints found.")
		return nil
	}

	// Display checkpoints in a table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB ID\tTIMESTAMP\tITERATION\tBEST COST\tSIZE")
	fmt.Fprintln(w, "------\t---------\t---------\t---------\t----")

	for _, info := range infos {
		// Get checkpoint directory size
		jobDir := filepath.Join(checkpointDataDir, "jobs", info.JobID)
		size, err := getDirSize(jobDir)
		sizeStr := "unknown"
		if err == nil {
			sizeStr = formatBytes(size)
		}

		// Format timestamp
		timestamp := info.Timestamp.Format("2006-01-02 15:04:05")

		// Truncate job ID for display
		displayID := info.JobID
		if len(displayID) > 12 {
			displayID = displayID[:12] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%.6f\t%s\n",
			displayID,
			timestamp,
			info.Iteration,
			info.BestCost,
			sizeStr,
		)
	}

	w.Flush()

	fmt.Printf("\nTotal checkpoints: %d\n", len(infos))
	return nil
}

func runCleanCheckpoints(cmd *cobra.Command, args []string) error {
	// Validate flags
	if keepLast == 0 && olderThanDays == 0 {
		return fmt.Errorf("must specify either --keep-last or --older-than")
	}

	// Create store
	checkpointStore, err := store.NewFSStore(checkpointDataDir)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint store: %w", err)
	}

	// List all checkpoints
	infos, err := checkpointStore.ListCheckpoints()
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(infos) == 0 {
		fmt.Println("No checkpoints to clean.")
		return nil
	}

	// Determine which checkpoints to delete
	toDelete := selectCheckpointsForDeletion(infos, keepLast, olderThanDays)

	if len(toDelete) == 0 {
		fmt.Println("No checkpoints match deletion criteria.")
		return nil
	}

	// Show what will be deleted
	fmt.Printf("Found %d checkpoint(s) to delete:\n", len(toDelete))
	for _, info := range toDelete {
		displayID := info.JobID
		if len(displayID) > 12 {
			displayID = displayID[:12] + "..."
		}
		fmt.Printf("  - %s (iteration %d, %s)\n",
			displayID,
			info.Iteration,
			info.Timestamp.Format("2006-01-02 15:04:05"),
		)
	}

	// Ask for confirmation unless --force is set
	if !forceClean {
		fmt.Print("\nProceed with deletion? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Delete checkpoints
	deleted := 0
	failed := 0
	for _, info := range toDelete {
		err := checkpointStore.DeleteCheckpoint(info.JobID)
		if err != nil {
			slog.Error("Failed to delete checkpoint", "job_id", info.JobID, "error", err)
			failed++
		} else {
			slog.Info("Deleted checkpoint", "job_id", info.JobID)
			deleted++
		}
	}

	fmt.Printf("\nDeleted %d checkpoint(s), %d failed.\n", deleted, failed)
	return nil
}

// selectCheckpointsForDeletion determines which checkpoints should be deleted based on retention policy
func selectCheckpointsForDeletion(infos []store.CheckpointInfo, keepLast int, olderThanDays int) []store.CheckpointInfo {
	var toDelete []store.CheckpointInfo

	// Apply age-based deletion
	if olderThanDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -olderThanDays)
		for _, info := range infos {
			if info.Timestamp.Before(cutoff) {
				toDelete = append(toDelete, info)
			}
		}
	}

	// Apply count-based deletion (keep last N per job)
	if keepLast > 0 {
		// Group by job ID (in our case, each checkpoint is per job, so this is simple)
		// Since each jobID has only one checkpoint, keepLast effectively means delete all but the most recent
		// But the structure supports multiple checkpoints per job in the future

		// For now, if keepLast > 0 and we have more than keepLast total checkpoints,
		// delete the oldest ones
		if len(infos) > keepLast {
			// Sort by timestamp (oldest first)
			sorted := make([]store.CheckpointInfo, len(infos))
			copy(sorted, infos)

			// Simple bubble sort by timestamp
			for i := 0; i < len(sorted)-1; i++ {
				for j := 0; j < len(sorted)-i-1; j++ {
					if sorted[j].Timestamp.After(sorted[j+1].Timestamp) {
						sorted[j], sorted[j+1] = sorted[j+1], sorted[j]
					}
				}
			}

			// Delete oldest checkpoints beyond keepLast
			numToDelete := len(sorted) - keepLast
			for i := 0; i < numToDelete; i++ {
				// Check if not already in toDelete list
				found := false
				for _, existing := range toDelete {
					if existing.JobID == sorted[i].JobID {
						found = true
						break
					}
				}
				if !found {
					toDelete = append(toDelete, sorted[i])
				}
			}
		}
	}

	return toDelete
}

// getDirSize calculates the total size of a directory
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatBytes formats bytes as human-readable string
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
