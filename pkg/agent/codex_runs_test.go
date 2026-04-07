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

func TestCodexSessionStore_CreateOrActivate_AllowsEquivalentGitHubRemotes(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeKey := "agent:test:main"
	slug := "skezza-picoclaw"
	repoPath, err := store.repoPathForSlug(slug)
	if err != nil {
		t.Fatalf("repoPathForSlug() error = %v", err)
	}
	if err := initGitRepo(t, repoPath); err != nil {
		t.Fatalf("initGitRepo() error = %v", err)
	}

	session, err := store.CreateOrActivate(scopeKey, slug, "git@github.com:Skezza/picoclaw.git")
	if err != nil {
		t.Fatalf("CreateOrActivate() error = %v", err)
	}
	if session == nil {
		t.Fatal("CreateOrActivate() returned nil session")
	}

	session, err = store.CreateOrActivate(scopeKey, slug, "https://github.com/Skezza/picoclaw.git")
	if err != nil {
		t.Fatalf("CreateOrActivate() with equivalent https remote error = %v", err)
	}
	if session == nil {
		t.Fatal("CreateOrActivate() returned nil session after remote normalization")
	}
	if got := session.RepoURL; got != "https://github.com/Skezza/picoclaw.git" {
		t.Fatalf("RepoURL = %q, want %q", got, "https://github.com/Skezza/picoclaw.git")
	}
}

