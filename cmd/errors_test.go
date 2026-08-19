package cmd

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestIsUsageError(t *testing.T) {
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
			if got := IsUsageError(test.err); got != test.want {
				t.Fatalf("IsUsageError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestUsageErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("unknown flag: --nope")
	err := NewUsageError(cause)
	if !errors.Is(err, cause) {
		t.Fatalf("NewUsageError does not unwrap to its cause")
	}
	if err.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want %q", err.Error(), cause.Error())
	}
}

func TestClassifyExecuteError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unknown command", err: errors.New(`unknown command "renderr" for "mayflycirclefit"`), want: true},
		{name: "arg count", err: errors.New("accepts 1 arg(s), received 3"), want: true},
		{name: "minimum args", err: errors.New("requires at least 1 arg(s), only received 0"), want: true},
		{name: "already typed", err: NewUsageError(errors.New("unknown flag")), want: true},
		{name: "work failure", err: errors.New("failed to load reference image"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsUsageError(classifyExecuteError(test.err)); got != test.want {
				t.Fatalf("classifyExecuteError(%v) usage = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

// TestExecuteClassifiesRealInvocations drives the actual root command so the
// classification is checked against the errors Cobra and pflag really produce.
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
	}
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
	previousLevel := logLevel
	return func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		logLevel = previousLevel
	}
}
