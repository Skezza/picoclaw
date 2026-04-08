package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexNew_ActivatesSession(t *testing.T) {
	var gotSlug, gotSource string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			gotSlug, gotSource = slug, source
			return &CodexSessionInfo{
				ID:       "abc123",
				Slug:     "acme-repo",
				RepoPath: "/workspace/repos/acme-repo",
				RepoURL:  "https://github.com/acme/repo.git",
			}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex new acme/repo",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	if gotSlug != "acme-repo" || gotSource != "acme/repo" {
		t.Fatalf("got slug/source = %q/%q", gotSlug, gotSource)
	}
	for _, want := range []string{"Codex session is ready.", "Repo: acme-repo", "Talk normally"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestCodexStatus_ShowsDiscussionPhase(t *testing.T) {
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, true
		},
		FindCodexModel:     func() string { return "gpt-5.4-mini" },
		GetSessionWorkMode: func() string { return "codex-plan" },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex status",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	for _, want := range []string{"Repo: picoclaw", "Phase: discussion"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestCodexBareHandlerCapturesBrief(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "picoclaw")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	var captured string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "picoclaw", RepoPath: repoPath}, true
		},
		CodexCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex update the readme for Home Assistant MCP setup",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	if captured != "update the readme for Home Assistant MCP setup" {
		t.Fatalf("captured=%q", captured)
	}
	for _, want := range []string{"Codex conversational mode is ready.", "Captured brief:", "/codex execute"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestCodexExecute_UsesCallback(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "picoclaw")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	var captured, executed string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "picoclaw", RepoPath: repoPath}, true
		},
		CodexCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
		CodexExecute: func(_ context.Context, brief string) (string, error) {
			executed = brief
			return "Starting Codex run run-1.\nTask: " + brief, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex execute update README and add tests",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	if captured != "update README and add tests" || executed != "update README and add tests" {
		t.Fatalf("captured/executed = %q/%q", captured, executed)
	}
	if !strings.Contains(reply, "Starting Codex run") {
		t.Fatalf("reply=%q", reply)
	}
}

func TestCodexGuide_ShowsExecuteFlow(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex guide",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	for _, want := range []string{"/codex execute", "Talk normally", "/codex new owner/repo"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestCodexExecute_IgnoresStaleActiveSessionAndFallsBack(t *testing.T) {
	tmp := t.TempDir()
	staleRepo := filepath.Join(tmp, "stale")
	if err := os.MkdirAll(staleRepo, 0o755); err != nil {
		t.Fatalf("mkdir stale repo: %v", err)
	}
	validRepo := filepath.Join(tmp, "valid")
	if err := os.MkdirAll(filepath.Join(validRepo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir valid repo: %v", err)
	}

	attached := ""
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "stale", Slug: "test", RepoPath: staleRepo, Active: true}, true
		},
		CodexListSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{{ID: "good", Slug: "skezza-picoclaw", RepoPath: validRepo}}
		},
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			attached = ref
			return &CodexSessionInfo{ID: "good", Slug: "skezza-picoclaw", RepoPath: validRepo, Active: true}, nil
		},
		CodexExecute: func(_ context.Context, brief string) (string, error) {
			return "Starting Codex run run-1.\nTask: " + brief, nil
		},
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex execute",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	if attached != "good" {
		t.Fatalf("attached=%q, want good", attached)
	}
	if !strings.Contains(reply, "Starting Codex run") {
		t.Fatalf("reply=%q", reply)
	}
}

func TestCodexExecute_StaleActiveSessionWithoutFallbackPromptsNewSession(t *testing.T) {
	tmp := t.TempDir()
	staleRepo := filepath.Join(tmp, "stale")
	if err := os.MkdirAll(staleRepo, 0o755); err != nil {
		t.Fatalf("mkdir stale repo: %v", err)
	}

	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "stale", Slug: "test", RepoPath: staleRepo, Active: true}, true
		},
		CodexListSessions: func() []CodexSessionInfo { return nil },
		CodexExecute: func(_ context.Context, _ string) (string, error) {
			t.Fatal("CodexExecute should not run when no valid session is available")
			return "", nil
		},
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex execute",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want handled", res.Outcome)
	}
	if !strings.Contains(reply, "Ignored stale active codex session") {
		t.Fatalf("reply=%q missing stale-session guidance", reply)
	}
	if !strings.Contains(reply, "/codex new owner/repo") {
		t.Fatalf("reply=%q missing new-session guidance", reply)
	}
}
