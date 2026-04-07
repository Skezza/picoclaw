package commands

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCodexNew_ActivatesSession(t *testing.T) {
	var gotSlug, gotSource string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			gotSlug, gotSource = slug, source
			return &CodexSessionInfo{
				ID:       "abc123",
				Slug:     "acme_repo",
				RepoPath: "/workspace/repos/acme_repo",
				RepoURL:  "https://github.com/acme/repo.git",
			}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex new acme_repo https://github.com/acme/repo.git",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotSlug != "acme_repo" {
		t.Fatalf("slug=%q, want=%q", gotSlug, "acme_repo")
	}
	if gotSource != "https://github.com/acme/repo.git" {
		t.Fatalf("source=%q, want repo url", gotSource)
	}
	if !strings.Contains(reply, "Codex session is active") {
		t.Fatalf("reply=%q, expected activation message", reply)
	}
	if !strings.Contains(reply, "Planner model: codex-local") {
		t.Fatalf("reply=%q, expected planner model line", reply)
	}
	if !strings.Contains(reply, "Executor model: codex-local") {
		t.Fatalf("reply=%q, expected executor model line", reply)
	}
	if !strings.Contains(reply, "reply `proceed`") {
		t.Fatalf("reply=%q, expected proceed guidance", reply)
	}
}

func TestCodexNew_RequiresCodexModel(t *testing.T) {
	rt := &Runtime{
		FindCodexModel: func() string { return "" },
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			return nil, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex new acme_repo",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Codex mode unavailable: no codex-cli model is configured." {
		t.Fatalf("reply=%q, want unavailable codex model message", reply)
	}
}

func TestCodexNew_InferOwnerRepoSource(t *testing.T) {
	var gotSlug, gotSource string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			gotSlug, gotSource = slug, source
			return &CodexSessionInfo{
				ID:       "abc123",
				Slug:     slug,
				RepoPath: "/workspace/repos/" + slug,
				RepoURL:  "https://github.com/acme/repo.git",
			}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex new acme/repo",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotSlug != "acme-repo" {
		t.Fatalf("slug=%q, want=%q", gotSlug, "acme-repo")
	}
	if gotSource != "acme/repo" {
		t.Fatalf("source=%q, want=%q", gotSource, "acme/repo")
	}
	if !strings.Contains(reply, "Codex session is active") {
		t.Fatalf("reply=%q, expected activation message", reply)
	}
}

func TestCodexNew_InferURLSource(t *testing.T) {
	var gotSlug, gotSource string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			gotSlug, gotSource = slug, source
			return &CodexSessionInfo{
				ID:       "abc123",
				Slug:     slug,
				RepoPath: "/workspace/repos/" + slug,
				RepoURL:  source,
			}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex new https://github.com/acme/repo.git",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotSlug != "acme-repo" {
		t.Fatalf("slug=%q, want=%q", gotSlug, "acme-repo")
	}
	if gotSource != "https://github.com/acme/repo.git" {
		t.Fatalf("source=%q, want URL", gotSource)
	}
	if !strings.Contains(reply, "Codex session is active") {
		t.Fatalf("reply=%q, expected activation message", reply)
	}
}

func TestCodexStatus_NoActiveSession(t *testing.T) {
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex status",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "No active codex session in this chat. Use /codex new or /codex resume." {
		t.Fatalf("reply=%q, want inactive message", reply)
	}
}

