package app

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCircleSpecsToParams(t *testing.T) {
	t.Parallel()

	specs := CircleSpecs{
		{X: 256, Y: 660, R: 300, Color: "#232650"},
		{X: 10, Y: 20, R: 5, Color: "ffffff", Opacity: 0.5},
	}
	specs.ApplyDefaults()

	params, err := specs.ToParams()
	if err != nil {
		t.Fatal(err)
	}

	want := []float64{
		256, 660, 300, 35.0 / 255, 38.0 / 255, 80.0 / 255, 1,
		10, 20, 5, 1, 1, 1, 0.5,
	}
	if len(params) != len(want) {
		t.Fatalf("params length = %d, want %d", len(params), len(want))
	}

	for i := range want {
		if math.Abs(params[i]-want[i]) > 1e-12 {
			t.Fatalf("params[%d] = %v, want %v", i, params[i], want[i])
		}
	}
}

func TestCircleSpecsValidate(t *testing.T) {
	t.Parallel()

	valid := CircleSpec{X: 1, Y: 2, R: 3, Color: "#010203", Opacity: 1}

	cases := []struct {
		name  string
		spec  CircleSpec
		field string
	}{
		{"opaque by default", func() CircleSpec { s := valid; s.Opacity = 1; return s }(), ""},
		{"radius below one", func() CircleSpec { s := valid; s.R = 0.5; return s }(), "initialCircles[0].r"},
		{"colour too short", func() CircleSpec { s := valid; s.Color = "#abc"; return s }(), "initialCircles[0].color"},
		{"colour not hex", func() CircleSpec { s := valid; s.Color = "#gggggg"; return s }(), "initialCircles[0].color"},
		{"opacity above one", func() CircleSpec { s := valid; s.Opacity = 1.5; return s }(), "initialCircles[0].opacity"},
		{"opacity zero", func() CircleSpec { s := valid; s.Opacity = 0; return s }(), "initialCircles[0].opacity"},
		{"position not finite", func() CircleSpec { s := valid; s.X = math.Inf(1); return s }(), "initialCircles[0].x"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := CircleSpecs{testCase.spec}.Validate()
			if testCase.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate() = nil, want an error naming %s", testCase.field)
			}

			var fieldErr *ValidationError
			if !errors.As(err, &fieldErr) || fieldErr.Field != testCase.field {
				t.Fatalf("Validate() = %v, want an error naming %s", err, testCase.field)
			}
		})
	}
}

func TestJobConfigValidateInitialCircles(t *testing.T) {
	t.Parallel()

	base := func() JobConfig {
		config := DefaultConfig()
		config.RefPath = "ref.png"
		config.Mode = ModeBatch
		config.Circles = 2
		config.BatchSize = 2
		config.PolishingActiveSetSize = 2
		config.Seed = 1
		config.EffectiveSeed = 1
		config.InitialCircles = CircleSpecs{
			{X: 1, Y: 2, R: 3, Color: "#010203", Opacity: 1},
			{X: 4, Y: 5, R: 6, Color: "#040506", Opacity: 1},
		}

		return config
	}

	valid := base()

	err := valid.Validate()
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	wrongMode := base()

	wrongMode.Mode = ModeJoint

	err = wrongMode.Validate()
	if err == nil {
		t.Fatal("Validate() accepted initialCircles outside batch mode")
	}

	wrongCount := base()
	wrongCount.Circles = 3

	wrongCount.PolishingActiveSetSize = 3

	err = wrongCount.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a spec list shorter than circles")
	}

	// A batch smaller than the circle count optimizes the vector in chunks, so
	// it would seed the first chunk and drop the rest. Refused here, because at
	// dispatch it surfaces as a failed run rather than a rejected request.
	partialBatch := base()

	partialBatch.BatchSize = 1

	err = partialBatch.Validate()
	if err == nil {
		t.Fatal("Validate() accepted initialCircles with a batch smaller than circles")
	}
}

