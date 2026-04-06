package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const codexPromptHistoryLimit = 24

func (al *AgentLoop) findCodexPlannerModelName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	if mc, err := cfg.GetModelConfig("gpt-5.4-mini"); err == nil && mc != nil {
		proto, _ := providers.ExtractProtocol(mc.Model)
		if !strings.EqualFold(proto, "codex-cli") && !strings.EqualFold(proto, "codexcli") {
			return "gpt-5.4-mini"
		}
	}

	defaultName := strings.TrimSpace(cfg.Agents.Defaults.ModelName)
	if defaultName != "" {
		if mc, err := cfg.GetModelConfig(defaultName); err == nil && mc != nil {
			proto, _ := providers.ExtractProtocol(mc.Model)
			if !strings.EqualFold(proto, "codex-cli") && !strings.EqualFold(proto, "codexcli") {
				return defaultName
			}
		}
	}

	for _, mc := range cfg.ModelList {
		if mc == nil || strings.TrimSpace(mc.ModelName) == "" {
			continue
		}
		proto, _ := providers.ExtractProtocol(mc.Model)
		if !strings.EqualFold(proto, "codex-cli") && !strings.EqualFold(proto, "codexcli") {
			return strings.TrimSpace(mc.ModelName)
		}
	}
	return ""
}

func resolveCodexCLIModelArg(cfg *config.Config, modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}

	if cfg != nil {
		if mc, err := cfg.GetModelConfig(modelName); err == nil && mc != nil {
			proto, modelID := providers.ExtractProtocol(mc.Model)
			if strings.EqualFold(proto, "codex-cli") || strings.EqualFold(proto, "codexcli") {
				return strings.TrimSpace(modelID)
			}
		}
	}

	proto, modelID := providers.ExtractProtocol(modelName)
	if strings.EqualFold(proto, "codex-cli") || strings.EqualFold(proto, "codexcli") {
		return strings.TrimSpace(modelID)
	}

	return modelName
}

func (al *AgentLoop) codexPlannerStatusRuntimeInfo(sessionKey string) (*commands.CodexPlannerStatusInfo, bool) {
	if al == nil || al.codexStore == nil {
		return nil, false
	}
	sessionRec, ok := al.codexStore.Active(sessionKey)
	if !ok || sessionRec == nil {
		return nil, false
	}
	runtime, _ := al.codexStore.SessionRuntime(sessionKey)
	phase := "planning"
	switch {
	case runtime.DeployConfirmPending:
		phase = "awaiting deploy approval"
	case runtime.ApprovalPending:
		phase = "awaiting approval"
	}
	if active, ok := al.codexStore.ActiveRun(sessionKey); ok && active != nil {
		switch strings.ToLower(strings.TrimSpace(active.Status)) {
		case codexRunStatusQueued:
			phase = "queued"
		case codexRunStatusRunning:
			phase = "executing"
		default:
			phase = strings.TrimSpace(active.Status)
		}
	}
	info := &commands.CodexPlannerStatusInfo{
		Phase:           phase,
		Model:           strings.TrimSpace(runtime.PlannerModel),
		SessionID:       sessionRec.ID,
		RepoSlug:        sessionRec.Slug,
		RepoPath:        sessionRec.RepoPath,
		RepoURL:         sessionRec.RepoURL,
		ApprovalPending: runtime.ApprovalPending,
	}
	if info.Model == "" {
		info.Model = strings.TrimSpace(al.findCodexPlannerModelName(al.GetConfig()))
	}
	return info, true
}

func (al *AgentLoop) codexRunStatusRuntimeInfo(sessionKey string) (*commands.CodexRunInfo, bool) {
	if al == nil || al.codexStore == nil {
		return nil, false
	}
	if rec, ok := al.codexStore.ActiveRun(sessionKey); ok && rec != nil {
		info := codexRunRecordToInfo(rec, true)
		return &info, true
	}
	runs := al.codexStore.ListRuns(sessionKey)
	if len(runs) == 0 {
		return nil, false
	}
	info := codexRunRecordToInfo(&runs[0], false)
	return &info, true
}

