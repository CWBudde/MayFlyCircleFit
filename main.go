package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cwbudde/mayflycirclefit/cmd"
)

// Exit statuses: 0 on success, 1 when a command failed at its work, and 2 when
// the command was invoked incorrectly.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	err := cmd.Execute()
	code := exitCode(err)
	if code != exitOK {
		printCLIError(os.Stderr, code, err)
	}
	os.Exit(code)
}

// exitCode maps a command error to the process exit status.
func exitCode(err error) int {
	switch {
	case err == nil:
		return exitOK
	case cmd.IsUsageError(err):
		return exitUsage
	default:
		return exitError
	}
}

// printCLIError writes a single user-facing report for err, adding a concrete
// next step when the failure names one.
func printCLIError(writer io.Writer, exitCode int, err error) {
	if err == nil {
		return
	}
	if exitCode == exitUsage {
		fmt.Fprintf(writer, "Usage error: %v\n", err)
		fmt.Fprintf(writer, "Tip: run `mayflycirclefit --help` to see available commands.\n")
		return
	}

	fmt.Fprintf(writer, "Error: %v\n", err)
	if suggestion := suggestFix(err); suggestion != "" {
		fmt.Fprintf(writer, "Suggestion: %s\n", suggestion)
	}
}

// suggestFix returns a remedy for the failures a user can act on directly, or
// an empty string when there is nothing specific to add.
func suggestFix(err error) string {
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		return ""
	}
	switch {
	case errors.Is(pathError.Err, os.ErrNotExist):
		return fmt.Sprintf("check that %q exists and is readable.", pathError.Path)
	case errors.Is(pathError.Err, os.ErrPermission):
		return fmt.Sprintf("check file permissions for %q.", pathError.Path)
	default:
		return ""
	}
}
