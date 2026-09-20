package cmd

import (
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cwbudde/circlefit/internal/anim"
	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/spf13/cobra"
)

var (
	animateRefPath        string
	animateCirclesPath    string
	animateCheckpointPath string
	animateCanvasPath     string
	animateOutDir         string
	animateStyle          string
	animateScale          float64
	animateMargin         float64
	animateHalfLife       int
	animateMaxActive      int
	animateFrames         int
	animateOutro          int
	animateReverse        bool
	animateBackground     string
	animateIgnoreCanvas   bool
	animateMP4Path        string
	animateFPS            int
	animateSupersample    int
	animateWorkers        int
)

// frameNamePattern is both the file name and what ffmpeg is handed as its input
// pattern, so the two cannot drift. The padding is a deliberate departure from
// the original, which wrote Frame7.png and left the sequence unsortable; the
// same mistake is in this project's own snapshots/canvas-%02d.png.
const frameNamePattern = "frame-%06d.png"

// progressThreshold is how many frames a sequence needs before it reports
// progress while writing.
const progressThreshold = 50

var animateCmd = &cobra.Command{
	Use:   "animate",
	Short: "Replay a finished circle arrangement as a sequence of PNG frames",
	Long: `Renders an arrangement as an animation, one PNG per frame, and
optionally encodes it to MP4 with ffmpeg.

This is a port of the animation export in this project's Pascal predecessor,
CircledPictureDrawing. Like the original it replays a finished arrangement
rather than recording the search, so it works on any circle list: a checkpoint
a run left behind, a hand-authored file, or the initialCircles of a schedule.

Four styles are available. static adds one circle per frame. grow expands each
circle in turn from a point, fading it in as it goes. cascade grows up to
--max-active circles at once in a sliding window. inflate expands the whole
arrangement together.`,
	Args: cobra.NoArgs,
	RunE: runAnimate,
}

func init() {
	styles := make([]string, 0, len(anim.Styles()))
	for _, style := range anim.Styles() {
		styles = append(styles, string(style))
	}

	defaults := anim.DefaultOptions()

	flags := animateCmd.Flags()
	flags.StringVar(&animateRefPath, "ref", "", "Reference image, which sets the frame size (required)")
	flags.StringVar(&animateCirclesPath, "circles", "", "Circle list or schedule document")
	flags.StringVar(&animateCheckpointPath, "checkpoint", "", "Checkpoint file whose best solution to animate")
	flags.StringVar(&animateCanvasPath, "canvas", "", "Base canvas the arrangement was fitted over")
	flags.StringVar(&animateOutDir, "out-dir", "", "Directory to write the frames into (required)")
	flags.StringVar(&animateStyle, "style", string(defaults.Style), "Animation style: "+strings.Join(styles, ", "))
	flags.Float64Var(&animateScale, "scale", defaults.Scale, "Multiply every coordinate and radius by this")
	flags.Float64Var(&animateMargin, "margin", 0,
		"Empty space on each side, as a fraction of the scaled size; 0.5 reproduces the original framing")
	flags.IntVar(&animateHalfLife, "half-life", defaults.HalfLife,
		"How aggressively grow drops frames as it goes; 0 keeps every frame")
	flags.IntVar(&animateMaxActive, "max-active", defaults.MaxActive, "How many circles cascade grows at once")
	flags.IntVar(&animateFrames, "frames", 0, "Frame count for inflate; 0 derives it from the largest radius")
	flags.IntVar(&animateOutro, "outro", 0, "Append this many closing vignette frames; 41 reproduces the original")
	flags.BoolVar(&animateReverse, "reverse", false, "Play inflate backwards, deflating to nothing")
	flags.StringVar(&animateBackground, "background", "#FFFFFF", "Background colour behind the arrangement")
	flags.BoolVar(&animateIgnoreCanvas, "ignore-canvas", false,
		"Animate on the background colour, ignoring the base canvas the source records")
	flags.StringVar(&animateMP4Path, "mp4", "", "Also encode the frames to this MP4, using ffmpeg")
	flags.IntVar(&animateFPS, "fps", 30, "Frame rate for --mp4")
	flags.IntVar(&animateSupersample, "supersample", 1,
		"Render this many times larger and average down, to antialias the circle edges")
	flags.IntVar(&animateWorkers, "workers", 0,
		"Goroutines that average and encode frames alongside the render, capped at GOMAXPROCS; 0 uses every core")

	_ = animateCmd.MarkFlagRequired("ref")
	_ = animateCmd.MarkFlagRequired("out-dir")
	animateCmd.MarkFlagsMutuallyExclusive("circles", "checkpoint")
	animateCmd.MarkFlagsOneRequired("circles", "checkpoint")

	rootCmd.AddCommand(animateCmd)
}

