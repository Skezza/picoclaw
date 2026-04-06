package commands

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func codexCommand() Definition {
	attachHandler := func(actionLabel string) func(context.Context, Request, *Runtime) error {
		return func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.CodexAttach == nil {
				return req.Reply(unavailableMsg)
			}
			executorModel := codexExecutorModel(rt)
			if executorModel == "" {
				return req.Reply("Codex mode unavailable: no codex-cli model is configured.")
			}
			ref := strings.TrimSpace(nthToken(req.Text, 2))
			if ref == "" {
				return req.Reply("Usage: /codex " + actionLabel + " <session-id|repo-slug>")
			}
			info, err := rt.CodexAttach(ref)
			if err != nil {
				return req.Reply(err.Error())
			}
			_, plannerModel, _ := codexPlannerState(rt)
			return req.Reply(formatCodexActivatedMessage(info, plannerModel, executorModel))
		}
	}

	return Definition{
		Name:        "codex",
		Description: "Manage repo-scoped Codex sessions",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			return handleCodexConversationalEntry(req, rt)
		},
		SubCommands: []SubCommand{
			{
				Name:        "new",
				Description: "Create and activate a Codex session",
				ArgsUsage:   "<repo-slug|owner/repo|repo-url> [repo-url]",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexNewSession == nil {
						return req.Reply(unavailableMsg)
					}
					executorModel := codexExecutorModel(rt)
					if executorModel == "" {
						return req.Reply("Codex mode unavailable: no codex-cli model is configured.")
					}

					slug, source, err := parseCodexNewArgs(req.Text)
					if err != nil {
						return req.Reply(err.Error())
					}

					info, err := rt.CodexNewSession(slug, source)
					if err != nil {
						return req.Reply(err.Error())
					}
					_, plannerModel, _ := codexPlannerState(rt)
					return req.Reply(formatCodexActivatedMessage(info, plannerModel, executorModel))
				},
			},
			{
				Name:        "resume",
				Description: "Resume an existing Codex session",
				ArgsUsage:   "<session-id|repo-slug>",
				Handler:     attachHandler("resume"),
			},
			{
				Name:        "projects",
				Description: "List known Codex projects",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexListSessions == nil {
						return req.Reply(unavailableMsg)
					}
					sessions := rt.CodexListSessions()
					if len(sessions) == 0 {
						return req.Reply("No codex projects yet. Start one with /codex new <repo-slug> [repo-url].")
					}

					lines := make([]string, 0, len(sessions)+1)
					lines = append(lines, "Codex projects:")
					for _, s := range sessions {
						flag := ""
						if s.Active {
							flag = " [active]"
						}
						extra := ""
						if s.RepoPath != "" {
							extra += fmt.Sprintf(" path=%s", s.RepoPath)
						}
						if s.RepoURL != "" {
							extra += fmt.Sprintf(" remote=%s", s.RepoURL)
						}
						updated := ""
						if !s.Updated.IsZero() {
							updated = " @ " + s.Updated.Format(time.RFC3339)
						}
						lines = append(lines, fmt.Sprintf("- %s %s%s%s%s", s.ID, s.Slug, flag, extra, updated))
					}
					return req.Reply(strings.Join(lines, "\n"))
				},
			},
			{
				Name:        "repos",
				Description: "List GitHub repos visible to local gh auth",
				ArgsUsage:   "[limit]",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.ListCodexRepoTargets == nil {
						return req.Reply("Codex repo discovery unavailable: runtime callback is not configured.")
					}

					limit := 20
					rawLimit := strings.TrimSpace(nthToken(req.Text, 2))
					if rawLimit != "" {
						parsed, err := parsePositiveInt(rawLimit)
						if err != nil {
							return req.Reply("Usage: /codex repos [limit]")
						}
						limit = parsed
					}
					if limit > 50 {
						limit = 50
					}

					repos, err := rt.ListCodexRepoTargets(limit)
					if err != nil {
						return req.Reply("GitHub repo discovery unavailable: " + err.Error())
					}
					if len(repos) == 0 {
						return req.Reply("No GitHub repos discovered. Verify gh auth on this host.")
					}

					lines := make([]string, 0, len(repos)+1)
					lines = append(lines, "GitHub repos:")
					for _, repo := range repos {
						lines = append(lines, "- "+repo)
					}
					return req.Reply(strings.Join(lines, "\n"))
				},
			},
			{
				Name:        "plan",
				Description: "Refine the active Codex plan",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexActive == nil || rt.SetSessionWorkMode == nil {
						return req.Reply(unavailableMsg)
					}
					info, ok := rt.CodexActive()
					if !ok || info == nil {
						return req.Reply("No active codex session in this chat. Use /codex new or /codex resume first.")
					}
					if rt.ClearCodexApprovalPending != nil {
						rt.ClearCodexApprovalPending()
					}
					if err := rt.SetSessionWorkMode("codex-plan"); err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply("Codex planning mode enabled. I will discuss and refine a plan only. When the plan is ready, I will ask you to reply `proceed` to execute it.")
				},
			},
			{
				Name:        "guide",
				Description: "Show recommended conversational /codex workflow",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					lines := []string{
						"Conversational /codex quick start:",
						"1) Send /codex once. It resumes your most recent project when possible, otherwise it helps you pick one.",
						"2) Then chat normally, e.g. \"Open SkezOS, review latest changes, and propose the next feature.\"",
						"3) I stay in planning mode first. When the plan is ready, I will tell you to reply `proceed`.",
						"4) Reply `proceed` and I will launch the approved Codex run in the active repo session.",
						"",
						"Optional controls:",
						"- /codex new owner/repo",
						"- /codex resume <session-id|repo-slug>",
						"- /codex status",
						"- /codex runs",
						"- /codex tail [run-id] [lines]",
						"- /codex plan",
						"- /codex repos [limit]",
						"- /codex projects",
					}
					return req.Reply(strings.Join(lines, "\n"))
				},
			},
			{
				Name:        "status",
				Description: "Show current Codex planner and run state",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexActive == nil {
						return req.Reply(unavailableMsg)
					}
					info, ok := rt.CodexActive()
					if !ok || info == nil {
						return req.Reply("No active codex session in this chat. Use /codex new or /codex resume.")
					}
					phase, model, planner := codexPlannerState(rt)
					run := codexActiveRunState(rt)
					lines := []string{
						"Codex session is active.",
						fmt.Sprintf("ID: %s", info.ID),
						fmt.Sprintf("Repo: %s", info.Slug),
						fmt.Sprintf("Path: %s", info.RepoPath),
					}
					if info.RepoURL != "" {
						lines = append(lines, fmt.Sprintf("Remote: %s", info.RepoURL))
					}
					lines = append(lines, fmt.Sprintf("Phase: %s", phase))
					if planner != nil {
						if planner.Model != "" {
							lines = append(lines, fmt.Sprintf("Planner model: %s", planner.Model))
						}
						if planner.SessionID != "" {
							lines = append(lines, fmt.Sprintf("Planner session: %s", planner.SessionID))
						}
					} else if model != "" {
						lines = append(lines, fmt.Sprintf("Planner model: %s", model))
					}
					if executorModel := codexExecutorModel(rt); executorModel != "" {
						lines = append(lines, fmt.Sprintf("Executor model: %s", executorModel))
					}
					if run != nil {
						lines = append(lines, fmt.Sprintf("Run: %s", codexRunLabel(run)))
						if run.ID != "" {
							lines = append(lines, fmt.Sprintf("Run ID: %s", run.ID))
						}
						if run.Status != "" {
							lines = append(lines, fmt.Sprintf("Run status: %s", run.Status))
						}
						if run.Model != "" {
							lines = append(lines, fmt.Sprintf("Run model: %s", run.Model))
						}
						if run.Branch != "" {
							lines = append(lines, fmt.Sprintf("Run branch: %s", run.Branch))
						}
						if run.Worktree != "" {
							lines = append(lines, fmt.Sprintf("Run worktree: %s", run.Worktree))
						}
						if run.PID > 0 {
							lines = append(lines, fmt.Sprintf("Run pid: %d", run.PID))
						}
					} else {
						lines = append(lines, "Run: none")
					}
					return req.Reply(strings.Join(lines, "\n"))
				},
			},
			{
				Name:        "runs",
				Description: "List background Codex runs",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexRunList == nil {
						return req.Reply("Codex run listing unavailable: runtime callback is not configured.")
					}
					runs := rt.CodexRunList()
					if len(runs) == 0 {
						return req.Reply("No codex runs recorded yet.")
					}

					lines := make([]string, 0, len(runs)+1)
					lines = append(lines, "Codex runs:")
					for _, run := range runs {
						lines = append(lines, formatCodexRunListLine(run))
					}
					return req.Reply(strings.Join(lines, "\n"))
				},
			},
			{
				Name:        "tail",
				Description: "Show the tail of a Codex run log",
				ArgsUsage:   "[run-id] [lines]",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexRunTail == nil {
						return req.Reply("Codex run tail unavailable: runtime callback is not configured.")
					}
					runID := strings.TrimSpace(nthToken(req.Text, 2))
					lines := 120
					rawLines := strings.TrimSpace(nthToken(req.Text, 3))
					if rawLines != "" {
						parsed, err := parsePositiveInt(rawLines)
						if err != nil {
							return req.Reply("Usage: /codex tail [run-id] [lines]")
						}
						lines = parsed
					}
					if lines > 500 {
						lines = 500
					}
					if runID == "" {
						if current := codexActiveRunState(rt); current != nil && current.ID != "" {
							runID = current.ID
						}
					}
					if runID == "" {
						return req.Reply("No codex run is active yet. Start or resume a run first.")
					}

					output, err := rt.CodexRunTail(runID, lines)
					if err != nil {
						return req.Reply(err.Error())
					}
					if strings.TrimSpace(output) == "" {
						return req.Reply(fmt.Sprintf("No log output available for run %s.", runID))
					}
					return req.Reply(output)
				},
			},
			{
				Name:        "stop",
				Description: "Stop the active Codex run and detach this chat",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.CodexRunStop == nil {
						return req.Reply(unavailableMsg)
					}
					if err := rt.CodexRunStop(); err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply("Codex run stopped. Session routing returned to default.")
				},
			},
		},
	}
}