func TestCodexStatus_ShowsAwaitingApprovalPhase(t *testing.T) {
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, true
		},
		FindCodexModel:          func() string { return "gpt-5.4-mini" },
		GetSessionWorkMode:      func() string { return "codex-plan" },
		GetCodexApprovalPending: func() bool { return true },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex status",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{"Repo: picoclaw", "Phase: awaiting approval"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexStatus_ShowsPlannerAndRunState(t *testing.T) {
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw", RepoURL: "https://github.com/sipeed/picoclaw.git"}, true
		},
		CodexPlannerStatus: func() (*CodexPlannerStatusInfo, bool) {
			return &CodexPlannerStatusInfo{
				Phase:           "planning",
				Model:           "gpt-5.4-mini",
				SessionID:       "chat-1",
				ApprovalPending: false,
			}, true
		},
		CodexRunStatus: func() (*CodexRunInfo, bool) {
			return &CodexRunInfo{
				ID:       "run-1",
				RepoSlug: "picoclaw",
				Status:   "running",
				Model:    "codex-cli-local",
				Branch:   "pc/chat-1/run-1",
				Worktree: "/workspace/worktrees/picoclaw/run-1",
				PID:      1234,
				Active:   true,
			}, true
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex status",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{
		"Phase: planning",
		"Planner model: gpt-5.4-mini",
		"Planner session: chat-1",
		"Run: running",
		"Run ID: run-1",
		"Run status: running",
		"Run model: codex-cli-local",
		"Run branch: pc/chat-1/run-1",
		"Run worktree: /workspace/worktrees/picoclaw/run-1",
		"Run pid: 1234",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexPlan_EntersPlanningModeWhenNoApprovalPending(t *testing.T) {
	var setMode string
	var cleared bool
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, true
		},
		SetSessionWorkMode: func(value string) error {
			setMode = value
			return nil
		},
		GetCodexApprovalPending: func() bool { return false },
		ClearCodexApprovalPending: func() {
			cleared = true
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex plan",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if setMode != "codex-plan" {
		t.Fatalf("setMode=%q, want codex-plan", setMode)
	}
	if cleared {
		t.Fatal("expected pending approval to be preserved when none was pending")
	}
	if !strings.Contains(reply, "Codex planning mode enabled") {
		t.Fatalf("reply=%q, expected planning mode confirmation", reply)
	}
}

func TestCodexPlan_PreservesPendingApproval(t *testing.T) {
	var setMode string
	var cleared bool
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, true
		},
		SetSessionWorkMode: func(value string) error {
			setMode = value
			return nil
		},
		GetCodexApprovalPending: func() bool { return true },
		ClearCodexApprovalPending: func() {
			cleared = true
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex plan",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if setMode != "codex-plan" {
		t.Fatalf("setMode=%q, want codex-plan", setMode)
	}
	if cleared {
		t.Fatal("expected /codex plan not to clear a pending approval")
	}
	if !strings.Contains(reply, "A plan is already ready.") {
		t.Fatalf("reply=%q, expected already-ready guidance", reply)
	}
	if !strings.Contains(reply, "/codex replan") {
		t.Fatalf("reply=%q, expected replan guidance", reply)
	}
}

func TestCodexReplan_ClearsPendingApproval(t *testing.T) {
	var setMode string
	var cleared bool
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, true
		},
		SetSessionWorkMode: func(value string) error {
			setMode = value
			return nil
		},
		ClearCodexApprovalPending: func() {
			cleared = true
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex replan",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if setMode != "codex-plan" {
		t.Fatalf("setMode=%q, want codex-plan", setMode)
	}
	if !cleared {
		t.Fatal("expected /codex replan to clear pending approval")
	}
	if reply != "Previous pending plan discarded. I’m back in planning mode." {
		t.Fatalf("reply=%q, want explicit replan confirmation", reply)
	}
}

func TestCodexResume_UsesAttach(t *testing.T) {
	var gotRef string
	rt := &Runtime{
		FindCodexModel: func() string { return "codex-local" },
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			gotRef = ref
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex resume picoclaw",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotRef != "picoclaw" {
		t.Fatalf("ref=%q, want=%q", gotRef, "picoclaw")
	}
	if !strings.Contains(reply, "Codex session is active") {
		t.Fatalf("reply=%q, expected activation message", reply)
	}
}

func TestCodexProjects_ShowsRepoPathsAndRemote(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 30, 0, 0, time.UTC)
	rt := &Runtime{
		CodexListSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{
				{ID: "a1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw", RepoURL: "https://github.com/sipeed/picoclaw.git", Active: true, Updated: now},
				{ID: "b2", Slug: "docs"},
			}
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex projects",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "Codex projects:") {
		t.Fatalf("reply=%q, expected header", reply)
	}
	if !strings.Contains(reply, "a1 picoclaw [active] path=/workspace/repos/picoclaw remote=https://github.com/sipeed/picoclaw.git @ 2026-04-06T14:30:00Z") {
		t.Fatalf("reply=%q, expected detailed project listing", reply)
	}
	if !strings.Contains(reply, "b2 docs") {
		t.Fatalf("reply=%q, expected second project listing", reply)
	}
}

