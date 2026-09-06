//nolint:testpackage // reads the unexported guard and the version override field
package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// runningVersion is the version this build pretends to link, so every arm below
// differs from its control in the recorded value alone.
const runningVersion = "v9.9.9"

// polishingCheckpoint is a checkpoint for a job that runs two optimizer
// libraries: a CMA-ES base stage finished by the default MayFly sweep. It is
// the ordinary shape the second version exists for.
func polishingCheckpoint(polishing string) *store.Checkpoint {
	return &store.Checkpoint{
		OptimizerVersion:          runningVersion,
		PolishingOptimizerVersion: polishing,
		Config: store.JobConfig{
			Optimizer:          app.OptimizerCMAES,
			PolishingOptimizer: app.OptimizerMayfly,
			PolishingEnabled:   true,
		},
	}
}

// TestGuardCheckpointVersionsRefusesAPolishingMismatch is the case the base
// version cannot see. Both engines are linked into the run, so an upgrade of
// the sweep's library is exactly as much of a comparability boundary as an
// upgrade of the stage's, and the resume has to be refused either way.
func TestGuardCheckpointVersionsRefusesAPolishingMismatch(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}

	_, err := server.guardCheckpointVersions(polishingCheckpoint("v0.0.1"), false)
	if !errors.Is(err, opt.ErrOptimizerVersionMismatch) {
		t.Fatalf("guardCheckpointVersions() error = %v, want an optimizer version mismatch", err)
	}

	if !strings.Contains(err.Error(), "v0.0.1") {
		t.Errorf("error %q does not name the recorded polishing version", err)
	}
}

// TestGuardCheckpointVersionsAcceptsMatchingLibraries keeps the guard from
// refusing the job it is meant to let through.
func TestGuardCheckpointVersionsAcceptsMatchingLibraries(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}

	warnings, err := server.guardCheckpointVersions(polishingCheckpoint(runningVersion), false)
	if err != nil {
		t.Fatalf("guardCheckpointVersions() error = %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

// TestGuardCheckpointVersionsWarnsOnALegacyPolishingVersion pins the additive
// contract at the guard. A checkpoint written before the field existed carries
// nothing, and refusing every one of them would strand jobs already on disk, so
// the resume proceeds and the operator is told.
func TestGuardCheckpointVersionsWarnsOnALegacyPolishingVersion(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}

	warnings, err := server.guardCheckpointVersions(polishingCheckpoint(""), false)
	if err != nil {
		t.Fatalf("guardCheckpointVersions() error = %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}

	if !strings.Contains(warnings[0], "MayFly") {
		t.Errorf("warning %q does not name the sweep's library", warnings[0])
	}
}

// TestGuardCheckpointVersionsSkipsASingleLibraryJob keeps the second reading
// off a job that never linked a second library: the base version already speaks
// for the sweep, and a legacy warning there would be pure noise.
func TestGuardCheckpointVersionsSkipsASingleLibraryJob(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}

	checkpoint := &store.Checkpoint{
		OptimizerVersion: runningVersion,
		Config: store.JobConfig{
			Optimizer:          app.OptimizerMayfly,
			PolishingOptimizer: app.OptimizerMayfly,
			PolishingEnabled:   true,
		},
	}

	warnings, err := server.guardCheckpointVersions(checkpoint, false)
	if err != nil {
		t.Fatalf("guardCheckpointVersions() error = %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

// TestGuardCheckpointVersionsHonorsTheOverride pins that the existing escape
// hatch reaches the second library too, and that it downgrades the refusal to a
// warning rather than dropping it.
func TestGuardCheckpointVersionsHonorsTheOverride(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}

	warnings, err := server.guardCheckpointVersions(polishingCheckpoint("v0.0.1"), true)
	if err != nil {
		t.Fatalf("guardCheckpointVersions() error = %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
}