func formatCodexActivatedMessage(info *CodexSessionInfo, plannerModel, executorModel string) string {
	if info == nil {
		return "Codex session is active."
	}
	lines := []string{
		"Codex session is active in planning mode.",
		fmt.Sprintf("ID: %s", info.ID),
		fmt.Sprintf("Repo: %s", info.Slug),
		fmt.Sprintf("Path: %s", info.RepoPath),
	}
	if info.RepoURL != "" {
		lines = append(lines, fmt.Sprintf("Remote: %s", info.RepoURL))
	}
	if plannerModel != "" {
		lines = append(lines, fmt.Sprintf("Planner model: %s", plannerModel))
	}
	if executorModel != "" {
		lines = append(lines, fmt.Sprintf("Executor model: %s", executorModel))
	}
	lines = append(lines, "Chat normally now. I will plan first, then ask you to reply `proceed` when the approved run is ready.")
	return strings.Join(lines, "\n")
}

func handleCodexConversationalEntry(req Request, rt *Runtime) error {
	if rt == nil {
		return req.Reply(unavailableMsg)
	}

	executorModel := codexExecutorModel(rt)
	if executorModel == "" {
		return req.Reply("Codex mode unavailable: no codex-cli model is configured.")
	}

	intent := codexCommandTail(req.Text)
	info, note, err := activateCodexSessionForConversationalEntry(rt, intent)
	if err != nil {
		return req.Reply(err.Error())
	}
	if info == nil {
		if strings.TrimSpace(intent) == "" {
			return req.Reply("No codex session is active yet. Start one with /codex new owner/repo, then chat normally.")
		}
		return req.Reply("I understand your intent, but I need a repo session first. Use /codex new owner/repo (or /codex repos), then send the request again without /codex.")
	}

	lines := []string{
		"Codex conversational mode is ready.",
		fmt.Sprintf("Repo: %s", info.Slug),
		fmt.Sprintf("Path: %s", info.RepoPath),
	}
	phase, _, planner := codexPlannerState(rt)
	if strings.TrimSpace(phase) == "" || strings.EqualFold(strings.TrimSpace(phase), "inactive") {
		phase = "planning"
	}
	lines = append(lines, fmt.Sprintf("Phase: %s", phase))
	if planner != nil && planner.Model != "" {
		lines = append(lines, fmt.Sprintf("Planner model: %s", planner.Model))
	}
	if executorModel != "" {
		lines = append(lines, fmt.Sprintf("Executor model: %s", executorModel))
	}
	if note != "" {
		lines = append(lines, note)
	}
	if strings.TrimSpace(intent) == "" {
		lines = append(lines, `Talk normally now, for example: "review latest changes and propose the next 3 tasks".`)
	} else {
		lines = append(lines, `I captured your kickoff request. Send it again without "/codex" and I will use it as the planning brief for this repo session.`)
	}
	return req.Reply(strings.Join(lines, "\n"))
}

