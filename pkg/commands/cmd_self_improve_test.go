package commands

import (
	"context"
	"strings"
	"testing"
)

func TestSelfImproveDefaultActivatesSession(t *testing.T) {
	rt := &Runtime{
		SelfImproveActivate: func() (*CodexSessionInfo, error) {
			return &CodexSessionInfo{
				ID:       "cx-1",
				Slug:     "skezza-picoclaw",
				RepoPath: "/repo/picoclaw",
				RepoURL:  "git@github.com:Skezza/picoclaw.git",
			}, nil
		},
		FindCodexModel: func() string { return "codex-cli" },
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if !strings.Contains(reply, "Self-improve session is active in planning mode.") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSelfImproveShipRequiresTarget(t *testing.T) {
	rt := &Runtime{
		SelfImproveShip: func(target string) (string, error) { return "", nil },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve ship",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if reply != "Usage: /self-improve ship <target>" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSelfImproveTargets(t *testing.T) {
	rt := &Runtime{
		ListSelfImproveTargets: func() []string {
			return []string{"fallback", "quanta"}
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve targets",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	for _, want := range []string{"fallback", "quanta"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply %q missing %q", reply, want)
		}
	}
}
