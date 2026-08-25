package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// TraceEntry represents a single entry in the cost history trace.
type TraceEntry struct {
	OptimizerDiagnostics *opt.SearchDiagnostics `json:"optimizerDiagnostics,omitempty"`
	Iteration            int                    `json:"iteration"`
	Cost                 float64                `json:"cost"`
	Evaluations          int                    `json:"evaluations"`
	PSNR                 *float64               `json:"psnr"`
	PSNRInfinite         bool                   `json:"psnrInfinite,omitempty"`
	SSIM                 *float64               `json:"ssim,omitempty"`
	CPS                  float64                `json:"cps"`
	Timestamp            time.Time              `json:"timestamp"`
	Params               []float64              `json:"params,omitempty"`
}

// TraceWriter writes buffered JSONL entries and is safe for concurrent use.
type TraceWriter struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	path   string
	closed bool
}

// NewTraceWriter opens the store-owned trace for a job.
func (fs *FSStore) NewTraceWriter(jobID string, appendMode bool) (*TraceWriter, error) {
	if _, err := fs.ensureJobDir(jobID); err != nil {
		return nil, err
	}

	path, err := fs.ArtifactPath(jobID, ArtifactTrace)
	if err != nil {
		return nil, err
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendMode {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}

	file, err := os.OpenFile(path, flags, artifactMode)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}

	if err := file.Chmod(artifactMode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure trace file permissions: %w", err)
	}

	return &TraceWriter{
		file:   file,
		writer: bufio.NewWriterSize(file, 64*1024),
		path:   path,
	}, nil
}

// NewTraceWriter is the compatibility constructor for callers that still
// provide a base directory. New code should use FSStore.NewTraceWriter.
func NewTraceWriter(baseDir, jobID string, appendMode bool) (*TraceWriter, error) {
	fs, err := NewFSStore(baseDir)
	if err != nil {
		return nil, err
	}

	return fs.NewTraceWriter(jobID, appendMode)
}

func (tw *TraceWriter) Write(entry TraceEntry) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.closed {
		return errors.New("trace writer is closed")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal trace entry: %w", err)
	}

	if _, err := tw.writer.Write(data); err != nil {
		return fmt.Errorf("write trace entry: %w", err)
	}

	if err := tw.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("write trace newline: %w", err)
	}

	return nil
}

func (tw *TraceWriter) Flush() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.closed {
		return errors.New("trace writer is closed")
	}

	err := tw.writer.Flush()
	if err != nil {
		return fmt.Errorf("flush trace writer: %w", err)
	}

	err = tw.file.Sync()
	if err != nil {
		return fmt.Errorf("sync trace file: %w", err)
	}

	return nil
}

func (tw *TraceWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.closed {
		return nil
	}

	tw.closed = true

	err := tw.writer.Flush()
	if err != nil {
		_ = tw.file.Close()
		return fmt.Errorf("flush trace on close: %w", err)
	}

	err = tw.file.Sync()
	if err != nil {
		_ = tw.file.Close()
		return fmt.Errorf("sync trace on close: %w", err)
	}

	err = tw.file.Close()
	if err != nil {
		return fmt.Errorf("close trace file: %w", err)
	}

	return nil
}

func (tw *TraceWriter) Path() string { return tw.path }

type TraceReader struct {
	file    *os.File
	scanner *bufio.Scanner
}

// NewTraceReader opens the store-owned trace for a job.
func (fs *FSStore) NewTraceReader(jobID string) (*TraceReader, error) {
	if _, err := fs.existingJobDir(jobID); err != nil {
		return nil, err
	}

	path, err := fs.ArtifactPath(jobID, ArtifactTrace)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: jobID}
	}

	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	return &TraceReader{file: file, scanner: scanner}, nil
}

// NewTraceReader is the compatibility constructor for base-directory callers.
func NewTraceReader(baseDir, jobID string) (*TraceReader, error) {
	fs, err := NewFSStore(baseDir)
	if err != nil {
		return nil, err
	}

	return fs.NewTraceReader(jobID)
}

func (tr *TraceReader) Read() (*TraceEntry, error) {
	if !tr.scanner.Scan() {
		err := tr.scanner.Err()
		if err != nil {
			return nil, fmt.Errorf("scan trace line: %w", err)
		}

		return nil, io.EOF
	}

	var entry TraceEntry

	err := json.Unmarshal(tr.scanner.Bytes(), &entry)
	if err != nil {
		return nil, fmt.Errorf("unmarshal trace entry: %w", err)
	}

	return &entry, nil
}

func (tr *TraceReader) ReadAll() ([]TraceEntry, error) {
	var entries []TraceEntry

	for {
		entry, err := tr.Read()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}

		if err != nil {
			return nil, err
		}

		entries = append(entries, *entry)
	}
}

func (tr *TraceReader) Close() error {
	err := tr.file.Close()
	if err != nil {
		return fmt.Errorf("close trace file: %w", err)
	}

	return nil
}

func (fs *FSStore) DeleteTrace(jobID string) error {
	if err := validateJobID(jobID); err != nil {
		return fmt.Errorf("invalid jobID: %w", err)
	}

	path, err := fs.ArtifactPath(jobID, ArtifactTrace)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete trace file: %w", err)
	}

	return nil
}

// DeleteTrace is the compatibility helper for base-directory callers.
func DeleteTrace(baseDir, jobID string) error {
	fs, err := NewFSStore(baseDir)
	if err != nil {
		return err
	}

	return fs.DeleteTrace(jobID)
}
