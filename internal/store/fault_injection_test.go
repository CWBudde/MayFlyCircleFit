package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests inject (1) an error after partial data reaches the unique
// temporary file and (2) an error at the atomic rename boundary. They verify
// byte-for-byte preservation, temp cleanup, and a successful subsequent save.
// They intentionally do not emulate disk exhaustion, process termination, or
// filesystem/fsync failures, which require an integration fault filesystem.

func TestAtomicWriteFailurePreservesCheckpointAndRecovers(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)
	original := createTestCheckpoint(jobID)

	original.BestCost = 0.75
	if err := fs.SaveCheckpoint(jobID, original); err != nil {
		t.Fatalf("save original checkpoint: %v", err)
	}

	path, err := fs.ArtifactPath(jobID, ArtifactCheckpoint)
	if err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected temporary-file write failure")
	originalWrite := fs.atomic.write
	fs.atomic.write = func(file *os.File, _ func(io.Writer) error) error {
		if _, err := file.WriteString("{\"partial\":"); err != nil {
			return err
		}

		return injected
	}
	updated := createTestCheckpoint(jobID)
	updated.BestCost = 0.25
	err = fs.SaveCheckpoint(jobID, updated)
	fs.atomic.write = originalWrite

	if !errors.Is(err, injected) {
		t.Fatalf("SaveCheckpoint error = %v, want injected write failure", err)
	}

	assertArtifactUnchanged(t, path, before)
	assertNoAtomicTemps(t, filepath.Dir(path))

	if err := fs.SaveCheckpoint(jobID, updated); err != nil {
		t.Fatalf("save after write failure: %v", err)
	}

	recovered, err := fs.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("load recovered checkpoint: %v", err)
	}

	if recovered.BestCost != updated.BestCost {
		t.Fatalf("recovered BestCost = %v, want %v", recovered.BestCost, updated.BestCost)
	}
}

func TestAtomicRenameFailurePreservesCheckpointAndRecovers(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)
	original := createTestCheckpoint(jobID)

	original.BestCost = 0.75
	if err := fs.SaveCheckpoint(jobID, original); err != nil {
		t.Fatalf("save original checkpoint: %v", err)
	}

	path, err := fs.ArtifactPath(jobID, ArtifactCheckpoint)
	if err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected atomic rename failure")
	originalRename := fs.atomic.rename
	fs.atomic.rename = func(_, _ string) error { return injected }
	updated := createTestCheckpoint(jobID)
	updated.BestCost = 0.25
	err = fs.SaveCheckpoint(jobID, updated)
	fs.atomic.rename = originalRename

	if !errors.Is(err, injected) {
		t.Fatalf("SaveCheckpoint error = %v, want injected rename failure", err)
	}

	assertArtifactUnchanged(t, path, before)
	assertNoAtomicTemps(t, filepath.Dir(path))

	if err := fs.SaveCheckpoint(jobID, updated); err != nil {
		t.Fatalf("save after rename failure: %v", err)
	}

	recovered, err := fs.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("load recovered checkpoint: %v", err)
	}

	if recovered.BestCost != updated.BestCost {
		t.Fatalf("recovered BestCost = %v, want %v", recovered.BestCost, updated.BestCost)
	}
}

func assertArtifactUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved artifact: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatal("failed atomic replacement changed the previously committed artifact")
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(got, &checkpoint); err != nil {
		t.Fatalf("preserved artifact is invalid JSON: %v", err)
	}
}

func assertNoAtomicTemps(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("atomic temporary file leaked: %s", entry.Name())
		}
	}
}