func codexExecutorModel(rt *Runtime) string {
	if rt == nil || rt.FindCodexModel == nil {
		return ""
	}
	return strings.TrimSpace(rt.FindCodexModel())
}

func codexPhaseLabel(workMode string, approvalPending bool) string {
	switch normalizeCodexWorkMode(workMode) {
	case "codex-plan":
		if approvalPending {
			return "awaiting approval"
		}
		return "planning"
	default:
		if approvalPending {
			return "awaiting approval"
		}
		return "inactive"
	}
}

func normalizeCodexWorkMode(workMode string) string {
	workMode = strings.ToLower(strings.TrimSpace(workMode))
	if workMode == "codex" {
		return "codex-plan"
	}
	return workMode
}

func codexPlannerState(rt *Runtime) (phase, model string, planner *CodexPlannerStatusInfo) {
	if rt == nil {
		return "", "", nil
	}
	if rt.CodexPlannerStatus != nil {
		if info, ok := rt.CodexPlannerStatus(); ok && info != nil {
			phase = strings.TrimSpace(info.Phase)
			if phase == "" {
				if info.ApprovalPending {
					phase = "awaiting approval"
				} else {
					phase = "inactive"
				}
			}
			if info.Phase == "" && info.ApprovalPending {
				info.Phase = phase
			}
			return phase, strings.TrimSpace(info.Model), info
		}
	}

	model = ""
	if rt.FindCodexModel != nil {
		model = strings.TrimSpace(rt.FindCodexModel())
	}
	phase = "inactive"
	approvalPending := false
	if rt.GetSessionWorkMode != nil {
		workMode := strings.TrimSpace(rt.GetSessionWorkMode())
		approvalPending = rt.GetCodexApprovalPending != nil && rt.GetCodexApprovalPending()
		phase = codexPhaseLabel(workMode, approvalPending)
	}
	if (phase == "" || phase == "inactive") && rt.CodexActive != nil {
		if info, ok := rt.CodexActive(); ok && info != nil {
			phase = codexPhaseLabel("codex-plan", approvalPending)
		}
	}
	return phase, model, &CodexPlannerStatusInfo{
		Phase:           phase,
		Model:           model,
		ApprovalPending: approvalPending,
	}
}

