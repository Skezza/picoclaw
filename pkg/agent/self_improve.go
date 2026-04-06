package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

const (
	selfImproveDefaultHealthURL = "http://127.0.0.1:18790/ready"
	selfImproveValidateTimeout  = 12 * time.Minute
)

func (al *AgentLoop) selfImproveTargets(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	targets := make([]string, 0, len(cfg.SelfImprove.Targets))
	for name, target := range cfg.SelfImprove.Targets {
		if !target.Enabled {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		targets = append(targets, name)
	}
	sort.Strings(targets)
	return targets
}

func (al *AgentLoop) ensureSelfImproveSession(scopeKey string, cfg *config.Config, agent *AgentInstance) (*codexSessionRecord, error) {
	if al == nil || al.codexStore == nil {
		return nil, fmt.Errorf("self-improve sessions are not initialized")
	}
	if cfg == nil || !cfg.SelfImprove.Enabled {
		return nil, fmt.Errorf("self-improve is disabled")
	}
	repo := strings.TrimSpace(cfg.SelfImprove.Repo)
	if repo == "" {
		return nil, fmt.Errorf("self-improve repo is not configured")
	}

	executorModel := strings.TrimSpace(al.findCodexModelName(cfg))
	if executorModel == "" {
		return nil, fmt.Errorf("no codex-cli model configured in model_list")
	}
	plannerModel := strings.TrimSpace(al.findCodexPlannerModelName(cfg))
	if plannerModel == "" {
		return nil, fmt.Errorf("no planner model configured for /codex")
	}

	if agent != nil {
		if _, _, _, err := al.resolveModelSelection(cfg, agent, executorModel, ""); err != nil {
			return nil, err
		}
		if _, _, _, err := al.resolveModelSelection(cfg, agent, plannerModel, ""); err != nil {
			return nil, err
		}
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, fmt.Errorf("codex binary not found on host; install Codex CLI first")
	}

	slug, source, err := selfImproveRepoSpec(cfg.SelfImprove)
	if err != nil {
		return nil, err
	}

	rec, err := al.codexStore.CreateOrActivate(scopeKey, slug, source)
	if err != nil {
		return nil, err
	}

	_ = al.codexStore.SetSessionRuntime(scopeKey, codexSessionRuntimeState{
		PlannerModel:  plannerModel,
		ExecutorModel: executorModel,
		WorkMode:      "codex-plan",
		LastRunID:     strings.TrimSpace(rec.LastRunID),
	})
	al.setSessionModelOverride(scopeKey, plannerModel)
	al.clearPendingModelOverride(scopeKey)
	al.clearCodexApprovalPending(scopeKey)
	al.setSessionWorkMode(scopeKey, "codex-plan")

	return rec, nil
}

func selfImproveRepoSpec(cfg config.SelfImproveConfig) (slug, source string, err error) {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		return "", "", fmt.Errorf("self-improve repo is not configured")
	}

	slug, err = sanitizeCodexSlug(repo)
	if err != nil {
		return "", "", err
	}
	if cfg.SSHPreferred && strings.Count(repo, "/") == 1 && !strings.ContainsAny(repo, " \t:@") {
		return slug, fmt.Sprintf("git@github.com:%s.git", repo), nil
	}
	source, err = normalizeRepoSource(repo)
	if err != nil {
		return "", "", err
	}
	return slug, source, nil
}

