package commands

import (
	"context"
	"strings"
	"testing"
)

func TestSelfImproveDefaultCapturesBrief(t *testing.T) {
	var captured string
	rt := &Runtime{
		SelfImproveActivate: func() (*CodexSessionInfo, error) {
			return &CodexSessionInfo{
				ID:       "cx-1",
				Slug:     "skezza-picoclaw",
				RepoPath: "/repo/picoclaw",
				RepoURL:  "git@github.com:Skezza/picoclaw.git",
			}, nil
		},
		SelfImproveCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
		SelfImproveStatus: func() (string, error) {
			return "Self-improve is configured.\nHost: tiny-m73\nTargets: m73", nil
		},
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve remove dead skills from discovery",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if captured != "remove dead skills from discovery" {
		t.Fatalf("captured=%q", captured)
	}
	for _, want := range []string{"Self-improve is configured.", "Captured brief:", "/self-improve execute"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestSelfImproveExecute_UsesCallback(t *testing.T) {
	var captured, executed string
	rt := &Runtime{
		SelfImproveActivate: func() (*CodexSessionInfo, error) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "skezza-picoclaw", RepoPath: "/repo/picoclaw"}, nil
		},
		SelfImproveCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
		SelfImproveExecute: func(_ context.Context, brief string) (string, error) {
			executed = brief
			return "Starting self-improve run run-1.\nTask: " + brief, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve execute tighten skill filtering",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if captured != "tighten skill filtering" || executed != "tighten skill filtering" {
		t.Fatalf("captured/executed = %q/%q", captured, executed)
	}
	if !strings.Contains(reply, "Starting self-improve run") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSelfImproveDeployRequiresTarget(t *testing.T) {
	rt := &Runtime{
		SelfImproveDeploy: func(target string) (string, error) { return "", nil },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve deploy",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if reply != "Usage: /self-improve deploy <target>" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSelfImproveExecute_RuntimeTasksAreRedirected(t *testing.T) {
	rt := &Runtime{
		SelfImproveActivate: func() (*CodexSessionInfo, error) {
			return &CodexSessionInfo{ID: "cx-1", Slug: "skezza-picoclaw", RepoPath: "/repo/picoclaw"}, nil
		},
		SelfImproveCaptureBrief: func(brief string) error { return nil },
		SelfImproveStatus: func() (string, error) {
			return "Self-improve is configured.\nCaptured brief: rotate the Home Assistant token in config.json", nil
		},
		SelfImproveExecute: func(_ context.Context, brief string) (string, error) {
			return "should not run", nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Text:  "/self-improve execute",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if !strings.Contains(reply, "/runtime") {
		t.Fatalf("reply = %q", reply)
	}
}