func codexActiveRunState(rt *Runtime) *CodexRunInfo {
	if rt == nil {
		return nil
	}
	if rt.CodexRunStatus != nil {
		if info, ok := rt.CodexRunStatus(); ok && info != nil {
			return info
		}
	}
	if rt.CodexRunList == nil {
		return nil
	}
	runs := rt.CodexRunList()
	if len(runs) == 0 {
		return nil
	}
	for i := range runs {
		run := runs[i]
		if run.Active || strings.EqualFold(strings.TrimSpace(run.Status), "running") {
			return &run
		}
	}
	run := runs[0]
	return &run
}

func codexRunLabel(info *CodexRunInfo) string {
	if info == nil {
		return "none"
	}
	status := strings.TrimSpace(info.Status)
	if status == "" {
		if info.Active {
			return "active"
		}
		return "unknown"
	}
	return status
}

func formatCodexRunListLine(run CodexRunInfo) string {
	parts := make([]string, 0, 8)
	head := run.ID
	if head == "" {
		head = "unknown"
	}
	if run.RepoSlug != "" {
		head += " " + run.RepoSlug
	}
	if run.Active {
		head += " [active]"
	}
	parts = append(parts, head)
	if run.Status != "" {
		parts = append(parts, "status="+run.Status)
	}
	if run.Model != "" {
		parts = append(parts, "model="+run.Model)
	}
	if run.Branch != "" {
		parts = append(parts, "branch="+run.Branch)
	}
	if run.Worktree != "" {
		parts = append(parts, "worktree="+run.Worktree)
	}
	if !run.StartedAt.IsZero() {
		parts = append(parts, "@ "+run.StartedAt.UTC().Format(time.RFC3339))
	}
	return "- " + strings.Join(parts, " ")
}