func (al *AgentLoop) codexRunListRuntimeInfo(sessionKey string) []commands.CodexRunInfo {
	if al == nil || al.codexStore == nil {
		return nil
	}
	runs := al.codexStore.ListRuns(sessionKey)
	out := make([]commands.CodexRunInfo, 0, len(runs))
	activeID := ""
	if active, ok := al.codexStore.ActiveRun(sessionKey); ok && active != nil {
		activeID = active.ID
	}
	for i := range runs {
		rec := runs[i]
		out = append(out, codexRunRecordToInfo(&rec, rec.ID == activeID))
	}
	return out
}

func codexRunRecordToInfo(rec *codexRunRecord, active bool) commands.CodexRunInfo {
	if rec == nil {
		return commands.CodexRunInfo{}
	}
	return commands.CodexRunInfo{
		ID:         rec.ID,
		SessionID:  rec.SessionID,
		RepoSlug:   rec.RepoSlug,
		RepoPath:   rec.RepoPath,
		RepoURL:    rec.RepoURL,
		Branch:     rec.BranchName,
		Worktree:   rec.WorktreePath,
		Model:      rec.ExecutorModel,
		Status:     rec.Status,
		PID:        rec.PID,
		ExitCode:   rec.ExitCode,
		Active:     active,
		StartedAt:  rec.StartedAt,
		UpdatedAt:  rec.UpdatedAt,
		FinishedAt: rec.FinishedAt,
	}
}

func (al *AgentLoop) codexRunTail(sessionKey, runID string, lines int) (string, error) {
	if al == nil || al.codexStore == nil {
		return "", fmt.Errorf("codex runs are not initialized")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		if active, ok := al.codexStore.ActiveRun(sessionKey); ok && active != nil {
			runID = active.ID
		} else {
			runs := al.codexStore.ListRuns(sessionKey)
			if len(runs) == 0 {
				return "", fmt.Errorf("no codex run is active yet")
			}
			runID = runs[0].ID
		}
	}
	run, ok := al.codexStore.GetRun(runID)
	if !ok || run == nil {
		return "", fmt.Errorf("codex run %q not found", runID)
	}
	if active, ok := al.codexStore.Active(sessionKey); ok && active != nil && strings.TrimSpace(active.ID) != strings.TrimSpace(run.SessionID) {
		return "", fmt.Errorf("codex run %q does not belong to the active session in this chat", runID)
	}
	if strings.TrimSpace(run.LogPath) == "" {
		return "", nil
	}
	return tailFileLines(run.LogPath, lines)
}

