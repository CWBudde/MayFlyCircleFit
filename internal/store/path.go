package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	directoryMode os.FileMode = 0o700
	artifactMode  os.FileMode = 0o600
)

func validateJobID(jobID string) error {
	parsed, err := uuid.Parse(jobID)
	if err != nil || parsed == uuid.Nil || parsed.String() != jobID {
		return fmt.Errorf("must be a canonical non-zero UUID")
	}
	return nil
}

func canonicalRoot(baseDir string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("base directory cannot be empty")
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base directory: %w", err)
	}
	if err := os.MkdirAll(abs, directoryMode); err != nil {
		return "", fmt.Errorf("create base directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve base directory symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat base directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("base path is not a directory")
	}
	if err := os.Chmod(resolved, directoryMode); err != nil {
		return "", fmt.Errorf("secure base directory permissions: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func ensureContained(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve path relative to store root: %w", err)
	}
	if rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes store root")
	}
	return nil
}

// ensureSecureDir creates exactly one directory level and refuses symlinks.
// Parents are created separately so every component below the canonical root
// is checked before it is used.
func ensureSecureDir(root, path string) error {
	if err := ensureContained(root, path); err != nil {
		return err
	}
	if err := os.Mkdir(path, directoryMode); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink path %q", path)
	}
	if err := os.Chmod(path, directoryMode); err != nil {
		return err
	}
	return nil
}
