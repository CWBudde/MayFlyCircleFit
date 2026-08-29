package server

import (
	"math"
	"testing"
	"time"
)

func TestSerializablePSNR(t *testing.T) {
	t.Parallel()

	value, infinite := serializablePSNR(1)
	if value == nil || infinite || math.Abs(*value-48.1308036086791) > 1e-12 {
		t.Fatalf("serializablePSNR(1) = (%v, %v)", value, infinite)
	}

	value, infinite = serializablePSNR(0)
	if value != nil || !infinite {
		t.Fatalf("serializablePSNR(0) = (%v, %v), want (nil, true)", value, infinite)
	}

	value, infinite = serializablePSNR(-1)
	if value != nil || infinite {
		t.Fatalf("serializablePSNR(-1) = (%v, %v), want unavailable", value, infinite)
	}
}

func TestShouldSampleSSIM(t *testing.T) {
	t.Parallel()

	last := time.Unix(100, 0)
	for _, test := range []struct {
		name string
		now  time.Time
		cost float64
		want bool
	}{
		{name: "improved after interval", now: last.Add(time.Second), cost: 9, want: true},
		{name: "too soon", now: last.Add(time.Second - 1), cost: 9},
		{name: "unchanged", now: last.Add(2 * time.Second), cost: 10},
		{name: "regressed", now: last.Add(2 * time.Second), cost: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldSampleSSIM(test.now, last, test.cost, 10); got != test.want {
				t.Fatalf("shouldSampleSSIM() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestThroughputCPS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		stageEvaluations int
		circles          int
		elapsed          float64
		want             float64
	}{
		{name: "stage work", stageEvaluations: 100, circles: 8, elapsed: 2, want: 400},
		{name: "no elapsed time", stageEvaluations: 100, circles: 8, elapsed: 0, want: 0},
		{name: "inherited only", stageEvaluations: 0, circles: 8, elapsed: 2, want: 0},
		{name: "no circles", stageEvaluations: 100, circles: 0, elapsed: 2, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := throughputCPS(test.stageEvaluations, test.circles, test.elapsed); got != test.want {
				t.Fatalf("throughputCPS() = %v, want %v", got, test.want)
			}
		})
	}
}