func activateCodexSessionForConversationalEntry(rt *Runtime, intent string) (*CodexSessionInfo, string, error) {
	if rt == nil || rt.CodexActive == nil {
		return nil, "", fmt.Errorf(unavailableMsg)
	}
	if info, ok := rt.CodexActive(); ok && info != nil {
		return info, "Using the already-active codex session for this chat.", nil
	}

	intent = strings.TrimSpace(intent)

	if rt.CodexListSessions != nil && rt.CodexAttach != nil {
		sessions := rt.CodexListSessions()
		if len(sessions) > 0 {
			if matched, ambiguous := matchCodexSessionByIntent(intent, sessions); matched != nil {
				info, err := rt.CodexAttach(matched.ID)
				if err != nil {
					return nil, "", err
				}
				return info, fmt.Sprintf("Matched existing project: %s.", matched.Slug), nil
			} else if len(ambiguous) > 0 {
				return nil, "", fmt.Errorf("multiple codex projects matched (%s). Use /codex resume <repo-slug>.", strings.Join(ambiguous, ", "))
			}

			if intent == "" {
				latest := sessions[0]
				info, err := rt.CodexAttach(latest.ID)
				if err != nil {
					return nil, "", err
				}
				return info, fmt.Sprintf("Resumed this chat's most recent project: %s.", latest.Slug), nil
			}
		}
	}

	if intent != "" && rt.CodexListGlobalSessions != nil && rt.CodexAttach != nil {
		sessions := rt.CodexListGlobalSessions()
		if len(sessions) > 0 {
			if matched, ambiguous := matchCodexSessionByIntent(intent, sessions); matched != nil {
				info, err := rt.CodexAttach(matched.ID)
				if err != nil {
					return nil, "", err
				}
				return info, fmt.Sprintf("Matched existing project from your global codex history: %s.", matched.Slug), nil
			} else if len(ambiguous) > 0 {
				return nil, "", fmt.Errorf("multiple codex projects matched (%s). Use /codex resume <repo-slug>.", strings.Join(ambiguous, ", "))
			}
		}
	}

	if intent == "" || rt.ListCodexRepoTargets == nil || rt.CodexNewSession == nil {
		return nil, "", nil
	}

	repos, err := rt.ListCodexRepoTargets(30)
	if err != nil {
		return nil, "", nil
	}
	matchedRepo, ambiguousRepos := matchCodexRepoByIntent(intent, repos)
	if matchedRepo == "" {
		if len(ambiguousRepos) > 0 {
			return nil, "", fmt.Errorf("multiple GitHub repos matched (%s). Use /codex new owner/repo.", strings.Join(ambiguousRepos, ", "))
		}
		return nil, "", nil
	}

	slug, source, ok := inferCodexNewFromSingleArg(matchedRepo)
	if !ok {
		slug = strings.ReplaceAll(strings.TrimSpace(matchedRepo), "/", "-")
		source = strings.TrimSpace(matchedRepo)
	}
	info, err := rt.CodexNewSession(slug, source)
	if err != nil {
		return nil, "", err
	}
	return info, fmt.Sprintf("Created new project from GitHub repo: %s.", matchedRepo), nil
}

func codexCommandTail(text string) string {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) <= 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(parts[1:], " "))
}

