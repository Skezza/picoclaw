package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultGitTimeout = 60 * time.Second

// GitRunner runs git commands. It exists so tests can inject a fake runner.
type GitRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

type osGitRunner struct{}

func (osGitRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Keep git non-interactive and avoid system/global hook/config surprises.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1",
	)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// GitTool wraps the git binary with workspace-scoped path validation.
type GitTool struct {
	workspace           string
	timeout             time.Duration
	restrictToWorkspace bool
	allowedPathPatterns []*regexp.Regexp
	runner              GitRunner
}

func NewGitTool(
	workspace string,
	restrict bool,
	timeoutSeconds int,
	allowPaths ...[]*regexp.Regexp,
) (*GitTool, error) {
	return newGitTool(workspace, restrict, timeoutSeconds, osGitRunner{}, allowPaths...)
}

func newGitTool(
	workspace string,
	restrict bool,
	timeoutSeconds int,
	runner GitRunner,
	allowPaths ...[]*regexp.Regexp,
) (*GitTool, error) {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultGitTimeout
	}
	if runner == nil {
		runner = osGitRunner{}
	}

	return &GitTool{
		workspace:           workspace,
		timeout:             timeout,
		restrictToWorkspace: restrict,
		allowedPathPatterns: patterns,
		runner:              runner,
	}, nil
}

func (t *GitTool) Name() string {
	return "git"
}

func (t *GitTool) Description() string {
	return "Work with git repositories using the git binary. Supports status, branch, log, diff, fetch, pull, checkout, clone, add, commit, and push. Paths are workspace-scoped when workspace restriction is enabled."
}

func (t *GitTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "branch", "log", "diff", "fetch", "pull", "checkout", "clone", "add", "commit", "push"},
				"description": "Git action to run",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Working tree path or repo path for non-clone actions",
			},
			"destination": map[string]any{
				"type":        "string",
				"description": "Destination path for clone",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Clone source URL or local path",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Checkout ref or branch name",
			},
			"remote": map[string]any{
				"type":        "string",
				"description": "Remote name for pull/push (defaults to origin)",
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "Branch name for checkout/pull/push/clone",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Commit message",
			},
			"paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "File paths for add or diff",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum log entries to return",
			},
			"cached": map[string]any{
				"type":        "boolean",
				"description": "Use staged changes for diff",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GitTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	var (
		output string
		err    error
	)

	switch action {
	case "status":
		output, err = t.runInRepo(ctx, args, "status", "--short", "--branch")
	case "branch":
		output, err = t.runInRepo(ctx, args, "branch", "--show-current")
	case "log":
		limit, limitErr := getInt64Arg(args, "limit", 10)
		if limitErr != nil {
			return ErrorResult(limitErr.Error())
		}
		if limit <= 0 {
			limit = 10
		}
		output, err = t.runInRepo(ctx, args, "log", "--oneline", "--decorate", "--graph", "-n", strconv.FormatInt(limit, 10))
	case "diff":
		output, err = t.runDiff(ctx, args)
	case "fetch":
		output, err = t.runFetch(ctx, args)
	case "pull":
		output, err = t.runPull(ctx, args)
	case "checkout":
		output, err = t.runCheckout(ctx, args)
	case "clone":
		output, err = t.runClone(ctx, args)
	case "add":
		output, err = t.runAdd(ctx, args)
	case "commit":
		output, err = t.runCommit(ctx, args)
	case "push":
		output, err = t.runPush(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown git action: %s", action))
	}

	if err != nil {
		msg := output
		if msg == "" {
			msg = err.Error()
		} else if !strings.Contains(msg, err.Error()) {
			msg += "\n" + err.Error()
		}
		return ErrorResult(msg).WithError(err)
	}

	if output == "" {
		output = "(no output)"
	}
	return SilentResult(output)
}

func (t *GitTool) runInRepo(ctx context.Context, args map[string]any, gitArgs ...string) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, gitArgs...)
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) runDiff(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}

	cmdArgs := []string{"-c", "core.hooksPath=/dev/null", "diff"}
	if cached, _ := boolArg(args, "cached"); cached {
		cmdArgs = append(cmdArgs, "--cached")
	}
	paths, err := stringSliceArg(args, "paths")
	if err != nil {
		return "", err
	}
	if len(paths) > 0 {
		cmdArgs = append(cmdArgs, "--")
		cmdArgs = append(cmdArgs, paths...)
	}
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) runFetch(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	remote, err := stringArg(args, "remote", "")
	if err != nil {
		return "", err
	}
	cmdArgs := []string{"-c", "core.hooksPath=/dev/null", "fetch", "--prune"}
	if remote != "" {
		if err := rejectOptionLike(remote, "remote"); err != nil {
			return "", err
		}
		cmdArgs = append(cmdArgs, remote)
	} else {
		cmdArgs = append([]string{"-c", "core.hooksPath=/dev/null", "fetch", "--all", "--prune"})
	}
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) runPull(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	remote, err := stringArg(args, "remote", "origin")
	if err != nil {
		return "", err
	}
	branch, err := stringArg(args, "branch", "")
	if err != nil {
		return "", err
	}
	if remote != "" {
		if err := rejectOptionLike(remote, "remote"); err != nil {
			return "", err
		}
	}
	if branch != "" {
		if err := rejectOptionLike(branch, "branch"); err != nil {
			return "", err
		}
	}
	cmdArgs := []string{"-c", "core.hooksPath=/dev/null", "pull", "--ff-only"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	if branch != "" {
		cmdArgs = append(cmdArgs, branch)
	}
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) runCheckout(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	ref, err := stringArg(args, "ref", "")
	if err != nil {
		return "", err
	}
	if err := rejectOptionLike(ref, "ref"); err != nil {
		return "", err
	}
	return t.runGit(ctx, repoDir, "-c", "core.hooksPath=/dev/null", "checkout", ref)
}

func (t *GitTool) runClone(ctx context.Context, args map[string]any) (string, error) {
	url, err := stringArg(args, "url", "")
	if err != nil {
		return "", err
	}
	if err := rejectOptionLike(url, "url"); err != nil {
		return "", err
	}

	dest, err := stringArg(args, "destination", "")
	if err != nil {
		return "", err
	}
	if err := rejectOptionLike(dest, "destination"); err != nil {
		return "", err
	}
	resolvedDest, err := t.resolvePath(dest, true)
	if err != nil {
		return "", fmt.Errorf("clone destination blocked by safety guard: %w", err)
	}

	branch, err := stringArg(args, "branch", "")
	if err != nil {
		return "", err
	}
	if branch != "" {
		if err := rejectOptionLike(branch, "branch"); err != nil {
			return "", err
		}
	}
	depth, err := getInt64Arg(args, "depth", 0)
	if err != nil {
		return "", err
	}

	cmdArgs := []string{"-c", "core.hooksPath=/dev/null", "clone"}
	if branch != "" {
		cmdArgs = append(cmdArgs, "--branch", branch)
	}
	if depth > 0 {
		cmdArgs = append(cmdArgs, "--depth", strconv.FormatInt(depth, 10))
	}
	cmdArgs = append(cmdArgs, url, resolvedDest)

	return t.runGit(ctx, t.workspace, cmdArgs...)
}

func (t *GitTool) runAdd(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	paths, err := stringSliceArg(args, "paths")
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("paths is required")
	}
	for _, p := range paths {
		if err := rejectOptionLike(p, "path"); err != nil {
			return "", err
		}
	}
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null", "add", "--"}, paths...)
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) runCommit(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	message, err := stringArg(args, "message", "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("message is required")
	}
	if err := rejectOptionLike(message, "message"); err != nil {
		return "", err
	}
	return t.runGit(ctx, repoDir, "-c", "core.hooksPath=/dev/null", "commit", "-m", message)
}

