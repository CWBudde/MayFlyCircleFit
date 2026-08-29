package store

import (
	"errors"
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
	return validateIdentifier(jobID)
}

// validateScheduleID applies the job-ID rule to a schedule. Schedules are keyed
// independently of jobs but live in the same tree, so the identifier has to
// meet the same containment guarantee.
func validateScheduleID(scheduleID string) error {
	return validateIdentifier(scheduleID)
}

func validateIdentifier(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return errors.New("must be a canonical non-zero UUID")
	}

	return nil
}

// validatePathSegment rejects anything that is not a single, literal directory
// name. It is a syntactic check only and carries no application meaning.
func validatePathSegment(name string) error {
	if name == "" {
		return errors.New("path segment cannot be empty")
	}

	if name == "." || name == ".." {
		return fmt.Errorf("path segment %q is not a directory name", name)
	}

	if strings.ContainsRune(name, 0) {
		return errors.New("path segment contains a NUL byte")
	}

	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("path segment %q must not contain a separator", name)
	}

	return nil
}

// EnsureSecureSubdir creates exactly one directory level beneath parent and
// returns its path. It refuses separators, traversal segments, and symlinks, so
// callers never have to join caller-supplied names into a path themselves.
func EnsureSecureSubdir(parent, name string) (string, error) {
	err := validatePathSegment(name)
	if err != nil {
		return "", err
	}

	root, err := canonicalRoot(parent)
	if err != nil {
		return "", err
	}

	path := filepath.Join(root, name)

	err = ensureSecureDir(root, path)
	if err != nil {
		return "", err
	}

	return path, nil
}

func canonicalRoot(baseDir string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", errors.New("base directory cannot be empty")
	}

	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base directory: %w", err)
	}

	err = os.MkdirAll(abs, directoryMode)
	if err != nil {
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
		return "", errors.New("base path is not a directory")
	}

	err = os.Chmod(resolved, directoryMode)
	if err != nil {
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
		return errors.New("path escapes store root")
	}

	return nil
}

// ensureSecureDir creates exactly one directory level and refuses symlinks.
// Parents are created separately so every component below the canonical root
// is checked before it is used.
func ensureSecureDir(root, path string) error {
	err := ensureContained(root, path)
	if err != nil {
		return err
	}

	err = os.Mkdir(path, directoryMode)
	if err != nil && !os.IsExist(err) {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink path %q", path)
	}

	err = os.Chmod(path, directoryMode)
	if err != nil {
		return err
	}

	return nil
}