func (al *AgentLoop) startApprovedCodexRun(
	_ context.Context,
	agent *AgentInstance,
	opts *processOptions,
	userMessage string,
) (string, error) {
	if al == nil || al.codexStore == nil {
		return "", fmt.Errorf("codex runs are not initialized")
	}
	if agent == nil || opts == nil {
		return "", fmt.Errorf("codex planner context is incomplete")
	}
	sessionRec, ok := al.codexStore.Active(opts.SessionKey)
	if !ok || sessionRec == nil {
		return "", fmt.Errorf("no active codex session in this chat")
	}
	runtime, _ := al.codexStore.SessionRuntime(opts.SessionKey)
	if !runtime.ApprovalPending {
		return "", fmt.Errorf("no codex plan is awaiting approval yet")
	}

	plannerModel := strings.TrimSpace(runtime.PlannerModel)
	if plannerModel == "" {
		plannerModel = strings.TrimSpace(al.getSessionModelOverride(opts.SessionKey))
	}
	if plannerModel == "" {
		plannerModel = strings.TrimSpace(al.findCodexPlannerModelName(al.GetConfig()))
	}

	executorModel := strings.TrimSpace(runtime.ExecutorModel)
	if executorModel == "" {
		executorModel = strings.TrimSpace(al.findCodexModelName(al.GetConfig()))
	}
	if executorModel == "" {
		return "", fmt.Errorf("no codex-cli model configured in model_list")
	}

	history := agent.Sessions.GetHistory(opts.SessionKey)
	summary := agent.Sessions.GetSummary(opts.SessionKey)
	planText := latestAssistantMessage(history)
	_, latestPlanHash := codexPlanIdentity(planText)
	if latestPlanHash == "" || (strings.TrimSpace(runtime.PendingPlanHash) != "" && latestPlanHash != strings.TrimSpace(runtime.PendingPlanHash)) {
		_ = al.codexStore.UpdateSessionRuntime(opts.SessionKey, func(runtime *codexSessionRuntimeState) {
			runtime.WorkMode = "codex-plan"
			runtime.ApprovalPending = false
			runtime.PendingPlanID = ""
			runtime.PendingPlanHash = ""
		})
		al.clearCodexApprovalPending(opts.SessionKey)
		return "The pending codex plan no longer matches the latest planner reply. Review the latest plan and reply `proceed` again.", nil
	}

	run, err := al.codexStore.CreateRun(opts.SessionKey, codexRunCreateOptions{
		PlannerModel:  plannerModel,
		ExecutorModel: executorModel,
		Mode:          "autonomous",
		PlanID:        runtime.PendingPlanID,
		PlanHash:      runtime.PendingPlanHash,
		InitiatedBy:   strings.TrimSpace(opts.SenderID),
	})
	if err != nil {
		return "", err
	}

	worktree, err := al.codexStore.PrepareRunWorktree(run.ID)
	if err != nil {
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", err
	}

	workspace := ""
	if agent != nil {
		workspace = agent.Workspace
	}
	logDir := filepath.Join(workspace, "logs", "codex")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", err
	}
	logPath := filepath.Join(logDir, run.ID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", err
	}

	promptMessages := buildCodexExecutionPromptMessages(sessionRec, run, summary, history, planText, userMessage)
	prompt := providers.BuildCodexCLIPrompt(promptMessages, nil)

	cliModel := resolveCodexCLIModelArg(al.GetConfig(), executorModel)
	args := providers.BuildCodexCLIArgs(cliModel, worktree)
	cmd := exec.Command("codex", args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", fmt.Errorf("failed to start codex run: %w", err)
	}

	if err := al.codexStore.MarkRunStarted(run.ID, cmd.Process.Pid, logPath); err != nil {
		_ = killCodexProcess(cmd.Process.Pid)
		_ = logFile.Close()
		return "", err
	}
	_ = al.codexStore.UpdateSessionRuntime(opts.SessionKey, func(runtime *codexSessionRuntimeState) {
		runtime.WorkMode = "codex-plan"
		runtime.ApprovalPending = false
		runtime.PendingPlanID = ""
		runtime.PendingPlanHash = ""
		runtime.ActiveRunID = run.ID
		runtime.LastRunID = run.ID
		runtime.PlannerModel = plannerModel
		runtime.ExecutorModel = executorModel
	})

	go al.waitForCodexRun(cmd, logFile, run.ID, opts.Channel, opts.ChatID)

	return strings.Join([]string{
		fmt.Sprintf("Codex run started: %s", run.ID),
		fmt.Sprintf("Repo: %s", sessionRec.Slug),
		fmt.Sprintf("Planner: %s", plannerModel),
		fmt.Sprintf("Executor: %s", executorModel),
		fmt.Sprintf("Worktree: %s", worktree),
		"Use /codex status or /codex tail to inspect progress.",
	}, "\n"), nil
}

