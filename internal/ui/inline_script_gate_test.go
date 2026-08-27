package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file is Task 18.7's gate. Phase 18 moved every piece of hand-written
// browser code out of the templ layer and into the React islands under
// web/src, and the point of a gate rather than a one-off cleanup is that the
// surface stays retired: a `<script>` body added to any templ source fails this
// package's tests.
//
// There is exactly one permitted exception, and it is the reason the gate is
// written as an allowlist of one rather than as a flat ban.
//
// # Why themePreloadScript stays inline
//
// The theme override is a stylesheet appended to <head>. It has to be in place
// before the browser paints, or the page paints in the wrong palette and then
// flips -- the flash of unstyled content that a theme preference exists to
// avoid. The island bundle cannot do it: <script type="module"> is deferred by
// specification, so it runs after the document has parsed and, in practice,
// after first paint. Only a blocking, inline script in <head> runs early
// enough. So themePreloadScript (layout.templ) stays, it stays inline, it stays
// in <head> ahead of the bundle, and it does nothing but apply the stored
// preference and publish window.mayflyTheme for ThemeToggleIsland to reuse.
//
// Everything else that used to live in a templ <script> is an island. See
// mountIslands(...) at the bottom of web/src/dashboard.tsx for the registry.

// scriptOpenTag matches the opening tag of any <script>, with or without
// attributes. templ's own @templ.JSONScript emits `<script type=...>` from the
// generated Go rather than from the .templ source, so it is not matched here --
// and it would not be a violation if it were: a JSON data block is not code.
var scriptOpenTag = regexp.MustCompile(`(?i)<script\b[^>]*>`)

// inlineHandlerAttr matches an HTML inline event-handler attribute
// (onclick=..., onchange=..., and the rest). These are hand-written JavaScript
// just as much as a <script> body is, so the gate refuses them too. There is no
// exception: an island owns its own event handlers, and a fallback control that
// needs script is either disabled or is a plain link.
var inlineHandlerAttr = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)

// theExceptionFile and theExceptionConst name the one permitted script body.
const (
	theExceptionFile  = "layout.templ"
	theExceptionConst = "themePreloadScript"
)

// scriptBody is one <script> ... </script> pair carrying something other than
// whitespace between its tags.
type scriptBody struct {
	file   string
	line   int
	offset int // byte offset of the "<script" that opens it
}

// uiSourceFiles returns every hand-written source file in this package that can
// reach the browser: the templ templates, plus the package's own Go files,
// which is where a const like themePreloadScript lives. Generated output
// (*_templ.go) and tests are excluded -- the generated files are a copy of the
// .templ sources this gate already reads, and counting them would report every
// finding twice.
func uiSourceFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	for _, pattern := range []string{"*.templ", "*.go"} {
		matched, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		for _, name := range matched {
			if strings.HasSuffix(name, "_templ.go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			files = append(files, name)
		}
	}

	// A glob that silently matched nothing would make every assertion below
	// vacuously true, which is the one way a gate like this fails open.
	if len(files) == 0 {
		t.Fatal("found no templ or Go sources in the package directory; the gate would pass vacuously")
	}

	return files
}

// findScriptBodies returns every non-empty <script> body in source.
func findScriptBodies(file, source string) []scriptBody {
	var found []scriptBody

	for _, open := range scriptOpenTag.FindAllStringIndex(source, -1) {
		rest := source[open[1]:]
		end := strings.Index(strings.ToLower(rest), "</script>")
		if end < 0 {
			// An unterminated <script> is reported as a body of its own
			// rather than skipped, so a truncated tag cannot slip past.
			end = len(rest)
		}

		body := rest[:end]
		if strings.TrimSpace(body) == "" {
			continue
		}

		found = append(found, scriptBody{
			file:   file,
			line:   strings.Count(source[:open[0]], "\n") + 1,
			offset: open[0],
		})
	}

	return found
}

// isThemePreloadException reports whether the <script> opening at offset is the
// one belonging to themePreloadScript. The constant is matched by its
// declaration and the script has to be the very first thing inside its raw
// string literal, so a second script added anywhere else in layout.templ --
// including later inside that same constant -- is still a violation.
func isThemePreloadException(file, source string, offset int) bool {
	if filepath.Base(file) != theExceptionFile {
		return false
	}

	opening := "const " + theExceptionConst + " = `"
	start := strings.Index(source, opening)
	if start < 0 {
		return false
	}

	return offset == start+len(opening)
}

// blankCommentLines replaces the body of every whole-line // comment with
// spaces, keeping the file's length and line breaks so reported offsets and
// line numbers still refer to the real source. Without it the gate would trip
// over prose: layout.templ's own doc comment explains the bundle's
// <script type="module"> tag, and a naive scan reads that sentence as markup.
// Only whole-line comments are blanked, because "//" also occurs inside URLs.
func blankCommentLines(source string) string {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") {
			lines[i] = strings.Repeat(" ", len(line))
		}
	}

	return strings.Join(lines, "\n")
}

