package server

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/cwbudde/mayflycirclefit/internal/app"
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
			return nil, fmt.Errorf("input root is not a directory")
		}
		policy.roots = append(policy.roots, filepath.Clean(canonical))
	}
	return policy, nil
}

func (p *inputPolicy) resolveImage(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("image path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid image path")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("image does not exist or is inaccessible")
	}
	canonical = filepath.Clean(canonical)
	if !p.contains(canonical) {
		return "", fmt.Errorf("image path is outside configured input roots")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("image path is not a regular file")
	}
	if info.Size() > app.MaxImageFileSize {
		return "", fmt.Errorf("image file exceeds the size limit")
	}
	file, err := os.Open(canonical)
	if err != nil {
		return "", fmt.Errorf("image is inaccessible")
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return "", fmt.Errorf("image format is invalid")
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
