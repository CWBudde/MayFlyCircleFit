package cmd

import (
	"errors"
	"strings"
)

// UsageError marks a failure caused by how the command was invoked rather than
// by the work it tried to do. The entry point maps it to exit status 2 so
// scripts can tell a misspelled flag from a failed optimization run.
type UsageError struct {
	err error
}

// NewUsageError wraps err as an invocation error. It returns nil for a nil
// error so that `return NewUsageError(validate())` cannot turn success into a
// failure with exit status 2.
func NewUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &UsageError{err: err}
}

func (e *UsageError) Error() string {
	if e == nil || e.err == nil {
		return "usage error"
	}
	return e.err.Error()
}

func (e *UsageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsUsageError reports whether err was caused by the invocation rather than by
// the command's work.
func IsUsageError(err error) bool {
	var usage *UsageError
	return errors.As(err, &usage)
}

// cobraUsagePrefixes are the argument-validation messages Cobra builds itself.
// Flag parsing errors are typed by SetFlagErrorFunc, and command handlers
// report their own invocation problems through NewUsageError, so this string
// match only has to cover what Cobra reports without a distinguishable type.
//
// The match is anchored at the start of the message, because these phrases also
// occur inside failures that are not invocation errors at all: a syscall EINVAL
// renders as "invalid argument", and it must not turn into exit status 2.
var cobraUsagePrefixes = []string{
	"unknown command",
	"unknown flag",
	"unknown shorthand flag",
	"accepts ",
	"requires at least",
	"requires at most",
	"requires exactly",
	"invalid argument",
	"subcommand is required",
}

// classifyExecuteError tags Cobra's own invocation failures as usage errors.
func classifyExecuteError(err error) error {
	if err == nil || IsUsageError(err) {
		return err
	}
	message := strings.ToLower(err.Error())
	for _, prefix := range cobraUsagePrefixes {
		if strings.HasPrefix(message, prefix) {
			return NewUsageError(err)
		}
	}
	return err
}