func (al *AgentLoop) selfImproveStatus(scopeKey string, cfg *config.Config, agent *AgentInstance) (string, error) {
	if cfg == nil || !cfg.SelfImprove.Enabled {
		return "", fmt.Errorf("self-improve is disabled")
	}
	rec, err := al.ensureSelfImproveSession(scopeKey, cfg, agent)
	if err != nil {
		return "", err
	}

	lines := []string{
		"Self-improve is configured.",
		fmt.Sprintf("Repo: %s", strings.TrimSpace(cfg.SelfImprove.Repo)),
		fmt.Sprintf("Session: %s", rec.ID),
		fmt.Sprintf("Path: %s", rec.RepoPath),
	}
	targets := al.selfImproveTargets(cfg)
	if len(targets) > 0 {
		lines = append(lines, "Targets: "+strings.Join(targets, ", "))
	}

	runs := al.codexStore.ListRuns(scopeKey)
	if len(runs) == 0 {
		lines = append(lines, "Latest run: none")
		return strings.Join(lines, "\n"), nil
	}
	run := runs[0]
	lines = append(lines,
		fmt.Sprintf("Latest run: %s", run.ID),
		fmt.Sprintf("Run status: %s", run.Status),
	)
	if strings.TrimSpace(run.PublishedBranch) != "" {
		lines = append(lines, fmt.Sprintf("Published branch: %s", run.PublishedBranch))
	}
	if strings.TrimSpace(run.PublishedSHA) != "" {
		lines = append(lines, fmt.Sprintf("Published sha: %s", run.PublishedSHA))
	}
	if strings.TrimSpace(run.ShippedTarget) != "" {
		lines = append(lines, fmt.Sprintf("Shipped target: %s", run.ShippedTarget))
	}
	if strings.TrimSpace(run.ShippedDeployBranch) != "" {
		lines = append(lines, fmt.Sprintf("Deploy branch: %s", run.ShippedDeployBranch))
	}
	if strings.TrimSpace(run.ShippedSHA) != "" {
		lines = append(lines, fmt.Sprintf("Shipped sha: %s", run.ShippedSHA))
	}
	return strings.Join(lines, "\n"), nil
}

func (al *AgentLoop) selfImproveShip(scopeKey string, cfg *config.Config, agent *AgentInstance, targetName string) (string, error) {
	if cfg == nil || !cfg.SelfImprove.Enabled {
		return "", fmt.Errorf("self-improve is disabled")
	}
	rec, err := al.ensureSelfImproveSession(scopeKey, cfg, agent)
	if err != nil {
		return "", err
	}

	targetName = strings.TrimSpace(targetName)
	target, ok := cfg.SelfImprove.Targets[targetName]
	if !ok || !target.Enabled {
		return "", fmt.Errorf("unknown self-improve target %q", targetName)
	}
	deployBranch := strings.TrimSpace(target.DeployBranch)
	if deployBranch == "" {
		return "", fmt.Errorf("self-improve target %q does not define a deploy_branch", targetName)
	}

	run, err := al.latestSuccessfulSelfImproveRun(scopeKey)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(run.WorktreePath) == "" || !isGitRepo(run.WorktreePath) {
		return "", fmt.Errorf("latest successful self-improve run does not have a valid worktree")
	}

	if err := ensureGitRemoteForSelfImprove(rec.RepoPath, cfg.SelfImprove); err != nil {
		return "", err
	}
	if err := validateSelfImproveWorktree(run.WorktreePath); err != nil {
		return "", err
	}

	if dirty, err := gitOutput(run.WorktreePath, "status", "--porcelain"); err != nil {
		return "", err
	} else if strings.TrimSpace(dirty) != "" {
		if _, err := gitOutput(run.WorktreePath, "add", "-A"); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("self-improve: publish %s for %s", run.ID, targetName)
		if _, err := gitOutput(run.WorktreePath, "commit", "-m", msg); err != nil {
			return "", err
		}
	}

	sha, err := gitOutput(run.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return "", fmt.Errorf("failed to resolve published commit sha")
	}

	branchPrefix := strings.Trim(strings.TrimSpace(cfg.SelfImprove.PublishBranchPrefix), "/")
	if branchPrefix == "" {
		branchPrefix = "self-improve"
	}
	publishBranch := fmt.Sprintf("%s/%s/%s", branchPrefix, rec.Slug, run.ID)
	if _, err := gitOutput(run.WorktreePath, "push", "origin", fmt.Sprintf("HEAD:refs/heads/%s", publishBranch)); err != nil {
		return "", err
	}
	if _, err := gitOutput(run.WorktreePath, "push", "origin", fmt.Sprintf("%s:refs/heads/%s", sha, deployBranch)); err != nil {
		return "", fmt.Errorf("failed to fast-forward deploy branch %s: %w", deployBranch, err)
	}

	if err := al.codexStore.UpdateRun(run.ID, func(rec *codexRunRecord) {
		rec.PublishedBranch = publishBranch
		rec.PublishedSHA = sha
		rec.ShippedTarget = targetName
		rec.ShippedDeployBranch = deployBranch
		rec.ShippedSHA = sha
	}); err != nil {
		return "", err
	}

	return strings.Join([]string{
		fmt.Sprintf("Self-improve publish complete for target %s.", targetName),
		fmt.Sprintf("Repo: %s", rec.Slug),
		fmt.Sprintf("Run: %s", run.ID),
		fmt.Sprintf("Published branch: %s", publishBranch),
		fmt.Sprintf("Deploy branch: %s", deployBranch),
		fmt.Sprintf("Commit: %s", sha),
	}, "\n"), nil
}