func runAnimate(cmd *cobra.Command, _ []string) error {
	ref, err := loadScoreReference(animateRefPath)
	if err != nil {
		return err
	}

	circles, canvasPath, err := animateArrangement(ref)
	if err != nil {
		return err
	}

	if animateSupersample < 1 {
		return fmt.Errorf("--supersample must be at least 1, got %d", animateSupersample)
	}

	sequence, err := anim.Plan(circles, ref.Bounds().Dx(), ref.Bounds().Dy(), animateOptions())
	if err != nil {
		return err
	}

	background, err := animateCanvas(sequence, ref, canvasPath)
	if err != nil {
		return err
	}

	err = os.MkdirAll(animateOutDir, 0o750)
	if err != nil {
		return fmt.Errorf("create %s: %w", animateOutDir, err)
	}

	removed, err := clearStaleFrames(animateOutDir)
	if err != nil {
		return err
	}

	if removed > 0 {
		cmd.Printf("replaced:   %d frames from a previous run\n", removed)
	}

	cmd.Printf("style:      %s\n", animateStyle)
	cmd.Printf("circles:    %d\n", len(circles))

	reportFrames(cmd, sequence)

	err = writeFrames(cmd, sequence, background)
	if err != nil {
		return err
	}

	cmd.Printf("wrote:      %s\n", filepath.Join(animateOutDir, fmt.Sprintf(frameNamePattern, 0)))

	return encodeVideo(cmd, len(sequence.Frames))
}

// clearStaleFrames deletes the frames a previous run left in the output
// directory.
//
// Overwriting only the new prefix is not enough. A shorter animation leaves the
// old tail behind, and because the numbering is contiguous those leftovers are
// not inert: ffmpeg reads frame-%06d.png until the sequence breaks, so a
// previous run's ending would be spliced onto this one's. The directory would
// also no longer describe one animation.
//
// It removes only names this command writes -- frame- followed by exactly six
// digits and .png -- and only in the directory it was pointed at. Anything else
// in there is left alone.
func clearStaleFrames(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}

	removed := 0

	for _, entry := range entries {
		if entry.IsDir() || !staleFrameName.MatchString(entry.Name()) {
			continue
		}

		err = os.Remove(filepath.Join(dir, entry.Name()))
		if err != nil {
			return removed, fmt.Errorf("remove stale frame %s: %w", entry.Name(), err)
		}

		removed++
	}

	return removed, nil
}

// staleFrameName matches exactly what frameNamePattern produces, so nothing
// else a directory happens to hold can be deleted by it.
var staleFrameName = regexp.MustCompile(`^frame-\d{6}\.png$`)

// reportFrames states the size the frames are written at, and the size they
// were rendered at when those differ, because the render size is what the cost
// and the memory follow from.
func reportFrames(cmd *cobra.Command, sequence *anim.Sequence) {
	if animateSupersample <= 1 {
		cmd.Printf("frames:     %d at %dx%d\n", len(sequence.Frames), sequence.Width, sequence.Height)

		return
	}

	cmd.Printf("frames:     %d at %dx%d, averaged down from %dx%d\n",
		len(sequence.Frames),
		sequence.Width/animateSupersample, sequence.Height/animateSupersample,
		sequence.Width, sequence.Height)
}

func animateOptions() anim.Options {
	opts := anim.DefaultOptions()
	opts.Style = anim.Style(animateStyle)
	// Supersampling is a larger render, so it multiplies the scale and the
	// frames are averaged back down on the way out.
	opts.Scale = animateScale * float64(animateSupersample)
	opts.Margin = animateMargin
	opts.HalfLife = animateHalfLife
	opts.MaxActive = animateMaxActive
	opts.Frames = animateFrames
	opts.Outro = animateOutro
	opts.Reverse = animateReverse

	return opts
}

