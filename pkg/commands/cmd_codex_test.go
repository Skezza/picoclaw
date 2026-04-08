package commands

import (
	"context"
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
	var captured string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "picoclaw", RepoPath: "/repo/picoclaw"}, true
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
	var captured, executed string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "picoclaw", RepoPath: "/repo/picoclaw"}, true
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
