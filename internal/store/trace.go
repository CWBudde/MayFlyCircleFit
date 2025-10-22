package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TraceEntry represents a single entry in the cost history trace.
// Each entry is serialized as a JSON line in trace.jsonl.
type TraceEntry struct {
	// Iteration is the optimization iteration number
	Iteration int `json:"iteration"`

	// Cost is the best cost at this iteration
	Cost float64 `json:"cost"`

	// Timestamp records when this trace entry was created
	Timestamp time.Time `json:"timestamp"`

	// Params are the circle parameters (optional, can be nil to save space)
	// If included, this should be the best parameters at this iteration
	Params []float64 `json:"params,omitempty"`
}

// TraceWriter writes trace entries to a JSONL file.
// It uses buffered I/O for performance and is safe for concurrent use.
type TraceWriter struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	path   string
}

// NewTraceWriter creates a new trace writer for the given job.
// The trace file is created at <baseDir>/jobs/<jobID>/trace.jsonl.
// If append is true, new entries are appended to existing file.
func NewTraceWriter(baseDir, jobID string, append bool) (*TraceWriter, error) {
	jobDir := filepath.Join(baseDir, "jobs", jobID)

	// Ensure job directory exists
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create job directory: %w", err)
	}

	path := filepath.Join(jobDir, "trace.jsonl")

	// Open file in append or create mode
	var file *os.File
	var err error
	if append {
		file, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create(path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file: %w", err)
	}

	writer := bufio.NewWriterSize(file, 64*1024) // 64KB buffer

	return &TraceWriter{
		file:   file,
		writer: writer,
		path:   path,
	}, nil
}

// Write appends a trace entry to the file.
// The entry is buffered and will be written on Flush() or Close().
func (tw *TraceWriter) Write(entry TraceEntry) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	// Serialize to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal trace entry: %w", err)
	}

	// Write JSON line
	if _, err := tw.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write trace entry: %w", err)
	}

	// Write newline
	if err := tw.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	return nil
}

// Flush writes any buffered data to the file.
func (tw *TraceWriter) Flush() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if err := tw.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush trace writer: %w", err)
	}

	// Also sync to disk for durability
	if err := tw.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync trace file: %w", err)
	}

	return nil
}

// Close flushes buffered data and closes the trace file.
func (tw *TraceWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	// Flush buffer first
	if err := tw.writer.Flush(); err != nil {
		tw.file.Close() // Try to close anyway
		return fmt.Errorf("failed to flush on close: %w", err)
	}

	// Close file
	if err := tw.file.Close(); err != nil {
		return fmt.Errorf("failed to close trace file: %w", err)
	}

	return nil
}

// Path returns the filesystem path to the trace file.
func (tw *TraceWriter) Path() string {
	return tw.path
}

// TraceReader reads trace entries from a JSONL file.
type TraceReader struct {
	file    *os.File
	scanner *bufio.Scanner
}

// NewTraceReader creates a new trace reader for the given job.
func NewTraceReader(baseDir, jobID string) (*TraceReader, error) {
	path := filepath.Join(baseDir, "jobs", jobID, "trace.jsonl")

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &NotFoundError{JobID: jobID}
		}
		return nil, fmt.Errorf("failed to open trace file: %w", err)
	}

	scanner := bufio.NewScanner(file)
	// Set larger buffer for long lines (if params are included)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 64KB initial, 1MB max

	return &TraceReader{
		file:    file,
		scanner: scanner,
	}, nil
}

// Read reads the next trace entry from the file.
// Returns io.EOF when no more entries are available.
func (tr *TraceReader) Read() (*TraceEntry, error) {
	if !tr.scanner.Scan() {
		// Check for error or EOF
		if err := tr.scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to scan trace line: %w", err)
		}
		return nil, io.EOF
	}

	line := tr.scanner.Bytes()
	var entry TraceEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal trace entry: %w", err)
	}

	return &entry, nil
}

// ReadAll reads all trace entries from the file.
func (tr *TraceReader) ReadAll() ([]TraceEntry, error) {
	var entries []TraceEntry

	for {
		entry, err := tr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, *entry)
	}

	return entries, nil
}

// Close closes the trace reader.
func (tr *TraceReader) Close() error {
	if err := tr.file.Close(); err != nil {
		return fmt.Errorf("failed to close trace file: %w", err)
	}
	return nil
}

// DeleteTrace removes the trace file for the given job.
// Returns nil if the file doesn't exist.
func DeleteTrace(baseDir, jobID string) error {
	path := filepath.Join(baseDir, "jobs", jobID, "trace.jsonl")

	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete trace file: %w", err)
	}

	return nil
}