// animateArrangement reads the circles from whichever source was named, and
// reports the base canvas the source was fitted over: a schedule document's
// base.canvasPath, or the canvasPath a checkpoint recorded in its own
// configuration. Both are overridden by --canvas.
//
// Carrying it matters because a solution fitted over a canvas does not describe
// the finished image without it. Rendering those circles on white would produce
// an animation that never matches the run it came from, and nothing in the
// output would say so.
func animateArrangement(ref *image.NRGBA) ([]fit.Circle, string, error) {
	if animateCheckpointPath != "" {
		return checkpointCircles(animateCheckpointPath, ref)
	}

	specs, canvasPath, err := loadCircleSpecs(animateCirclesPath)
	if err != nil {
		return nil, "", err
	}

	if len(specs) == 0 {
		return nil, "", fmt.Errorf("%s contains no circles", animateCirclesPath)
	}

	specs.ApplyDefaults()

	err = specs.Validate()
	if err != nil {
		return nil, "", err
	}

	params, err := specs.ToParams()
	if err != nil {
		return nil, "", err
	}

	err = checkWithinBounds(params, ref)
	if err != nil {
		return nil, "", err
	}

	return decodeCircles(params), canvasPath, nil
}

// checkWithinBounds refuses a parameter vector the reference cannot hold. An
// arrangement outside the bounds would be quietly pulled inside by the renderer,
// and the animation would then show something nobody authored; score refuses it
// for the same reason. The clamp covers every parameter -- centre, radius,
// colour and opacity -- so a vector that passes here is one the renderer draws
// as written, whichever route it arrived by.
func checkWithinBounds(params []float64, ref *image.NRGBA) error {
	bounds := fit.NewBounds(len(params)/app.ParamsPerCircle, ref.Bounds().Dx(), ref.Bounds().Dy())

	clamped := append([]float64(nil), params...)
	bounds.ClampVector(clamped)

	for i := range params {
		// A NaN never equals anything, so it is caught here too.
		if params[i] != clamped[i] {
			return fmt.Errorf("circle %d is outside the bounds a %dx%d canvas allows",
				i/app.ParamsPerCircle, ref.Bounds().Dx(), ref.Bounds().Dy())
		}
	}

	return nil
}

func checkpointCircles(path string, ref *image.NRGBA) ([]fit.Circle, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read checkpoint: %w", err)
	}

	var checkpoint store.Checkpoint

	err = json.Unmarshal(data, &checkpoint)
	if err != nil {
		return nil, "", fmt.Errorf("%s is not a checkpoint: %w", path, err)
	}

	// Deliberately not Checkpoint.Validate: that checks the whole job
	// configuration -- reference path, seed, optimizer settings -- and this
	// command reads one field. A checkpoint copied off the machine that ran it
	// names a reference path that does not exist here, and refusing to draw its
	// circles over that would be a rule with no purpose. What has to be sound
	// is the parameter vector, and it is held to the same bounds as a circle
	// list: a checkpoint from another reference, or a hand-edited one, is not
	// trusted just for being a checkpoint.
	if len(checkpoint.BestParams) == 0 {
		return nil, "", fmt.Errorf("checkpoint %s records no solution", path)
	}

	if len(checkpoint.BestParams)%app.ParamsPerCircle != 0 {
		return nil, "", fmt.Errorf("checkpoint %s holds %d parameters, which is not a whole number of circles",
			path, len(checkpoint.BestParams))
	}

	err = checkWithinBounds(checkpoint.BestParams, ref)
	if err != nil {
		return nil, "", fmt.Errorf("checkpoint %s: %w", path, err)
	}

	return decodeCircles(checkpoint.BestParams), checkpoint.Config.CanvasPath, nil
}

func decodeCircles(params []float64) []fit.Circle {
	vector := fit.ParamVector{Data: params, K: len(params) / app.ParamsPerCircle}

	circles := make([]fit.Circle, vector.K)
	for i := range circles {
		circles[i] = vector.DecodeCircle(i)
	}

	return circles
}

