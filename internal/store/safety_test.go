package store

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFSStoreRejectsUnsafeJobIDs(t *testing.T) {
	fs, _ := setupTestStore(t)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))

	invalid := []string{
		"",
		"../escape",
		"00000000000040008000000000000001",
		"00000000-0000-4000-8000-000000000001/child",
		"00000000-0000-0000-0000-000000000000",
		"00000000-0000-4000-8000-00000000000A",
	}
	for _, jobID := range invalid {
		t.Run(fmt.Sprintf("%q", jobID), func(t *testing.T) {
			checkpoint := createTestCheckpoint(jobID)

			operations := []struct {
				name string
				run  func() error
			}{
				{"save checkpoint", func() error { return fs.SaveCheckpoint(jobID, checkpoint) }},
				{"load checkpoint", func() error { _, err := fs.LoadCheckpoint(jobID); return err }},
				{"delete checkpoint", func() error { return fs.DeleteCheckpoint(jobID) }},
				{"save snapshot", func() error { return fs.SaveCircleSnapshot(jobID, 1, img) }},
				{"save circles", func() error { return fs.SaveCircleData(jobID, nil) }},
				{"artifact path", func() error { _, err := fs.ArtifactPath(jobID, ArtifactBest); return err }},
				{"new trace writer", func() error { _, err := fs.NewTraceWriter(jobID, false); return err }},
				{"new trace reader", func() error { _, err := fs.NewTraceReader(jobID); return err }},
				{"delete trace", func() error { return fs.DeleteTrace(jobID) }},
			}
			for _, operation := range operations {
				err := operation.run()
				if err == nil {
					t.Errorf("%s accepted unsafe job ID", operation.name)
				}
			}
		})
	}
}

func TestSaveCheckpointRequiresMatchingJobID(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)

	checkpoint := createTestCheckpoint(testJobID(2))
	err := fs.SaveCheckpoint(jobID, checkpoint)
	if err == nil {
		t.Fatal("SaveCheckpoint accepted a mismatched checkpoint JobID")
	}
}

func TestConcurrentSaveSameJobIsAtomic(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)

	const writers = 64
	var wait sync.WaitGroup

	errors := make(chan error, writers)
	for i := range writers {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()

			checkpoint := createTestCheckpoint(jobID)
			checkpoint.BestCost = float64(value)

			checkpoint.Iteration = value
			errors <- fs.SaveCheckpoint(jobID, checkpoint)
		}(i)
	}

	wait.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent save failed: %v", err)
		}
	}

	checkpoint, err := fs.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("load final checkpoint: %v", err)
	}

	if checkpoint.BestCost < 0 || checkpoint.BestCost >= writers {
		t.Fatalf("final checkpoint is not one of the complete writes: %v", checkpoint.BestCost)
	}

	infos, err := fs.ListCheckpoints()
	if err != nil {
		t.Fatalf("list final checkpoint: %v", err)
	}

	if len(infos) != 1 || infos[0].BestCost != checkpoint.BestCost || infos[0].Iteration != checkpoint.Iterations {
		t.Fatalf("checkpoint summary %+v does not describe final checkpoint cost=%v iterations=%v", infos, checkpoint.BestCost, checkpoint.Iterations)
	}

	jobDir, err := fs.existingJobDir(jobID)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(jobDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("temporary file leaked after concurrent saves: %s", entry.Name())
		}
	}
}

func TestAtomicSnapshotAndCircleDataWrites(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)
	const writers = 24

	var wait sync.WaitGroup

	errors := make(chan error, writers*2)
	for i := range writers {
		wait.Add(2)
		go func(value uint8) {
			defer wait.Done()

			img := image.NewRGBA(image.Rect(0, 0, 8, 8))

			for y := range 8 {
				for x := range 8 {
					img.SetRGBA(x, y, color.RGBA{R: value, A: 255})
				}
			}

			errors <- fs.SaveCircleSnapshot(jobID, 1, img)
		}(uint8(i))
		go func(value int) {
			defer wait.Done()

			errors <- fs.SaveCircleData(jobID, []CircleData{{CircleNum: value + 1}})
		}(i)
	}

	wait.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent artifact save failed: %v", err)
		}
	}

	jobDir, err := fs.existingJobDir(jobID)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := os.Open(filepath.Join(jobDir, "snapshots", "canvas-01.png"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := png.Decode(snapshot); err != nil {
		_ = snapshot.Close()

		t.Fatalf("final snapshot is incomplete: %v", err)
	}

	_ = snapshot.Close()

	data, err := os.ReadFile(filepath.Join(jobDir, string(ArtifactCircles)))
	if err != nil {
		t.Fatal(err)
	}

	var circles []CircleData
	if err := json.Unmarshal(data, &circles); err != nil {
		t.Fatalf("final circles JSON is incomplete: %v", err)
	}

	if len(circles) != 1 || circles[0].CircleNum < 1 || circles[0].CircleNum > writers {
		t.Fatalf("final circles JSON is not one complete write: %#v", circles)
	}
}

func TestFSStoreRefusesSymlinkJobDirectory(t *testing.T) {
	fs, root := setupTestStore(t)
	outside := t.TempDir()
	jobID := testJobID(1)

	jobPath := filepath.Join(root, "jobs", jobID)
	if err := os.Symlink(outside, jobPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := fs.SaveCheckpoint(jobID, createTestCheckpoint(jobID)); err == nil {
		t.Fatal("store followed a symlink job directory")
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("store wrote outside its root: %v", entries)
	}
}

func TestFSStoreRefusesSymlinkArtifact(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)

	jobDir, err := fs.ensureJobDir(jobID)
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"jobId":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(jobDir, string(ArtifactCheckpoint))); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := fs.LoadCheckpoint(jobID); err == nil {
		t.Fatal("store followed a symlink checkpoint artifact")
	}

	if _, err := fs.ArtifactPath(jobID, ArtifactCheckpoint); err == nil {
		t.Fatal("ArtifactPath returned a symlink artifact")
	}
}