func (al *AgentLoop) latestSuccessfulSelfImproveRun(scopeKey string) (*codexRunRecord, error) {
	runs := al.codexStore.ListRuns(scopeKey)
	for i := range runs {
		run := runs[i]
		if !strings.EqualFold(strings.TrimSpace(run.Status), codexRunStatusSucceeded) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(run.Mode), "deploy") {
			continue
		}
		return &run, nil
	}
	return nil, fmt.Errorf("no successful self-improve run is ready to ship yet")
}

func ensureGitRemoteForSelfImprove(repoPath string, cfg config.SelfImproveConfig) error {
	if !cfg.SSHPreferred {
		return nil
	}
	repo := strings.TrimSpace(cfg.Repo)
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " \t:@") {
		return nil
	}
	expected := fmt.Sprintf("git@github.com:%s.git", repo)
	current, err := gitOutput(repoPath, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if strings.TrimSpace(current) == expected {
		return nil
	}
	_, err = gitOutput(repoPath, "remote", "set-url", "origin", expected)
	return err
}

func validateSelfImproveWorktree(worktree string) error {
	ctx, cancel := context.WithTimeout(context.Background(), selfImproveValidateTimeout)
	defer cancel()

	tmpDir := filepath.Join(worktree, ".tmp", "self-improve")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	goBin, err := resolveSelfImproveBinary("go")
	if err != nil {
		return err
	}
	baseEnv := selfImproveCommandEnv()

	steps := [][]string{
		{goBin, "test", "./pkg/agent", "./pkg/config", "./pkg/tools"},
		{goBin, "generate", "./..."},
		{goBin, "build", "-v", "-tags", "goolm,stdjson", "-o", filepath.Join(tmpDir, "picoclaw"), "./cmd/picoclaw"},
		{goBin, "build", "-v", "-tags", "goolm,stdjson", "-o", filepath.Join(tmpDir, "picoclaw-launcher"), "./web/backend"},
	}
	if dirExists(filepath.Join(worktree, "cmd", "picoclaw-mcp-fs")) {
		steps = append(steps, []string{goBin, "build", "-v", "-tags", "goolm,stdjson", "-o", filepath.Join(tmpDir, "picoclaw-mcp-fs"), "./cmd/picoclaw-mcp-fs"})
	}
	for _, step := range steps {
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		cmd.Dir = worktree
		cmd.Env = append(baseEnv, "GIT_TERMINAL_PROMPT=0", "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %w: %s", strings.Join(step, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func resolveSelfImproveBinary(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("binary name is required")
	}
	if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidate := filepath.Join(home, ".local", "bin", name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s binary not found in PATH or ~/.local/bin", name)
}

func selfImproveCommandEnv() []string {
	env := append([]string(nil), os.Environ()...)
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		localBin := filepath.Join(home, ".local", "bin")
		hasPath := false
		for i, entry := range env {
			if !strings.HasPrefix(entry, "PATH=") {
				continue
			}
			hasPath = true
			current := strings.TrimPrefix(entry, "PATH=")
			if current == "" {
				env[i] = "PATH=" + localBin
			} else if !strings.Contains(current, localBin) {
				env[i] = "PATH=" + localBin + string(os.PathListSeparator) + current
			}
			break
		}
		if !hasPath {
			env = append(env, "PATH="+localBin)
		}
	}
	return env
}

func gitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), text)
	}
	return text, nil
}