// animateCanvas builds the background the frames are drawn over: the chosen
// colour everywhere, with the base canvas placed on top of it when one is
// named.
//
// A base canvas cannot be scaled. Resampling it would invent pixels the fit
// never saw, and the frames would stop matching the image the run produced, so
// the combination is refused rather than approximated.
//
// Supersampling is different, and is allowed: the canvas is taken up by pixel
// replication, which the box filter on the way out reverses exactly, so the
// averaged frame carries the canvas byte for byte and only the circle edges
// drawn over it are softened.
func animateCanvas(sequence *anim.Sequence, ref *image.NRGBA, canvasPath string) (*image.NRGBA, error) {
	canvasPath = effectiveCanvasPath(canvasPath)

	fill, err := app.ParseHexColor(animateBackground)
	if err != nil {
		return nil, fmt.Errorf("background: %w", err)
	}

	background := filledCanvas(sequence.Width, sequence.Height, fill)

	if canvasPath == "" {
		return background, nil
	}

	if animateScale != 1 {
		return nil, fmt.Errorf("--canvas cannot be combined with --scale %g: "+
			"scaling the base image would invent pixels the fit never saw", animateScale)
	}

	canvas, err := loadBaseCanvas(canvasPath, ref)
	if err != nil {
		return nil, err
	}

	placeCanvas(background, anim.Upsample(canvas, animateSupersample), sequence.OffsetX, sequence.OffsetY)

	return background, nil
}

// effectiveCanvasPath resolves the three ways a base canvas is chosen: the flag
// wins, --ignore-canvas discards what the source recorded, and otherwise the
// recorded path stands.
func effectiveCanvasPath(recorded string) string {
	if animateCanvasPath != "" {
		return animateCanvasPath
	}

	if animateIgnoreCanvas {
		return ""
	}

	return recorded
}

func filledCanvas(width, height int, fill [3]float64) *image.NRGBA {
	background := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(background.Pix); i += 4 {
		background.Pix[i+0] = uint8(fill[0]*255 + 0.5)
		background.Pix[i+1] = uint8(fill[1]*255 + 0.5)
		background.Pix[i+2] = uint8(fill[2]*255 + 0.5)
		background.Pix[i+3] = 0xFF
	}

	return background
}

// loadBaseCanvas reads the canvas the arrangement was fitted over and checks it
// lines up with the reference.
func loadBaseCanvas(canvasPath string, ref *image.NRGBA) (*image.NRGBA, error) {
	canvas, err := loadScoreReference(canvasPath)
	if err != nil {
		if animateCanvasPath != "" {
			return nil, fmt.Errorf("canvas %s: %w", canvasPath, err)
		}

		// The path was recorded by the run, on whichever machine produced it,
		// so it is often absent here. Falling back to the background colour
		// would quietly animate something that is not what was fitted, so name
		// what is missing and both ways out of it.
		return nil, fmt.Errorf("the source was fitted over canvas %q, which is not readable here: %w"+
			"\npass --canvas with a local copy, or --ignore-canvas to animate on the background colour",
			canvasPath, err)
	}

	if canvas.Bounds().Dx() != ref.Bounds().Dx() || canvas.Bounds().Dy() != ref.Bounds().Dy() {
		return nil, fmt.Errorf("canvas %s is %dx%d but the reference is %dx%d",
			canvasPath, canvas.Bounds().Dx(), canvas.Bounds().Dy(), ref.Bounds().Dx(), ref.Bounds().Dy())
	}

	return canvas, nil
}

func placeCanvas(background, canvas *image.NRGBA, offsetX, offsetY int) {
	target := image.Rect(offsetX, offsetY,
		offsetX+canvas.Bounds().Dx(), offsetY+canvas.Bounds().Dy())

	draw.Draw(background, target, canvas, canvas.Bounds().Min, draw.Src)
}

