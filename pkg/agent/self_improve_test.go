package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSelfImproveArchitecturePolicyRejectsNewNativeTool(t *testing.T) {
	t.Parallel()

	err := validateSelfImproveArchitecturePolicy(t.TempDir(), []selfImproveChange{
		{Status: "A", Path: "pkg/tools/home_assistant.go", Committed: true},
		{Status: "??", Path: "pkg/tools/outlook.go"},
	})
	if err == nil {
		t.Fatal("expected policy violation")
	}
	text := err.Error()
	for _, want := range []string{
		"pkg/tools/home_assistant.go",
		"pkg/tools/outlook.go",
		"cmd/picoclaw-mcp-*",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}

func TestValidateSelfImproveArchitecturePolicyRejectsMissingMCPMain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "picoclaw-mcp-ebay"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	err := validateSelfImproveArchitecturePolicy(root, []selfImproveChange{
		{Status: "A", Path: "cmd/picoclaw-mcp-ebay/client.go", Committed: true},
	})
	if err == nil || !strings.Contains(err.Error(), "cmd/picoclaw-mcp-ebay/main.go (missing)") {
		t.Fatalf("validateSelfImproveArchitecturePolicy error = %v", err)
	}
}

func TestValidateSelfImproveArchitecturePolicyAllowsMCPEntryPoints(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "picoclaw-mcp-homeassistant"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "picoclaw-mcp-homeassistant", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	err := validateSelfImproveArchitecturePolicy(root, []selfImproveChange{
		{Status: "A", Path: "cmd/picoclaw-mcp-homeassistant/main.go", Committed: true},
		{Status: "A", Path: "internal/homeassistant/client.go", Committed: true},
		{Status: "M", Path: "pkg/tools/mcp_tool.go", Committed: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseStatusPorcelainChangesParsesRenamesAndUntracked(t *testing.T) {
	t.Parallel()

	changes := parseStatusPorcelainChanges("?? pkg/tools/home_assistant.go\nR  old/path.go -> cmd/picoclaw-mcp-homeassistant/main.go\n")
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2", len(changes))
	}
	if changes[0].Status != "??" || changes[0].Path != "pkg/tools/home_assistant.go" {
		t.Fatalf("unexpected first change: %+v", changes[0])
	}
	if changes[1].OldPath != "old/path.go" || changes[1].Path != "cmd/picoclaw-mcp-homeassistant/main.go" {
		t.Fatalf("unexpected second change: %+v", changes[1])
	}
}

func TestSelfImproveStageablePathsRejectsSuspiciousFiles(t *testing.T) {
	t.Parallel()

	_, err := selfImproveStageablePaths([]selfImproveChange{
		{Status: "??", Path: ".env"},
		{Status: " M", Path: "pkg/agent/self_improve.go"},
	})
	if err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("selfImproveStageablePaths error = %v", err)
	}
}

func TestSelfImproveStageablePathsIgnoresGeneratedTmpFiles(t *testing.T) {
	t.Parallel()

	paths, err := selfImproveStageablePaths([]selfImproveChange{
		{Status: "??", Path: ".tmp/self-improve/cache/output"},
		{Status: " M", Path: "pkg/agent/self_improve.go"},
		{Status: "??", Path: "cmd/picoclaw-mcp-ebay/main.go"},
	})
	if err != nil {
		t.Fatalf("selfImproveStageablePaths error = %v", err)
	}
	got := strings.Join(paths, ",")
	if got != "cmd/picoclaw-mcp-ebay/main.go,pkg/agent/self_improve.go" {
		t.Fatalf("selfImproveStageablePaths = %q", got)
	}
}

func TestValidateSelfImproveDeployBranch(t *testing.T) {
	t.Parallel()

	for _, branch := range []string{"deploy/quanta", "deploy/m73"} {
		if err := validateSelfImproveDeployBranch(branch); err != nil {
			t.Fatalf("validateSelfImproveDeployBranch(%q) error = %v", branch, err)
		}
	}

	for _, branch := range []string{"main", "master", "feature/foo", "release/quanta"} {
		if err := validateSelfImproveDeployBranch(branch); err == nil {
			t.Fatalf("validateSelfImproveDeployBranch(%q) unexpectedly succeeded", branch)
		}
	}
}

func TestLatestShippableSelfImproveRunRejectsNewerFailedRun(t *testing.T) {
	t.Parallel()

	store := &codexSessionStore{
		runs: codexRunSnapshot{
			Runs: map[string]*codexRunRecord{
				"failed": {
					ID:        "failed",
					ScopeKey:  "scope",
					Status:    codexRunStatusFailed,
					UpdatedAt: mustSelfImproveParseTime(t, "2026-04-07T22:10:00Z"),
				},
				"ok": {
					ID:        "ok",
					ScopeKey:  "scope",
					Status:    codexRunStatusSucceeded,
					UpdatedAt: mustSelfImproveParseTime(t, "2026-04-07T22:00:00Z"),
				},
			},
		},
	}
	store.normalizeRunStateLocked()

	al := &AgentLoop{codexStore: store}
	if _, err := al.latestShippableSelfImproveRun("scope"); err == nil || !strings.Contains(err.Error(), "latest self-improve run failed is failed") {
		t.Fatalf("latestShippableSelfImproveRun error = %v", err)
	}
}

func TestListSelfImproveMCPBinaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "cmd", "picoclaw-mcp-homeassistant"),
		filepath.Join(root, "cmd", "picoclaw-mcp-ebay"),
		filepath.Join(root, "cmd", "picoclaw"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}

	names, err := listSelfImproveMCPBinaries(root)
	if err != nil {
		t.Fatalf("listSelfImproveMCPBinaries error = %v", err)
	}
	got := strings.Join(names, ",")
	if got != "picoclaw-mcp-ebay,picoclaw-mcp-homeassistant" {
		t.Fatalf("listSelfImproveMCPBinaries = %q", got)
	}
}

func mustSelfImproveParseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("time.Parse(%q) error = %v", raw, err)
	}
	return ts
}
