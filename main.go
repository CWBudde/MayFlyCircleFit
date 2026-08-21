package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"syscall"

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
//
// The two classes worth naming are the ones whose message alone does not say
// what to do: a filesystem error names a path but not why it is unwritable,
// and a transport error names a URL but not that no server is listening on it.
func suggestFix(err error) string {
	if suggestion := suggestFilesystemFix(err); suggestion != "" {
		return suggestion
	}
	return suggestNetworkFix(err)
}

// suggestFilesystemFix covers the errors a run hits while reading its inputs or
// writing its artifacts. A full or read-only filesystem is worth calling out
// separately from a missing file: the path in the message is correct, so
// "no space left on device" otherwise reads as a problem with the path itself.
func suggestFilesystemFix(err error) string {
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		return ""
	}
	switch {
	case errors.Is(pathError.Err, os.ErrNotExist):
		return fmt.Sprintf("check that %q exists and is readable.", pathError.Path)
	case errors.Is(pathError.Err, os.ErrPermission):
		return fmt.Sprintf("check file permissions for %q.", pathError.Path)
	case errors.Is(pathError.Err, syscall.ENOSPC), errors.Is(pathError.Err, syscall.EDQUOT):
		return fmt.Sprintf("the filesystem holding %q is full or over quota; free space or write elsewhere. Any artifact already written there may be truncated.", pathError.Path)
	case errors.Is(pathError.Err, syscall.EROFS):
		return fmt.Sprintf("the filesystem holding %q is mounted read-only; choose a writable output path.", pathError.Path)
	default:
		return ""
	}
}

// suggestNetworkFix covers the client commands that talk to a running server.
// A refused connection is the common case and means the server is not up,
// which is not something the transport error says. A timeout says less than it
// looks like it does: url.Error.Timeout reports a deadline hit anywhere from
// DNS through the response body, so the remedy names both the address and the
// server rather than asserting that a server accepted the connection.
func suggestNetworkFix(err error) string {
	var urlError *url.Error
	if !errors.As(err, &urlError) {
		return ""
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "no server is listening there; start one with `mayflycirclefit serve`, or point the server flag (--server for `status`, --server-url for `resume`) at the right address."
	case urlError.Timeout():
		return "the request timed out while contacting the server or reading its response; check that the address is right and that the server is running and not blocked on a long request."
	default:
		return "check that the server address is reachable and that `mayflycirclefit serve` is running."
	}
}
