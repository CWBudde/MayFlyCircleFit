package cmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRequestCLIReportsARefusedConnection covers the most common network
// failure: the CLI was pointed at an address where no server is listening.
//
// The error has to reach the entry point as a *url.Error wrapping
// ECONNREFUSED, because that is what turns into the "start one with
// mayflycirclefit serve" suggestion rather than a bare transport dump.
func TestRequestCLIReportsARefusedConnection(t *testing.T) {
	// Bind and immediately release a port so the address is well-formed and
	// routable but has nothing behind it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}

	endpoint := "http://" + listener.Addr().String() + "/api/v1/jobs"
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}

	_, err = requestCLI(context.Background(), http.MethodGet, endpoint)
	if err == nil {
		t.Fatal("requestCLI() = nil, want an error against a closed port")
	}

	if !strings.Contains(err.Error(), "connect to server") {
		t.Fatalf("requestCLI() error = %v, want it to say it could not connect", err)
	}

	var urlError *url.Error
	if !errors.As(err, &urlError) {
		t.Fatalf("requestCLI() error = %v, want a *url.Error in the chain", err)
	}

	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("requestCLI() error = %v, want it to wrap ECONNREFUSED", err)
	}
}

// TestRequestCLIReportsATruncatedResponse covers a connection that dies while
// the body is being read, which is what a server crash or a dropped link looks
// like from here. A partial body must not be decoded as if it were complete.
func TestRequestCLIReportsATruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write([]byte(`[{"jobId":"abc"`)); err != nil {
			return
		}
		// Hijack and close hard so the client sees a short body rather than a
		// clean EOF.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}

		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}

		_ = conn.Close()
	}))
	defer server.Close()

	_, err := requestCLI(context.Background(), http.MethodGet, server.URL+"/api/v1/jobs")
	if err == nil {
		t.Fatal("requestCLI() = nil, want an error for a body that ended early")
	}

	if !strings.Contains(err.Error(), "read server response") && !strings.Contains(err.Error(), "connect to server") {
		t.Fatalf("requestCLI() error = %v, want a read or transport failure", err)
	}
}

// TestRequestCLIHonoursADeadline covers a server that accepts the connection
// and then never answers. The CLI must give up on its own rather than hang.
func TestRequestCLIHonoursADeadline(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))

	defer func() {
		close(blocked)
		server.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := requestCLI(ctx, http.MethodGet, server.URL+"/api/v1/jobs")
	if err == nil {
		t.Fatal("requestCLI() = nil, want a deadline failure")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("requestCLI() error = %v, want context.DeadlineExceeded", err)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("requestCLI() took %v, want it to give up at the deadline", elapsed)
	}
}
