package opt

import (
	"errors"
	"fmt"
	"strings"
)

// ErrOptimizerVersionMismatch reports that a checkpoint was written by an
// optimizer version other than the one compiled into this binary.
//
// The optimizer version is a comparability boundary rather than a cosmetic
// detail: v0.5.0 scales the crossover offspring count with the population where
// v0.4.0 held it at an absolute 20, and v0.5.1 restores blend crossover so
// offspring may land outside the interval their parents span. Continuing a run
// across such a boundary produces a cost that cannot be compared with the one
// the checkpoint recorded, and nothing downstream would say so.
var ErrOptimizerVersionMismatch = errors.New("optimizer version mismatch")

// GuardCheckpointVersion decides whether a checkpoint that recorded optimizer
// version recorded may be resumed by a build linking optimizer version running.
//
// The running version is a parameter rather than a LibraryVersion call so the
// decision stays a pure function: a test binary carries no module information,
// so a guard that read the version itself could only ever exercise its unknown
// branch.
//
// It returns at most one of two answers:
//
//   - a non-empty warning with a nil error: the resume proceeds, but the caller
//     must surface the warning. This covers a legacy checkpoint that predates
//     the field, a build without module information on either side, and an
//     explicitly overridden mismatch.
//   - a non-nil error wrapping ErrOptimizerVersionMismatch: the resume must be
//     refused. The message names both versions so the operator can decide
//     between re-baselining and overriding.
//
// A checkpoint whose version is absent is never refused. Every checkpoint
// written before this guard existed is in that state, so refusing them would
// strand every campaign already on disk.
func GuardCheckpointVersion(recorded, running string, allowMismatch bool) (string, error) {
	recorded = strings.TrimSpace(recorded)
	running = strings.TrimSpace(running)

	if running == "" {
		running = unknownLibraryVersion
	}

	switch {
	case recorded == "" || recorded == unknownLibraryVersion:
		return fmt.Sprintf(
			"checkpoint records no optimizer version; resuming it under MayFly %s, whose costs may not be comparable with the recorded ones",
			running,
		), nil
	case running == unknownLibraryVersion:
		return fmt.Sprintf(
			"this build reports no optimizer version, so a checkpoint written by MayFly %s cannot be verified against it",
			recorded,
		), nil
	case recorded == running:
		return "", nil
	case allowMismatch:
		return fmt.Sprintf(
			"resuming a checkpoint written by MayFly %s under MayFly %s; the resulting cost is not comparable with the recorded one",
			recorded, running,
		), nil
	default:
		return "", fmt.Errorf(
			"%w: checkpoint was written by MayFly %s but this build links MayFly %s, and costs across that boundary are not comparable; re-baseline the run, or override the check to resume anyway",
			ErrOptimizerVersionMismatch, recorded, running,
		)
	}
}
