package store_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
)

const (
	backendProvenanceJobID = "00000000-0000-4000-8000-000000000041"
	backendProvenanceMode  = "joint"
)

func backendProvenanceCheckpoint(t *testing.T) *store.Checkpoint {
	t.Helper()

	config := store.JobConfig{
		RefPath: "reference.png", Mode: backendProvenanceMode, Circles: 1, Iters: 100, PopSize: 30, Seed: 9,
	}

	return store.NewCheckpoint(
		backendProvenanceJobID, []float64{1, 2, 3, 0.1, 0.2, 0.3, 0.4}, 0.1, 1, 42, config,
	)
}

// TestCheckpointBackendProvenanceRoundTrips pins the two fields that say what
// actually produced a cost, as opposed to Config.Backend, which says only what
// was asked for. A GPU cost is held to a measured budget rather than the CPU's
// byte-exact contract, so a reader that cannot tell the two apart cannot tell
// whether two rows are comparable.
func TestCheckpointBackendProvenanceRoundTrips(t *testing.T) {
	t.Parallel()

	checkpoint := backendProvenanceCheckpoint(t)
	checkpoint.EffectiveBackend = app.BackendOpenCL
	checkpoint.BackendDegraded = true

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), `"effectiveBackend":"opencl"`) {
		t.Fatalf("encoded checkpoint omits the effective backend: %s", data)
	}

	if !strings.Contains(string(data), `"backendDegraded":true`) {
		t.Fatalf("encoded checkpoint omits the degradation flag: %s", data)
	}

	var restored store.Checkpoint

	err = json.Unmarshal(data, &restored)
	if err != nil {
		t.Fatal(err)
	}

	if restored.EffectiveBackend != app.BackendOpenCL {
		t.Errorf("EffectiveBackend = %q, want %q", restored.EffectiveBackend, app.BackendOpenCL)
	}

	if !restored.BackendDegraded {
		t.Error("BackendDegraded = false, want true")
	}
}

// TestCheckpointWithoutBackendProvenanceDecodesEmpty pins the compatibility
// claim the field comments make. Both fields are additive and optional, so a
// checkpoint written before they existed has to decode without them rather than
// fail, and it has to decode to empty rather than to cpu -- "nothing recorded"
// and "the CPU ran it" are different facts, and only one of them is known here.
func TestCheckpointWithoutBackendProvenanceDecodesEmpty(t *testing.T) {
	t.Parallel()

	checkpoint := backendProvenanceCheckpoint(t)

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "effectiveBackend") || strings.Contains(string(data), "backendDegraded") {
		t.Fatalf("an unset backend was encoded rather than omitted: %s", data)
	}

	var restored store.Checkpoint

	err = json.Unmarshal(data, &restored)
	if err != nil {
		t.Fatal(err)
	}

	if restored.EffectiveBackend != "" {
		t.Errorf("EffectiveBackend = %q, want empty", restored.EffectiveBackend)
	}

	if restored.BackendDegraded {
		t.Error("BackendDegraded = true, want false")
	}
}
