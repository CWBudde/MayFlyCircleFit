package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/opt"
)

func TestTraceWriter_WriteAndRead(t *testing.T) {
	t.Parallel()

	// Create temp directory
	tmpDir := t.TempDir()

	jobID := testJobID(1)

	// Create trace writer
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	// Write some entries
	psnr, ssim := 32.5, 0.91
	entries := []TraceEntry{
		{Iteration: 0, Cost: 1.0, Timestamp: time.Now()},
		{Iteration: 10, Cost: 0.8, PSNR: &psnr, SSIM: &ssim, Timestamp: time.Now()},
		{Iteration: 15, Cost: 0, PSNRInfinite: true, Timestamp: time.Now()},
		{Iteration: 20, Cost: 0.6, Timestamp: time.Now(), Params: []float64{1, 2, 3}},
		{
			OptimizerDiagnostics: &opt.SearchDiagnostics{Sigma: 0.2, ConditionNumber: 7},
			Iteration:            30, Cost: 0.4, Timestamp: time.Now(),
		},
	}

	for _, entry := range entries {
		err := writer.Write(entry)
		if err != nil {
			t.Fatalf("Failed to write entry: %v", err)
		}
	}

	// Close writer
	err = writer.Close()
	if err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Verify file exists
	tracePath := filepath.Join(tmpDir, "jobs", jobID, "trace.jsonl")

	_, err = os.Stat(tracePath)
	if os.IsNotExist(err) {
		t.Fatalf("Trace file not created: %s", tracePath)
	}

	// Read entries back
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	readEntries, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read entries: %v", err)
	}

	// Verify count
	if len(readEntries) != len(entries) {
		t.Fatalf("Expected %d entries, got %d", len(entries), len(readEntries))
	}

	if diagnostics := readEntries[4].OptimizerDiagnostics; diagnostics == nil ||
		diagnostics.Sigma != 0.2 || diagnostics.ConditionNumber != 7 {
		t.Fatalf("optimizer diagnostics = %+v, want sigma 0.2 and condition 7", diagnostics)
	}

	// Verify data
	for i, entry := range readEntries {
		if entry.Iteration != entries[i].Iteration {
			t.Errorf("Entry %d: expected iteration %d, got %d", i, entries[i].Iteration, entry.Iteration)
		}

		if entry.Cost != entries[i].Cost {
			t.Errorf("Entry %d: expected cost %f, got %f", i, entries[i].Cost, entry.Cost)
		}

		if len(entry.Params) != len(entries[i].Params) {
			t.Errorf("Entry %d: expected %d params, got %d", i, len(entries[i].Params), len(entry.Params))
		}

		if entry.PSNRInfinite != entries[i].PSNRInfinite || (entry.PSNR == nil) != (entries[i].PSNR == nil) || (entry.SSIM == nil) != (entries[i].SSIM == nil) {
			t.Errorf("Entry %d metric availability mismatch: got %+v want %+v", i, entry, entries[i])
		}

		if entry.PSNR != nil && *entry.PSNR != *entries[i].PSNR {
			t.Errorf("Entry %d PSNR = %v, want %v", i, *entry.PSNR, *entries[i].PSNR)
		}

		if entry.SSIM != nil && *entry.SSIM != *entries[i].SSIM {
			t.Errorf("Entry %d SSIM = %v, want %v", i, *entry.SSIM, *entries[i].SSIM)
		}
	}
}

func TestTraceWriter_Append(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	// Write initial entries
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	err = writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Append more entries
	writer, err = NewTraceWriter(tmpDir, jobID, true)
	if err != nil {
		t.Fatalf("Failed to create trace writer in append mode: %v", err)
	}

	err = writer.Write(TraceEntry{Iteration: 10, Cost: 0.8, Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Read all entries
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	entries, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read entries: %v", err)
	}

	// Should have both entries
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(entries))
	}

	if entries[0].Iteration != 0 {
		t.Errorf("First entry: expected iteration 0, got %d", entries[0].Iteration)
	}

	if entries[1].Iteration != 10 {
		t.Errorf("Second entry: expected iteration 10, got %d", entries[1].Iteration)
	}
}

func TestTraceWriter_Flush(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}
	defer writer.Close()

	// Write entry
	err = writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}

	// Flush
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Data should be on disk now (even without closing)
	tracePath := filepath.Join(tmpDir, "jobs", jobID, "trace.jsonl")

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	if len(data) == 0 {
		t.Error("Trace file is empty after flush")
	}
}