func matchCodexSessionByIntent(intent string, sessions []CodexSessionInfo) (*CodexSessionInfo, []string) {
	intentNorm := normalizeCodexIntentToken(intent)
	if intentNorm == "" {
		return nil, nil
	}

	matches := make([]CodexSessionInfo, 0, 3)
	for _, session := range sessions {
		if codexSessionMatchesIntent(intentNorm, session) {
			matches = append(matches, session)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	default:
		labels := make([]string, 0, len(matches))
		for _, m := range matches {
			if len(labels) >= 3 {
				break
			}
			labels = append(labels, m.Slug)
		}
		return nil, labels
	}
}

func matchCodexRepoByIntent(intent string, repos []string) (string, []string) {
	intentNorm := normalizeCodexIntentToken(intent)
	if intentNorm == "" {
		return "", nil
	}

	matches := make([]string, 0, 3)
	for _, repo := range repos {
		if codexRepoMatchesIntent(intentNorm, repo) {
			matches = append(matches, repo)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		if len(matches) > 3 {
			matches = matches[:3]
		}
		return "", matches
	}
}

func codexSessionMatchesIntent(intentNorm string, session CodexSessionInfo) bool {
	aliases := make([]string, 0, 4)
	aliases = append(aliases, session.Slug)
	if idx := strings.LastIndex(session.Slug, "-"); idx >= 0 && idx+1 < len(session.Slug) {
		aliases = append(aliases, session.Slug[idx+1:])
	}
	if repoName := repoNameFromOwnerRepo(session.RepoURL); repoName != "" {
		aliases = append(aliases, repoName)
	}
	if repoName := repoNameFromPath(session.RepoPath); repoName != "" {
		aliases = append(aliases, repoName)
	}

	for _, alias := range aliases {
		aliasNorm := normalizeCodexIntentToken(alias)
		if len(aliasNorm) < 3 {
			continue
		}
		if strings.Contains(intentNorm, aliasNorm) {
			return true
		}
	}
	return false
}

func codexRepoMatchesIntent(intentNorm, repo string) bool {
	ownerRepoNorm := normalizeCodexIntentToken(repo)
	if len(ownerRepoNorm) >= 3 && strings.Contains(intentNorm, ownerRepoNorm) {
		return true
	}
	repoName := repoNameFromOwnerRepo(repo)
	repoNameNorm := normalizeCodexIntentToken(repoName)
	return len(repoNameNorm) >= 3 && strings.Contains(intentNorm, repoNameNorm)
}

func repoNameFromOwnerRepo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "ssh://") {
		parsed, err := url.Parse(value)
		if err == nil {
			value = strings.Trim(parsed.Path, "/")
		}
	}
	if strings.HasPrefix(value, "git@") {
		if idx := strings.Index(value, ":"); idx >= 0 && idx+1 < len(value) {
			value = value[idx+1:]
		}
	}
	value = strings.TrimSuffix(strings.TrimSpace(value), ".git")
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return strings.TrimSpace(parts[len(parts)-1])
}

func repoNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func normalizeCodexIntentToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseCodexNewArgs(text string) (string, string, error) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) < 3 {
		return "", "", fmt.Errorf("Usage: /codex new <repo-slug|owner/repo|repo-url> [repo-url]")
	}

	first := strings.TrimSpace(parts[2])
	if first == "" {
		return "", "", fmt.Errorf("Usage: /codex new <repo-slug|owner/repo|repo-url> [repo-url]")
	}

	if len(parts) == 3 {
		if slug, source, ok := inferCodexNewFromSingleArg(first); ok {
			return slug, source, nil
		}
		return first, "", nil
	}

	source := strings.TrimSpace(strings.Join(parts[3:], " "))
	return first, source, nil
}

func inferCodexNewFromSingleArg(arg string) (slug, source string, ok bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", false
	}

	isURL := strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "ssh://") || strings.HasPrefix(arg, "git@")
	isOwnerRepo := strings.Count(arg, "/") == 1 && !strings.ContainsAny(arg, " \t:")
	if !isURL && !isOwnerRepo {
		return "", "", false
	}

	source = arg
	if isOwnerRepo {
		slug = strings.ReplaceAll(arg, "/", "-")
		return slug, source, true
	}

	slug = slugFromRepoSource(arg)
	if slug == "" {
		return "", "", false
	}
	return slug, source, true
}

func slugFromRepoSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	pathPart := source
	if strings.HasPrefix(source, "git@") {
		if idx := strings.Index(source, ":"); idx >= 0 && idx+1 < len(source) {
			pathPart = source[idx+1:]
		}
	} else if parsed, err := url.Parse(source); err == nil {
		pathPart = parsed.Path
	}

	pathPart = strings.TrimSpace(strings.Trim(pathPart, "/"))
	pathPart = strings.TrimSuffix(pathPart, ".git")
	pathPart = strings.ReplaceAll(pathPart, "/", "-")
	pathPart = strings.Trim(pathPart, "-._")
	return pathPart
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func containsStringFold(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}
