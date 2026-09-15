package provider

import (
	"time"

	"agentcli.local/ai/internal/gitutil"
)

type ProviderInfo struct {
	ID             string    `json:"id"`
	DisplayName    string    `json:"display_name"`
	Origin         string    `json:"origin"`
	Installed      bool      `json:"installed"`
	BinaryPath     string    `json:"binary_path,omitempty"`
	ConfigPath     string    `json:"config_path,omitempty"`
	DataPath       string    `json:"data_path,omitempty"`
	Running        bool      `json:"running"`
	Busy           bool      `json:"busy"`
	ActiveTask     string    `json:"active_task,omitempty"`
	PIDs           []int     `json:"pids,omitempty"`
	CPUPercent     float64   `json:"cpu_percent,omitempty"`
	MemoryRSSBytes int64     `json:"memory_rss_bytes,omitempty"`
	MCPCount       int       `json:"mcp_count"`
	LastActive     time.Time `json:"last_active,omitempty"`
}

type ConversationSummary struct {
	Provider       string    `json:"provider"`
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	MessagesCount  int       `json:"messages_count"`
	ArtifactsCount int       `json:"artifacts_count"`
	TotalSizeBytes int64     `json:"total_size_bytes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	WorkspaceDir   string    `json:"workspace_dir,omitempty"`
	Model          string    `json:"model,omitempty"`
	TranscriptPath string    `json:"transcript_path,omitempty"`
}

type ArtifactInfo struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	SizeBytes  int64     `json:"size_bytes"`
	Type       string    `json:"type"`
	ModifiedAt time.Time `json:"modified_at"`
}

type TurnInfo struct {
	StepIndex      int       `json:"step_index"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	Timestamp      time.Time `json:"timestamp"`
	ToolCall       string    `json:"tool_call,omitempty"`
	Thinking       string    `json:"thinking,omitempty"`
	IsHarnessError bool      `json:"is_harness_error,omitempty"`
}

type ConversationDetail struct {
	Summary        ConversationSummary `json:"summary"`
	WorkspaceGit   gitutil.GitStatus   `json:"workspace_git"`
	Artifacts      []ArtifactInfo      `json:"artifacts"`
	Turns          []TurnInfo          `json:"turns"`
	InitialPrompt  string              `json:"initial_prompt,omitempty"`
	LastResponse   string              `json:"last_response,omitempty"`
	TranscriptPath string              `json:"transcript_path,omitempty"`
}

type AuditDossier struct {
	Provider           string            `json:"provider"`
	ConversationID     string            `json:"conversation_id"`
	Title              string            `json:"title"`
	LastActive         time.Time         `json:"last_active"`
	WorkspaceDir       string            `json:"workspace_dir"`
	WorkspaceGit       gitutil.GitStatus `json:"workspace_git"`
	UserPrompt         string            `json:"user_prompt"`
	AssistantSummary   string            `json:"assistant_summary"`
	PendingOpenTasks   []string          `json:"pending_open_tasks"`
	TouchedArtifacts   []ArtifactInfo    `json:"touched_artifacts"`
	FullTranscriptPath string            `json:"full_transcript_path"`
}

type MemoryItem struct {
	Provider   string    `json:"provider"`
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	SourceFile string    `json:"source_file,omitempty"`
}

type SkillItem struct {
	Provider     string   `json:"provider"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Path         string   `json:"path"`
	IsBuiltin    bool     `json:"is_builtin"`
	RulesCount   int      `json:"rules_count"`
	ScriptsCount int      `json:"scripts_count"`
	Tools        []string `json:"tools,omitempty"`
}

type HistoryOptions struct {
	Provider  string
	Since     time.Time
	Last      bool
	Limit     int
	Search    string
	Workspace string
}

type PluginItem struct {
	Provider    string `json:"provider"`
	Name        string `json:"name"`
	Type        string `json:"type"` // e.g. "Hook / ASAR script", "Extension bundle", "App server tool"
	Path        string `json:"path"`
	Status      string `json:"status"` // "active", "installed", "disabled"
	Description string `json:"description,omitempty"`
}

// LimitInfo represents a single quota/rate-limit dimension for a provider.
type LimitInfo struct {
	Provider    string    `json:"provider"`
	Name        string    `json:"name"`         // e.g. "Fast messages", "Daily messages", "Tokens today"
	Used        int64     `json:"used"`         // absolute used count (tokens, messages, …). -1 = unknown.
	Limit       int64     `json:"limit"`        // total quota.  -1 = unlimited / unknown.
	UsedPct     float64   `json:"used_pct"`     // 0‒100 percentage used
	Unit        string    `json:"unit"`         // e.g. "tokens", "messages", "%"
	RefillAt    time.Time `json:"refill_at"`    // when the quota resets; zero = unknown
	RefillEvery string    `json:"refill_every"` // human description e.g. "5 h", "daily"
	Note        string    `json:"note,omitempty"`
}

// ModelInfo represents a single model available from a provider.
type ModelInfo struct {
	Provider        string   `json:"provider"`
	ID              string   `json:"id"` // canonical slug
	DisplayName     string   `json:"display_name"`
	Description     string   `json:"description,omitempty"`
	ContextWindow   int64    `json:"context_window"`   // tokens; 0 = unknown
	InputModalities []string `json:"input_modalities"` // e.g. ["text","image"]
	ServiceTiers    []string `json:"service_tiers"`    // e.g. ["pro","free"]
	ReasoningLevels []string `json:"reasoning_levels"` // e.g. ["low","medium","high"]
	IsActive        bool     `json:"is_active"`        // currently selected / default model
	Visibility      string   `json:"visibility"`       // "public", "internal", etc.
}

// AccountInfo represents a user account or saved profile associated with a provider.
type AccountInfo struct {
	Provider    string `json:"provider"`              // e.g. "antigravity", "claude", "codex"
	ID          string `json:"id"`                    // unique account UUID or profile name
	DisplayName string `json:"display_name"`          // e.g. "Blus cream", "Bluscream"
	Email       string `json:"email,omitempty"`       // e.g. "bluscreamlp@gmail.com"
	Plan        string `json:"plan,omitempty"`        // e.g. "Google AI Pro", "Claude Pro", "ChatGPT Plus"
	IsActive    bool   `json:"is_active"`             // true if currently active/configured in the provider
	ActiveIn    string `json:"active_in,omitempty"`   // e.g. "Antigravity IDE", "Claude Desktop", "Codex Desktop", or "-"
	ConfigPath  string `json:"config_path,omitempty"` // path to config or profile file
}

type Provider interface {
	Name() string
	DisplayName() string
	Status() (ProviderInfo, error)
	ListConversations(opts HistoryOptions) ([]ConversationSummary, error)
	GetConversation(id string) (*ConversationDetail, error)
	AuditConversation(id string) (*AuditDossier, error)
	ListMemories() ([]MemoryItem, error)
	BackupMemories(destDir string) (string, error)
	PurgeMemories() (int, error)
	ImportMemory(item MemoryItem) error
	ListSkills() ([]SkillItem, error)
	ImportSkill(skillDir string) error
	PurgeSkills() (int, error)
	ListPlugins() ([]PluginItem, error)
	InstallPlugin(sourcePath string) error
	UninstallPlugin(name string) error
	GetLimits() ([]LimitInfo, error)
	GetModels() ([]ModelInfo, error)
	GetAccounts() ([]AccountInfo, error)
	GetActiveAccount() (*AccountInfo, error)
	RecoverConversations(dryRun bool) (string, error)
}
