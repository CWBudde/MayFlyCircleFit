package main

import (
	"math"
	"testing"
)

func TestCampaignArmsAreEvaluationMatched(t *testing.T) {
	t.Parallel()

	arms, err := campaignArms(defaultBudget)
	if err != nil {
		t.Fatal(err)
	}

	if len(arms) != 5 {
		t.Fatalf("arms = %d, want 5", len(arms))
	}

	for _, current := range arms[2:] {
		if got := current.iters * defaultPop; got != defaultBudget {
			t.Errorf("%s budget = %d, want %d", current.name, got, defaultBudget)
		}
	}
}

func TestPairedImprovement(t *testing.T) {
	t.Parallel()

	control := []resultRow{
		{manifestRow: manifestRow{Block: 1}, Score: 10},
		{manifestRow: manifestRow{Block: 2}, Score: 12},
		{manifestRow: manifestRow{Block: 3}, Score: 14},
	}
	candidate := []resultRow{
		{manifestRow: manifestRow{Block: 1}, Score: 9},
		{manifestRow: manifestRow{Block: 2}, Score: 10},
		{manifestRow: manifestRow{Block: 3}, Score: 11},
	}

	mean, statistic, wins := pairedImprovement(control, candidate)
	if mean != 2 || wins != 3 || math.Abs(statistic-3.464101615137754) > 1e-12 {
		t.Fatalf("pairedImprovement() = (%v, %v, %d)", mean, statistic, wins)
	}
}
