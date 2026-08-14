package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLayoutIncludesThemeSwitcher(t *testing.T) {
	var output bytes.Buffer
	if err := Layout("Theme test").Render(context.Background(), &output); err != nil {
		t.Fatalf("render layout: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		`@media (prefers-color-scheme: dark)`,
		`:root[data-theme="dark"]`,
		`role="group" aria-label="Color theme"`,
		`data-theme-value="auto"`,
		`data-theme-value="light"`,
		`data-theme-value="dark"`,
		`mayflycirclefit.theme`,
		`localStorage.removeItem(storageKey)`,
		`aria-label="Use system theme" aria-pressed="true"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered layout missing %q", marker)
		}
	}
}
