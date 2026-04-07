package agent

import (
	"strings"
	"testing"
)

func TestValidateSelfImproveArchitecturePolicyRejectsNewNativeTool(t *testing.T) {
	t.Parallel()

	err := validateSelfImproveArchitecturePolicy([]selfImproveChange{
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

func TestValidateSelfImproveArchitecturePolicyAllowsMCPEntryPoints(t *testing.T) {
	t.Parallel()

	err := validateSelfImproveArchitecturePolicy([]selfImproveChange{
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
