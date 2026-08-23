package app

import "testing"

func TestCrossoverCountBounds(t *testing.T) {
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
		{"negative", -4, false},
		{"beyond what mating can consume", 2*popSize + 1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Normalize(JobConfig{
				RefPath:        "reference.png",
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
	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}

	if config.CrossoverCount != 0 {
		t.Fatalf("CrossoverCount = %d, want 0 so an unset config keeps the library's own scaling",
			config.CrossoverCount)
	}
}