func (al *AgentLoop) waitForCodexRun(cmd *exec.Cmd, logFile *os.File, runID, channel, chatID string) {
	err := cmd.Wait()
	_ = logFile.Close()

	run, ok := al.codexStore.GetRun(runID)
	if !ok || run == nil {
		return
	}

	exitCode := 0
	status := codexRunStatusSucceeded
	if err != nil {
		status = codexRunStatusFailed
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if status == codexRunStatusSucceeded {
		_ = al.codexStore.MarkRunFinished(runID, codexRunStatusSucceeded, exitCode, "")
	} else {
		_ = al.codexStore.MarkRunFailed(runID, exitCode, err.Error())
	}

	if finished, ok := al.codexStore.GetRun(runID); ok && finished != nil {
		if status == codexRunStatusSucceeded && isPicoClawRun(finished) {
			_ = al.codexStore.UpdateSessionRuntime(finished.ScopeKey, func(runtime *codexSessionRuntimeState) {
				runtime.DeployConfirmPending = true
				runtime.LastRunID = finished.ID
				runtime.ActiveRunID = ""
			})
		}
		al.sendCodexRunNotification(channel, chatID, finished)
	}
}

func (al *AgentLoop) startApprovedPicoClawDeploy(scopeKey, channel, chatID string) (string, error) {
	if al == nil || al.codexStore == nil {
		return "", fmt.Errorf("codex runs are not initialized")
	}
	sessionRec, ok := al.codexStore.Active(scopeKey)
	if !ok || sessionRec == nil {
		return "", fmt.Errorf("no active codex session in this chat")
	}
	runtime, ok := al.codexStore.SessionRuntime(scopeKey)
	if !ok || !runtime.DeployConfirmPending || strings.TrimSpace(runtime.LastRunID) == "" {
		return "", fmt.Errorf("no PicoClaw deploy is awaiting confirmation")
	}
	baseRun, ok := al.codexStore.GetRun(runtime.LastRunID)
	if !ok || baseRun == nil {
		return "", fmt.Errorf("the last approved PicoClaw run could not be found")
	}
	if strings.TrimSpace(baseRun.WorktreePath) == "" {
		return "", fmt.Errorf("the approved PicoClaw run does not have a worktree to deploy")
	}

	plannerModel := strings.TrimSpace(runtime.PlannerModel)
	executorModel := "deploy-script"
	run, err := al.codexStore.CreateRun(scopeKey, codexRunCreateOptions{
		PlannerModel:  plannerModel,
		ExecutorModel: executorModel,
		Mode:          "deploy",
		InitiatedBy:   "deploy-approval",
	})
	if err != nil {
		return "", err
	}

	workspace := filepath.Dir(filepath.Dir(al.codexStore.stateFile))
	logDir := filepath.Join(workspace, "logs", "codex")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", err
	}
	logPath := filepath.Join(logDir, run.ID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", err
	}

	scriptPath := filepath.Join(workspace, "scripts", "picoclaw_deploy_local.sh")
	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"SRC_DIR="+baseRun.WorktreePath,
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = al.codexStore.MarkRunFailed(run.ID, -1, err.Error())
		return "", fmt.Errorf("failed to start deploy run: %w", err)
	}

	if err := al.codexStore.MarkRunStarted(run.ID, cmd.Process.Pid, logPath); err != nil {
		_ = killCodexProcess(cmd.Process.Pid)
		_ = logFile.Close()
		return "", err
	}
	_ = al.codexStore.UpdateSessionRuntime(scopeKey, func(runtime *codexSessionRuntimeState) {
		runtime.DeployConfirmPending = false
		runtime.WorkMode = "codex-plan"
		runtime.ActiveRunID = run.ID
		runtime.LastRunID = run.ID
	})

	go al.waitForCodexRun(cmd, logFile, run.ID, channel, chatID)

	return strings.Join([]string{
		fmt.Sprintf("PicoClaw deploy started: %s", run.ID),
		fmt.Sprintf("Source worktree: %s", baseRun.WorktreePath),
		"Use /codex status or /codex tail to inspect progress.",
	}, "\n"), nil
}

func (al *AgentLoop) stopActiveCodexRun(scopeKey string) error {
	if al == nil || al.codexStore == nil {
		return fmt.Errorf("codex runs are not initialized")
	}
	run, ok := al.codexStore.ActiveRun(scopeKey)
	if !ok || run == nil {
		return nil
	}
	if run.PID <= 0 {
		return al.codexStore.MarkRunStopped(run.ID, "run stopped without a live pid")
	}
	if err := killCodexProcess(run.PID); err != nil {
		return err
	}
	return al.codexStore.MarkRunStopped(run.ID, "run stopped by user")
}