// TestNoTemplSourceCarriesAnInlineScript is the gate. It fails on any
// hand-written <script> body in this package's sources, with exactly one
// documented exception: themePreloadScript in layout.templ, which must run
// before first paint and therefore cannot move into the deferred island bundle.
// See this file's header for the full reasoning.
func TestNoTemplSourceCarriesAnInlineScript(t *testing.T) {
	t.Parallel()

	exceptions := 0

	for _, file := range uiSourceFiles(t) {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := blankCommentLines(string(content))

		for _, found := range findScriptBodies(file, source) {
			if isThemePreloadException(file, source, found.offset) {
				exceptions++
				continue
			}

			t.Errorf(
				"%s:%d carries an inline <script> body. Phase 18 retired the inline-script surface: "+
					"put the behavior in a React island under web/src and mount it with data-island. "+
					"The only permitted inline script is %s in %s, because it must run before first paint.",
				found.file, found.line, theExceptionConst, theExceptionFile,
			)
		}
	}

	// Assert the exception is still there. Without this the gate would keep
	// passing after someone deleted the pre-paint script, and the theme would
	// silently regain the flash of the wrong palette this file exists to
	// explain.
	if exceptions != 1 {
		t.Errorf("found %d permitted inline scripts, want exactly 1 (%s in %s)", exceptions, theExceptionConst, theExceptionFile)
	}
}

// TestNoTemplSourceCarriesAnInlineEventHandler is the gate's second half. An
// onclick= attribute is hand-written JavaScript in the markup just as a
// <script> body is, and it also breaks the fallback contract in a subtler way:
// it works when script runs and silently does nothing when it does not, so the
// control looks alive on a page where it is not. There is no exception.
func TestNoTemplSourceCarriesAnInlineEventHandler(t *testing.T) {
	t.Parallel()

	for _, file := range uiSourceFiles(t) {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := blankCommentLines(string(content))

		for _, match := range inlineHandlerAttr.FindAllStringIndex(source, -1) {
			line := strings.Count(source[:match[0]], "\n") + 1
			t.Errorf(
				"%s:%d carries the inline event handler %q. Register the handler in the island that owns "+
					"the control, or make the fallback a plain link or a disabled button.",
				file, line, strings.TrimSpace(source[match[0]:match[1]]),
			)
		}
	}
}

// TestThemePreloadScriptRunsBeforeTheBundle asserts the reason the exception
// exists. If the pre-paint script ever moved below the bundle -- or the bundle
// moved into <head> above it -- the exception would no longer be earning its
// keep, and the theme would apply after the browser had already painted.
func TestThemePreloadScriptRunsBeforeTheBundle(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := Layout("Gate").Render(context.Background(), &output); err != nil {
		t.Fatalf("render layout: %v", err)
	}
	body := output.String()

	preload := strings.Index(body, "window.mayflyTheme")
	if preload < 0 {
		t.Fatal("layout renders no pre-paint theme script")
	}

	bundle := strings.Index(body, BundleURL())
	if bundle < 0 {
		t.Fatal("layout does not link the island bundle")
	}

	if preload > bundle {
		t.Errorf("the pre-paint theme script is emitted after the bundle (%d > %d); it has to run before first paint", preload, bundle)
	}

	// The bundle is a deferred module script, which is exactly why it cannot
	// take the pre-paint job over.
	if !strings.Contains(body, `<script type="module"`) {
		t.Error("the bundle is not loaded as a module script; the deferral this exception depends on may have changed")
	}
}

// TestOnlyTheLayoutLinksTheIslandBundle keeps the bundle linked once, from the
// shell. Task 18.3 moved the link into Layout because the theme switch is
// chrome on every page, but left five per-page call sites behind; they were
// inert -- one module URL resolves to one entry in the module map -- and they
// were also the last thing in the tree still implying the bundle was opt-in per
// page. Task 18.7 removed them. Restoring one is not a bug in the browser, but
// it is a lie in the source, so this fails on it.
func TestOnlyTheLayoutLinksTheIslandBundle(t *testing.T) {
	t.Parallel()

	callers := map[string]int{}

	for _, file := range uiSourceFiles(t) {
		if !strings.HasSuffix(file, ".templ") {
			continue
		}

		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}

		count := strings.Count(blankCommentLines(string(content)), "@IslandBundle()")
		if count > 0 {
			callers[filepath.Base(file)] = count
		}
	}

	for file, count := range callers {
		if file != theExceptionFile {
			t.Errorf("%s calls @IslandBundle() %d time(s); Layout links the bundle for every page already", file, count)
		}
	}

	if callers[theExceptionFile] != 1 {
		t.Errorf("%s calls @IslandBundle() %d time(s), want exactly 1", theExceptionFile, callers[theExceptionFile])
	}
}