func TestRepoSourcesEquivalent_GitHubSSHAndHTTPS(t *testing.T) {
	if !repoSourcesEquivalent("git@github.com:Skezza/picoclaw.git", "https://github.com/Skezza/picoclaw.git") {
		t.Fatal("expected GitHub SSH and HTTPS remotes to be treated as equivalent")
	}
	if repoSourcesEquivalent("git@github.com:Skezza/picoclaw.git", "https://github.com/Skezza/other.git") {
		t.Fatal("different GitHub repositories should not be equivalent")
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

func TestCodexSessionStore_ActiveRunReturnsClone(t *testing.T) {
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
	if _, err := store.CreateOrActivate(scopeKey, slug, "https://github.com/Skezza/picoclaw.git"); err != nil {
		t.Fatalf("CreateOrActivate() error = %v", err)
	}

	run, err := store.CreateRun(scopeKey, codexRunCreateOptions{PlannerModel: "gpt-5.4-mini", Mode: "autonomous"})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	active, ok := store.ActiveRun(scopeKey)
	if !ok || active == nil {
		t.Fatal("expected active run")
	}
	active.Status = "tampered"
	active.PID = 123

	reloaded, ok := store.GetRun(run.ID)
	if !ok || reloaded == nil {
		t.Fatal("expected stored run")
	}
	if reloaded.Status == "tampered" || reloaded.PID == 123 {
		t.Fatalf("stored run mutated through ActiveRun(): %+v", reloaded)
	}
}

func TestCodexSessionStore_ListIsScopedByChatRecentHistory(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeA := "agent:test:chat-a"
	scopeB := "agent:test:chat-b"
	for _, slug := range []string{"picoclaw", "skezos", "docs"} {
		repoPath, err := store.repoPathForSlug(slug)
		if err != nil {
			t.Fatalf("repoPathForSlug(%q) error = %v", slug, err)
		}
		if err := initGitRepo(t, repoPath); err != nil {
			t.Fatalf("initGitRepo(%q) error = %v", slug, err)
		}
	}

	picoclaw, err := store.CreateOrActivate(scopeA, "picoclaw", "")
	if err != nil {
		t.Fatalf("CreateOrActivate(picoclaw) error = %v", err)
	}
	skezos, err := store.CreateOrActivate(scopeA, "skezos", "")
	if err != nil {
		t.Fatalf("CreateOrActivate(skezos) error = %v", err)
	}
	docs, err := store.CreateOrActivate(scopeB, "docs", "")
	if err != nil {
		t.Fatalf("CreateOrActivate(docs) error = %v", err)
	}
	if _, err := store.Attach(scopeA, picoclaw.ID); err != nil {
		t.Fatalf("Attach(picoclaw) error = %v", err)
	}

	listA := store.List(scopeA)
	if len(listA) != 2 {
		t.Fatalf("List(scopeA) len=%d, want 2", len(listA))
	}
	if listA[0].Slug != "picoclaw" || listA[1].Slug != "skezos" {
		t.Fatalf("List(scopeA) = [%s, %s], want [picoclaw, skezos]", listA[0].Slug, listA[1].Slug)
	}

	listB := store.List(scopeB)
	if len(listB) != 1 || listB[0].Slug != "docs" {
		t.Fatalf("List(scopeB) = %+v, want only docs", listB)
	}

	global := store.List("")
	if len(global) != 3 {
		t.Fatalf("List(\"\") len=%d, want 3", len(global))
	}
	got := map[string]bool{}
	for _, rec := range global {
		got[rec.Slug] = true
	}
	for _, want := range []string{picoclaw.Slug, skezos.Slug, docs.Slug} {
		if !got[want] {
			t.Fatalf("global list missing %q: %+v", want, global)
		}
	}
}

func TestCodexRunTail_AllowsActiveSessionRunsAcrossChats(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeA := "agent:test:chat-a"
	scopeB := "agent:test:chat-b"
	repoPath, err := store.repoPathForSlug("picoclaw")
	if err != nil {
		t.Fatalf("repoPathForSlug() error = %v", err)
	}
	if err := initGitRepo(t, repoPath); err != nil {
		t.Fatalf("initGitRepo() error = %v", err)
	}

	session, err := store.CreateOrActivate(scopeA, "picoclaw", "")
	if err != nil {
		t.Fatalf("CreateOrActivate(scopeA) error = %v", err)
	}
	run, err := store.CreateRun(scopeA, codexRunCreateOptions{PlannerModel: "gpt-5.4-mini", Mode: "autonomous"})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	logPath := filepath.Join(workspace, "run.log")
	if err := os.WriteFile(logPath, []byte("tail line 1\ntail line 2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := store.MarkRunStarted(run.ID, 12345, logPath); err != nil {
		t.Fatalf("MarkRunStarted() error = %v", err)
	}
	if _, err := store.Attach(scopeB, session.ID); err != nil {
		t.Fatalf("Attach(scopeB) error = %v", err)
	}

	al := &AgentLoop{codexStore: store}
	output, err := al.codexRunTail(scopeB, run.ID, 20)
	if err != nil {
		t.Fatalf("codexRunTail() error = %v", err)
	}
	if output != "tail line 1\ntail line 2" {
		t.Fatalf("codexRunTail() = %q, want log contents", output)
	}
}

func TestCodexRunTail_RejectsRunOutsideActiveSession(t *testing.T) {
	workspace := t.TempDir()
	store := newCodexSessionStore(workspace)
	if store == nil {
		t.Fatal("expected codex session store")
	}

	scopeA := "agent:test:chat-a"
	scopeB := "agent:test:chat-b"
	for _, slug := range []string{"picoclaw", "skezos"} {
		repoPath, err := store.repoPathForSlug(slug)
		if err != nil {
			t.Fatalf("repoPathForSlug(%q) error = %v", slug, err)
		}
		if err := initGitRepo(t, repoPath); err != nil {
			t.Fatalf("initGitRepo(%q) error = %v", slug, err)
		}
	}

	if _, err := store.CreateOrActivate(scopeA, "picoclaw", ""); err != nil {
		t.Fatalf("CreateOrActivate(scopeA) error = %v", err)
	}
	runA, err := store.CreateRun(scopeA, codexRunCreateOptions{PlannerModel: "gpt-5.4-mini", Mode: "autonomous"})
	if err != nil {
		t.Fatalf("CreateRun(scopeA) error = %v", err)
	}
	logPath := filepath.Join(workspace, "run-a.log")
	if err := os.WriteFile(logPath, []byte("tail line 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := store.MarkRunStarted(runA.ID, 12345, logPath); err != nil {
		t.Fatalf("MarkRunStarted() error = %v", err)
	}
	if _, err := store.CreateOrActivate(scopeB, "skezos", ""); err != nil {
		t.Fatalf("CreateOrActivate(scopeB) error = %v", err)
	}

	al := &AgentLoop{codexStore: store}
	if _, err := al.codexRunTail(scopeB, runA.ID, 20); err == nil || !strings.Contains(err.Error(), "does not belong to the active session in this chat") {
		t.Fatalf("codexRunTail() error = %v, want scope rejection", err)
	}
}

func TestIsPicoClawRun_MatchesExactRepoIdentity(t *testing.T) {
	tests := []struct {
		name string
		run  *codexRunRecord
		want bool
	}{
		{
			name: "exact repo url basename",
			run:  &codexRunRecord{RepoURL: "https://github.com/Skezza/picoclaw.git", RepoSlug: "Skezza-picoclaw"},
			want: true,
		},
		{
			name: "exact local path basename",
			run:  &codexRunRecord{RepoPath: "/workspace/repos/picoclaw"},
			want: true,
		},
		{
			name: "exact slug only",
			run:  &codexRunRecord{RepoSlug: "picoclaw"},
			want: true,
		},
		{
			name: "substring slug should not match",
			run:  &codexRunRecord{RepoSlug: "picoclaw-demo"},
			want: false,
		},
		{
			name: "substring path should not match",
			run:  &codexRunRecord{RepoPath: "/workspace/repos/my-picoclaw-fork-copy"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPicoClawRun(tc.run); got != tc.want {
				t.Fatalf("isPicoClawRun() = %v, want %v", got, tc.want)
			}
		})
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
