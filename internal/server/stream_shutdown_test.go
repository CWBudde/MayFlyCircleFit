package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// An SSE response never completes on its own, and http.Server.Shutdown waits
// for active requests. A stream that watched only its client therefore held
// every shutdown open until the caller's timeout expired -- ten seconds each
// time a dashboard happened to be connected, which reads exactly like a server
// ignoring SIGTERM.
func TestShutdownDoesNotWaitForOpenEventStreams(t *testing.T) {
	// One case per stream the fix touches. The per-job endpoint needs a
	// registered job before it will answer with anything but 404, so the path
	// is built against the server rather than written as a constant.
	tests := []struct {
		name string
		path func(*Server) string
	}{
		{
			name: "all jobs",
			path: func(*Server) string { return "/api/v1/stream" },
		},
		{
			name: "ui events",
			path: func(*Server) string { return "/api/v1/events" },
		},
		{
			name: "single job",
			path: func(server *Server) string {
				job := server.jobManager.CreateJob(app.DefaultProject, app.JobConfig{
					RefPath: "reference.png", Mode: app.ModeBatch,
					Circles: 8, Iters: 100, PopSize: 30, Seed: 42,
				})

				return "/api/v1/jobs/" + job.ID + "/stream"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(":0", nil)
			path := tt.path(server)

			// The drain being tested belongs to the http.Server that owns the
			// connection, and Server.Shutdown skips it entirely when s.server
			// is nil. Wrapping only the handler would therefore pass whether
			// the streams cooperate or not.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}

			httpServer := &http.Server{
				Handler:           server.Handler(),
				ReadHeaderTimeout: 5 * time.Second,
			}
			server.server = httpServer

			go func() { _ = httpServer.Serve(listener) }()

			defer func() { _ = httpServer.Close() }()

			request, err := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}

			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}
			defer func() { _ = response.Body.Close() }()

			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}

			// A 200 means the handler already flushed its headers, which both
			// streams do immediately before entering their select loop. Do not
			// read from the body to synchronize: the all-jobs stream sends
			// nothing until its first 30-second ping when no job is running.
			time.Sleep(50 * time.Millisecond)

			// A budget far larger than a prompt shutdown needs and far smaller
			// than the ten seconds the bug consumed, so the test states the
			// behaviour rather than the timing of one machine.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- server.Shutdown(ctx) }()

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("shutdown returned %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown blocked on an open event stream; " +
					"the stream must end when the server context is cancelled")
			}
		})
	}
}
