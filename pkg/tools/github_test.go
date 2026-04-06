package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNewGitHubTool_UsesEnvFallback(t *testing.T) {
	t.Setenv("GITHUB_MCP_PAT", "env-token")

	tool, err := NewGitHubTool("", "", "", 0)
	if err != nil {
		t.Fatalf("NewGitHubTool() error = %v", err)
	}
	if tool.token != "env-token" {
		t.Fatalf("tool.token = %q, want env-token", tool.token)
	}
	if tool.baseURL != defaultGitHubBaseURL {
		t.Fatalf("tool.baseURL = %q, want %q", tool.baseURL, defaultGitHubBaseURL)
	}
}

func TestGitHubTool_Me(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("path = %q, want /user", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != defaultGitHubAPIVersion {
			t.Fatalf("X-GitHub-Api-Version = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"login":"Skezza",
			"name":"Joe",
			"html_url":"https://github.com/Skezza",
			"bio":"Builder",
			"public_repos":12,
			"total_private_repos":5,
			"followers":3,
			"following":4
		}`)
	}))
	defer server.Close()

	tool, err := NewGitHubTool("test-token", server.URL, "", 5)
	if err != nil {
		t.Fatalf("NewGitHubTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{"action": "me"})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatal("me should be silent")
	}
	if !strings.Contains(result.ForLLM, "Authenticated GitHub user: Skezza") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestGitHubTool_GetFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/Skezza/picoclaw/contents/docs/readme.md"; r.URL.Path != want {
			t.Fatalf("path = %q, want %q", r.URL.Path, want)
		}
		if got := r.URL.Query().Get("ref"); got != "main" {
			t.Fatalf("ref query = %q, want main", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"type":"file",
			"path":"docs/readme.md",
			"size":24,
			"encoding":"base64",
			"content":"SGVsbG8gZnJvbSBHaXRIdWIgZmlsZSEK"
		}`)
	}))
	defer server.Close()

	tool, err := NewGitHubTool("test-token", server.URL, "", 5)
	if err != nil {
		t.Fatalf("NewGitHubTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":    "get_file",
		"repo":      "Skezza/picoclaw",
		"path":      "docs/readme.md",
		"ref":       "main",
		"max_chars": 512,
	})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "File: docs/readme.md") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Hello from GitHub file!") {
		t.Fatalf("ForLLM missing decoded content: %q", result.ForLLM)
	}
}

func TestGitHubTool_GetWorkflowRunLogs(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	fw, err := zw.Create("build/1_Setup.txt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fw.Write([]byte("setup complete\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	fw, err = zw.Create("build/2_Test.txt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fw.Write([]byte("tests passed\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/Skezza/picoclaw/actions/runs/42/logs"; r.URL.Path != want {
			t.Fatalf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/zip")
		if _, err := w.Write(zipBuf.Bytes()); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}))
	defer server.Close()

	tool, err := NewGitHubTool("test-token", server.URL, "", 5)
	if err != nil {
		t.Fatalf("NewGitHubTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":    "get_workflow_run_logs",
		"repo":      "Skezza/picoclaw",
		"run_id":    42,
		"max_chars": 4096,
	})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Workflow logs for Skezza/picoclaw run #42") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "setup complete") || !strings.Contains(result.ForLLM, "tests passed") {
		t.Fatalf("ForLLM missing log contents: %q", result.ForLLM)
	}
}

func TestNewGitHubTool_RequiresToken(t *testing.T) {
	_ = os.Unsetenv("GITHUB_MCP_PAT")
	_ = os.Unsetenv("GITHUB_TOKEN")

	if _, err := NewGitHubTool("", "", "", 0); err == nil {
		t.Fatal("expected missing token error")
	}
}
