package store

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceWriter_WriteAndRead(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	jobID := "test-job-123"

	// Create trace writer
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	// Write some entries
	entries := []TraceEntry{
		{Iteration: 0, Cost: 1.0, Timestamp: time.Now()},
		{Iteration: 10, Cost: 0.8, Timestamp: time.Now()},
		{Iteration: 20, Cost: 0.6, Timestamp: time.Now(), Params: []float64{1, 2, 3}},
		{Iteration: 30, Cost: 0.4, Timestamp: time.Now()},
	}

	for _, entry := range entries {
		if err := writer.Write(entry); err != nil {
			t.Fatalf("Failed to write entry: %v", err)
		}
	}

	// Close writer
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Verify file exists
	tracePath := filepath.Join(tmpDir, "jobs", jobID, "trace.jsonl")
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
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
	}
}

func TestTraceWriter_Append(t *testing.T) {
	tmpDir := t.TempDir()
	jobID := "test-job-append"

	// Write initial entries
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	if err := writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	// Append more entries
	writer, err = NewTraceWriter(tmpDir, jobID, true)
	if err != nil {
		t.Fatalf("Failed to create trace writer in append mode: %v", err)
	}

	if err := writer.Write(TraceEntry{Iteration: 10, Cost: 0.8, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}
	if err := writer.Close(); err != nil {
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
	tmpDir := t.TempDir()
	jobID := "test-job-flush"

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}
	defer writer.Close()

	// Write entry
	if err := writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Failed to write entry: %v", err)
	}

	// Flush
	if err := writer.Flush(); err != nil {
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
	tmpDir := t.TempDir()
	jobID := "test-job-iter"

	// Write entries
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := writer.Write(TraceEntry{Iteration: i * 10, Cost: 1.0 - float64(i)*0.1, Timestamp: time.Now()}); err != nil {
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
		if err == io.EOF {
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
	tmpDir := t.TempDir()
	jobID := "nonexistent-job"

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
	tmpDir := t.TempDir()
	jobID := "test-job-params"

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

	if err := writer.Write(entry); err != nil {
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
	tmpDir := t.TempDir()
	jobID := "test-job-no-params"

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

	if err := writer.Write(entry); err != nil {
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
	if readEntry.Params != nil && len(readEntry.Params) > 0 {
		t.Errorf("Expected no params, got %d params", len(readEntry.Params))
	}
}

func TestDeleteTrace(t *testing.T) {
	tmpDir := t.TempDir()
	jobID := "test-job-delete"

	// Create trace file
	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}
	writer.Write(TraceEntry{Iteration: 0, Cost: 1.0, Timestamp: time.Now()})
	writer.Close()

	// Verify file exists
	tracePath := filepath.Join(tmpDir, "jobs", jobID, "trace.jsonl")
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Fatal("Trace file was not created")
	}

	// Delete trace
	if err := DeleteTrace(tmpDir, jobID); err != nil {
		t.Fatalf("Failed to delete trace: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Error("Trace file still exists after delete")
	}
}

func TestDeleteTrace_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	jobID := "nonexistent-job"

	// Should not error when deleting nonexistent trace
	if err := DeleteTrace(tmpDir, jobID); err != nil {
		t.Errorf("DeleteTrace should not error for nonexistent file, got: %v", err)
	}
}

func TestTraceWriter_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	jobID := "test-job-concurrent"

	writer, err := NewTraceWriter(tmpDir, jobID, false)
	if err != nil {
		t.Fatalf("Failed to create trace writer: %v", err)
	}
	defer writer.Close()

	// Write from multiple goroutines
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(iter int) {
			entry := TraceEntry{
				Iteration: iter,
				Cost:      float64(iter),
				Timestamp: time.Now(),
			}
			if err := writer.Write(entry); err != nil {
				t.Errorf("Concurrent write failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
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

// Helper function to check if error is NotFoundError
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*NotFoundError)
	return ok
}
