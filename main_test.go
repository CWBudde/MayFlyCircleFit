package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/cwbudde/circlefit/cmd"
)

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "success", err: nil, want: exitOK},
		{name: "work failure", err: errors.New("optimizer diverged"), want: exitError},
		{name: "usage failure", err: cmd.NewUsageError(errors.New("unknown flag: --nope")), want: exitUsage},
		{name: "wrapped usage failure", err: fmt.Errorf("run: %w", cmd.NewUsageError(errors.New("bad flag"))), want: exitUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := exitCode(test.err); got != test.want {
				t.Fatalf("exitCode(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

func TestPrintCLIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    int
		err     error
		want    []string
		notWant []string
	}{
		{
			name: "usage error names the help command",
			code: exitUsage,
			err:  cmd.NewUsageError(errors.New("unknown flag: --nope")),
			want: []string{"Usage error: unknown flag: --nope", "--help"},
		},
		{
			name: "missing file suggests checking the path",
			code: exitError,
			err:  &os.PathError{Op: "open", Path: "assets/missing.png", Err: fs.ErrNotExist},
			want: []string{"Error: open assets/missing.png", `Suggestion: check that "assets/missing.png" exists`},
		},
		{
			name: "permission denied suggests checking permissions",
			code: exitError,
			err:  fmt.Errorf("load reference: %w", &os.PathError{Op: "open", Path: "/root/ref.png", Err: fs.ErrPermission}),
			want: []string{"Error: load reference: open /root/ref.png", `Suggestion: check file permissions for "/root/ref.png"`},
		},
		{
			name: "full filesystem suggests freeing space and warns about truncation",
			code: exitError,
			err:  fmt.Errorf("failed to write output: %w", &os.PathError{Op: "write", Path: "out.png", Err: syscall.ENOSPC}),
			want: []string{
				"Error: failed to write output: write out.png",
				`Suggestion: the filesystem holding "out.png" is full`,
				"may be truncated",
			},
		},
		{
			name: "over quota is treated like a full filesystem",
			code: exitError,
			err:  &os.PathError{Op: "write", Path: "/data/checkpoint.json", Err: syscall.EDQUOT},
			want: []string{`Suggestion: the filesystem holding "/data/checkpoint.json" is full or over quota`},
		},
		{
			name: "read-only filesystem suggests a writable path",
			code: exitError,
			err:  &os.PathError{Op: "open", Path: "/mnt/ro/out.png", Err: syscall.EROFS},
			want: []string{`Suggestion: the filesystem holding "/mnt/ro/out.png" is mounted read-only`},
		},
		{
			name: "refused connection suggests starting the server",
			code: exitError,
			err: fmt.Errorf("connect to server: %w", &url.Error{
				Op:  "Get",
				URL: "http://localhost:8080/api/v1/jobs",
				Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
			}),
			want: []string{"Suggestion: no server is listening there", "circlefit serve"},
		},
		{
			name: "timed out request stays neutral about where the deadline was hit",
			code: exitError,
			err: &url.Error{
				Op:  "Get",
				URL: "http://localhost:8080/api/v1/jobs",
				Err: timeoutError{},
			},
			want:    []string{"Suggestion: the request timed out while contacting the server or reading its response"},
			notWant: []string{"no server is listening"},
		},
		{
			name: "other transport errors suggest checking reachability",
			code: exitError,
			err: &url.Error{
				Op:  "Get",
				URL: "http://nowhere.invalid/api/v1/jobs",
				Err: errors.New("no such host"),
			},
			want: []string{"Suggestion: check that the server address is reachable"},
		},
		{
			name:    "other path errors are reported exactly once",
			code:    exitError,
			err:     &os.PathError{Op: "read", Path: "out.png", Err: errors.New("input/output error")},
			want:    []string{"Error: read out.png: input/output error"},
			notWant: []string{"Suggestion:"},
		},
		{
			name:    "plain error carries no suggestion",
			code:    exitError,
			err:     errors.New("optimizer diverged"),
			want:    []string{"Error: optimizer diverged"},
			notWant: []string{"Suggestion:", "Usage error:"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			printCLIError(&out, test.code, test.err)

			got := out.String()
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("output %q does not contain %q", got, want)
				}
			}

			for _, notWant := range test.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("output %q unexpectedly contains %q", got, notWant)
				}
			}
			// "Error:" and "Usage error:" both end in this suffix, so a
			// single match proves the fall-through does not report twice.
			if count := strings.Count(got, "rror: "); count != 1 {
				t.Errorf("output %q reports the error %d times, want exactly 1", got, count)
			}
		})
	}
}

func TestPrintCLIErrorIgnoresNil(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printCLIError(&out, exitOK, nil)

	if out.Len() != 0 {
		t.Fatalf("printCLIError wrote %q for a nil error", out.String())
	}
}

// timeoutError is a net.Error whose Timeout reports true, so the suggestion for
// a timed-out request can be exercised without waiting for a real one.
type timeoutError struct{}

func (timeoutError) Error() string   { return "context deadline exceeded (Client.Timeout exceeded)" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
