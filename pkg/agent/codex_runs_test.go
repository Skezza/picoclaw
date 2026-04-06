package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSessionStore_RunLifecycleAndPersistence(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeKey := "agent:test:main"
	slug := "picoclaw-demo"
	repoPath, err := store.repoPathForSlug(slug)
	if err != nil {
		t.Fatalf("repoPathForSlug() error = %v", err)
	}
	if err := initGitRepo(t, repoPath); err != nil {
		t.Fatalf("initGitRepo() error = %v", err)
	}

	session, err := store.CreateOrActivate(scopeKey, slug, "")
	if err != nil {
		t.Fatalf("CreateOrActivate() error = %v", err)
	}
	if session == nil {
		t.Fatal("CreateOrActivate() returned nil session")
	}

	runtime := codexSessionRuntimeState{
		PlannerModel:         "gpt-5.4-mini",
		ExecutorModel:        "codex-cli-local",
		WorkMode:             "codex-plan",
		ApprovalPending:      true,
		DeployConfirmPending: true,
		PendingPlanID:        "plan-1",
		PendingPlanHash:      "hash-1",
	}
	if err := store.SetSessionRuntime(scopeKey, runtime); err != nil {
		t.Fatalf("SetSessionRuntime() error = %v", err)
	}

	gotRuntime, ok := store.SessionRuntime(scopeKey)
	if !ok {
		t.Fatal("SessionRuntime() not found")
	}
	if gotRuntime.PlannerModel != runtime.PlannerModel || gotRuntime.ExecutorModel != runtime.ExecutorModel {
		t.Fatalf("runtime=%+v, want planner/executor models preserved", gotRuntime)
	}
	if !gotRuntime.ApprovalPending || !gotRuntime.DeployConfirmPending {
		t.Fatalf("runtime=%+v, want approval/deploy pending", gotRuntime)
	}

	run, err := store.CreateRun(scopeKey, codexRunCreateOptions{
		PlannerModel:         runtime.PlannerModel,
		ExecutorModel:        runtime.ExecutorModel,
		Mode:                 "autonomous",
		PlanID:               runtime.PendingPlanID,
		PlanHash:             runtime.PendingPlanHash,
		InitiatedBy:          "telegram:123",
		DeployConfirmPending: true,
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if run == nil {
		t.Fatal("CreateRun() returned nil run")
	}
	if !strings.Contains(run.WorktreePath, filepath.Join("worktrees", slug, run.ID)) {
		t.Fatalf("worktree path=%q, want repo/run hierarchy", run.WorktreePath)
	}
	if run.BranchName != "pc/"+session.ID+"/"+run.ID {
		t.Fatalf("branch=%q, want deterministic branch name", run.BranchName)
	}

	if _, err := store.CreateRun(scopeKey, codexRunCreateOptions{PlannerModel: runtime.PlannerModel}); err == nil {
		t.Fatal("CreateRun() on active session should fail")
	}

	scopeKey2 := "agent:test:other"
	if _, err := store.CreateOrActivate(scopeKey2, slug, ""); err != nil {
		t.Fatalf("CreateOrActivate() for second scope error = %v", err)
	}
	if _, err := store.CreateRun(scopeKey2, codexRunCreateOptions{PlannerModel: runtime.PlannerModel}); err == nil {
		t.Fatal("CreateRun() on same repo from another scope should fail")
	}

	if _, err := store.PrepareRunWorktree(run.ID); err != nil {
		t.Fatalf("PrepareRunWorktree() error = %v", err)
	}
	if stat, err := os.Stat(run.WorktreePath); err != nil || !stat.IsDir() {
		t.Fatalf("worktree path missing after prepare: stat=%v err=%v", stat, err)
	}

	if err := store.MarkRunStarted(run.ID, 999999, filepath.Join(workspace, "run.log")); err != nil {
		t.Fatalf("MarkRunStarted() error = %v", err)
	}
	if err := store.MarkRunFinished(run.ID, codexRunStatusSucceeded, 0, ""); err != nil {
		t.Fatalf("MarkRunFinished() error = %v", err)
	}

	loaded := newCodexSessionStore(workspace)
	if loaded == nil {
		t.Fatal("expected reloaded store")
	}

	loadedRun, ok := loaded.GetRun(run.ID)
	if !ok {
		t.Fatal("reloaded run missing")
	}
	if loadedRun.Status != codexRunStatusSucceeded {
		t.Fatalf("reloaded run status=%q, want %q", loadedRun.Status, codexRunStatusSucceeded)
	}
	loadedRuntime, ok := loaded.SessionRuntime(scopeKey)
	if !ok {
		t.Fatal("reloaded session runtime missing")
	}
	if loadedRuntime.ActiveRunID != "" {
		t.Fatalf("reloaded active run id=%q, want cleared after finish", loadedRuntime.ActiveRunID)
	}
}

func TestCodexSessionStore_ReconcileRunsMarksDeadPidUnknown(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeKey := "agent:test:main"
	slug := "picoclaw-demo"
	repoPath, err := store.repoPathForSlug(slug)
	if err != nil {
		t.Fatalf("repoPathForSlug() error = %v", err)
	}
	if err := initGitRepo(t, repoPath); err != nil {
		t.Fatalf("initGitRepo() error = %v", err)
	}
	if _, err := store.CreateOrActivate(scopeKey, slug, ""); err != nil {
		t.Fatalf("CreateOrActivate() error = %v", err)
	}

	run, err := store.CreateRun(scopeKey, codexRunCreateOptions{PlannerModel: "gpt-5.4-mini", Mode: "autonomous"})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.MarkRunStarted(run.ID, 999999, ""); err != nil {
		t.Fatalf("MarkRunStarted() error = %v", err)
	}

	changed, err := store.ReconcileRuns()
	if err != nil {
		t.Fatalf("ReconcileRuns() error = %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected reconciliation to report a changed run")
	}

	updated, ok := store.GetRun(run.ID)
	if !ok {
		t.Fatal("updated run missing")
	}
	if updated.Status != codexRunStatusUnknown {
		t.Fatalf("updated status=%q, want %q", updated.Status, codexRunStatusUnknown)
	}
	if runtime, ok := store.SessionRuntime(scopeKey); !ok || runtime.ActiveRunID != "" {
		t.Fatalf("session runtime after reconcile = %+v, want cleared active run", runtime)
	}
}

func TestCodexSessionStore_ReconcileRunsMarksCompletedLogSucceeded(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeKey := "agent:test:main"
	slug := "picoclaw-demo"
	repoPath, err := store.repoPathForSlug(slug)
	if err != nil {
		t.Fatalf("repoPathForSlug() error = %v", err)
	}
	if err := initGitRepo(t, repoPath); err != nil {
		t.Fatalf("initGitRepo() error = %v", err)
	}
	if _, err := store.CreateOrActivate(scopeKey, slug, ""); err != nil {
		t.Fatalf("CreateOrActivate() error = %v", err)
	}

	run, err := store.CreateRun(scopeKey, codexRunCreateOptions{PlannerModel: "gpt-5.4-mini", Mode: "autonomous"})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	logPath := filepath.Join(workspace, "codex.log")
	if err := os.WriteFile(logPath, []byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"done\"}}\n{\"type\":\"turn.completed\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := store.MarkRunStarted(run.ID, 999999, logPath); err != nil {
		t.Fatalf("MarkRunStarted() error = %v", err)
	}

	changed, err := store.ReconcileRuns()
	if err != nil {
		t.Fatalf("ReconcileRuns() error = %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected reconciliation to report a changed run")
	}

	updated, ok := store.GetRun(run.ID)
	if !ok {
		t.Fatal("updated run missing")
	}
	if updated.Status != codexRunStatusSucceeded {
		t.Fatalf("updated status=%q, want %q", updated.Status, codexRunStatusSucceeded)
	}
	if updated.ExitCode != 0 {
		t.Fatalf("updated exit code=%d, want 0", updated.ExitCode)
	}
}

func TestSanitizeRepoRemoteStripsCredentials(t *testing.T) {
	got := sanitizeRepoRemote("https://user:token@example.com/org/repo.git")
	if got != "https://example.com/org/repo.git" {
		t.Fatalf("sanitizeRepoRemote() = %q, want %q", got, "https://example.com/org/repo.git")
	}
}

func initGitRepo(t *testing.T, dir string) error {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := runGit(dir, "init"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "user.email", "test@example.com"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "user.name", "Test User"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		return err
	}
	if err := runGit(dir, "add", "README.md"); err != nil {
		return err
	}
	if err := runGit(dir, "commit", "-m", "initial commit"); err != nil {
		return err
	}
	return nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
