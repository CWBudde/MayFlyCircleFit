//nolint:testpackage // exercises the unexported cloneCircleSpecs helper
package server

import (
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

// TestCloneCircleSpecsDeepCopiesRGB pins the one field of CircleSpec that a
// slice copy does not reach.
//
// RGB is a pointer, so a shallow clone leaves the caller and the manager
// sharing one array: mutating the exact colour of a submitted configuration
// would then reach past the manager lock and change the seed of a live job
// after validation accepted it.
func TestCloneCircleSpecsDeepCopiesRGB(t *testing.T) {
	t.Parallel()

	const hexRed = "#ff0000"

	original := app.CircleSpecs{
		{X: 10, Y: 10, R: 4, RGB: &[3]float64{0.25, 0.5, 0.75}, Opacity: 1},
		{X: 20, Y: 20, R: 4, Color: hexRed, Opacity: 1},
	}

	cloned := cloneCircleSpecs(original)

	if cloned[0].RGB == original[0].RGB {
		t.Fatal("cloneCircleSpecs() shared the RGB array with the original")
	}

	if *cloned[0].RGB != *original[0].RGB {
		t.Fatalf("cloned RGB = %v, want %v", *cloned[0].RGB, *original[0].RGB)
	}

	original[0].RGB[0] = 1

	if cloned[0].RGB[0] != 0.25 {
		t.Errorf("mutating the original changed the clone: red = %v, want 0.25", cloned[0].RGB[0])
	}
	// A spec authored in hex has no exact colour, and the clone must keep
	// saying so rather than inventing a zero array.
	if cloned[1].RGB != nil {
		t.Errorf("cloned RGB of a hex spec = %v, want nil", cloned[1].RGB)
	}
}

// TestCreateJobWithIDDeepCopiesRGB is the same guarantee at the boundary that
// needs it: the manager's stored configuration must not alias the caller's.
func TestCreateJobWithIDDeepCopiesRGB(t *testing.T) {
	t.Parallel()

	config := JobConfig{
		InitialCircles: app.CircleSpecs{{X: 10, Y: 10, R: 4, RGB: &[3]float64{0.25, 0.5, 0.75}, Opacity: 1}},
	}

	manager := NewJobManager()

	job, err := manager.CreateJobWithID("", app.DefaultProject, config)
	if err != nil {
		t.Fatalf("CreateJobWithID() error = %v", err)
	}

	config.InitialCircles[0].RGB[0] = 1

	if job.Config.InitialCircles[0].RGB[0] != 0.25 {
		t.Errorf("the stored seed followed the caller's mutation: red = %v, want 0.25",
			job.Config.InitialCircles[0].RGB[0])
	}

	snapshot, ok := manager.GetJob(job.ID)
	if !ok {
		t.Fatal("GetJob() did not find the job it just created")
	}

	snapshot.Config.InitialCircles[0].RGB[1] = 1

	if job.Config.InitialCircles[0].RGB[1] != 0.5 {
		t.Errorf("mutating a fetched snapshot changed the live job: green = %v, want 0.5",
			job.Config.InitialCircles[0].RGB[1])
	}
}
