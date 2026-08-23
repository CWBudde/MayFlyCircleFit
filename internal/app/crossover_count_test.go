package app_test

import (
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// crossoverRefPath is the reference image every case in this file normalizes
// against. It is a constant so the literal appears once.
const crossoverRefPath = "reference.png"

func TestCrossoverCountBounds(t *testing.T) {
	t.Parallel()

	const popSize = 64

	for _, testCase := range []struct {
		name  string
		count int
		valid bool
	}{
		{"zero defers to the library", 0, true},
		{"two is the library's minimum", 2, true},
		{"within the population", popSize, true},
		{"at the mating limit", 2 * popSize, true},
		{"one starves the mutant pool", 1, false},
		{"a negative count", -4, false},
		{"beyond what mating can consume", 2*popSize + 1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := app.Normalize(app.JobConfig{
				RefPath:        crossoverRefPath,
				PopSize:        popSize,
				CrossoverCount: testCase.count,
			})

			if testCase.valid && err != nil {
				t.Fatalf("Normalize() = %v, want nil", err)
			}

			if !testCase.valid && err == nil {
				t.Fatal("Normalize() = nil, want an error")
			}
		})
	}
}

func TestCrossoverCountDefaultsToLibraryScaling(t *testing.T) {
	t.Parallel()

	config, err := app.Normalize(app.JobConfig{RefPath: crossoverRefPath})
	if err != nil {
		t.Fatal(err)
	}

	if config.CrossoverCount != 0 {
		t.Fatalf("CrossoverCount = %d, want 0 so an unset config keeps the library's own scaling",
			config.CrossoverCount)
	}
}
