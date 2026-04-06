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
					ModelName: "gpt-5-mini",
					Routing: &config.RoutingConfig{
						PaidTier: "heavy",
						Tiers: []config.RoutingTierConfig{
							{
								Name: "fast",
								Model: &config.AgentModelConfig{
									Primary: "gpt-5.4-nano",
								},
							},
							{
								Name: "tools",
								Model: &config.AgentModelConfig{
									Primary: "gpt-5.4-mini",
								},
							},
							{
								Name: "heavy",
								Model: &config.AgentModelConfig{
									Primary: "gpt-5-mini",
								},
							},
						},
					},
				},
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
	if armed != "tier:heavy" {
		t.Fatalf("armed=%q, want %q", armed, "tier:heavy")
	}
	if reply != "Boost armed. Next message will use gpt-5-mini." {
		t.Fatalf("reply=%q, want boost confirmation", reply)
	}
}

func TestPaidCommand_SetsPersistentModel(t *testing.T) {
	rt := newModeTestRuntime()
	var persistent string
	workMode := "code"
	rt.SetSessionModelMode = func(value string) error {
		persistent = value
		return nil
	}
	rt.ClearSessionWorkMode = func() error {
		workMode = ""
		return nil
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/paid",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if persistent != "tier:heavy" {
		t.Fatalf("persistent=%q, want %q", persistent, "tier:heavy")
	}
	if workMode != "" {
		t.Fatalf("workMode=%q, want cleared", workMode)
	}
	if reply != "Legacy paid mode set to heavy (gpt-5-mini)." {
		t.Fatalf("reply=%q, want paid confirmation", reply)
	}
}

func TestCodeCommand_SetsWorkModeAndPaidModel(t *testing.T) {
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
	if persistent != "tier:tools" {
		t.Fatalf("persistent=%q, want %q", persistent, "tier:tools")
	}
	if workMode != "code" {
		t.Fatalf("workMode=%q, want %q", workMode, "code")
	}
	if reply != "Session mode set to code (gpt-5.4-mini)." {
		t.Fatalf("reply=%q, want code confirmation", reply)
	}
}

func TestSessionModeTargets_DefaultConfigAlignsWithRoutingDefaults(t *testing.T) {
	rt := &Runtime{Config: config.DefaultConfig()}

	targets := sessionModeTargets(rt)

	if targets.Fast.Target != "tier:fast" || targets.Fast.Label != "gpt-5.4-nano" {
		t.Fatalf("fast=%+v, want tier:fast / gpt-5.4-nano", targets.Fast)
	}
	if targets.Tools.Target != "tier:tools" || targets.Tools.Label != "gpt-5.4-mini" {
		t.Fatalf("tools=%+v, want tier:tools / gpt-5.4-mini", targets.Tools)
	}
	if targets.Heavy.Target != "tier:heavy" || targets.Heavy.Label != "gpt-5-mini" {
		t.Fatalf("heavy=%+v, want tier:heavy / gpt-5-mini", targets.Heavy)
	}
}

func TestDefaultCommand_ClearsSessionModel(t *testing.T) {
	rt := newModeTestRuntime()
	persistent := "tier:heavy"
	pending := "tier:heavy"
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
	if reply != "Session mode set to route." {
		t.Fatalf("reply=%q, want default confirmation", reply)
	}
}

func TestRouteCommand_ClearsSessionModel(t *testing.T) {
	rt := newModeTestRuntime()
	cleared := false
	rt.ClearSessionModelMode = func() error {
		cleared = true
		return nil
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/route",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !cleared {
		t.Fatal("expected session mode to be cleared")
	}
	if reply != "Session mode set to route." {
		t.Fatalf("reply=%q, want route confirmation", reply)
	}
}

func TestStatusCommand_ReportsPendingBoost(t *testing.T) {
	rt := newModeTestRuntime()
	rt.GetModelInfo = func() (string, string) {
		return "gpt-5.4-mini", "openai"
	}
	rt.GetSessionModelMode = func() (string, string) {
		return "tier:tools", "tier:heavy"
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
		"Session Mode: boost armed for next message (gpt-5-mini)",
		"Work Mode: code",
		"Pending Boost: gpt-5-mini",
		"Fast Model: gpt-5.4-nano",
		"Heavy Model: gpt-5-mini",
		"Tools Model: gpt-5.4-mini",
	}) {
		t.Fatalf("reply=%q, missing expected status content", reply)
	}
}

func containsAll(text string, want []string) bool {
	for _, s := range want {
		if !strings.Contains(text, s) {
			return false
		}
	}
	return true
}