func TestTraceReader_ReadIteratively(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	// Write entries
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	for i := range 5 {
		err := writer.Write(TraceEntry{Iteration: i * 10, Cost: 1.0 - float64(i)*0.1, Timestamp: time.Now()})
		if err != nil {
			t.Fatalf("Failed to write entry: %v", err)
		}
	}

	writer.Close()

	// Read iteratively
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	count := 0

	for {
		entry, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("Failed to read entry: %v", err)
		}

		expectedIter := count * 10
		if entry.Iteration != expectedIter {
			t.Errorf("Entry %d: expected iteration %d, got %d", count, expectedIter, entry.Iteration)
		}

		count++
	}

	if count != 5 {
		t.Errorf("Expected to read 5 entries, got %d", count)
	}
}

func TestTraceReader_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(99)

	_, err := NewTraceReader(tmpDir, jobID)
	if err == nil {
		t.Fatal("Expected error for nonexistent trace file")
	}

	// Should be NotFoundError
	if !isNotFoundError(err) {
		t.Errorf("Expected NotFoundError, got: %v", err)
	}
}

func TestTraceWriter_WithParams(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	// Write entry with large params array
	params := make([]float64, 70) // 10 circles * 7 params
	for i := range params {
		params[i] = float64(i)
	}

	entry := TraceEntry{
		Iteration: 100,
		Cost:      0.123,
		Timestamp: time.Now(),
		Params:    params,
	}

	err = writer.Write(entry)
	if err != nil {
		t.Fatalf("Failed to write entry with params: %v", err)
	}

	writer.Close()

	// Read back
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	readEntry, err := reader.Read()
	if err != nil {
		t.Fatalf("Failed to read entry: %v", err)
	}

	if len(readEntry.Params) != len(params) {
		t.Fatalf("Expected %d params, got %d", len(params), len(readEntry.Params))
	}

	for i, p := range readEntry.Params {
		if p != params[i] {
			t.Errorf("Param %d: expected %f, got %f", i, params[i], p)
		}
	}
}

func TestTraceWriter_EmptyParams(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	// Write entry without params (nil)
	entry := TraceEntry{
		Iteration: 50,
		Cost:      0.456,
		Timestamp: time.Now(),
		Params:    nil, // No params
	}

	err = writer.Write(entry)
	if err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}

	writer.Close()

	// Read back
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	readEntry, err := reader.Read()
	if err != nil {
		t.Fatalf("Failed to read entry: %v", err)
	}

	// Params should be nil or empty
	if len(readEntry.Params) > 0 {
		t.Errorf("Expected no params, got %d params", len(readEntry.Params))
	}
}

func TestDeleteTrace(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	// Create trace file
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	err = writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("Failed to write trace entry: %v", err)
	}

	writer.Close()

	// Verify file exists
	tracePath := filepath.Join(tmpDir, "jobs", jobID, "trace.jsonl")

	_, err = os.Stat(tracePath)
	if os.IsNotExist(err) {
		t.Fatal("Trace file was not created")
	}

	// Delete trace
	err = DeleteTrace(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to delete trace: %v", err)
	}

	// Verify file is gone
	_, err = os.Stat(tracePath)
	if !os.IsNotExist(err) {
		t.Error("Trace file still exists after delete")
	}
}

func TestDeleteTrace_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(99)

	// Should not error when deleting nonexistent trace
	err := DeleteTrace(tmpDir, jobID)
	if err != nil {
		t.Errorf("DeleteTrace should not error for nonexistent file, got: %v", err)
	}
}

func TestTraceWriter_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	jobID := testJobID(1)

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}
	defer writer.Close()

	// Write from multiple goroutines
	done := make(chan bool)

	for i := range 10 {
		go func(iter int) {
			entry := TraceEntry{
				Iteration: iter,
				Cost:      float64(iter),
				Timestamp: time.Now(),
			}

			err := writer.Write(entry)
			if err != nil {
				t.Errorf("Concurrent write failed: %v", err)
			}

			done <- true
		}(i)
	}

	// Wait for all writes
	for range 10 {
		<-done
	}

	writer.Flush()

	// Read back and verify we got 10 entries
	reader, err := NewTraceReader(tmpDir, jobID)
	if err != nil {
		t.Fatalf("Failed to create trace reader: %v", err)
	}
	defer reader.Close()

	entries, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read entries: %v", err)
	}

	if len(entries) != 10 {
		t.Errorf("Expected 10 entries, got %d", len(entries))
	}
}

// Helper function to check if error is NotFoundError.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	notFoundError := &NotFoundError{}
	ok := errors.As(err, &notFoundError)

	return ok
}
