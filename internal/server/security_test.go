package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustedLocalOriginPolicy(t *testing.T) {
	server := NewServer("localhost:8080", nil)

	tests := []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "untrusted browser", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "same origin", origin: "http://mayfly.local", wantStatus: http.StatusBadRequest},
		{name: "non browser client", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://mayfly.local/api/v1/jobs", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")

			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}

			response := httptest.NewRecorder()

			server.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}

			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("server emitted an unsafe CORS allow-origin header")
			}
		})
	}
}

func TestInputPolicyRejectsTraversalAndSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideImage := filepath.Join(outside, "outside.png")
	createSimpleTestImage(t, outsideImage)

	insideImage := filepath.Join(root, "inside.png")
	createSimpleTestImage(t, insideImage)

	link := filepath.Join(root, "escape.png")
	if err := os.Symlink(outsideImage, link); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not image"), 0o644); err != nil {
		t.Fatal(err)
	}

	policy, err := newInputPolicy([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := policy.resolveImage(insideImage)
	if err != nil || resolved != insideImage {
		t.Fatalf("valid image resolved to %q: %v", resolved, err)
	}

	for _, path := range []string{
		outsideImage,
		link,
		filepath.Join(root, "..", filepath.Base(outside), "outside.png"),
		filepath.Join(root, "notes.txt"),
	} {
		if _, err := policy.resolveImage(path); err == nil {
			t.Fatalf("escape path %q was accepted", path)
		}
	}
}

func TestCreateJobRejectsInvalidPayloads(t *testing.T) {
	tmpDir := t.TempDir()
	validImage := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, validImage)
	outside := t.TempDir()
	outsideImage := filepath.Join(outside, "outside.png")
	createSimpleTestImage(t, outsideImage)

	nonImage := filepath.Join(tmpDir, "notes.txt")

	err := os.WriteFile(nonImage, []byte("not image"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:8080", nil, ServerOptions{InputRoots: []string{tmpDir}})
	tests := []struct {
		name string
		body map[string]any
		code string
		want string
	}{
		{
			name: "missing refPath",
			body: map[string]any{"mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_config",
			want: "refPath is required",
		},
		{
			name: "invalid mode",
			body: map[string]any{"refPath": validImage, "mode": "invalid", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_config",
			want: "mode",
		},
		{
			name: "invalid circles",
			body: map[string]any{"refPath": validImage, "mode": "joint", "circles": 0, "iters": 100, "popSize": 30},
			code: "invalid_config",
			want: "circles",
		},
		{
			name: "invalid iters",
			body: map[string]any{"refPath": validImage, "mode": "joint", "circles": 10, "iters": 0, "popSize": 30},
			code: "invalid_config",
			want: "iters",
		},
		{
			name: "invalid popSize",
			body: map[string]any{"refPath": validImage, "mode": "joint", "circles": 10, "iters": 100, "popSize": 1},
			code: "invalid_config",
			want: "popSize",
		},
		{
			name: "outside root",
			body: map[string]any{"refPath": outsideImage, "mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_ref_path",
		},
		{
			name: "outside root via traversal",
			body: map[string]any{"refPath": filepath.Join(tmpDir, "..", filepath.Base(outside), "outside.png"), "mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_ref_path",
		},
		{
			name: "non image",
			body: map[string]any{"refPath": nonImage, "mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_ref_path",
			want: "image format is invalid",
		},
		{
			name: "outside canvas path",
			body: map[string]any{"refPath": validImage, "canvasPath": outsideImage, "mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_canvas_path",
		},
		{
			name: "non-image canvas path",
			body: map[string]any{"refPath": validImage, "canvasPath": nonImage, "mode": "joint", "circles": 10, "iters": 100, "popSize": 30},
			code: "invalid_canvas_path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatalf("json.Marshal(%v): %v", tt.body, err)
			}

			request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
			response := httptest.NewRecorder()
			server.handleCreateJob(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}

			var decoded apiErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("response %q is not the API error envelope: %v", response.Body.String(), err)
			}

			if decoded.Error.Code != tt.code {
				t.Fatalf("error code = %q, want %q", decoded.Error.Code, tt.code)
			}

			if tt.want != "" && !strings.Contains(decoded.Error.Message, tt.want) {
				t.Fatalf("error message = %q, want substring %q", decoded.Error.Message, tt.want)
			}
		})
	}
}

func TestPprofDisabledByDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://mayfly.local/debug/pprof/", nil)

	response := httptest.NewRecorder()
	NewServer("localhost:8080", nil).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("default pprof status = %d, want 404", response.Code)
	}

	response = httptest.NewRecorder()
	NewServerWithOptions("localhost:8080", nil, ServerOptions{EnablePprof: true}).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("enabled pprof status = %d, want 200", response.Code)
	}
}

func TestCreateJobRejectsUnknownAndTrailingJSON(t *testing.T) {
	server := NewServer("localhost:8080", nil)

	tests := []string{
		`{"refPath":"ref.png","unknown":true}`,
		`{"refPath":"ref.png"} {"refPath":"other.png"}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, response.Code)
		}
	}
}

func TestAPIMethodResponseIncludesAllow(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs", nil)
	response := httptest.NewRecorder()
	NewServer("localhost:8080", nil).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}

	if got := response.Header().Get("Allow"); got != "GET, POST" {
		t.Fatalf("Allow = %q, want GET, POST", got)
	}
}

func TestCreateJobRejectsOversizedBody(t *testing.T) {
	body := `{"refPath":"` + strings.Repeat("x", 1<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	response := httptest.NewRecorder()
	NewServer("localhost:8080", nil).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", response.Code, response.Body.String())
	}
}
