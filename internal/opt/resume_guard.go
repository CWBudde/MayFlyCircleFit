package opt

import (
	"errors"
	"fmt"
	"strings"
)

// ErrOptimizerVersionMismatch reports that a checkpoint was written by an
// optimizer version other than the one compiled into this binary. The guard is
// library-neutral: MayFly, Dragonfly, and CMA-ES all use the same exact-version
// rule, with only measured behaviour-neutral pairs admitted below.
//
// The optimizer version is a comparability boundary rather than a cosmetic
// detail: v0.5.0 scales the crossover offspring count with the population where
// v0.4.0 held it at an absolute 20, and v0.5.1 restores blend crossover so
// offspring may land outside the interval their parents span. Continuing a run
// across such a boundary produces a cost that cannot be compared with the one
// the checkpoint recorded, and nothing downstream would say so.
var ErrOptimizerVersionMismatch = errors.New("optimizer version mismatch")

// interchangeableVersion names an ordered-insensitive pair of versions of one
// optimizer library that produce identical results, so a checkpoint written by
// either may be resumed by a build linking the other.
type interchangeableVersion struct {
	library string
	first   string
	second  string
}

// interchangeableVersions returns an explicit allowlist, never a semver rule:
// only a pair that has been measured to be bit-identical belongs here. MayFly
// v0.7.1 is a lint and readability release over v0.7.0, verified directly --
// standard MA on a sphere at seed 4242 returns bit-identical costs under both, for
// uniform, sobol, and halton initialization, at 10 and 56 dimensions. Matching
// on a version prefix or a minor number instead would silently admit the next
// release, which has no such guarantee.
//
// The CMA-ES pair is the same situation reached from the other direction: this
// repository pinned the library by pseudo-version before it carried any tag,
// and v0.1.0 is that code plus a benchmark function suite, a demo, and the
// version constant itself. No file on the search path differs, and Rosenbrock
// at seeds 4242 and 7 returns bit-identical costs, iteration counts, and
// evaluation counts under both, in full and separable mode, at 5 and 14
// dimensions. Without the pair, every CMA-ES checkpoint written before the tag
// would be refused by a build linking it.
func interchangeableVersions() []interchangeableVersion {
	return []interchangeableVersion{
		{library: "MayFly", first: "v0.7.0", second: "v0.7.1"},
		{
			library: "CMA-ES",
			first:   "v0.0.0-20260825143954-e528faf326bf",
			second:  "v0.1.0",
		},
	}
}

// versionsInterchangeable reports whether recorded and running name a pair this
// build knows to be behaviour-neutral for the named library.
func versionsInterchangeable(library, recorded, running string) bool {
	for _, pair := range interchangeableVersions() {
		if pair.library != library {
			continue
		}

		if (recorded == pair.first && running == pair.second) ||
			(recorded == pair.second && running == pair.first) {
			return true
		}
	}

	return false
}

// GuardCheckpointVersion decides whether a checkpoint that recorded optimizer
// version recorded may be resumed by a build linking optimizer version running.
//
// library names the optimizer both versions belong to, so a checkpoint written
// by Dragonfly or CMA-ES is not described as a MayFly one. An empty name reads
// as MayFly, which is what every caller predating a second engine meant.
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
// A pair of versions listed in interchangeableVersions is treated exactly like
// an exact match: those releases are measured to be behaviour-neutral against
// each other, so refusing the resume would strand in-flight jobs for nothing.
//
// A checkpoint whose version is absent is never refused. Every checkpoint
// written before this guard existed is in that state, so refusing them would
// strand every campaign already on disk.
func GuardCheckpointVersion(library, recorded, running string, allowMismatch bool) (string, error) {
	recorded = strings.TrimSpace(recorded)
	running = strings.TrimSpace(running)

	library = strings.TrimSpace(library)
	if library == "" {
		library = "MayFly"
	}

	if running == "" {
		running = unknownLibraryVersion
	}

	switch {
	case recorded == "" || recorded == unknownLibraryVersion:
		return fmt.Sprintf(
			"checkpoint records no optimizer version; resuming it under %s %s, "+
				"whose costs may not be comparable with the recorded ones",
			library, running,
		), nil
	case running == unknownLibraryVersion:
		return fmt.Sprintf(
			"this build reports no optimizer version, so a checkpoint written by %s %s cannot be verified against it",
			library, recorded,
		), nil
	case recorded == running:
		return "", nil
	case versionsInterchangeable(library, recorded, running):
		return "", nil
	case allowMismatch:
		return fmt.Sprintf(
			"resuming a checkpoint written by %s %s under %s %s; the resulting cost is not comparable with the recorded one",
			library, recorded, library, running,
		), nil
	default:
		return "", fmt.Errorf(
			"%w: checkpoint was written by %s %s but this build links %s %s, "+
				"and costs across that boundary are not comparable; "+
				"re-baseline the run, or override the check to resume anyway",
			ErrOptimizerVersionMismatch, library, recorded, library, running,
		)
	}
}
