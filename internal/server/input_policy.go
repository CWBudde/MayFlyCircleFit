package server

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/cwbudde/circlefit/internal/app"
)

type inputPolicy struct {
	roots []string
}

func newInputPolicy(roots []string) (*inputPolicy, error) {
	if len(roots) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("determine input root: %w", err)
		}

		roots = []string{cwd}
	}

	policy := &inputPolicy{roots: make([]string, 0, len(roots))}
	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve input root: %w", err)
		}

		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, fmt.Errorf("resolve input root symlinks: %w", err)
		}

		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil, errors.New("input root is not a directory")
		}

		policy.roots = append(policy.roots, filepath.Clean(canonical))
	}

	return policy, nil
}

func (p *inputPolicy) resolveImage(path string) (string, error) {
	if path == "" {
		return "", errors.New("image path is required")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("invalid image path")
	}

	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errors.New("image does not exist or is inaccessible")
	}

	canonical = filepath.Clean(canonical)
	if !p.contains(canonical) {
		return "", errors.New("image path is outside configured input roots")
	}

	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("image path is not a regular file")
	}

	if info.Size() > app.MaxImageFileSize {
		return "", errors.New("image file exceeds the size limit")
	}

	file, err := os.Open(canonical)
	if err != nil {
		return "", errors.New("image is inaccessible")
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return "", errors.New("image format is invalid")
	}

	if err := app.ValidateImageDimensions(config.Width, config.Height); err != nil {
		return "", err
	}

	return canonical, nil
}

func (p *inputPolicy) contains(path string) bool {
	for _, root := range p.roots {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}

		if relative == "." {
			return true
		}

		if relative == ".." || filepath.IsAbs(relative) {
			continue
		}

		if strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}

		return true
	}

	return false
}