func writeFrames(cmd *cobra.Command, sequence *anim.Sequence, background *image.NRGBA) error {
	total := len(sequence.Frames)

	// Progress is for sequences long enough to wait on. A short one finishes
	// before the first line would be read, and printing ten of them for five
	// frames is noise.
	step := total / 10
	if total < progressThreshold {
		step = 0
	}

	pool := anim.NewSinkPool(animateWorkers, func(index int, img *image.NRGBA) error {
		path := filepath.Join(animateOutDir, fmt.Sprintf(frameNamePattern, index))

		err := writePNG(path, img)
		if err != nil {
			return fmt.Errorf("write frame %d: %w", index, err)
		}

		return nil
	})

	renderErr := anim.Render(sequence, background, func(index int, img *image.NRGBA) error {
		if step > 0 && index > 0 && index%step == 0 {
			cmd.Printf("            %d/%d frames\n", index, total)
		}

		return pool.Submit(index, detachFrame(img, animateSupersample))
	})

	// Close waits for the frames still being encoded, so it has to run whether
	// or not the render got to the end -- and its error is the one that says a
	// frame failed to write, since Render only ever sees the failure of an
	// earlier frame.
	closeErr := pool.Close()

	if renderErr != nil {
		return renderErr
	}

	return closeErr
}

// detachFrame produces the image the encoder pool is given: the averaged-down
// frame, or a copy when there is nothing to average.
//
// The copy is not optional. Render hands its sink the renderer's own buffer and
// overwrites it for the next frame, and at --supersample 1 Downsample returns
// that buffer unchanged, so handing it straight to a worker would encode
// whichever frame happened to be rendering by then. Downsampling already
// allocates, so the copy costs nothing on the path that does it.
func detachFrame(img *image.NRGBA, factor int) *image.NRGBA {
	frame := anim.Downsample(img, factor, animateWorkers)
	if frame != img {
		return frame
	}

	copied := image.NewNRGBA(img.Bounds())
	copy(copied.Pix, img.Pix)

	return copied
}

// encodeVideo runs ffmpeg over the frames just written.
//
// An absent ffmpeg is an error rather than a warning: --mp4 was asked for
// explicitly, and a command that quietly produces nothing it was told to
// produce is worse than one that says why. The message carries the command, so
// it can be run by hand once ffmpeg is installed.
func encodeVideo(cmd *cobra.Command, frames int) error {
	if animateMP4Path == "" {
		return nil
	}

	// The background was already parsed to build the frames, so this cannot
	// fail; it is re-read here only because the pad colour has to match it.
	fill, err := app.ParseHexColor(animateBackground)
	if err != nil {
		return fmt.Errorf("background: %w", err)
	}

	args := ffmpegArgs(fill)

	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("--mp4 needs ffmpeg, which is not on PATH; "+
			"the frames are written, so run this once it is installed:\n  ffmpeg %s",
			strings.Join(args, " "))
	}

	cmd.Printf("encoding:   %d frames at %d fps\n", frames, animateFPS)

	// No shell is involved and the argument vector is fixed; the only variable
	// parts are paths the operator named on the command line. The context is the
	// command's own, so interrupting the run stops the encoder with it.
	command := exec.CommandContext(cmd.Context(), binary, args...)

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, output)
	}

	cmd.Printf("video:      %s\n", animateMP4Path)

	return nil
}

// ffmpegArgs is separate so a test can assert the command without running it.
//
// The pad filter is not cosmetic: libx264 refuses odd dimensions for yuv420p,
// and the reference, --scale and --margin can all produce them. Padding to the
// next even size with the background colour keeps every pixel of every frame;
// truncating instead would crop the last row or column, and would reduce a
// one-pixel dimension to zero, which the encoder also refuses.
func ffmpegArgs(fill [3]float64) []string {
	return []string{
		"-y",
		"-framerate", strconv.Itoa(animateFPS),
		"-i", filepath.Join(animateOutDir, frameNamePattern),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-vf", "pad=w=ceil(iw/2)*2:h=ceil(ih/2)*2:color=" + ffmpegColor(fill),
		animateMP4Path,
	}
}

// ffmpegColor writes a parsed colour in the 0xRRGGBB form ffmpeg reads, rounded
// the same way filledCanvas rounds it into the frames, so the padding cannot be
// off by one from the background it continues.
func ffmpegColor(fill [3]float64) string {
	return fmt.Sprintf("0x%02X%02X%02X",
		uint8(fill[0]*255+0.5), uint8(fill[1]*255+0.5), uint8(fill[2]*255+0.5))
}
