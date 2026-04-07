package tools

import (
	"context"
	"strings"
	"testing"
)

type fakeGitRunner struct {
	lastDir  string
	lastArgs []string
	output   string
	err      error
}

func (r *fakeGitRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	r.lastDir = dir
	r.lastArgs = append([]string(nil), args...)
	return r.output, r.err
}

func TestGitTool_BlocksMutatingActionsInCodexPlanningMode(t *testing.T) {
	tmpDir := t.TempDir()
	runner := &fakeGitRunner{}
	tool, err := newGitTool(tmpDir, false, 0, runner)
	if err != nil {
		t.Fatalf("newGitTool() error = %v", err)
	}

	ctx := WithToolWorkMode(context.Background(), "codex-plan")
	result := tool.Execute(ctx, map[string]any{
		"action": "commit",
		"path":   tmpDir,
	})
	if result == nil {
		t.Fatal("expected result")
	}
	if !result.IsError {
		t.Fatal("expected mutating git action to be rejected in planning mode")
	}
	if !strings.Contains(result.ForLLM, `git action "commit" is not allowed`) {
		t.Fatalf("unexpected error: %q", result.ForLLM)
	}
	if len(runner.lastArgs) != 0 {
		t.Fatalf("runner should not be invoked, got args=%v", runner.lastArgs)
	}
}

func TestGitTool_AllowsReadOnlyActionsInCodexPlanningMode(t *testing.T) {
	tmpDir := t.TempDir()
	runner := &fakeGitRunner{output: "main\n"}
	tool, err := newGitTool(tmpDir, false, 0, runner)
	if err != nil {
		t.Fatalf("newGitTool() error = %v", err)
	}

	ctx := WithToolWorkMode(context.Background(), "codex-plan")
	result := tool.Execute(ctx, map[string]any{
		"action": "branch",
		"path":   tmpDir,
	})
	if result == nil {
		t.Fatal("expected result")
	}
	if result.IsError {
		t.Fatalf("expected read-only git action to be allowed, got error %q", result.ForLLM)
	}
	if len(runner.lastArgs) == 0 {
		t.Fatal("expected runner to be invoked")
	}
}