func TestCodexRepos_ListsTargets(t *testing.T) {
	rt := &Runtime{
		ListCodexRepoTargets: func(limit int) ([]string, error) {
			if limit != 20 {
				t.Fatalf("limit=%d, want=20", limit)
			}
			return []string{"acme/repo-one", "acme/repo-two"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex repos",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "GitHub repos:") {
		t.Fatalf("reply=%q, expected header", reply)
	}
	if !strings.Contains(reply, "- acme/repo-one") || !strings.Contains(reply, "- acme/repo-two") {
		t.Fatalf("reply=%q, expected repo entries", reply)
	}
}

func TestCodexRepos_InvalidLimitShowsUsage(t *testing.T) {
	rt := &Runtime{
		ListCodexRepoTargets: func(limit int) ([]string, error) {
			return nil, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex repos abc",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Usage: /codex repos [limit]" {
		t.Fatalf("reply=%q, want usage", reply)
	}
}

func TestCodexPlan_SetsPlanningWorkMode(t *testing.T) {
	var setMode string
	cleared := false
	rt := &Runtime{
		CodexActive: func() (*CodexSessionInfo, bool) {
			return &CodexSessionInfo{ID: "a1", Slug: "picoclaw"}, true
		},
		SetSessionWorkMode: func(value string) error {
			setMode = value
			return nil
		},
		GetCodexApprovalPending: func() bool { return false },
		ClearCodexApprovalPending: func() {
			cleared = true
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex plan",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if setMode != "codex-plan" {
		t.Fatalf("work mode=%q, want=%q", setMode, "codex-plan")
	}
	if cleared {
		t.Fatal("expected /codex plan not to clear approval state")
	}
	if !strings.Contains(reply, "planning mode enabled") {
		t.Fatalf("reply=%q, want planning mode confirmation", reply)
	}
	if !strings.Contains(reply, "reply `proceed`") {
		t.Fatalf("reply=%q, want proceed guidance", reply)
	}
}

func TestCodexGuide_ContainsPlanAndExecuteFlow(t *testing.T) {
	rt := &Runtime{}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex guide",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{"reply `proceed`", "/codex status", "/codex runs", "/codex tail [run-id] [lines]"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexRuns_ListsRuns(t *testing.T) {
	now := time.Date(2026, 4, 6, 15, 0, 0, 0, time.UTC)
	rt := &Runtime{
		CodexRunList: func() []CodexRunInfo {
			return []CodexRunInfo{
				{ID: "run-1", RepoSlug: "picoclaw", Status: "running", Model: "codex-cli-local", Branch: "pc/chat-1/run-1", Worktree: "/workspace/worktrees/picoclaw/run-1", Active: true, StartedAt: now},
				{ID: "run-2", RepoSlug: "skezos", Status: "succeeded", Model: "codex-cli-local", Branch: "pc/chat-2/run-2", Worktree: "/workspace/worktrees/skezos/run-2", StartedAt: now.Add(-time.Hour)},
			}
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex runs",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{
		"Codex runs:",
		"run-1 picoclaw [active]",
		"status=running",
		"model=codex-cli-local",
		"branch=pc/chat-1/run-1",
		"worktree=/workspace/worktrees/picoclaw/run-1",
		"run-2 skezos",
		"status=succeeded",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexTail_UsesActiveRunWhenIDMissing(t *testing.T) {
	var gotID string
	var gotLines int
	rt := &Runtime{
		CodexRunStatus: func() (*CodexRunInfo, bool) {
			return &CodexRunInfo{ID: "run-1", RepoSlug: "picoclaw", Status: "running", Active: true}, true
		},
		CodexRunTail: func(runID string, lines int) (string, error) {
			gotID = runID
			gotLines = lines
			return "tail line 1\ntail line 2", nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex tail",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotID != "run-1" {
		t.Fatalf("runID=%q, want=%q", gotID, "run-1")
	}
	if gotLines != 120 {
		t.Fatalf("lines=%d, want=120", gotLines)
	}
	if reply != "tail line 1\ntail line 2" {
		t.Fatalf("reply=%q, want tail contents", reply)
	}
}

func TestCodexConversationalFallback_BareCodexResumesMostRecent(t *testing.T) {
	var attachRef string
	rt := &Runtime{
		FindCodexModel: func() string { return "gpt-5.4-mini" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
		CodexListSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{
				{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"},
				{ID: "s2", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"},
			}
		},
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			attachRef = ref
			return &CodexSessionInfo{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if attachRef != "s1" {
		t.Fatalf("attach ref=%q, want=%q", attachRef, "s1")
	}
	for _, want := range []string{
		"Codex conversational mode is ready.",
		"Repo: skezos",
		"Resumed this chat's most recent project: skezos.",
		"Phase: planning",
		"Talk normally now",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexConversationalFallback_IntentMatchesGlobalSessionCatalog(t *testing.T) {
	var attachRef string
	rt := &Runtime{
		FindCodexModel: func() string { return "gpt-5.4-mini" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
		CodexListSessions: func() []CodexSessionInfo {
			return nil
		},
		CodexListGlobalSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{
				{ID: "p1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"},
				{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"},
			}
		},
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			attachRef = ref
			return &CodexSessionInfo{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex I want you to reopen my old SkezOS session",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if attachRef != "s1" {
		t.Fatalf("attach ref=%q, want=%q", attachRef, "s1")
	}
	for _, want := range []string{
		"Matched existing project from your global codex history: skezos.",
		"planning brief",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexConversationalFallback_IntentMatchesExistingSession(t *testing.T) {
	var attachRef string
	rt := &Runtime{
		FindCodexModel: func() string { return "gpt-5.4-mini" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
		CodexListSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{
				{ID: "p1", Slug: "picoclaw", RepoPath: "/workspace/repos/picoclaw"},
				{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"},
			}
		},
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			attachRef = ref
			return &CodexSessionInfo{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex I want you to check out SkezOS and review latest changes",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if attachRef != "s1" {
		t.Fatalf("attach ref=%q, want=%q", attachRef, "s1")
	}
	for _, want := range []string{
		"Matched existing project: skezos.",
		"planning brief",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q, missing %q", reply, want)
		}
	}
}

func TestCodexConversationalFallback_BareCodexDoesNotAutoResumeGlobalHistory(t *testing.T) {
	attachCalled := false
	rt := &Runtime{
		FindCodexModel: func() string { return "gpt-5.4-mini" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
		CodexListSessions: func() []CodexSessionInfo { return nil },
		CodexListGlobalSessions: func() []CodexSessionInfo {
			return []CodexSessionInfo{
				{ID: "s1", Slug: "skezos", RepoPath: "/workspace/repos/skezos"},
			}
		},
		CodexAttach: func(ref string) (*CodexSessionInfo, error) {
			attachCalled = true
			return &CodexSessionInfo{ID: ref, Slug: "skezos", RepoPath: "/workspace/repos/skezos"}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if attachCalled {
		t.Fatal("expected bare /codex not to auto-attach from global history")
	}
	if reply != "No codex session is active yet. Start one with /codex new owner/repo, then chat normally." {
		t.Fatalf("reply=%q, want no-active-session guidance", reply)
	}
}

func TestCodexConversationalFallback_IntentCanCreateFromRepoDiscovery(t *testing.T) {
	var gotSlug, gotSource string
	rt := &Runtime{
		FindCodexModel: func() string { return "gpt-5.4-mini" },
		CodexActive: func() (*CodexSessionInfo, bool) {
			return nil, false
		},
		CodexListSessions: func() []CodexSessionInfo { return nil },
		ListCodexRepoTargets: func(limit int) ([]string, error) {
			return []string{"joe/SkezOS", "joe/picoclaw"}, nil
		},
		CodexNewSession: func(slug, source string) (*CodexSessionInfo, error) {
			gotSlug, gotSource = slug, source
			return &CodexSessionInfo{ID: "s3", Slug: slug, RepoPath: "/workspace/repos/" + slug}, nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex please open SkezOS so we can work on it",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if gotSlug != "joe-SkezOS" {
		t.Fatalf("slug=%q, want=%q", gotSlug, "joe-SkezOS")
	}
	if gotSource != "joe/SkezOS" {
		t.Fatalf("source=%q, want=%q", gotSource, "joe/SkezOS")
	}
	if !strings.Contains(reply, "Created new project from GitHub repo: joe/SkezOS.") {
		t.Fatalf("reply=%q, expected repo discovery note", reply)
	}
}

func TestCodexStop_ClearsSession(t *testing.T) {
	runStopped := false
	rt := &Runtime{
		CodexRunStop: func() error {
			runStopped = true
			return nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/codex stop",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !runStopped {
		t.Fatal("CodexRunStop callback was not called")
	}
	if reply != "Codex run stopped. Session routing returned to default." {
		t.Fatalf("reply=%q, want stop confirmation", reply)
	}
}
