package commands

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeDefaultCapturesBrief(t *testing.T) {
	var captured string
	var reply string
	rt := &Runtime{
		RuntimeCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
		RuntimeStatus: func() (string, error) {
			return "Runtime context is ready.", nil
		},
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	res := ex.Execute(context.Background(), Request{
		Text:  "/runtime update the local service env",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if captured != "update the local service env" {
		t.Fatalf("captured = %q", captured)
	}
	if !strings.Contains(reply, "Captured brief: update the local service env") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestRuntimeExecuteUsesCallback(t *testing.T) {
	var captured string
	var executed string
	var reply string
	rt := &Runtime{
		RuntimeCaptureBrief: func(brief string) error {
			captured = brief
			return nil
		},
		RuntimeExecute: func(_ context.Context, brief string) (string, error) {
			executed = brief
			return "runtime run started", nil
		},
	}

	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)
	res := ex.Execute(context.Background(), Request{
		Text:  "/runtime execute rotate the service token",
		Reply: func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("Outcome = %v, want handled", res.Outcome)
	}
	if captured != "rotate the service token" {
		t.Fatalf("captured = %q", captured)
	}
	if executed != "rotate the service token" {
		t.Fatalf("executed = %q", executed)
	}
	if reply != "runtime run started" {
		t.Fatalf("reply = %q", reply)
	}
}
