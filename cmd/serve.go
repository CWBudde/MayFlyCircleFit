package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/server"
	"github.com/spf13/cobra"
)

var (
	serverAddr string
	serverPort int
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
	rootCmd.AddCommand(serveCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	addr := fmt.Sprintf("%s:%d", serverAddr, serverPort)

	slog.Info("Starting MayFlyCircleFit server", "addr", addr)
	fmt.Printf("Server listening on http://%s\n", addr)
	fmt.Println("API endpoints:")
	fmt.Println("  POST   /api/v1/jobs          - Create new job")
	fmt.Println("  GET    /api/v1/jobs          - List all jobs")
	fmt.Println("  GET    /api/v1/jobs/:id      - Get job status")
	fmt.Println("  GET    /api/v1/jobs/:id/best.png  - Get current best image")
	fmt.Println("  GET    /api/v1/jobs/:id/diff.png  - Get difference image")
	fmt.Println("\nPress Ctrl+C to shutdown")

	// Create server
	srv := server.NewServer(addr)

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

		fmt.Println("Server stopped gracefully")
	}

	return nil
}
