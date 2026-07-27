package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/provider"
)

type stubExecutor struct {
	stdout string
	stderr string
	err    error

	gotCmd  string
	gotArgs []string
}

func (s *stubExecutor) Run(_ context.Context, cmd string, args ...string) (string, string, error) {
	s.gotCmd = cmd
	s.gotArgs = append([]string(nil), args...)
	return s.stdout, s.stderr, s.err
}

func (s *stubExecutor) RunWithStdin(ctx context.Context, _ string, cmd string, args ...string) (string, string, error) {
	return s.Run(ctx, cmd, args...)
}

func TestRunCmd_SuccessForwardsArgs(t *testing.T) {
	exec := &stubExecutor{}
	if err := provider.RunCmd(context.Background(), exec, "install", "brew", "install", "ripgrep"); err != nil {
		t.Fatalf("RunCmd: %v", err)
	}
	if exec.gotCmd != "brew" {
		t.Errorf("cmd = %q, want brew", exec.gotCmd)
	}
	if len(exec.gotArgs) != 2 || exec.gotArgs[0] != "install" || exec.gotArgs[1] != "ripgrep" {
		t.Errorf("args = %v, want [install ripgrep]", exec.gotArgs)
	}
}

func TestRunCmd_ErrorIncludesLabelAndStderr(t *testing.T) {
	exec := &stubExecutor{
		stderr: "  package not found\n",
		err:    errors.New("exit 1"),
	}
	err := provider.RunCmd(context.Background(), exec, "install", "brew", "install", "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "install: ") {
		t.Errorf("missing label prefix: %q", msg)
	}
	if !strings.Contains(msg, "exit 1") {
		t.Errorf("missing wrapped err: %q", msg)
	}
	if !strings.Contains(msg, "package not found") {
		t.Errorf("missing trimmed stderr: %q", msg)
	}
	if strings.Contains(msg, "stderr:   package") || strings.Contains(msg, "stderr: package not found\n") {
		t.Errorf("stderr not trimmed: %q", msg)
	}
}

func TestRunCmd_PreservesWrappedError(t *testing.T) {
	sentinel := errors.New("sentinel")
	exec := &stubExecutor{err: sentinel}
	err := provider.RunCmd(context.Background(), exec, "label", "cmd")
	if !errors.Is(err, sentinel) {
		t.Errorf("err chain does not contain sentinel: %v", err)
	}
}

func TestFetchJSON_SuccessDecodesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"ripgrep","version":"14.1.0"}`))
	}))
	defer srv.Close()

	var got struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	status, err := provider.FetchJSON(context.Background(), srv.Client(), srv.URL, &got)
	if err != nil {
		t.Fatalf("FetchJSON: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got.Name != "ripgrep" || got.Version != "14.1.0" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestFetchJSON_NonSuccessStatusDoesNotDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"unexpected":"body"}`))
	}))
	defer srv.Close()

	var got map[string]string
	status, err := provider.FetchJSON(context.Background(), srv.Client(), srv.URL, &got)
	if err != nil {
		t.Fatalf("FetchJSON should not error on non-2xx: %v", err)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	if got != nil {
		t.Errorf("body decoded on non-2xx: %+v", got)
	}
}

func TestFetchJSON_InvalidJSONReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	var got map[string]any
	status, err := provider.FetchJSON(context.Background(), srv.Client(), srv.URL, &got)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (decode error preserves status)", status)
	}
}

func TestFetchJSON_NetworkErrorReturnsZeroStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	var got map[string]any
	client := &http.Client{Timeout: 200 * time.Millisecond}
	status, err := provider.FetchJSON(context.Background(), client, srv.URL, &got)
	if err == nil {
		t.Fatal("expected network error")
	}
	if status != 0 {
		t.Errorf("status = %d, want 0 on transport error", status)
	}
}

func TestFetchJSON_BadURLReturnsRequestError(t *testing.T) {
	var got map[string]any
	status, err := provider.FetchJSON(context.Background(), http.DefaultClient, "://malformed", &got)
	if err == nil {
		t.Fatal("expected request build error")
	}
	if status != 0 {
		t.Errorf("status = %d, want 0 on request build error", status)
	}
}

func TestTool_EffectivePackageDefaultsToName(t *testing.T) {
	tests := []struct {
		name string
		tool provider.Tool
		want string
	}{
		{"empty package falls back to name", provider.Tool{Name: "ripgrep", Provider: "brew"}, "ripgrep"},
		{"explicit package wins", provider.Tool{Name: "rg", Provider: "brew", Package: "ripgrep"}, "ripgrep"},
		{"empty name + empty package", provider.Tool{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tool.EffectivePackage(); got != tt.want {
				t.Errorf("EffectivePackage = %q, want %q", got, tt.want)
			}
		})
	}
}