func TestStorePermissionsAndPNGArtifactAPI(t *testing.T) {
	fs, _ := setupTestStore(t)
	jobID := testJobID(1)

	checkpoint := createTestCheckpoint(jobID)
	if err := fs.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatal(err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := fs.SavePNGArtifact(jobID, ArtifactBest, img); err != nil {
		t.Fatal(err)
	}

	jobDir, err := fs.existingJobDir(jobID)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{fs.baseDir, fs.jobsDir, jobDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}

		if got := info.Mode().Perm(); got != directoryMode {
			t.Errorf("directory %s mode = %o, want %o", dir, got, directoryMode)
		}
	}

	for _, artifact := range []Artifact{ArtifactCheckpoint, ArtifactBest} {
		path, err := fs.ArtifactPath(jobID, artifact)
		if err != nil {
			t.Fatal(err)
		}

		if err := ensureContained(fs.baseDir, path); err != nil {
			t.Fatalf("artifact escaped root: %v", err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if got := info.Mode().Perm(); got != artifactMode {
			t.Errorf("artifact %s mode = %o, want %o", path, got, artifactMode)
		}
	}

	if _, err := fs.ArtifactPath(jobID, Artifact("../escape")); err == nil {
		t.Fatal("ArtifactPath accepted an arbitrary artifact name")
	}
}

func TestCheckpointSchemaV2RoundTrip(t *testing.T) {
	config := JobConfig{
		RefPath:       "reference.png",
		Mode:          "joint",
		Circles:       1,
		Iters:         100,
		PopSize:       30,
		Seed:          7,
		EffectiveSeed: 9,
		ResumeCount:   2,
	}
	checkpoint := NewCheckpoint(testJobID(1), []float64{1, 2, 3, 0.1, 0.2, 0.3, 0.4}, 0.1, 1, 42, config)
	checkpoint.Evaluations = 1260
	checkpoint.Termination = "completed"

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), `"iteration"`) {
		t.Fatalf("v2 checkpoint contains legacy iteration field: %s", data)
	}

	var restored Checkpoint
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	if restored.SchemaVersion != CheckpointSchemaVersion || restored.Iterations != 42 || restored.Iteration != 42 {
		t.Fatalf("progress metadata lost: %#v", restored)
	}

	if restored.RequestedCircles != 1 || restored.ActualCircles != 1 || restored.EffectiveSeed != 9 || restored.ResumeCount != 2 || restored.Evaluations != 1260 || restored.Termination != "completed" {
		t.Fatalf("v2 metadata lost: %#v", restored)
	}
}

func TestCheckpointMigratesMissingVersionV1(t *testing.T) {
	legacy := fmt.Sprintf(`{
		"jobId": %q,
		"bestParams": [1,2,3,0.1,0.2,0.3,0.4],
		"bestCost": 0.1,
		"initialCost": 1,
		"iteration": 5,
		"timestamp": %q,
		"config": {"refPath":"reference.png","mode":"joint","circles":1,"iters":100,"popSize":30,"seed":7}
	}`, testJobID(1), time.Now().UTC().Format(time.RFC3339Nano))

	var migrated Checkpoint
	err := json.Unmarshal([]byte(legacy), &migrated)
	if err != nil {
		t.Fatal(err)
	}

	if migrated.SchemaVersion != CheckpointSchemaVersion || migrated.Iterations != 5 || migrated.Iteration != 5 {
		t.Fatalf("legacy iteration was not migrated: %#v", migrated)
	}

	if migrated.RequestedCircles != 1 || migrated.ActualCircles != 1 || migrated.EffectiveSeed != 7 || migrated.Evaluations != 150 || migrated.Termination != TerminationLegacy {
		t.Fatalf("legacy metadata was not derived: %#v", migrated)
	}

	err := migrated.Validate()
	if err != nil {
		t.Fatalf("migrated checkpoint is invalid: %v", err)
	}
}

func TestCheckpointRejectsFutureSchema(t *testing.T) {
	data := fmt.Sprintf(`{"schemaVersion":%d,"jobId":%q}`, CheckpointSchemaVersion+1, testJobID(1))

	var checkpoint Checkpoint
	err := json.Unmarshal([]byte(data), &checkpoint)
	if err == nil {
		t.Fatal("future checkpoint schema was accepted")
	}
}
