package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	codexRunStatusQueued    = "queued"
	codexRunStatusRunning   = "running"
	codexRunStatusSucceeded = "succeeded"
	codexRunStatusFailed    = "failed"
	codexRunStatusStopped   = "stopped"
	codexRunStatusUnknown   = "unknown"
)

type codexSessionRuntimeState struct {
	PlannerModel         string `json:"planner_model,omitempty"`
	ExecutorModel        string `json:"executor_model,omitempty"`
	WorkMode             string `json:"work_mode,omitempty"`
	ApprovalPending      bool   `json:"approval_pending,omitempty"`
	DeployConfirmPending bool   `json:"deploy_confirm_pending,omitempty"`
	PendingPlanID        string `json:"pending_plan_id,omitempty"`
	PendingPlanHash      string `json:"pending_plan_hash,omitempty"`
	ActiveRunID          string `json:"active_run_id,omitempty"`
	LastRunID            string `json:"last_run_id,omitempty"`
}

type codexRunRecord struct {
	ID                   string    `json:"id"`
	ScopeKey             string    `json:"scope_key,omitempty"`
	SessionID            string    `json:"session_id"`
	RepoSlug             string    `json:"repo_slug"`
	RepoPath             string    `json:"repo_path"`
	RepoURL              string    `json:"repo_url,omitempty"`
	WorktreePath         string    `json:"worktree_path"`
	BranchName           string    `json:"branch_name"`
	PlannerModel         string    `json:"planner_model,omitempty"`
	ExecutorModel        string    `json:"executor_model,omitempty"`
	Mode                 string    `json:"mode,omitempty"`
	Status               string    `json:"status"`
	PID                  int       `json:"pid,omitempty"`
	ExitCode             int       `json:"exit_code,omitempty"`
	LogPath              string    `json:"log_path,omitempty"`
	PlanID               string    `json:"plan_id,omitempty"`
	PlanHash             string    `json:"plan_hash,omitempty"`
	InitiatedBy          string    `json:"initiated_by,omitempty"`
	DeployConfirmPending bool      `json:"deploy_confirm_pending,omitempty"`
	PublishedBranch      string    `json:"published_branch,omitempty"`
	PublishedSHA         string    `json:"published_sha,omitempty"`
	ShippedTarget        string    `json:"shipped_target,omitempty"`
	ShippedDeployBranch  string    `json:"shipped_deploy_branch,omitempty"`
	ShippedSHA           string    `json:"shipped_sha,omitempty"`
	Error                string    `json:"error,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	FinishedAt           time.Time `json:"finished_at,omitempty"`
	LastHeartbeatAt      time.Time `json:"last_heartbeat_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type codexRunSnapshot struct {
	Version         int                        `json:"version"`
	Runs            map[string]*codexRunRecord `json:"runs"`
	ActiveBySession map[string]string          `json:"active_by_session"`
	ActiveByRepo    map[string]string          `json:"active_by_repo"`
}

type codexRunCreateOptions struct {
	PlannerModel         string
	ExecutorModel        string
	Mode                 string
	PlanID               string
	PlanHash             string
	InitiatedBy          string
	DeployConfirmPending bool
}

