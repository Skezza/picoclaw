package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newModeTestRuntime() *Runtime {
	return &Runtime{
		Config: &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					ModelName: "gpt-5.4-mini",
				},
			},
			ModelList: []*config.ModelConfig{
				{ModelName: "gpt-5.4-mini", Model: "openai/gpt-5.4-mini"},
				{ModelName: "gpt-5.4", Model: "openai/gpt-5.4"},
			},
		},
	}
}

func TestBoostCommand_ArmsNextModel(t *testing.T) {
	rt := newModeTestRuntime()
	var armed string
	rt.ArmNextModelMode = func(value string) error {
		armed = value
		return nil
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/boost",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if armed != "gpt-5.4" {
		t.Fatalf("armed=%q, want %q", armed, "gpt-5.4")
	}
	if reply != "Boost armed. Next message will use gpt-5.4." {
		t.Fatalf("reply=%q, want boost confirmation", reply)
	}
}

func TestCodeCommand_SetsWorkModeAndDefaultModel(t *testing.T) {
	rt := newModeTestRuntime()
	var persistent, workMode string
	rt.SetSessionModelMode = func(value string) error {
		persistent = value
		return nil
	}
	rt.SetSessionWorkMode = func(value string) error {
		workMode = value
		return nil
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/code",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if persistent != "gpt-5.4-mini" {
		t.Fatalf("persistent=%q, want %q", persistent, "gpt-5.4-mini")
	}
	if workMode != "code" {
		t.Fatalf("workMode=%q, want %q", workMode, "code")
	}
	if reply != "Session mode set to code (gpt-5.4-mini)." {
		t.Fatalf("reply=%q, want code confirmation", reply)
	}
}

func TestSessionModeTargets_DefaultConfigAlignsWithDirectModels(t *testing.T) {
	rt := &Runtime{Config: config.DefaultConfig()}

	targets := sessionModeTargets(rt)

	if targets.Code.Target != "gpt-5.4-mini" || targets.Code.Label != "gpt-5.4-mini" {
		t.Fatalf("code=%+v, want gpt-5.4-mini", targets.Code)
	}
	if targets.Boost.Target != "gpt-5.4" || targets.Boost.Label != "gpt-5.4" {
		t.Fatalf("boost=%+v, want gpt-5.4", targets.Boost)
	}
}

func TestDefaultCommand_ClearsSessionModel(t *testing.T) {
	rt := newModeTestRuntime()
	persistent := "gpt-5.4"
	pending := "gpt-5.4"
	workMode := "code"
	rt.GetSessionModelMode = func() (string, string) {
		return persistent, pending
	}
	rt.GetSessionWorkMode = func() string { return workMode }
	rt.ClearSessionModelMode = func() error {
		persistent = ""
		pending = ""
		return nil
	}
	rt.ClearSessionWorkMode = func() error {
		workMode = ""
		return nil
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/default",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if persistent != "" || pending != "" || workMode != "" {
		t.Fatalf("persistent=%q pending=%q workMode=%q, want all cleared", persistent, pending, workMode)
	}
	if reply != "Session mode set to default." {
		t.Fatalf("reply=%q, want default confirmation", reply)
	}
}

func TestStatusCommand_ReportsPendingBoost(t *testing.T) {
	rt := newModeTestRuntime()
	rt.GetModelInfo = func() (string, string) {
		return "gpt-5.4-mini", "openai"
	}
	rt.GetSessionModelMode = func() (string, string) {
		return "gpt-5.4-mini", "gpt-5.4"
	}
	rt.GetSessionWorkMode = func() string { return "code" }

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/status",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !containsAll(reply, []string{
		"Current Model: gpt-5.4-mini (Provider: openai)",
		"Session Mode: boost armed for next message (gpt-5.4)",
		"Pending Boost: gpt-5.4",
		"Default Model: gpt-5.4-mini",
		"Boost Model: gpt-5.4",
	}) {
		t.Fatalf("reply missing expected lines:\n%s", reply)
	}
	if strings.Contains(reply, "Fast Model:") || strings.Contains(reply, "Heavy Model:") || strings.Contains(reply, "Tools Model:") {
		t.Fatalf("reply should not mention routing tiers:\n%s", reply)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