// TestApplyDefaultsWidensTheBatchForAnAuthoredSeed pins the normalization half
// of that rule: the stock batch size of five would otherwise queue a seeded
// ten-circle run that the batch dispatch then refuses.
func TestApplyDefaultsWidensTheBatchForAnAuthoredSeed(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.RefPath = "ref.png"
	config.Mode = ModeBatch
	config.Circles = 10
	config.BatchSize = 0
	config.Seed = 1

	config.InitialCircles = make(CircleSpecs, 10)
	for i := range config.InitialCircles {
		config.InitialCircles[i] = CircleSpec{X: 1, Y: 1, R: 1, Color: "#010203"}
	}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatal(err)
	}

	if config.BatchSize != 10 {
		t.Fatalf("batchSize = %d, want 10 so the seeded vector is one optimizer stage", config.BatchSize)
	}

	err = config.Validate()
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	// Without a seed the default is unchanged, so this normalization cannot
	// quietly widen every batch run.
	unseeded := DefaultConfig()
	unseeded.RefPath = "ref.png"
	unseeded.Mode = ModeBatch
	unseeded.Circles = 10
	unseeded.BatchSize = 0

	unseeded.Seed = 1

	err = unseeded.ApplyDefaults()
	if err != nil {
		t.Fatal(err)
	}

	if unseeded.BatchSize != 5 {
		t.Fatalf("unseeded batchSize = %d, want the default of 5", unseeded.BatchSize)
	}
}

// TestApplyDefaultsMakesInitialCirclesOpaque pins the one default the field
// has: an omitted opacity is a circle the author meant to be solid.
func TestApplyDefaultsMakesInitialCirclesOpaque(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.RefPath = "ref.png"
	config.Seed = 1

	config.InitialCircles = CircleSpecs{{X: 1, Y: 2, R: 3, Color: "#010203"}}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatal(err)
	}

	if config.InitialCircles[0].Opacity != 1 {
		t.Fatalf("opacity = %v, want 1", config.InitialCircles[0].Opacity)
	}
}

// TestCircleSpecsExactColorRoundTrips is the point of the rgb form: a solution
// carried back into a run must return the vector it left as, bit for bit.
//
// The hex form cannot do that. A channel round trips through eight bits, and
// the loss is no longer cosmetic -- re-seeding the eight-circle record costs
// 3.73 against a polishing gain of 3.34, so the codec eats more than the search
// finds. The two assertions below are that pair: exact for rgb, lossy for hex.
func TestCircleSpecsExactColorRoundTrips(t *testing.T) {
	t.Parallel()

	channels := [3]float64{0.4823529411764706, 0.19607843137254902, 0.7333333333333333}
	specs := CircleSpecs{{X: 10, Y: 20, R: 5, RGB: &channels, Opacity: 1}}

	err := specs.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	params, err := specs.ToParams()
	if err != nil {
		t.Fatalf("ToParams() error = %v", err)
	}

	for i, want := range channels {
		if got := params[3+i]; got != want {
			t.Errorf("channel %d = %v, want %v", i, got, want)
		}
	}
}

// TestCircleSpecsHexColorStillQuantizes pins the behaviour the exact form
// exists to sidestep, so a future reader does not mistake the hex path for
// lossless and reintroduce the round trip.
func TestCircleSpecsHexColorStillQuantizes(t *testing.T) {
	t.Parallel()

	specs := CircleSpecs{{X: 10, Y: 20, R: 5, Color: "#7b3239", Opacity: 1}}

	params, err := specs.ToParams()
	if err != nil {
		t.Fatalf("ToParams() error = %v", err)
	}

	if params[3] != 123.0/255.0 {
		t.Errorf("red = %v, want %v", params[3], 123.0/255.0)
	}
}

func TestCircleSpecsRefusesAmbiguousOrMissingColor(t *testing.T) {
	t.Parallel()

	channels := [3]float64{0.5, 0.5, 0.5}
	outOfRange := [3]float64{0.5, 1.5, 0.5}

	tests := map[string]struct {
		spec  CircleSpec
		field string
	}{
		"neither":     {CircleSpec{X: 1, Y: 1, R: 5, Opacity: 1}, "color"},
		"both":        {CircleSpec{X: 1, Y: 1, R: 5, Color: "#ffffff", RGB: &channels, Opacity: 1}, "rgb"},
		"outOfRange":  {CircleSpec{X: 1, Y: 1, R: 5, RGB: &outOfRange, Opacity: 1}, "rgb"},
		"badHexColor": {CircleSpec{X: 1, Y: 1, R: 5, Color: "#zzz", Opacity: 1}, "color"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := CircleSpecs{test.spec}.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %+v", test.spec)
			}

			if !strings.Contains(err.Error(), test.field) {
				t.Errorf("error = %v, want it to name %q", err, test.field)
			}
		})
	}
}
