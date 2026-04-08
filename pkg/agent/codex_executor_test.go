package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolveCodexExecutorModel_IgnoresDeployScriptRuntime(t *testing.T) {
	al := &AgentLoop{
		cfg: &config.Config{
			ModelList: []*config.ModelConfig{
				{ModelName: "codex-cli-local", Model: "codex-cli/codex"},
			},
		},
	}

	got := al.resolveCodexExecutorModel(codexSessionRuntimeState{ExecutorModel: "deploy-script"})
	if got != "codex-cli-local" {
		t.Fatalf("executor model=%q, want codex-cli-local", got)
	}
}

