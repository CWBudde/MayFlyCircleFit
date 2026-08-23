package cmd

import (
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/spf13/cobra"
)

func TestParseOptimizerFlag(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{name: "", want: optimizerMayfly},
		{name: optimizerMayfly, want: optimizerMayfly},
		{name: optimizerDragonfly, want: optimizerDragonfly},
		{name: "swarm", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOptimizerFlag(test.name)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseOptimizerFlag(%q) accepted an unknown optimizer", test.name)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseOptimizerFlag(%q) error = %v", test.name, err)
			}

			if got != test.want {
				t.Errorf("parseOptimizerFlag(%q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestNewStageOptimizerSelectsTheRequestedLibrary(t *testing.T) {
	config := app.JobConfig{
		Variant: app.VariantStandard,
		Iters:   10,
		PopSize: 20,
	}

	mayflyOptimizer, err := newStageOptimizer(nil, optimizerMayfly, config, nil)
	if err != nil {
		t.Fatalf("newStageOptimizer(mayfly) error = %v", err)
	}

	if _, ok := mayflyOptimizer.(*opt.MayflyAdapter); !ok {
		t.Errorf("newStageOptimizer(mayfly) = %T, want *opt.MayflyAdapter", mayflyOptimizer)
	}

	dragonflyOptimizer, err := newStageOptimizer(nil, optimizerDragonfly, config, nil)
	if err != nil {
		t.Fatalf("newStageOptimizer(dragonfly) error = %v", err)
	}

	if _, ok := dragonflyOptimizer.(*opt.DragonflyAdapter); !ok {
		t.Errorf("newStageOptimizer(dragonfly) = %T, want *opt.DragonflyAdapter", dragonflyOptimizer)
	}
}

func TestNewStageOptimizerRefusesMayflyOnlyFlagsForDragonfly(t *testing.T) {
	for _, name := range mayflyOnlyFlags {
		t.Run(name, func(t *testing.T) {
			command := &cobra.Command{Use: "run"}
			for _, flag := range mayflyOnlyFlags {
				command.Flags().String(flag, "", "")
			}

			err := command.Flags().Set(name, "value")
			if err != nil {
				t.Fatalf("set --%s: %v", name, err)
			}

			_, err = newStageOptimizer(command, optimizerDragonfly, app.JobConfig{Iters: 10, PopSize: 20}, nil)
			if err == nil {
				t.Fatalf("newStageOptimizer accepted --%s with the dragonfly optimizer", name)
			}

			if !IsUsageError(err) || !strings.Contains(err.Error(), name) {
				t.Errorf("error = %v, want a usage error naming --%s", err, name)
			}
		})
	}
}

func TestNewStageOptimizerRejectsAnUnknownLibrary(t *testing.T) {
	_, err := newStageOptimizer(nil, "swarm", app.JobConfig{Iters: 10, PopSize: 20}, nil)
	if !IsUsageError(err) {
		t.Fatalf("newStageOptimizer error = %v, want a usage error", err)
	}
}
