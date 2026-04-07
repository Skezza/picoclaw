package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const maxCodexRepoDiscoveryResults = 30

type codexSessionRecord struct {
	ID                   string    `json:"id"`
	Slug                 string    `json:"slug"`
	RepoPath             string    `json:"repo_path"`
	RepoURL              string    `json:"repo_url,omitempty"`
	PlannerModel         string    `json:"planner_model,omitempty"`
	ExecutorModel        string    `json:"executor_model,omitempty"`
	WorkMode             string    `json:"work_mode,omitempty"`
	ApprovalPending      bool      `json:"approval_pending,omitempty"`
	DeployConfirmPending bool      `json:"deploy_confirm_pending,omitempty"`
	PendingPlanID        string    `json:"pending_plan_id,omitempty"`
	PendingPlanHash      string    `json:"pending_plan_hash,omitempty"`
	PendingPlanText      string    `json:"pending_plan_text,omitempty"`
	ActiveRunID          string    `json:"active_run_id,omitempty"`
	LastRunID            string    `json:"last_run_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type codexSessionSnapshot struct {
	Version  int                            `json:"version"`
	Sessions map[string]*codexSessionRecord `json:"sessions"`
	Bindings map[string]string              `json:"bindings"`
	Recent   map[string][]string            `json:"recent,omitempty"`
}

type codexSessionStore struct {
	stateFile     string
	runsFile      string
	reposRoot     string
	worktreesRoot string

	mu    sync.RWMutex
	state codexSessionSnapshot
	runs  codexRunSnapshot
}

func newCodexSessionStore(workspace string) *codexSessionStore {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}

	store := &codexSessionStore{
		stateFile:     filepath.Join(workspace, "state", "codex_sessions.json"),
		runsFile:      filepath.Join(workspace, "state", "codex_runs.json"),
		reposRoot:     filepath.Join(workspace, "repos"),
		worktreesRoot: filepath.Join(workspace, "worktrees"),
		state: codexSessionSnapshot{
			Version:  2,
			Sessions: make(map[string]*codexSessionRecord),
			Bindings: make(map[string]string),
			Recent:   make(map[string][]string),
		},
		runs: codexRunSnapshot{
			Version:         1,
			Runs:            make(map[string]*codexRunRecord),
			ActiveBySession: make(map[string]string),
			ActiveByRepo:    make(map[string]string),
		},
	}

	if err := os.MkdirAll(filepath.Dir(store.stateFile), 0o700); err != nil {
		logger.WarnCF("agent", "Failed to create codex state directory", map[string]any{"error": err.Error()})
		return nil
	}
	if err := os.MkdirAll(store.reposRoot, 0o700); err != nil {
		logger.WarnCF("agent", "Failed to create codex repos directory", map[string]any{"error": err.Error()})
		return nil
	}
	if err := os.MkdirAll(store.worktreesRoot, 0o700); err != nil {
		logger.WarnCF("agent", "Failed to create codex worktrees directory", map[string]any{"error": err.Error()})
		return nil
	}
	if err := store.load(); err != nil {
		logger.WarnCF("agent", "Failed to load codex session state", map[string]any{"error": err.Error()})
	}
	if err := store.loadRuns(); err != nil {
		logger.WarnCF("agent", "Failed to load codex run state", map[string]any{"error": err.Error()})
	}

	return store
}

func (s *codexSessionStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var snapshot codexSessionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.Sessions == nil {
		snapshot.Sessions = make(map[string]*codexSessionRecord)
	}
	if snapshot.Bindings == nil {
		snapshot.Bindings = make(map[string]string)
	}
	if snapshot.Recent == nil {
		snapshot.Recent = make(map[string][]string)
	}
	if snapshot.Version == 0 {
		snapshot.Version = 2
	}
	s.state = snapshot
	s.normalizeSessionStateLocked()
	return nil
}

func (s *codexSessionStore) loadRuns() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.runsFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.normalizeRunStateLocked()
			return nil
		}
		return err
	}

	var snapshot codexRunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.Runs == nil {
		snapshot.Runs = make(map[string]*codexRunRecord)
	}
	if snapshot.ActiveBySession == nil {
		snapshot.ActiveBySession = make(map[string]string)
	}
	if snapshot.ActiveByRepo == nil {
		snapshot.ActiveByRepo = make(map[string]string)
	}
	if snapshot.Version == 0 {
		snapshot.Version = 1
	}
	s.runs = snapshot
	s.normalizeRunStateLocked()
	return nil
}

func (s *codexSessionStore) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.stateFile, data, 0o600)
}

func (s *codexSessionStore) saveRunsLocked() error {
	data, err := json.MarshalIndent(s.runs, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.runsFile, data, 0o600)
}

func (s *codexSessionStore) CreateOrActivate(scopeKey, slug, source string) (*codexSessionRecord, error) {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil, fmt.Errorf("codex sessions require a valid session scope")
	}

	slug, err := sanitizeCodexSlug(slug)
	if err != nil {
		return nil, err
	}

	repoURL, err := normalizeRepoSource(source)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	rec := s.findBySlugLocked(slug)
	if rec == nil {
		repoPath, pathErr := s.repoPathForSlug(slug)
		if pathErr != nil {
			s.mu.Unlock()
			return nil, pathErr
		}
		now := time.Now().UTC()
		rec = &codexSessionRecord{
			ID:        newCodexSessionID(),
			Slug:      slug,
			RepoPath:  repoPath,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.state.Sessions[rec.ID] = rec
	} else if repoURL != "" && rec.RepoURL != "" && !strings.EqualFold(rec.RepoURL, repoURL) {
		s.mu.Unlock()
		return nil, fmt.Errorf("repo source does not match existing session remote")
	}
	repoPath := rec.RepoPath
	recID := rec.ID
	s.mu.Unlock()

	if err := s.prepareRepo(repoPath, repoURL); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.state.Sessions[recID]
	if !ok || current == nil {
		return nil, fmt.Errorf("codex session state changed while preparing repo")
	}
	if current.RepoURL == "" && repoURL != "" {
		current.RepoURL = sanitizeRepoRemote(repoURL)
	}
	current.UpdatedAt = time.Now().UTC()
	s.touchScopeSessionLocked(scopeKey, current.ID)

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return cloneCodexRecord(current), nil
}

func (s *codexSessionStore) Attach(scopeKey, ref string) (*codexSessionRecord, error) {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil, fmt.Errorf("codex sessions require a valid session scope")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("session id or repo slug is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.state.Sessions[ref]
	if rec == nil {
		normalizedRef, err := sanitizeCodexSlug(ref)
		if err == nil {
			rec = s.findBySlugLocked(normalizedRef)
		}
	}
	if rec == nil {
		return nil, fmt.Errorf("codex session %q not found", ref)
	}

	rec.UpdatedAt = time.Now().UTC()
	if rec.RepoURL != "" {
		rec.RepoURL = sanitizeRepoRemote(rec.RepoURL)
	}
	s.touchScopeSessionLocked(scopeKey, rec.ID)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return cloneCodexRecord(rec), nil
}

func (s *codexSessionStore) Active(scopeKey string) (*codexSessionRecord, bool) {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	id := strings.TrimSpace(s.state.Bindings[scopeKey])
	if id == "" {
		return nil, false
	}
	rec := s.state.Sessions[id]
	if rec == nil {
		return nil, false
	}
	return cloneCodexRecord(rec), true
}

func (s *codexSessionStore) List(scopeKey string) []codexSessionRecord {
	scopeKey = strings.TrimSpace(scopeKey)

	s.mu.RLock()
	defer s.mu.RUnlock()

	if scopeKey != "" {
		recent := s.state.Recent[scopeKey]
		if len(recent) == 0 {
			return nil
		}
		result := make([]codexSessionRecord, 0, len(recent))
		seen := make(map[string]struct{}, len(recent))
		for _, id := range recent {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			rec := s.state.Sessions[id]
			if rec == nil {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, *rec)
		}
		return result
	}

	result := make([]codexSessionRecord, 0, len(s.state.Sessions))
	for _, rec := range s.state.Sessions {
		if rec == nil {
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

func (s *codexSessionStore) Stop(scopeKey string) error {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.state.Bindings[scopeKey]; !ok {
		return nil
	}
	delete(s.state.Bindings, scopeKey)
	return s.saveLocked()
}

func (s *codexSessionStore) findBySlugLocked(slug string) *codexSessionRecord {
	for _, rec := range s.state.Sessions {
		if rec != nil && rec.Slug == slug {
			return rec
		}
	}
	return nil
}

func (s *codexSessionStore) touchScopeSessionLocked(scopeKey, sessionID string) {
	scopeKey = strings.TrimSpace(scopeKey)
	sessionID = strings.TrimSpace(sessionID)
	if scopeKey == "" || sessionID == "" {
		return
	}
	s.state.Bindings[scopeKey] = sessionID
	recent := s.state.Recent[scopeKey]
	next := make([]string, 0, len(recent)+1)
	next = append(next, sessionID)
	for _, existing := range recent {
		existing = strings.TrimSpace(existing)
		if existing == "" || existing == sessionID {
			continue
		}
		next = append(next, existing)
		if len(next) >= 20 {
			break
		}
	}
	s.state.Recent[scopeKey] = next
}

func (s *codexSessionStore) repoPathForSlug(slug string) (string, error) {
	candidate := filepath.Clean(filepath.Join(s.reposRoot, slug))
	rel, err := filepath.Rel(s.reposRoot, candidate)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid repo path for slug %q", slug)
	}
	return candidate, nil
}

func (s *codexSessionStore) prepareRepo(repoPath, repoURL string) error {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	if repoPath == "" {
		return fmt.Errorf("repo path is required")
	}

	_, statErr := os.Stat(repoPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}

	if os.IsNotExist(statErr) {
		if repoURL == "" {
			return os.MkdirAll(repoPath, 0o755)
		}
		return cloneRepo(repoURL, repoPath)
	}

	if repoURL == "" {
		return nil
	}

	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := os.Remove(repoPath); err != nil {
			return err
		}
		return cloneRepo(repoURL, repoPath)
	}

	if !isGitRepo(repoPath) {
		return fmt.Errorf("repo path already exists and is not a git repository")
	}
	return nil
}

func sanitizeCodexSlug(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", fmt.Errorf("repo slug is required")
	}

	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
			lastDash = false
		case r == '/' || r == ' ' || r == ':' || r == '@':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}

	slug := strings.Trim(b.String(), "-._")
	if slug == "" {
		return "", fmt.Errorf("repo slug %q is invalid", raw)
	}
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "-._")
	}
	if slug == "" {
		return "", fmt.Errorf("repo slug %q is invalid", raw)
	}
	return slug, nil
}

func normalizeRepoSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}

	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "ssh://") || strings.HasPrefix(source, "git@") {
		return source, nil
	}

	if strings.Count(source, "/") == 1 && !strings.ContainsAny(source, " \t:") {
		return "https://github.com/" + source + ".git", nil
	}

	return "", fmt.Errorf("repo source must be https://, ssh://, git@, or owner/repo")
}

func newCodexSessionID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("cx-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func cloneRepo(repoURL, repoPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "clone", repoURL, repoPath)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("git clone timed out")
		}
		return fmt.Errorf("git clone failed (check repository access/PAT): %w", err)
	}
	return nil
}

func isGitRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func cloneCodexRecord(rec *codexSessionRecord) *codexSessionRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}

func (s *codexSessionStore) normalizeSessionStateLocked() {
	if s == nil {
		return
	}
	if s.state.Sessions == nil {
		s.state.Sessions = make(map[string]*codexSessionRecord)
	}
	if s.state.Bindings == nil {
		s.state.Bindings = make(map[string]string)
	}
	if s.state.Recent == nil {
		s.state.Recent = make(map[string][]string)
	}
	for _, rec := range s.state.Sessions {
		if rec == nil {
			continue
		}
		rec.RepoURL = sanitizeRepoRemote(rec.RepoURL)
	}
}

func codexRecordToInfo(rec *codexSessionRecord, active bool) commands.CodexSessionInfo {
	if rec == nil {
		return commands.CodexSessionInfo{}
	}
	return commands.CodexSessionInfo{
		ID:       rec.ID,
		Slug:     rec.Slug,
		RepoPath: rec.RepoPath,
		RepoURL:  rec.RepoURL,
		Updated:  rec.UpdatedAt,
		Active:   active,
	}
}

func (al *AgentLoop) findCodexModelName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	defaultName := strings.TrimSpace(cfg.Agents.Defaults.ModelName)
	if defaultName != "" {
		if mc, err := cfg.GetModelConfig(defaultName); err == nil && mc != nil {
			proto, _ := providers.ExtractProtocol(mc.Model)
			if strings.EqualFold(proto, "codex-cli") || strings.EqualFold(proto, "codexcli") {
				return defaultName
			}
		}
	}

	for _, mc := range cfg.ModelList {
		if mc == nil || strings.TrimSpace(mc.ModelName) == "" {
			continue
		}
		proto, _ := providers.ExtractProtocol(mc.Model)
		if strings.EqualFold(proto, "codex-cli") || strings.EqualFold(proto, "codexcli") {
			return strings.TrimSpace(mc.ModelName)
		}
	}

	return ""
}

func (al *AgentLoop) codexWorkspaceOverride(sessionKey string, modelCfg *config.ModelConfig) string {
	if al == nil || al.codexStore == nil || modelCfg == nil {
		return ""
	}

	proto, _ := providers.ExtractProtocol(modelCfg.Model)
	if !strings.EqualFold(proto, "codex-cli") && !strings.EqualFold(proto, "codexcli") {
		return ""
	}

	active, ok := al.codexStore.Active(sessionKey)
	if !ok || active == nil {
		return ""
	}
	return strings.TrimSpace(active.RepoPath)
}

func (al *AgentLoop) codexActiveRuntimeInfo(sessionKey string) (*commands.CodexSessionInfo, bool) {
	if al == nil || al.codexStore == nil {
		return nil, false
	}

	rec, ok := al.codexStore.Active(sessionKey)
	if !ok || rec == nil {
		return nil, false
	}

	info := codexRecordToInfo(rec, true)
	return &info, true
}

func (al *AgentLoop) codexListRuntimeInfo(sessionKey string) []commands.CodexSessionInfo {
	if al == nil || al.codexStore == nil {
		return nil
	}

	activeID := ""
	if active, ok := al.codexStore.Active(sessionKey); ok && active != nil {
		activeID = active.ID
	}

	records := al.codexStore.List(sessionKey)
	result := make([]commands.CodexSessionInfo, 0, len(records))
	for i := range records {
		rec := records[i]
		active := activeID != "" && rec.ID == activeID
		result = append(result, commands.CodexSessionInfo{
			ID:       rec.ID,
			Slug:     rec.Slug,
			RepoPath: rec.RepoPath,
			RepoURL:  rec.RepoURL,
			Updated:  rec.UpdatedAt,
			Active:   active,
		})
	}
	return result
}

func (al *AgentLoop) codexListGlobalRuntimeInfo() []commands.CodexSessionInfo {
	if al == nil || al.codexStore == nil {
		return nil
	}

	records := al.codexStore.List("")
	result := make([]commands.CodexSessionInfo, 0, len(records))
	for i := range records {
		rec := records[i]
		result = append(result, commands.CodexSessionInfo{
			ID:       rec.ID,
			Slug:     rec.Slug,
			RepoPath: rec.RepoPath,
			RepoURL:  rec.RepoURL,
			Updated:  rec.UpdatedAt,
			Active:   false,
		})
	}
	return result
}

func (al *AgentLoop) codexGitHubRepos(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > maxCodexRepoDiscoveryResults {
		limit = maxCodexRepoDiscoveryResults
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found on host; install GitHub CLI first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	authCmd := exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", "github.com")
	authCmd.Env = os.Environ()
	if err := authCmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gh repo discovery timed out")
		}
		return nil, fmt.Errorf("gh CLI is not authenticated to github.com")
	}

	repoCmd := exec.CommandContext(
		ctx,
		"gh",
		"repo",
		"list",
		"--limit",
		fmt.Sprintf("%d", limit),
		"--json",
		"nameWithOwner",
		"--jq",
		".[].nameWithOwner",
	)
	repoCmd.Env = os.Environ()
	output, err := repoCmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gh repo discovery timed out")
		}
		return nil, fmt.Errorf("gh repo discovery failed")
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	repos := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		repos = append(repos, line)
		if len(repos) >= limit {
			break
		}
	}
	return repos, nil
}
