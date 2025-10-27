package cmd

import (
	"context"
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
	serverAddr        string
	serverPort        int
	serveCpuProfile   string
	serveMemProfile   string
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

	// Profiling flags
	serveCmd.Flags().StringVar(&serveCpuProfile, "cpuprofile", "", "Write CPU profile to file")
	serveCmd.Flags().StringVar(&serveMemProfile, "memprofile", "", "Write memory profile to file on shutdown")

	rootCmd.AddCommand(serveCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	// Start CPU profiling if requested
	if serveCpuProfile != "" {
		f, err := os.Create(serveCpuProfile)
		if err != nil {
			return fmt.Errorf("failed to create CPU profile: %w", err)
		}
		defer f.Close()
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
	fmt.Println("  GET    /api/v1/jobs          - List all jobs")
	fmt.Println("  GET    /api/v1/jobs/:id      - Get job status")
	fmt.Println("  GET    /api/v1/jobs/:id/best.png  - Get current best image")
	fmt.Println("  GET    /api/v1/jobs/:id/diff.png  - Get difference image")
	fmt.Println("\nProfiling endpoints:")
	fmt.Printf("  GET    http://%s/debug/pprof/        - pprof index\n", addr)
	fmt.Printf("  GET    http://%s/debug/pprof/profile - CPU profile (30s)\n", addr)
	fmt.Printf("  GET    http://%s/debug/pprof/heap    - Heap profile\n", addr)
	fmt.Printf("  GET    http://%s/debug/pprof/goroutine - Goroutine dump\n", addr)
	fmt.Println("\nPress Ctrl+C to shutdown")

	// Create checkpoint store
	checkpointStore, err := store.NewFSStore("./data")
	if err != nil {
		return fmt.Errorf("failed to create checkpoint store: %w", err)
	}

	// Create server
	srv := server.NewServer(addr, checkpointStore)

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

		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}

		// Write memory profile if requested
		if serveMemProfile != "" {
			f, err := os.Create(serveMemProfile)
			if err != nil {
				return fmt.Errorf("failed to create memory profile: %w", err)
			}
			defer f.Close()
			runtime.GC() // Run GC to get accurate heap stats
			if err := pprof.WriteHeapProfile(f); err != nil {
				return fmt.Errorf("failed to write memory profile: %w", err)
			}
			slog.Info("Memory profile written", "output", serveMemProfile)
		}

		fmt.Println("Server stopped gracefully")
	}

	return nil
}