func (s *codexSessionStore) SessionRuntime(scopeKey string) (codexSessionRuntimeState, bool) {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil || scopeKey == "" {
		return codexSessionRuntimeState{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.sessionRecordByScopeLocked(scopeKey)
	if rec == nil {
		return codexSessionRuntimeState{}, false
	}
	return sessionRuntimeFromRecord(rec), true
}

func (s *codexSessionStore) SetSessionRuntime(scopeKey string, runtime codexSessionRuntimeState) error {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil || scopeKey == "" {
		return fmt.Errorf("codex sessions require a valid session scope")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.sessionRecordByScopeLocked(scopeKey)
	if rec == nil {
		return fmt.Errorf("codex session not found for scope %q", scopeKey)
	}
	applySessionRuntimeLocked(rec, runtime)
	rec.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *codexSessionStore) UpdateSessionRuntime(scopeKey string, fn func(*codexSessionRuntimeState)) error {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil || scopeKey == "" {
		return fmt.Errorf("codex sessions require a valid session scope")
	}
	if fn == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.sessionRecordByScopeLocked(scopeKey)
	if rec == nil {
		return fmt.Errorf("codex session not found for scope %q", scopeKey)
	}

	runtime := sessionRuntimeFromRecord(rec)
	fn(&runtime)
	applySessionRuntimeLocked(rec, runtime)
	rec.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *codexSessionStore) CreateRun(scopeKey string, opts codexRunCreateOptions) (*codexRunRecord, error) {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil || scopeKey == "" {
		return nil, fmt.Errorf("codex sessions require a valid session scope")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.normalizeSessionStateLocked()
	s.normalizeRunStateLocked()

	sessionRec := s.sessionRecordByScopeLocked(scopeKey)
	if sessionRec == nil {
		return nil, fmt.Errorf("codex session not found for scope %q", scopeKey)
	}
	if active := s.activeRunForSessionLocked(scopeKey); active != nil {
		return nil, fmt.Errorf("codex session %q already has an active run (%s)", scopeKey, active.ID)
	}
	if active := s.activeRunForRepoLocked(sessionRec.RepoPath, sessionRec.Slug); active != nil {
		return nil, fmt.Errorf("repo %q already has an active run (%s)", sessionRec.Slug, active.ID)
	}

	runID := newCodexRunID()
	branchName := fmt.Sprintf("pc/%s/%s", sessionRec.ID, runID)
	worktreePath, err := s.worktreePathForRun(sessionRec.Slug, runID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	rec := &codexRunRecord{
		ID:                   runID,
		ScopeKey:             scopeKey,
		SessionID:            sessionRec.ID,
		RepoSlug:             sessionRec.Slug,
		RepoPath:             sessionRec.RepoPath,
		RepoURL:              sanitizeRepoRemote(sessionRec.RepoURL),
		WorktreePath:         worktreePath,
		BranchName:           branchName,
		PlannerModel:         strings.TrimSpace(opts.PlannerModel),
		ExecutorModel:        strings.TrimSpace(opts.ExecutorModel),
		Mode:                 strings.TrimSpace(opts.Mode),
		Status:               codexRunStatusQueued,
		PlanID:               strings.TrimSpace(opts.PlanID),
		PlanHash:             strings.TrimSpace(opts.PlanHash),
		InitiatedBy:          strings.TrimSpace(opts.InitiatedBy),
		DeployConfirmPending: opts.DeployConfirmPending,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if rec.Mode == "" {
		rec.Mode = "autonomous"
	}
	if rec.ExecutorModel == "" {
		rec.ExecutorModel = rec.PlannerModel
	}

	if s.runs.Runs == nil {
		s.runs.Runs = make(map[string]*codexRunRecord)
	}
	if s.runs.ActiveBySession == nil {
		s.runs.ActiveBySession = make(map[string]string)
	}
	if s.runs.ActiveByRepo == nil {
		s.runs.ActiveByRepo = make(map[string]string)
	}

	s.runs.Runs[rec.ID] = rec
	s.runs.ActiveBySession[scopeKey] = rec.ID
	s.runs.ActiveByRepo[sessionRec.RepoPath] = rec.ID
	sessionRec.ActiveRunID = rec.ID
	sessionRec.PlannerModel = rec.PlannerModel
	sessionRec.ExecutorModel = rec.ExecutorModel
	sessionRec.WorkMode = "codex-plan"
	sessionRec.ApprovalPending = false
	sessionRec.DeployConfirmPending = opts.DeployConfirmPending
	sessionRec.PendingPlanID = rec.PlanID
	sessionRec.PendingPlanHash = rec.PlanHash
	sessionRec.LastRunID = rec.ID
	sessionRec.UpdatedAt = now

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	if err := s.saveRunsLocked(); err != nil {
		return nil, err
	}

	return cloneCodexRunRecord(rec), nil
}

func (s *codexSessionStore) GetRun(runID string) (*codexRunRecord, bool) {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return nil, false
	}
	return cloneCodexRunRecord(rec), true
}

func (s *codexSessionStore) UpdateRun(runID string, fn func(*codexRunRecord)) error {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return fmt.Errorf("run id is required")
	}
	if fn == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return fmt.Errorf("run %q not found", runID)
	}
	fn(rec)
	rec.UpdatedAt = time.Now().UTC()
	return s.saveRunsLocked()
}

func (s *codexSessionStore) ListRuns(scopeKey string) []codexRunRecord {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]codexRunRecord, 0, len(s.runs.Runs))
	for _, rec := range s.runs.Runs {
		if rec == nil {
			continue
		}
		if scopeKey != "" && rec.ScopeKey != scopeKey {
			continue
		}
		result = append(result, *rec)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}

func (s *codexSessionStore) ActiveRun(scopeKey string) (*codexRunRecord, bool) {
	scopeKey = strings.TrimSpace(scopeKey)
	if s == nil || scopeKey == "" {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.activeRunForSessionLocked(scopeKey)
	if rec == nil {
		return nil, false
	}
	return cloneCodexRunRecord(rec), true
}

func (s *codexSessionStore) ActiveRunForRepo(repoPath, repoSlug string) (*codexRunRecord, bool) {
	if s == nil {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.activeRunForRepoLocked(repoPath, repoSlug)
	if rec == nil {
		return nil, false
	}
	return cloneCodexRunRecord(rec), true
}

func (s *codexSessionStore) MarkRunStarted(runID string, pid int, logPath string) error {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return fmt.Errorf("run id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return fmt.Errorf("run %q not found", runID)
	}
	rec.PID = pid
	rec.LogPath = strings.TrimSpace(logPath)
	if rec.Status == "" || rec.Status == codexRunStatusQueued || rec.Status == codexRunStatusUnknown {
		rec.Status = codexRunStatusRunning
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now().UTC()
	}
	rec.LastHeartbeatAt = time.Now().UTC()
	rec.UpdatedAt = rec.LastHeartbeatAt

	sessionRec := s.sessionRecordByIDLocked(rec.SessionID)
	if sessionRec != nil {
		sessionRec.ActiveRunID = rec.ID
		sessionRec.UpdatedAt = rec.UpdatedAt
	}
	s.runs.ActiveBySession[rec.ScopeKey] = rec.ID
	s.runs.ActiveByRepo[rec.RepoPath] = rec.ID
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRunsLocked()
}

func (s *codexSessionStore) MarkRunHeartbeat(runID string) error {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return fmt.Errorf("run id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return fmt.Errorf("run %q not found", runID)
	}
	rec.LastHeartbeatAt = time.Now().UTC()
	rec.UpdatedAt = rec.LastHeartbeatAt
	return s.saveRunsLocked()
}

func (s *codexSessionStore) MarkRunFinished(runID, status string, exitCode int, errMsg string) error {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return fmt.Errorf("run id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return fmt.Errorf("run %q not found", runID)
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = codexRunStatusSucceeded
	}
	rec.Status = status
	rec.ExitCode = exitCode
	rec.Error = strings.TrimSpace(errMsg)
	now := time.Now().UTC()
	rec.FinishedAt = now
	rec.UpdatedAt = now

	if activeID := s.runs.ActiveBySession[rec.ScopeKey]; activeID == rec.ID {
		delete(s.runs.ActiveBySession, rec.ScopeKey)
	}
	if activeID := s.runs.ActiveByRepo[rec.RepoPath]; activeID == rec.ID {
		delete(s.runs.ActiveByRepo, rec.RepoPath)
	}
	if sessionRec := s.sessionRecordByIDLocked(rec.SessionID); sessionRec != nil {
		sessionRec.LastRunID = rec.ID
		if strings.TrimSpace(sessionRec.ActiveRunID) == rec.ID {
			sessionRec.ActiveRunID = ""
		}
		sessionRec.DeployConfirmPending = false
		sessionRec.UpdatedAt = now
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRunsLocked()
}

func (s *codexSessionStore) MarkRunFailed(runID string, exitCode int, errMsg string) error {
	return s.MarkRunFinished(runID, codexRunStatusFailed, exitCode, errMsg)
}

func (s *codexSessionStore) MarkRunStopped(runID string, errMsg string) error {
	return s.MarkRunFinished(runID, codexRunStatusStopped, -1, errMsg)
}

func (s *codexSessionStore) ReconcileRuns() ([]codexRunRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("codex sessions are not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	changed := make([]codexRunRecord, 0)
	for _, rec := range s.runs.Runs {
		if rec == nil {
			continue
		}
		if isTerminalRunStatus(rec.Status) {
			continue
		}
		if rec.PID <= 0 {
			if rec.Status != codexRunStatusUnknown {
				rec.Status = codexRunStatusUnknown
				rec.ExitCode = -1
				rec.FinishedAt = time.Now().UTC()
				rec.UpdatedAt = rec.FinishedAt
				changed = append(changed, *rec)
			}
			continue
		}
		if !processLooksAlive(rec.PID) {
			outcome, ok := detectCodexRunOutcomeFromLog(rec.LogPath)
			if ok {
				rec.Status = outcome.Status
				rec.ExitCode = outcome.ExitCode
				rec.Error = outcome.Error
			} else {
				rec.Status = codexRunStatusUnknown
				rec.ExitCode = -1
				rec.Error = "process not found during reconciliation"
			}
			rec.FinishedAt = time.Now().UTC()
			rec.UpdatedAt = rec.FinishedAt
			if sessionRec := s.sessionRecordByIDLocked(rec.SessionID); sessionRec != nil && strings.TrimSpace(sessionRec.ActiveRunID) == rec.ID {
				sessionRec.ActiveRunID = ""
				sessionRec.UpdatedAt = rec.UpdatedAt
			}
			delete(s.runs.ActiveBySession, rec.ScopeKey)
			delete(s.runs.ActiveByRepo, rec.RepoPath)
			changed = append(changed, *rec)
		} else {
			rec.LastHeartbeatAt = time.Now().UTC()
			rec.UpdatedAt = rec.LastHeartbeatAt
			s.runs.ActiveBySession[rec.ScopeKey] = rec.ID
			s.runs.ActiveByRepo[rec.RepoPath] = rec.ID
			if sessionRec := s.sessionRecordByIDLocked(rec.SessionID); sessionRec != nil {
				sessionRec.ActiveRunID = rec.ID
				sessionRec.UpdatedAt = rec.UpdatedAt
			}
		}
	}

	s.normalizeRunStateLocked()
	if len(changed) > 0 {
		if err := s.saveLocked(); err != nil {
			return changed, err
		}
		if err := s.saveRunsLocked(); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

type codexRunLogEvent struct {
	Type     string `json:"type"`
	Message  string `json:"message,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Error    *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type codexRunLogOutcome struct {
	Status   string
	ExitCode int
	Error    string
}

func detectCodexRunOutcomeFromLog(logPath string) (codexRunLogOutcome, bool) {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return codexRunLogOutcome{}, false
	}

	f, err := os.Open(logPath)
	if err != nil {
		return codexRunLogOutcome{}, false
	}
	defer f.Close()

	var (
		completed bool
		failed    bool
		exitCode  = -1
		lastError string
	)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.Contains(line, `"type"`) {
			continue
		}

		var evt codexRunLogEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "turn.completed":
			completed = true
			if evt.ExitCode != nil {
				exitCode = *evt.ExitCode
			} else if exitCode < 0 {
				exitCode = 0
			}
		case "turn.failed":
			failed = true
			if evt.ExitCode != nil {
				exitCode = *evt.ExitCode
			}
			if evt.Error != nil && strings.TrimSpace(evt.Error.Message) != "" {
				lastError = strings.TrimSpace(evt.Error.Message)
			}
		case "error":
			if strings.TrimSpace(evt.Message) != "" {
				lastError = strings.TrimSpace(evt.Message)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return codexRunLogOutcome{}, false
	}

	if completed {
		if exitCode < 0 {
			exitCode = 0
		}
		return codexRunLogOutcome{
			Status:   codexRunStatusSucceeded,
			ExitCode: exitCode,
		}, true
	}
	if failed || lastError != "" {
		return codexRunLogOutcome{
			Status:   codexRunStatusFailed,
			ExitCode: exitCode,
			Error:    lastError,
		}, true
	}
	return codexRunLogOutcome{}, false
}

func (s *codexSessionStore) PrepareRunWorktree(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if s == nil || runID == "" {
		return "", fmt.Errorf("run id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.runs.Runs[runID]
	if rec == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	if strings.TrimSpace(rec.RepoPath) == "" {
		return "", fmt.Errorf("run %q has no repo path", runID)
	}
	if !isGitRepo(rec.RepoPath) {
		return "", fmt.Errorf("repo path %q is not a git repository", rec.RepoPath)
	}

	if strings.TrimSpace(rec.WorktreePath) == "" {
		worktreePath, err := s.worktreePathForRun(rec.RepoSlug, rec.ID)
		if err != nil {
			return "", err
		}
		rec.WorktreePath = worktreePath
	}

	if err := os.MkdirAll(filepath.Dir(rec.WorktreePath), 0o700); err != nil {
		return "", err
	}

	if _, err := os.Stat(rec.WorktreePath); err == nil {
		if isGitRepo(rec.WorktreePath) {
			return rec.WorktreePath, nil
		}
		return "", fmt.Errorf("worktree path %q already exists and is not a git repository", rec.WorktreePath)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	branch := strings.TrimSpace(rec.BranchName)
	if branch == "" {
		branch = fmt.Sprintf("pc/%s/%s", rec.SessionID, rec.ID)
		rec.BranchName = branch
	}

	baseRef := "HEAD"
	if out, err := exec.Command("git", "-C", rec.RepoPath, "branch", "--show-current").Output(); err == nil {
		if current := strings.TrimSpace(string(out)); current != "" {
			baseRef = current
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	args := []string{"-C", rec.RepoPath, "worktree", "add"}
	if !branchExists(rec.RepoPath, branch) {
		args = append(args, "-b", branch, rec.WorktreePath, baseRef)
	} else {
		args = append(args, rec.WorktreePath, branch)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	rec.UpdatedAt = time.Now().UTC()
	if err := s.saveRunsLocked(); err != nil {
		return "", err
	}
	return rec.WorktreePath, nil
}

func (s *codexSessionStore) worktreePathForRun(repoSlug, runID string) (string, error) {
	repoSlug = strings.TrimSpace(repoSlug)
	runID = strings.TrimSpace(runID)
	if repoSlug == "" || runID == "" {
		return "", fmt.Errorf("repo slug and run id are required")
	}

	candidate := filepath.Clean(filepath.Join(s.worktreesRoot, repoSlug, runID))
	rel, err := filepath.Rel(s.worktreesRoot, candidate)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid worktree path for repo %q", repoSlug)
	}
	return candidate, nil
}

func (s *codexSessionStore) sessionRecordByScopeLocked(scopeKey string) *codexSessionRecord {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil
	}
	id := strings.TrimSpace(s.state.Bindings[scopeKey])
	if id == "" {
		return nil
	}
	return s.state.Sessions[id]
}

func (s *codexSessionStore) sessionRecordByIDLocked(sessionID string) *codexSessionRecord {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	return s.state.Sessions[sessionID]
}

func (s *codexSessionStore) activeRunForSessionLocked(scopeKey string) *codexRunRecord {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil
	}
	if runID := strings.TrimSpace(s.runs.ActiveBySession[scopeKey]); runID != "" {
		if rec := s.runs.Runs[runID]; rec != nil {
			return rec
		}
	}
	for _, rec := range s.runs.Runs {
		if rec == nil {
			continue
		}
		if rec.ScopeKey == scopeKey && isRunActiveStatus(rec.Status) {
			return rec
		}
	}
	return nil
}

func (s *codexSessionStore) activeRunForRepoLocked(repoPath, repoSlug string) *codexRunRecord {
	repoPath = strings.TrimSpace(repoPath)
	repoSlug = strings.TrimSpace(repoSlug)
	if repoPath == "" && repoSlug == "" {
		return nil
	}
	if runID := strings.TrimSpace(s.runs.ActiveByRepo[repoPath]); runID != "" {
		if rec := s.runs.Runs[runID]; rec != nil {
			return rec
		}
	}
	for _, rec := range s.runs.Runs {
		if rec == nil {
			continue
		}
		if !isRunActiveStatus(rec.Status) {
			continue
		}
		if repoPath != "" && rec.RepoPath == repoPath {
			return rec
		}
		if repoSlug != "" && rec.RepoSlug == repoSlug {
			return rec
		}
	}
	return nil
}

func (s *codexSessionStore) normalizeRunStateLocked() {
	if s.runs.Runs == nil {
		s.runs.Runs = make(map[string]*codexRunRecord)
	}
	if s.runs.ActiveBySession == nil {
		s.runs.ActiveBySession = make(map[string]string)
	}
	if s.runs.ActiveByRepo == nil {
		s.runs.ActiveByRepo = make(map[string]string)
	}

	for id, rec := range s.runs.Runs {
		if rec == nil {
			delete(s.runs.Runs, id)
			continue
		}
		rec.RepoURL = sanitizeRepoRemote(rec.RepoURL)
		if strings.TrimSpace(rec.BranchName) == "" && strings.TrimSpace(rec.SessionID) != "" && strings.TrimSpace(rec.ID) != "" {
			rec.BranchName = fmt.Sprintf("pc/%s/%s", rec.SessionID, rec.ID)
		}
		if strings.TrimSpace(rec.WorktreePath) == "" && strings.TrimSpace(rec.RepoSlug) != "" && strings.TrimSpace(rec.ID) != "" {
			if worktreePath, err := s.worktreePathForRun(rec.RepoSlug, rec.ID); err == nil {
				rec.WorktreePath = worktreePath
			}
		}
	}

	for key, runID := range s.runs.ActiveBySession {
		runID = strings.TrimSpace(runID)
		rec := s.runs.Runs[runID]
		if rec == nil || !isRunActiveStatus(rec.Status) {
			delete(s.runs.ActiveBySession, key)
			continue
		}
		if rec.ScopeKey == "" {
			rec.ScopeKey = key
		}
	}

	for key, runID := range s.runs.ActiveByRepo {
		runID = strings.TrimSpace(runID)
		rec := s.runs.Runs[runID]
		if rec == nil || !isRunActiveStatus(rec.Status) {
			delete(s.runs.ActiveByRepo, key)
			continue
		}
		if key != rec.RepoPath && key != rec.RepoSlug {
			delete(s.runs.ActiveByRepo, key)
			continue
		}
	}

	for _, rec := range s.runs.Runs {
		if rec == nil {
			continue
		}
		if !isRunActiveStatus(rec.Status) {
			if sessionRec := s.sessionRecordByIDLocked(rec.SessionID); sessionRec != nil && sessionRec.ActiveRunID == rec.ID {
				sessionRec.ActiveRunID = ""
			}
			continue
		}
		if rec.ScopeKey != "" {
			s.runs.ActiveBySession[rec.ScopeKey] = rec.ID
		}
		if rec.RepoPath != "" {
			s.runs.ActiveByRepo[rec.RepoPath] = rec.ID
		}
		if sessionRec := s.sessionRecordByIDLocked(rec.SessionID); sessionRec != nil {
			sessionRec.ActiveRunID = rec.ID
			if sessionRec.PlannerModel == "" {
				sessionRec.PlannerModel = rec.PlannerModel
			}
			if sessionRec.ExecutorModel == "" {
				sessionRec.ExecutorModel = rec.ExecutorModel
			}
			if sessionRec.WorkMode == "" {
				sessionRec.WorkMode = "codex-plan"
			}
		}
	}
}

func applySessionRuntimeLocked(rec *codexSessionRecord, runtime codexSessionRuntimeState) {
	if rec == nil {
		return
	}
	rec.PlannerModel = strings.TrimSpace(runtime.PlannerModel)
	rec.ExecutorModel = strings.TrimSpace(runtime.ExecutorModel)
	rec.WorkMode = strings.TrimSpace(runtime.WorkMode)
	rec.ApprovalPending = runtime.ApprovalPending
	rec.DeployConfirmPending = runtime.DeployConfirmPending
	rec.PendingPlanID = strings.TrimSpace(runtime.PendingPlanID)
	rec.PendingPlanHash = strings.TrimSpace(runtime.PendingPlanHash)
	rec.ActiveRunID = strings.TrimSpace(runtime.ActiveRunID)
	rec.LastRunID = strings.TrimSpace(runtime.LastRunID)
}

func sessionRuntimeFromRecord(rec *codexSessionRecord) codexSessionRuntimeState {
	if rec == nil {
		return codexSessionRuntimeState{}
	}
	return codexSessionRuntimeState{
		PlannerModel:         strings.TrimSpace(rec.PlannerModel),
		ExecutorModel:        strings.TrimSpace(rec.ExecutorModel),
		WorkMode:             strings.TrimSpace(rec.WorkMode),
		ApprovalPending:      rec.ApprovalPending,
		DeployConfirmPending: rec.DeployConfirmPending,
		PendingPlanID:        strings.TrimSpace(rec.PendingPlanID),
		PendingPlanHash:      strings.TrimSpace(rec.PendingPlanHash),
		ActiveRunID:          strings.TrimSpace(rec.ActiveRunID),
		LastRunID:            strings.TrimSpace(rec.LastRunID),
	}
}

func cloneCodexRunRecord(rec *codexRunRecord) *codexRunRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}

func isRunActiveStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexRunStatusQueued, codexRunStatusRunning:
		return true
	default:
		return false
	}
}

func isTerminalRunStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexRunStatusSucceeded, codexRunStatusFailed, codexRunStatusStopped:
		return true
	default:
		return false
	}
}

func processLooksAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func branchExists(repoPath, branch string) bool {
	repoPath = strings.TrimSpace(repoPath)
	branch = strings.TrimSpace(branch)
	if repoPath == "" || branch == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "--quiet", branch)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func newCodexRunID() string {
	return newCodexSessionID()
}

func sanitizeRepoRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		return raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User != nil {
		parsed.User = nil
	}
	return parsed.String()
}
