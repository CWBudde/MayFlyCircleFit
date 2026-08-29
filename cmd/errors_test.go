package cmd

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestIsUsageError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain", err: errors.New("boom"), want: false},
		{name: "usage", err: NewUsageError(errors.New("bad flag")), want: true},
		{name: "wrapped usage", err: fmt.Errorf("run: %w", NewUsageError(errors.New("bad flag"))), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := IsUsageError(test.err); got != test.want {
				t.Fatalf("IsUsageError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestNewUsageErrorPassesNilThrough(t *testing.T) {
	t.Parallel()

	err := NewUsageError(nil)
	if err != nil {
		t.Fatalf("NewUsageError(nil) = %v, want nil", err)
	}
}

func TestUsageErrorUnwrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("unknown flag: --nope")

	err := NewUsageError(cause)
	if err == nil {
		t.Fatal("NewUsageError returned nil for a non-nil cause")
	}

	if !errors.Is(err, cause) {
		t.Fatalf("NewUsageError does not unwrap to its cause")
	}

	if err.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want %q", err.Error(), cause.Error())
	}
}

func TestClassifyExecuteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unknown command", err: errors.New(`unknown command "renderr" for "circlefit"`), want: true},
		{name: "arg count", err: errors.New("accepts 1 arg(s), received 3"), want: true},
		{name: "minimum args", err: errors.New("requires at least 1 arg(s), only received 0"), want: true},
		{name: "already typed", err: NewUsageError(errors.New("unknown flag")), want: true},
		{name: "work failure", err: errors.New("failed to load reference image"), want: false},
		{name: "phrase inside a work failure", err: errors.New("write out.png: invalid argument"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := IsUsageError(classifyExecuteError(test.err)); got != test.want {
				t.Fatalf("classifyExecuteError(%v) usage = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

// TestExecuteClassifiesRealInvocations drives the actual root command so the
// classification is checked against the errors Cobra and pflag really produce.
//
//nolint:paralleltest // mutates the package-level root command and its flag targets, shared by every test here.
func TestExecuteClassifiesRealInvocations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "unknown flag", args: []string{"--definitely-not-a-flag"}, want: true},
		{name: "unknown shorthand flag", args: []string{"-Z"}, want: true},
		{name: "flag needs an argument", args: []string{"--log-level"}, want: true},
		{name: "unknown command", args: []string{"renderr"}, want: true},
		{name: "invalid log level", args: []string{"status", "--log-level", "chatty"}, want: true},
		{name: "out of range max-jobs", args: []string{"serve", "--max-jobs", "0"}, want: true},
		{name: "out of range queue-size", args: []string{"serve", "--queue-size", "0"}, want: true},
		{name: "unknown backend", args: []string{"serve", "--backend", "definitely-not-a-backend"}, want: true},
	}
	//nolint:paralleltest // mutates the package-level root command and its flag targets, shared by every test here.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Cleanup(resetRootCommand(t))
			rootCmd.SetArgs(test.args)

			err := Execute()
			if err == nil {
				t.Fatalf("Execute(%v) succeeded, want an error", test.args)
			}

			if got := IsUsageError(err); got != test.want {
				t.Fatalf("Execute(%v) = %v; usage = %v, want %v", test.args, err, got, test.want)
			}
		})
	}
}

// resetRootCommand keeps the shared root command usable by later tests.
func resetRootCommand(t *testing.T) func() {
	t.Helper()
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	// Flag targets are package-level variables that pflag writes in place, so a
	// parsed invocation would otherwise leak into every later test.
	previousLevel := logLevel
	previousMaxJobs := serveMaxJobs
	previousQueueSize := serveQueueSize
	previousBackend := serveBackend

	return func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)

		logLevel = previousLevel
		serveMaxJobs = previousMaxJobs
		serveQueueSize = previousQueueSize
		serveBackend = previousBackend
	}
}
