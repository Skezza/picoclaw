package commands

import (
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// CodexSessionInfo describes a repo-scoped codex session.
type CodexSessionInfo struct {
	ID       string
	Slug     string
	RepoPath string
	RepoURL  string
	Updated  time.Time
	Active   bool
}

// CodexPlannerStatusInfo describes the user-facing planner state for a repo-scoped codex session.
type CodexPlannerStatusInfo struct {
	Phase           string
	Model           string
	SessionID       string
	RepoSlug        string
	RepoPath        string
	RepoURL         string
	ApprovalPending bool
}

// CodexRunInfo describes a background codex execution run.
type CodexRunInfo struct {
	ID         string
	SessionID  string
	RepoSlug   string
	RepoPath   string
	RepoURL    string
	Branch     string
	Worktree   string
	Model      string
	Status     string
	PID        int
	ExitCode   int
	Active     bool
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt time.Time
}

// Runtime provides runtime dependencies to command handlers. It is constructed
// per-request by the agent loop so that per-request state (like session scope)
// can coexist with long-lived callbacks (like GetModelInfo).
type Runtime struct {
	Config             *config.Config
	GetModelInfo       func() (name, provider string)
	ListAgentIDs       func() []string
	ListDefinitions    func() []Definition
	ListSkillNames     func() []string
	GetEnabledChannels func() []string
	GetActiveTurn      func() any // Returning any to avoid circular dependency with agent package
	SwitchModel        func(value string) (oldModel string, err error)
	SwitchChannel      func(value string) error
	ClearHistory       func() error
	ReloadConfig       func() error

	GetSessionModelMode   func() (persistent, pending string)
	SetSessionModelMode   func(value string) error
	ArmNextModelMode      func(value string) error
	ClearSessionModelMode func() error

	GetSessionWorkMode        func() string
	SetSessionWorkMode        func(value string) error
	ClearSessionWorkMode      func() error
	GetCodexApprovalPending   func() bool
	ClearCodexApprovalPending func()

	FindCodexModel           func() string
	ListCodexDelegateTargets func() []string
	ListCodexRepoTargets     func(limit int) ([]string, error)
	CodexNewSession          func(slug, source string) (*CodexSessionInfo, error)
	CodexAttach              func(ref string) (*CodexSessionInfo, error)
	CodexListSessions        func() []CodexSessionInfo
	CodexListGlobalSessions  func() []CodexSessionInfo
	CodexActive              func() (*CodexSessionInfo, bool)
	CodexPlannerStatus       func() (*CodexPlannerStatusInfo, bool)
	CodexRunList             func() []CodexRunInfo
	CodexRunStatus           func() (*CodexRunInfo, bool)
	CodexRunTail             func(runID string, lines int) (string, error)
	CodexRunStop             func() error
	CodexStop                func() error

	ListSelfImproveTargets func() []string
	SelfImproveActivate    func() (*CodexSessionInfo, error)
	SelfImproveStatus      func() (string, error)
	SelfImproveShip        func(target string) (string, error)
}
