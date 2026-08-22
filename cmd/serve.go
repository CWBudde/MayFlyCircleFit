package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/server"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/spf13/cobra"
)

var (
	serverAddr      string
	serverPort      int
	serveCpuProfile string
	serveMemProfile string
	servePprof      bool
	serveBackend    string
	serveInputRoots []string
	serveMaxJobs    int
	serveQueueSize  int
	serveDataRoot   string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server for background optimization jobs",
	Long: `Starts an HTTP server that accepts optimization jobs via REST API.
Jobs run in the background and progress can be monitored via SSE or status endpoints.`,
	RunE: runServer,
}

func init() {
	serveCmd.Flags().StringVar(&serverAddr, "addr", "localhost", "Server bind address")
	serveCmd.Flags().IntVar(&serverPort, "port", 8080, "Server port")
	serveCmd.Flags().StringVar(&serveBackend, "backend", "cpu", "Renderer backend to use (cpu, opencl; aliases: gpu, cl)")
	serveCmd.Flags().StringSliceVar(&serveInputRoots, "input-root", []string{"."}, "Allowed image input root (repeatable)")
	serveCmd.Flags().IntVar(&serveMaxJobs, "max-jobs", 1, "Maximum concurrently running jobs")
	serveCmd.Flags().IntVar(&serveQueueSize, "queue-size", 16, "Maximum queued jobs")
	serveCmd.Flags().StringVar(&serveDataRoot, "data-root", "./data", "Job artifact and checkpoint root")
	serveCmd.Flags().BoolVar(&servePprof, "enable-pprof", false, "Enable trusted-local /debug/pprof endpoints")

	// Profiling flags
	serveCmd.Flags().StringVar(&serveCpuProfile, "cpuprofile", "", "Write CPU profile to file")
	serveCmd.Flags().StringVar(&serveMemProfile, "memprofile", "", "Write memory profile to file on shutdown")

	rootCmd.AddCommand(serveCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	// Flag values the user typed are invocation errors, so they exit with
	// status 2 rather than the status reserved for work that failed.
	if serveMaxJobs < 1 || serveMaxJobs > 16 {
		return NewUsageError(errors.New("max-jobs must be between 1 and 16"))
	}

	if serveQueueSize < 1 || serveQueueSize > 100 {
		return NewUsageError(errors.New("queue-size must be between 1 and 100"))
	}

	backend, err := parseBackendFlag(serveBackend)
	if err != nil {
		return NewUsageError(fmt.Errorf("invalid backend: %w", err))
	}

	if servePprof && serverAddr != "localhost" && serverAddr != "127.0.0.1" && serverAddr != "::1" {
		return NewUsageError(errors.New("pprof requires a trusted loopback bind address"))
	}

	// Start CPU profiling if requested
	if serveCpuProfile != "" {
		f, err := os.Create(serveCpuProfile)
		if err != nil {
			return fmt.Errorf("failed to create CPU profile: %w", err)
		}
		// LIFO: StopCPUProfile flushes the remaining samples before this
		// close runs. The profile is a diagnostic, so a failed close is
		// logged rather than promoted over the command's own result.
		defer func() {
			err := f.Close()
			if err != nil {
				slog.Error("Failed to close CPU profile", "output", serveCpuProfile, "error", err)
			}
		}()

		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("failed to start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()

		slog.Info("CPU profiling enabled", "output", serveCpuProfile)
	}

	addr := fmt.Sprintf("%s:%d", serverAddr, serverPort)

	slog.Info("Starting MayFlyCircleFit server", "addr", addr)
	fmt.Printf("Server listening on http://%s\n", addr)
	fmt.Println("API endpoints:")
	fmt.Println("  POST   /api/v1/jobs          - Create new job")
	fmt.Println("  GET    /api/v1/jobs          - List jobs (use ?limit=N for cursor pages)")
	fmt.Println("  GET    /api/v1/system        - Process capabilities")
	fmt.Println("  GET    /api/v1/stream        - Live progress for all jobs")
	fmt.Println("  GET    /api/v1/jobs/:id      - Get job status")
	fmt.Println("  GET    /api/v1/jobs/:id/best.png  - Get current best image")
	fmt.Println("  GET    /api/v1/jobs/:id/diff.png  - Get difference image")
	fmt.Println("  POST   /api/v1/jobs/:id/extend    - Append circles from a completed checkpoint")

	if servePprof {
		fmt.Println("\nProfiling endpoints enabled for this trusted-local server:")
		fmt.Printf("  GET    http://%s/debug/pprof/        - pprof index\n", addr)
	}

	fmt.Println("\nPress Ctrl+C to shutdown")

	// Create checkpoint store
	checkpointStore, err := store.NewFSStore(serveDataRoot)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint store: %w", err)
	}

	// Create server
	srv := server.NewServerWithOptions(addr, checkpointStore, server.ServerOptions{
		EnablePprof:       servePprof,
		InputRoots:        serveInputRoots,
		BuildMetadata:     server.BuildMetadata{Version: version, Commit: commit, BuildDate: buildDate},
		MaxConcurrentJobs: serveMaxJobs,
		QueueSize:         serveQueueSize,
		DefaultBackend:    backend,
		DataRoot:          serveDataRoot,
	})

	// Channel for server errors
	serverErrors := make(chan error, 1)

	// Start server in goroutine
	go func() {
		serverErrors <- srv.Start()
	}()

	// Setup signal handling for graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		slog.Info("Shutdown signal received", "signal", sig)
		fmt.Println("\nShutting down server...")

		// Give outstanding operations 10 seconds to complete
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}

		// Write memory profile if requested
		if serveMemProfile != "" {
			f, err := os.Create(serveMemProfile)
			if err != nil {
				return fmt.Errorf("failed to create memory profile: %w", err)
			}

			runtime.GC() // Run GC to get accurate heap stats

			if err := pprof.WriteHeapProfile(f); err != nil {
				_ = f.Close()
				return fmt.Errorf("failed to write memory profile: %w", err)
			}
			// Closed explicitly, not deferred: a full filesystem reports the
			// short write here, and a deferred close would discard it and let
			// the command claim a profile it did not finish writing.
			if err := f.Close(); err != nil {
				return fmt.Errorf("failed to close memory profile: %w", err)
			}

			slog.Info("Memory profile written", "output", serveMemProfile)
		}

		fmt.Println("Server stopped gracefully")
	}

	return nil
}