func (t *GitTool) runPush(ctx context.Context, args map[string]any) (string, error) {
	repoDir, err := t.resolveRepoDir(ctx, args)
	if err != nil {
		return "", err
	}
	remote, err := stringArg(args, "remote", "origin")
	if err != nil {
		return "", err
	}
	branch, err := stringArg(args, "branch", "")
	if err != nil {
		return "", err
	}
	if remote != "" {
		if err := rejectOptionLike(remote, "remote"); err != nil {
			return "", err
		}
	}
	if branch != "" {
		if err := rejectOptionLike(branch, "branch"); err != nil {
			return "", err
		}
	}
	cmdArgs := []string{"-c", "core.hooksPath=/dev/null", "push"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	if branch != "" {
		cmdArgs = append(cmdArgs, branch)
	}
	return t.runGit(ctx, repoDir, cmdArgs...)
}

func (t *GitTool) resolveRepoDir(ctx context.Context, args map[string]any) (string, error) {
	path, err := stringArg(args, "path", "")
	if err != nil {
		return "", err
	}
	resolved, err := t.resolvePath(path, false)
	if err != nil {
		return "", err
	}

	root, err := t.repoRoot(ctx, resolved)
	if err != nil {
		return "", err
	}
	return root, nil
}

func (t *GitTool) resolvePath(path string, allowMissing bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		if t.workspace == "" {
			return "", fmt.Errorf("workspace is not defined")
		}
		return filepath.Clean(t.workspace), nil
	}

	resolved, err := validatePathWithAllowPaths(path, t.workspace, t.restrictToWorkspace, t.allowedPathPatterns)
	if err != nil {
		return "", err
	}

	if allowMissing {
		return resolved, nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return resolved, nil
	}
	return filepath.Dir(resolved), nil
}

func (t *GitTool) repoRoot(ctx context.Context, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("repository path is required")
	}

	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	out, err := t.runner.Run(cmdCtx, dir, "-c", "core.hooksPath=/dev/null", "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git command timed out after %s", t.timeout)
		}
		return "", fmt.Errorf("not a git repository: %w", err)
	}

	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("not a git repository")
	}
	return root, nil
}

func (t *GitTool) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	out, err := t.runner.Run(cmdCtx, dir, args...)
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return strings.TrimSpace(out), fmt.Errorf("git command timed out after %s", t.timeout)
		}
		if strings.TrimSpace(out) == "" {
			return "", err
		}
		return strings.TrimSpace(out), err
	}

	return strings.TrimSpace(out), nil
}

func stringArg(args map[string]any, key, defaultVal string) (string, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return defaultVal, nil
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("%s must be a string", key)
	}
}

func boolArg(args map[string]any, key string) (bool, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true"), nil
	default:
		return false, fmt.Errorf("%s must be a boolean", key)
	}
}

func stringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain only strings", key)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func rejectOptionLike(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return fmt.Errorf("%s cannot start with '-'", field)
	}
	return nil
}