func latestAssistantMessage(history []providers.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" && strings.TrimSpace(history[i].Content) != "" {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

func buildCodexExecutionPromptMessages(
	sessionRec *codexSessionRecord,
	run *codexRunRecord,
	summary string,
	history []providers.Message,
	planText string,
	userMessage string,
) []providers.Message {
	systemLines := []string{
		"You are the Codex executor for a managed Telegram /codex session.",
		"Execute the approved plan inside the active repo worktree.",
		"Make concrete progress autonomously and validate changes with focused commands where practical.",
		"Do not deploy, restart services, or mutate system-wide state in this phase.",
	}
	if sessionRec != nil {
		systemLines = append(systemLines,
			"Repo slug: "+strings.TrimSpace(sessionRec.Slug),
			"Canonical repo path: "+strings.TrimSpace(sessionRec.RepoPath),
		)
		if strings.TrimSpace(sessionRec.RepoURL) != "" {
			systemLines = append(systemLines, "Repo remote: "+strings.TrimSpace(sessionRec.RepoURL))
		}
	}
	if run != nil && strings.TrimSpace(run.WorktreePath) != "" {
		systemLines = append(systemLines, "Execution worktree: "+strings.TrimSpace(run.WorktreePath))
	}

	msgs := []providers.Message{{Role: "system", Content: strings.Join(systemLines, "\n")}}
	if strings.TrimSpace(summary) != "" {
		msgs = append(msgs, providers.Message{
			Role:    "system",
			Content: "Conversation summary:\n" + strings.TrimSpace(summary),
		})
	}
	if len(history) > codexPromptHistoryLimit {
		history = history[len(history)-codexPromptHistoryLimit:]
	}
	for _, msg := range history {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		msgs = append(msgs, providers.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	approvalText := "The user explicitly approved the latest plan. Execute it now in the active repo worktree."
	if strings.TrimSpace(planText) != "" {
		approvalText += "\nApproved plan:\n" + strings.TrimSpace(planText)
	}
	if strings.TrimSpace(userMessage) != "" {
		approvalText += "\nApproval message: " + strings.TrimSpace(userMessage)
	}
	msgs = append(msgs, providers.Message{Role: "user", Content: approvalText})
	return msgs
}

func (al *AgentLoop) sendCodexRunNotification(channel, chatID string, run *codexRunRecord) {
	if al == nil || al.bus == nil || strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" || run == nil {
		return
	}

	text := fmt.Sprintf("Codex run %s finished with status: %s.", run.ID, run.Status)
	if strings.TrimSpace(run.LogPath) != "" {
		if summary, err := tailFileLines(run.LogPath, 20); err == nil && strings.TrimSpace(summary) != "" {
			if parsed, err := providers.ParseCodexCLIJSONLEvents(summary); err == nil && parsed != nil && strings.TrimSpace(parsed.Content) != "" {
				text += "\n" + strings.TrimSpace(parsed.Content)
			} else {
				text += "\nLog tail:\n" + strings.TrimSpace(summary)
			}
		}
	}
	if isPicoClawRun(run) && run.Status == codexRunStatusSucceeded && strings.TrimSpace(run.Mode) != "deploy" {
		if cfg := al.GetConfig(); cfg != nil && cfg.SelfImprove.Enabled {
			targets := al.selfImproveTargets(cfg)
			if len(targets) > 0 {
				text += "\nUse `/self-improve ship <target>` to publish this run and advance a target deploy branch."
			}
		}
		text += "\nReply `deploy` to apply and restart PicoClaw from this worktree."
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: text,
	})
}

func isPicoClawRun(run *codexRunRecord) bool {
	if run == nil {
		return false
	}
	equalsPicoclaw := func(raw string) bool {
		name := strings.ToLower(strings.TrimSpace(raw))
		return name == "picoclaw"
	}
	if equalsPicoclaw(run.RepoSlug) {
		return true
	}
	if base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.TrimSpace(run.RepoPath)))); base == "picoclaw" {
		return true
	}
	if repoURL := strings.TrimSpace(run.RepoURL); repoURL != "" {
		repoURL = strings.TrimSuffix(repoURL, ".git")
		repoURL = strings.TrimSuffix(repoURL, "/")
		if slash := strings.LastIndex(repoURL, "/"); slash >= 0 {
			return equalsPicoclaw(repoURL[slash+1:])
		}
	}
	return false
}

func killCodexProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

func codexPlanIdentity(planText string) (string, string) {
	planText = strings.TrimSpace(planText)
	if planText == "" {
		return "", ""
	}
	sum := sha256.Sum256([]byte(planText))
	return "plan-" + newCodexRunID(), hex.EncodeToString(sum[:8])
}

func tailFileLines(path string, lines int) (string, error) {
	if lines <= 0 {
		lines = 120
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}
