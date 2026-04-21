//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	routeIndex      = "/"
	routeOverview   = "/overview"
	routeSkillsPage = "/skills"
	routePrompts    = "/prompts"
	routeIdentity   = "/identity"
	routePersonas   = "/personas"
	routeChats      = "/chats"
	routeMemory     = "/memory"
	routeAutomation = "/automation"
	routeSessions   = "/sessions"
	routeDebug      = "/debug"
	routeBrowser    = "/browser"

	routeStatusJSON        = "/api/status"
	routeSkillsJSON        = "/api/skills/status"
	routeSkillsRefresh     = "/api/skills/refresh"
	routeSkillToggle       = "/api/skills/toggle"
	routeMemoryFilesJSON   = "/api/memory/files"
	routeMemoryFileAPI     = "/api/memory/file"
	routeMemoryFile        = "/memory/file"
	routeJobsJSON          = "/api/cron/jobs"
	routeJobRun            = "/api/cron/jobs/run"
	routeJobRemove         = "/api/cron/jobs/remove"
	routeJobsClear         = "/api/cron/jobs/clear"
	routeExecSessionsJSON  = "/api/exec/sessions"
	routeUploadsJSON       = "/api/uploads"
	routeUploadSessions    = "/api/uploads/sessions"
	routeUploadFile        = "/uploads/file"
	routeDebugSessionsJSON = "/api/debug/sessions"
	routeDebugTracesJSON   = "/api/debug/traces"
	routeDebugFile         = "/debug/file"
	routePageStateJSON     = "/api/page/state"

	queryNotice    = "notice"
	queryError     = "error"
	queryChatID    = "chat_id"
	querySessionID = "session_id"
	queryChannel   = "channel"
	queryUserID    = "user_id"
	queryKind      = "kind"
	queryMimeType  = "mime_type"
	querySource    = "source"
	queryTrace     = "trace"
	queryName      = "name"
	queryPath      = "path"
	queryDownload  = "download"
	queryCursor    = "cursor"
	queryView      = "view"
	formJobID      = "job_id"
	formSkillKey   = "skill_key"
	formSkillName  = "skill_name"
	formEnabled    = "enabled"
	formReturnTo   = "return_to"
	formReturnPath = "return_path"

	refreshSeconds = 15

	debugBySessionDir   = "by-session"
	debugMetaFileName   = "meta.json"
	debugEventsFileName = "events.jsonl"
	debugResultFileName = "result.json"
	debugEventsMIMEType = "application/x-ndjson; charset=utf-8"

	maxDebugSessionRows = 12
	maxDebugTraceRows   = 18
	maxJobOutputRunes   = 120
	browserProbeTimeout = 1500 * time.Millisecond

	formatTimeLayout = "2006-01-02 15:04:05 MST"

	adminBrandName     = "TRPC-CLAW"
	adminBrandTitle    = "TRPC-CLAW admin"
	adminRuntimePrefix = "trpc-claw"

	pageSummaryPrompts = "" +
		"Edit the main prompt blocks, inspect the assembled " +
		"prompt previews, and keep file-level edits in one place."
	pageSummaryIdentity = "" +
		"Set the default name, keep current-chat names readable, " +
		"and leave the runtime product as a separate read-only fact."
	pageSummaryPersonas = "" +
		"Manage the default persona and any file-backed " +
		"persona definitions exposed by this runtime."
	pageSummaryChats = "" +
		"Inspect each chat's current state, recent transcript, " +
		"and the safest next step for names and persona."
)

type adminView string

const (
	viewOverview   adminView = "overview"
	viewSkills     adminView = "skills"
	viewPrompts    adminView = "prompts"
	viewIdentity   adminView = "identity"
	viewPersonas   adminView = "personas"
	viewChats      adminView = "chats"
	viewMemory     adminView = "memory"
	viewAutomation adminView = "automation"
	viewSessions   adminView = "sessions"
	viewDebug      adminView = "debug"
	viewBrowser    adminView = "browser"
)

type Routes struct {
	HealthPath   string
	MessagesPath string
	StatusPath   string
	CancelPath   string
}

type Config struct {
	AppName    string
	InstanceID string
	StartedAt  time.Time
	Hostname   string
	PID        int
	GoVersion  string

	AgentType      string
	ModelMode      string
	ModelName      string
	SessionBackend string
	MemoryBackend  string

	GatewayAddr   string
	GatewayURL    string
	AdminAddr     string
	AdminURL      string
	AdminAutoPort bool
	Langfuse      LangfuseStatus

	StateDir string
	DebugDir string

	Channels         []string
	GatewayRoutes    Routes
	Skills           SkillsStatusProvider
	Prompts          PromptsProvider
	Identity         IdentityProvider
	Personas         PersonasProvider
	Chats            ChatsProvider
	MemoryFiles      MemoryFileStore
	MemoryUserLabels MemoryUserLabelResolver
	Browser          BrowserConfig

	Cron *cron.Service
	Exec *octool.Manager
}

type BrowserConfig struct {
	Providers []BrowserProvider            `json:"providers,omitempty"`
	Managed   BrowserManagedStatusProvider `json:"-"`
}

type BrowserProvider struct {
	Name             string           `json:"name,omitempty"`
	DefaultProfile   string           `json:"default_profile,omitempty"`
	EvaluateEnabled  bool             `json:"evaluate_enabled"`
	HostServerURL    string           `json:"host_server_url,omitempty"`
	SandboxServerURL string           `json:"sandbox_server_url,omitempty"`
	AllowLoopback    bool             `json:"allow_loopback"`
	AllowPrivateNet  bool             `json:"allow_private_networks"`
	AllowFileURLs    bool             `json:"allow_file_urls"`
	Profiles         []BrowserProfile `json:"profiles,omitempty"`
	Nodes            []BrowserNode    `json:"nodes,omitempty"`
}

type BrowserProfile struct {
	Name             string `json:"name,omitempty"`
	Description      string `json:"description,omitempty"`
	Transport        string `json:"transport,omitempty"`
	ServerURL        string `json:"server_url,omitempty"`
	BrowserServerURL string `json:"browser_server_url,omitempty"`
}

type BrowserNode struct {
	ID        string `json:"id,omitempty"`
	ServerURL string `json:"server_url,omitempty"`
}

type BrowserManagedStatusProvider interface {
	BrowserManagedStatus() BrowserManagedService
}

type SkillsStatusProvider interface {
	SkillsStatus() (ocskills.StatusReport, error)
	SkillsConfigPath() string
	SkillsRefreshable() bool
	RefreshSkills() error
	SetSkillEnabled(configKey string, enabled bool) error
}

type BrowserManagedService struct {
	Enabled         bool       `json:"enabled"`
	Managed         bool       `json:"managed"`
	State           string     `json:"state,omitempty"`
	URL             string     `json:"url,omitempty"`
	PID             int        `json:"pid,omitempty"`
	WorkDir         string     `json:"work_dir,omitempty"`
	Command         string     `json:"command,omitempty"`
	LogPath         string     `json:"log_path,omitempty"`
	LogRelativePath string     `json:"log_relative_path,omitempty"`
	LogURL          string     `json:"log_url,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	StoppedAt       *time.Time `json:"stopped_at,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	RecentLogs      []string   `json:"recent_logs,omitempty"`
}

type Service struct {
	cfg               Config
	runtimeConfig     RuntimeConfigProvider
	runtimeLifecycle  RuntimeLifecycleProvider
	now               func() time.Time
	browserHTTPClient *http.Client
}

type Option func(*Service)

func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if s != nil && fn != nil {
			s.now = fn
		}
	}
}

func WithBrowserHTTPClient(client *http.Client) Option {
	return func(s *Service) {
		if s != nil && client != nil {
			s.browserHTTPClient = client
		}
	}
}

func WithRuntimeConfigProvider(provider RuntimeConfigProvider) Option {
	return func(s *Service) {
		if s != nil {
			s.runtimeConfig = provider
		}
	}
}

func WithRuntimeLifecycleProvider(
	provider RuntimeLifecycleProvider,
) Option {
	return func(s *Service) {
		if s != nil {
			s.runtimeLifecycle = provider
		}
	}
}

func New(cfg Config, opts ...Option) *Service {
	svc := &Service{
		cfg: cfg,
		now: time.Now,
		browserHTTPClient: &http.Client{
			Timeout: browserProbeTimeout,
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(
		routeIndex,
		wrapRelativeLinksFunc(s.handleOverview),
	)
	mux.HandleFunc(
		routeOverview,
		wrapRelativeLinksFunc(s.handleOverview),
	)
	mux.HandleFunc(
		routeSkillsPage,
		wrapRelativeLinksFunc(s.handleSkillsPage),
	)
	mux.HandleFunc(
		routeConfigPage,
		wrapRelativeLinksFunc(s.handleConfigPage),
	)
	mux.HandleFunc(
		routeRuntimeControlPage,
		wrapRelativeLinksFunc(s.handleRuntimeControlPage),
	)
	mux.HandleFunc(
		routePrompts,
		wrapRelativeLinksFunc(s.handlePromptsPage),
	)
	mux.HandleFunc(
		routeIdentity,
		wrapRelativeLinksFunc(s.handleIdentityPage),
	)
	mux.HandleFunc(
		routePersonas,
		wrapRelativeLinksFunc(s.handlePersonasPage),
	)
	mux.HandleFunc(
		routeChats,
		wrapRelativeLinksFunc(s.handleChatsPage),
	)
	mux.HandleFunc(
		routeMemory,
		wrapRelativeLinksFunc(s.handleMemoryPage),
	)
	mux.HandleFunc(
		routeAutomation,
		wrapRelativeLinksFunc(s.handleAutomationPage),
	)
	mux.HandleFunc(
		routeSessions,
		wrapRelativeLinksFunc(s.handleSessionsPage),
	)
	mux.HandleFunc(
		routeDebug,
		wrapRelativeLinksFunc(s.handleDebugPage),
	)
	mux.HandleFunc(
		routeBrowser,
		wrapRelativeLinksFunc(s.handleBrowserPage),
	)
	mux.HandleFunc(routeStatusJSON, s.handleStatusJSON)
	mux.HandleFunc(routePageStateJSON, s.handlePageStateJSON)
	mux.HandleFunc(routeSkillsJSON, s.handleSkillsJSON)
	mux.HandleFunc(routeConfigJSON, s.handleConfigJSON)
	mux.HandleFunc(routePromptsJSON, s.handlePromptsJSON)
	mux.HandleFunc(routeIdentityJSON, s.handleIdentityJSON)
	mux.HandleFunc(routePersonasJSON, s.handlePersonasJSON)
	mux.HandleFunc(routeChatsJSON, s.handleChatsJSON)
	mux.HandleFunc(routeChatHistoryJSON, s.handleChatHistoryJSON)
	mux.HandleFunc(routeMemoryFilesJSON, s.handleMemoryFilesJSON)
	mux.HandleFunc(
		routeMemoryFileAPI,
		wrapRelativeLinksFunc(s.handleMemoryFileAPI),
	)
	mux.HandleFunc(routeMemoryFile, s.handleMemoryFile)
	mux.HandleFunc(
		routeConfigSave,
		wrapRelativeLinksFunc(s.handleSaveRuntimeConfig),
	)
	mux.HandleFunc(
		routeConfigReset,
		wrapRelativeLinksFunc(s.handleResetRuntimeConfig),
	)
	mux.HandleFunc(
		routeRuntimeControlAction,
		wrapRelativeLinksFunc(s.handleRuntimeControlAction),
	)
	mux.HandleFunc(
		routePromptInlineSave,
		wrapRelativeLinksFunc(s.handleSavePromptInline),
	)
	mux.HandleFunc(
		routePromptRuntimeSave,
		wrapRelativeLinksFunc(s.handleSavePromptRuntime),
	)
	mux.HandleFunc(
		routePromptFileSave,
		wrapRelativeLinksFunc(s.handleSavePromptFile),
	)
	mux.HandleFunc(
		routePromptFileCreate,
		wrapRelativeLinksFunc(s.handleCreatePromptFile),
	)
	mux.HandleFunc(
		routePromptFileDelete,
		wrapRelativeLinksFunc(s.handleDeletePromptFile),
	)
	mux.HandleFunc(
		routeIdentitySave,
		wrapRelativeLinksFunc(s.handleSaveIdentity),
	)
	mux.HandleFunc(
		routePersonaSave,
		wrapRelativeLinksFunc(s.handleSavePersona),
	)
	mux.HandleFunc(
		routePersonaDelete,
		wrapRelativeLinksFunc(s.handleDeletePersona),
	)
	mux.HandleFunc(
		routePersonaDefaultSave,
		wrapRelativeLinksFunc(s.handleSaveDefaultPersona),
	)
	mux.HandleFunc(
		routeSkillsRefresh,
		wrapRelativeLinksFunc(s.handleRefreshSkills),
	)
	mux.HandleFunc(
		routeSkillToggle,
		wrapRelativeLinksFunc(s.handleToggleSkill),
	)
	mux.HandleFunc(routeJobsJSON, s.handleJobsJSON)
	mux.HandleFunc(routeJobRun, wrapRelativeLinksFunc(s.handleRunJob))
	mux.HandleFunc(
		routeJobRemove,
		wrapRelativeLinksFunc(s.handleRemoveJob),
	)
	mux.HandleFunc(
		routeJobsClear,
		wrapRelativeLinksFunc(s.handleClearJobs),
	)
	mux.HandleFunc(routeExecSessionsJSON, s.handleExecSessionsJSON)
	mux.HandleFunc(routeUploadsJSON, s.handleUploadsJSON)
	mux.HandleFunc(routeUploadSessions, s.handleUploadSessionsJSON)
	mux.HandleFunc(routeUploadFile, s.handleUploadFile)
	mux.HandleFunc(routeDebugSessionsJSON, s.handleDebugSessionsJSON)
	mux.HandleFunc(routeDebugTracesJSON, s.handleDebugTracesJSON)
	mux.HandleFunc(routeDebugFile, s.handleDebugFile)
	return mux
}

type snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`

	AppName    string    `json:"app_name,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	Hostname   string    `json:"hostname,omitempty"`
	PID        int       `json:"pid,omitempty"`
	GoVersion  string    `json:"go_version,omitempty"`
	Uptime     string    `json:"uptime,omitempty"`

	AgentType      string `json:"agent_type,omitempty"`
	ModelMode      string `json:"model_mode,omitempty"`
	ModelName      string `json:"model_name,omitempty"`
	SessionBackend string `json:"session_backend,omitempty"`
	MemoryBackend  string `json:"memory_backend,omitempty"`

	GatewayAddr   string         `json:"gateway_addr,omitempty"`
	GatewayLabel  string         `json:"gateway_label,omitempty"`
	GatewayURL    string         `json:"gateway_url,omitempty"`
	AdminAddr     string         `json:"admin_addr,omitempty"`
	AdminURL      string         `json:"admin_url,omitempty"`
	AdminAutoPort bool           `json:"admin_auto_port"`
	Langfuse      LangfuseStatus `json:"langfuse"`

	StateDir string `json:"state_dir,omitempty"`
	DebugDir string `json:"debug_dir,omitempty"`

	Channels []string      `json:"channels,omitempty"`
	Routes   Routes        `json:"routes,omitempty"`
	Browser  browserStatus `json:"browser"`
	Skills   skillsStatus  `json:"skills"`
	Memory   memoryStatus  `json:"memory"`
	Exec     execStatus    `json:"exec"`
	Uploads  uploadsStatus `json:"uploads"`
	Cron     cronStatus    `json:"cron"`
	Debug    debugStatus   `json:"debug"`
}

type browserStatus struct {
	Enabled       bool                  `json:"enabled"`
	ProviderCount int                   `json:"provider_count"`
	ProfileCount  int                   `json:"profile_count"`
	NodeCount     int                   `json:"node_count"`
	Managed       BrowserManagedService `json:"managed,omitempty"`
	Providers     []browserProviderView `json:"providers,omitempty"`
}

type browserProviderView struct {
	Name             string               `json:"name,omitempty"`
	DefaultProfile   string               `json:"default_profile,omitempty"`
	EvaluateEnabled  bool                 `json:"evaluate_enabled"`
	HostServerURL    string               `json:"host_server_url,omitempty"`
	SandboxServerURL string               `json:"sandbox_server_url,omitempty"`
	AllowLoopback    bool                 `json:"allow_loopback"`
	AllowPrivateNet  bool                 `json:"allow_private_networks"`
	AllowFileURLs    bool                 `json:"allow_file_urls"`
	Host             browserEndpointView  `json:"host,omitempty"`
	Sandbox          browserEndpointView  `json:"sandbox,omitempty"`
	Profiles         []browserProfileView `json:"profiles,omitempty"`
	Nodes            []browserNodeView    `json:"nodes,omitempty"`
}

type browserProfileView struct {
	Name             string `json:"name,omitempty"`
	Description      string `json:"description,omitempty"`
	Transport        string `json:"transport,omitempty"`
	ServerURL        string `json:"server_url,omitempty"`
	BrowserServerURL string `json:"browser_server_url,omitempty"`
}

type browserEndpointView struct {
	URL       string               `json:"url,omitempty"`
	Reachable bool                 `json:"reachable"`
	Error     string               `json:"error,omitempty"`
	Profiles  []browserRemoteProbe `json:"profiles,omitempty"`
}

type browserRemoteProbe struct {
	Name   string `json:"name,omitempty"`
	State  string `json:"state,omitempty"`
	Driver string `json:"driver,omitempty"`
	Tabs   int    `json:"tabs,omitempty"`
}

type browserNodeView struct {
	ID        string              `json:"id,omitempty"`
	ServerURL string              `json:"server_url,omitempty"`
	Status    browserEndpointView `json:"status,omitempty"`
}

type cronStatus struct {
	Enabled     bool      `json:"enabled"`
	JobCount    int       `json:"job_count"`
	RunningJobs int       `json:"running_jobs"`
	Channels    []string  `json:"channels,omitempty"`
	Jobs        []jobView `json:"jobs,omitempty"`
}

type jobView struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	UserID   string `json:"user_id,omitempty"`

	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`

	MessagePreview string `json:"message_preview,omitempty"`
	LastOutput     string `json:"last_output,omitempty"`

	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

type skillsStatus struct {
	Enabled         bool              `json:"enabled"`
	Error           string            `json:"error,omitempty"`
	Writable        bool              `json:"writable"`
	Refreshable     bool              `json:"refreshable"`
	ConfigPath      string            `json:"config_path,omitempty"`
	TotalCount      int               `json:"total_count"`
	ReadyCount      int               `json:"ready_count"`
	NeedsSetupCount int               `json:"needs_setup_count"`
	DisabledCount   int               `json:"disabled_count"`
	BundledCount    int               `json:"bundled_count"`
	Groups          []skillsGroupView `json:"groups,omitempty"`
}

type skillsGroupView struct {
	ID     string      `json:"id,omitempty"`
	Label  string      `json:"label,omitempty"`
	Skills []skillView `json:"skills,omitempty"`
}

type skillView struct {
	Name               string                `json:"name,omitempty"`
	Description        string                `json:"description,omitempty"`
	SkillKey           string                `json:"skill_key,omitempty"`
	ConfigKey          string                `json:"config_key,omitempty"`
	FilePath           string                `json:"file_path,omitempty"`
	Source             string                `json:"source,omitempty"`
	Reason             string                `json:"reason,omitempty"`
	Emoji              string                `json:"emoji,omitempty"`
	Homepage           string                `json:"homepage,omitempty"`
	PrimaryEnv         string                `json:"primary_env,omitempty"`
	Status             string                `json:"status,omitempty"`
	SearchText         string                `json:"search_text,omitempty"`
	Bundled            bool                  `json:"bundled"`
	Always             bool                  `json:"always"`
	Disabled           bool                  `json:"disabled"`
	Eligible           bool                  `json:"eligible"`
	BlockedByAllowlist bool                  `json:"blocked_by_allowlist"`
	Requirements       skillRequirementsView `json:"requirements,omitempty"`
	Missing            skillRequirementsView `json:"missing,omitempty"`
	Install            []skillInstallView    `json:"install,omitempty"`
}

type skillRequirementsView struct {
	OS      []string `json:"os,omitempty"`
	Bins    []string `json:"bins,omitempty"`
	AnyBins []string `json:"any_bins,omitempty"`
	Env     []string `json:"env,omitempty"`
	Config  []string `json:"config,omitempty"`
}

type skillInstallView struct {
	ID    string   `json:"id,omitempty"`
	Kind  string   `json:"kind,omitempty"`
	Label string   `json:"label,omitempty"`
	Bins  []string `json:"bins,omitempty"`
}

type pageData struct {
	Snapshot          snapshot
	Config            RuntimeConfigStatus
	ConfigPending     int
	ConfigCanRestart  bool
	RuntimeControl    RuntimeLifecyclePageStatus
	Prompts           PromptsStatus
	Identity          IdentityStatus
	Personas          PersonasStatus
	Chats             ChatsStatus
	ChatHistoryPath   string
	SelectedChat      *ChatView
	SelectedChatError string
	Notice            string
	Error             string
	PageRefresh       pageRefreshData
	View              adminView
	PageTitle         string
	PageSummary       string
	NavSections       []adminNavSection
}

type pageRefreshData struct {
	CurrentPath     string
	StatePath       string
	Token           string
	UpdatedAt       time.Time
	IntervalSeconds int
	Watch           bool
}

type pageStateStatus struct {
	Token     string    `json:"token,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type pageRefreshInput struct {
	Snapshot          snapshot
	Config            RuntimeConfigStatus
	RuntimeControl    RuntimeLifecyclePageStatus
	Prompts           PromptsStatus
	Identity          IdentityStatus
	Personas          PersonasStatus
	Chats             ChatsStatus
	SelectedChat      *ChatView
	SelectedChatError string
}

type adminNavSection struct {
	Label string
	Items []adminNavItem
}

type adminNavItem struct {
	Label  string
	Path   string
	Active bool
}

func (s *Service) Snapshot() snapshot {
	out := s.baseSnapshot()
	out.Skills = s.skillsStatus()
	out.Memory = s.memoryStatusSummary()
	out.Browser = s.browserStatus()
	out.Exec = s.execStatus()
	out.Uploads = s.uploadsStatus()
	out.Debug = s.debugStatus()
	out.Cron = s.cronStatus()
	return out
}

func (s *Service) baseSnapshot() snapshot {
	now := s.now()
	out := snapshot{
		GeneratedAt:    now,
		AppName:        strings.TrimSpace(s.cfg.AppName),
		InstanceID:     strings.TrimSpace(s.cfg.InstanceID),
		StartedAt:      s.cfg.StartedAt,
		Hostname:       strings.TrimSpace(s.cfg.Hostname),
		PID:            s.cfg.PID,
		GoVersion:      strings.TrimSpace(s.cfg.GoVersion),
		Uptime:         formatUptime(s.cfg.StartedAt, now),
		AgentType:      strings.TrimSpace(s.cfg.AgentType),
		ModelMode:      strings.TrimSpace(s.cfg.ModelMode),
		ModelName:      strings.TrimSpace(s.cfg.ModelName),
		SessionBackend: strings.TrimSpace(s.cfg.SessionBackend),
		MemoryBackend:  strings.TrimSpace(s.cfg.MemoryBackend),
		GatewayAddr:    strings.TrimSpace(s.cfg.GatewayAddr),
		GatewayLabel:   compactPortLabel(s.cfg.GatewayAddr),
		GatewayURL:     strings.TrimSpace(s.cfg.GatewayURL),
		AdminAddr:      strings.TrimSpace(s.cfg.AdminAddr),
		AdminURL:       strings.TrimSpace(s.cfg.AdminURL),
		AdminAutoPort:  s.cfg.AdminAutoPort,
		Langfuse:       normalizeLangfuseStatus(s.cfg.Langfuse),
		StateDir:       strings.TrimSpace(s.cfg.StateDir),
		DebugDir:       strings.TrimSpace(s.cfg.DebugDir),
		Routes:         s.cfg.GatewayRoutes,
	}

	if len(s.cfg.Channels) > 0 {
		out.Channels = append([]string(nil), s.cfg.Channels...)
		sort.Strings(out.Channels)
	}
	return out
}

func (s *Service) snapshotForView(view adminView) snapshot {
	if view == viewOverview {
		return s.Snapshot()
	}

	out := s.baseSnapshot()
	switch view {
	case viewSkills:
		out.Skills = s.skillsStatus()
	case viewConfig:
	case viewMemory:
		out.Memory = s.memoryStatus()
	case viewAutomation:
		out.Cron = s.cronStatus()
	case viewChats:
		out.Exec = s.execStatus()
	case viewSessions:
		out.Exec = s.execStatus()
		out.Uploads = s.uploadsStatus()
	case viewDebug:
		out.Debug = s.debugStatus()
	case viewBrowser:
		out.Browser = s.browserStatus()
	}
	return out
}

func (s *Service) cronStatus() cronStatus {
	if s == nil || s.cfg.Cron == nil {
		return cronStatus{}
	}

	status := s.cfg.Cron.Status()
	jobs := s.cfg.Cron.List()
	out := cronStatus{
		Enabled:     true,
		JobCount:    len(jobs),
		RunningJobs: intFromMap(status["jobs_running"]),
		Channels:    stringSliceFromMap(status["channels"]),
		Jobs:        make([]jobView, 0, len(jobs)),
	}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		out.Jobs = append(out.Jobs, jobViewFromJob(job))
	}
	return out
}

func (s *Service) skillsStatus() skillsStatus {
	if s == nil || s.cfg.Skills == nil {
		return skillsStatus{}
	}

	configPath := strings.TrimSpace(s.cfg.Skills.SkillsConfigPath())
	out := skillsStatus{
		Enabled:     true,
		Writable:    configPath != "",
		Refreshable: s.cfg.Skills.SkillsRefreshable(),
		ConfigPath:  configPath,
	}

	report, err := s.cfg.Skills.SkillsStatus()
	if err != nil {
		out.Error = strings.TrimSpace(err.Error())
		return out
	}

	out.TotalCount = len(report.Skills)

	bundledSkills := make([]skillView, 0, len(report.Skills))
	otherSkills := make([]skillView, 0, len(report.Skills))
	for _, entry := range report.Skills {
		view := skillViewFromStatus(entry)
		switch view.Status {
		case "disabled":
			out.DisabledCount++
		case "ready":
			out.ReadyCount++
		default:
			out.NeedsSetupCount++
		}
		if view.Bundled {
			out.BundledCount++
			bundledSkills = append(bundledSkills, view)
			continue
		}
		otherSkills = append(otherSkills, view)
	}

	if len(bundledSkills) > 0 {
		out.Groups = append(out.Groups, skillsGroupView{
			ID:     "bundled",
			Label:  "Bundled Skills",
			Skills: bundledSkills,
		})
	}
	if len(otherSkills) > 0 {
		out.Groups = append(out.Groups, skillsGroupView{
			ID:     "other",
			Label:  "Additional Skills",
			Skills: otherSkills,
		})
	}
	return out
}

func skillViewFromStatus(entry ocskills.StatusEntry) skillView {
	view := skillView{
		Name:               strings.TrimSpace(entry.Name),
		Description:        strings.TrimSpace(entry.Description),
		SkillKey:           strings.TrimSpace(entry.SkillKey),
		ConfigKey:          strings.TrimSpace(entry.ConfigKey),
		FilePath:           strings.TrimSpace(entry.FilePath),
		Source:             strings.TrimSpace(entry.Source),
		Reason:             strings.TrimSpace(entry.Reason),
		Emoji:              strings.TrimSpace(entry.Emoji),
		Homepage:           strings.TrimSpace(entry.Homepage),
		PrimaryEnv:         strings.TrimSpace(entry.PrimaryEnv),
		Bundled:            entry.Bundled,
		Always:             entry.Always,
		Disabled:           entry.Disabled,
		Eligible:           entry.Eligible,
		BlockedByAllowlist: entry.BlockedByAllowlist,
		Requirements:       skillRequirementsViewFromStatus(entry.Requirements),
		Missing:            skillRequirementsViewFromStatus(entry.Missing),
		Install:            skillInstallViewsFromStatus(entry.Install),
	}
	switch {
	case view.Disabled:
		view.Status = "disabled"
	case view.Eligible:
		view.Status = "ready"
	default:
		view.Status = "needs-setup"
	}
	view.SearchText = strings.ToLower(strings.Join([]string{
		view.Name,
		view.Description,
		view.SkillKey,
		view.Source,
		view.Reason,
		view.PrimaryEnv,
		view.FilePath,
	}, " "))
	return view
}

func skillRequirementsViewFromStatus(
	req ocskills.StatusRequirements,
) skillRequirementsView {
	return skillRequirementsView{
		OS:      append([]string(nil), req.OS...),
		Bins:    append([]string(nil), req.Bins...),
		AnyBins: append([]string(nil), req.AnyBins...),
		Env:     append([]string(nil), req.Env...),
		Config:  append([]string(nil), req.Config...),
	}
}

func skillInstallViewsFromStatus(
	options []ocskills.StatusInstallOption,
) []skillInstallView {
	if len(options) == 0 {
		return nil
	}
	out := make([]skillInstallView, 0, len(options))
	for _, option := range options {
		out = append(out, skillInstallView{
			ID:    strings.TrimSpace(option.ID),
			Kind:  strings.TrimSpace(option.Kind),
			Label: strings.TrimSpace(option.Label),
			Bins:  append([]string(nil), option.Bins...),
		})
	}
	return out
}

func (s *Service) browserStatus() browserStatus {
	if len(s.cfg.Browser.Providers) == 0 &&
		s.cfg.Browser.Managed == nil {
		return browserStatus{}
	}

	probes := make(map[string]browserEndpointView)
	out := browserStatus{}
	if s.cfg.Browser.Managed != nil {
		managed := s.cfg.Browser.Managed.BrowserManagedStatus()
		managed.LogRelativePath = filepath.ToSlash(strings.TrimSpace(
			managed.LogRelativePath,
		))
		if managed.LogURL == "" &&
			managed.LogRelativePath != "" {
			managed.LogURL = routeDebugFile + "?" + url.Values{
				queryPath: {managed.LogRelativePath},
			}.Encode()
		}
		if len(managed.RecentLogs) > 0 {
			managed.RecentLogs = append(
				[]string(nil),
				managed.RecentLogs...,
			)
		}
		out.Managed = managed
	}
	out.Enabled = len(s.cfg.Browser.Providers) > 0 || out.Managed.Enabled
	out.ProviderCount = len(s.cfg.Browser.Providers)
	out.Providers = make([]browserProviderView, 0,
		len(s.cfg.Browser.Providers))
	for i := range s.cfg.Browser.Providers {
		provider := s.cfg.Browser.Providers[i]
		view := browserProviderView{
			Name:             strings.TrimSpace(provider.Name),
			DefaultProfile:   strings.TrimSpace(provider.DefaultProfile),
			EvaluateEnabled:  provider.EvaluateEnabled,
			HostServerURL:    strings.TrimSpace(provider.HostServerURL),
			SandboxServerURL: strings.TrimSpace(provider.SandboxServerURL),
			AllowLoopback:    provider.AllowLoopback,
			AllowPrivateNet:  provider.AllowPrivateNet,
			AllowFileURLs:    provider.AllowFileURLs,
		}
		view.Host = s.probeBrowserEndpoint(view.HostServerURL, probes)
		view.Sandbox = s.probeBrowserEndpoint(
			view.SandboxServerURL,
			probes,
		)
		out.NodeCount += len(provider.Nodes)
		if len(provider.Profiles) > 0 {
			view.Profiles = make([]browserProfileView, 0,
				len(provider.Profiles))
		}
		for j := range provider.Profiles {
			profile := provider.Profiles[j]
			view.Profiles = append(view.Profiles, browserProfileView{
				Name:        strings.TrimSpace(profile.Name),
				Description: strings.TrimSpace(profile.Description),
				Transport:   strings.TrimSpace(profile.Transport),
				ServerURL:   strings.TrimSpace(profile.ServerURL),
				BrowserServerURL: strings.TrimSpace(
					profile.BrowserServerURL,
				),
			})
		}
		if len(provider.Nodes) > 0 {
			view.Nodes = make([]browserNodeView, 0, len(provider.Nodes))
		}
		for j := range provider.Nodes {
			node := provider.Nodes[j]
			serverURL := strings.TrimSpace(node.ServerURL)
			view.Nodes = append(view.Nodes, browserNodeView{
				ID:        strings.TrimSpace(node.ID),
				ServerURL: serverURL,
				Status: s.probeBrowserEndpoint(
					serverURL,
					probes,
				),
			})
		}
		out.ProfileCount += len(view.Profiles)
		sort.Slice(view.Profiles, func(a, b int) bool {
			return view.Profiles[a].Name < view.Profiles[b].Name
		})
		sort.Slice(view.Nodes, func(a, b int) bool {
			return view.Nodes[a].ID < view.Nodes[b].ID
		})
		out.Providers = append(out.Providers, view)
	}
	sort.Slice(out.Providers, func(a, b int) bool {
		return out.Providers[a].Name < out.Providers[b].Name
	})
	return out
}

func (s *Service) probeBrowserEndpoint(
	rawURL string,
	cache map[string]browserEndpointView,
) browserEndpointView {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return browserEndpointView{}
	}
	if cached, ok := cache[trimmed]; ok {
		return cached
	}

	view := browserEndpointView{
		URL: trimmed,
	}
	client := s.browserHTTPClient
	if client == nil {
		client = &http.Client{Timeout: browserProbeTimeout}
	}
	resp, err := client.Get(strings.TrimRight(trimmed, "/") + "/profiles")
	if err != nil {
		view.Error = err.Error()
		cache[trimmed] = view
		return view
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		view.Error = fmt.Sprintf("unexpected status %s", resp.Status)
		cache[trimmed] = view
		return view
	}

	var payload struct {
		Profiles []browserRemoteProbe `json:"profiles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		view.Error = fmt.Sprintf("decode profiles: %v", err)
		cache[trimmed] = view
		return view
	}
	view.Reachable = true
	view.Profiles = append([]browserRemoteProbe(nil), payload.Profiles...)
	sort.Slice(view.Profiles, func(a, b int) bool {
		return view.Profiles[a].Name < view.Profiles[b].Name
	})
	cache[trimmed] = view
	return view
}

func (s *Service) handleOverview(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewOverview)
}

func (s *Service) handleSkillsPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewSkills)
}

func (s *Service) handlePromptsPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewPrompts)
}

func (s *Service) handleIdentityPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewIdentity)
}

func (s *Service) handlePersonasPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewPersonas)
}

func (s *Service) handleChatsPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewChats)
}

func (s *Service) handleMemoryPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewMemory)
}

func (s *Service) handleAutomationPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewAutomation)
}

func (s *Service) handleSessionsPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewSessions)
}

func (s *Service) handleDebugPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewDebug)
}

func (s *Service) handleBrowserPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewBrowser)
}

func (s *Service) renderPage(
	w http.ResponseWriter,
	r *http.Request,
	view adminView,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.snapshotForView(view)
	configStatus := s.runtimeConfigStatus()
	runtimeControl := s.runtimeLifecycleStatus(r)
	refreshRuntimeControl := runtimeControl
	if view == viewRuntimeControl {
		refreshRuntimeControl = s.runtimeLifecycleRefreshStatus(r)
	}
	prompts := s.promptsStatus()
	identity := s.identityStatus()
	personas := s.personasStatus()
	chats := s.chatsStatus()
	data := pageData{
		Snapshot:         snapshot,
		Config:           configStatus,
		ConfigPending:    pendingRestartFields(configStatus),
		ConfigCanRestart: s.hasRuntimeLifecycleProvider(),
		RuntimeControl:   runtimeControl,
		Prompts:          prompts,
		Identity:         identity,
		Personas:         personas,
		Chats:            chats,
		ChatHistoryPath:  routeChatHistoryJSON,
		Notice:           strings.TrimSpace(r.URL.Query().Get(queryNotice)),
		Error:            strings.TrimSpace(r.URL.Query().Get(queryError)),
		View:             view,
		PageTitle:        pageTitle(view),
		PageSummary:      pageSummary(view),
		NavSections: adminNavSections(
			view,
			s.hasRuntimeConfigProvider(),
			s.hasRuntimeLifecycleProvider(),
		),
	}
	if view == viewChats {
		data.SelectedChat, data.SelectedChatError = resolveSelectedChat(
			chats,
			s.cfg.Chats,
			selectedChatID(r),
		)
	}
	data.PageRefresh = buildPageRefreshData(
		r,
		view,
		pageRefreshInput{
			Snapshot:          snapshot,
			Config:            configStatus,
			RuntimeControl:    refreshRuntimeControl,
			Prompts:           prompts,
			Identity:          identity,
			Personas:          personas,
			Chats:             chats,
			SelectedChat:      data.SelectedChat,
			SelectedChatError: data.SelectedChatError,
		},
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminPage.Execute(w, data); err != nil {
		http.Error(
			w,
			fmt.Sprintf("render admin page: %v", err),
			http.StatusInternalServerError,
		)
	}
}

func pendingRestartFields(status RuntimeConfigStatus) int {
	count := 0
	for _, section := range status.Sections {
		for _, field := range section.Fields {
			if field.PendingRestart {
				count++
			}
		}
	}
	return count
}

func resolveSelectedChat(
	status ChatsStatus,
	provider ChatsProvider,
	selectedID string,
) (*ChatView, string) {
	selected := selectChatView(status, selectedID)
	if selected == nil {
		return nil, ""
	}
	detailProvider, ok := provider.(ChatDetailProvider)
	if !ok {
		return selected, ""
	}
	detail, err := detailProvider.ChatDetail(
		strings.TrimSpace(selected.BaseSessionID),
	)
	if err != nil {
		return selected, strings.TrimSpace(err.Error())
	}
	if err := validateChatDetail(*selected, detail); err != nil {
		return selected, strings.TrimSpace(err.Error())
	}
	merged := mergeChatView(*selected, detail)
	return &merged, ""
}

func validateChatDetail(base ChatView, detail ChatView) error {
	baseSessionID := strings.TrimSpace(base.BaseSessionID)
	detailSessionID := strings.TrimSpace(detail.BaseSessionID)
	if detailSessionID == "" || baseSessionID == "" {
		return nil
	}
	if detailSessionID == baseSessionID {
		return nil
	}
	return fmt.Errorf(
		"chat detail mismatch: expected %q, got %q",
		baseSessionID,
		detailSessionID,
	)
}

func mergeChatView(
	base ChatView,
	detail ChatView,
) ChatView {
	merged := base
	if strings.TrimSpace(merged.DisplayLabel) == "" {
		merged.DisplayLabel = strings.TrimSpace(detail.DisplayLabel)
	}
	if strings.TrimSpace(merged.Kind) == "" {
		merged.Kind = strings.TrimSpace(detail.Kind)
	}
	if strings.TrimSpace(merged.KindLabel) == "" {
		merged.KindLabel = strings.TrimSpace(detail.KindLabel)
	}
	if strings.TrimSpace(merged.CurrentSessionID) == "" {
		merged.CurrentSessionID = strings.TrimSpace(
			detail.CurrentSessionID,
		)
	}
	if strings.TrimSpace(merged.RecallSessionID) == "" {
		merged.RecallSessionID = strings.TrimSpace(
			detail.RecallSessionID,
		)
	}
	if !detail.LastActivity.IsZero() {
		merged.LastActivity = detail.LastActivity
	}
	if detail.Epoch != 0 {
		merged.Epoch = detail.Epoch
	}
	if value := strings.TrimSpace(detail.EffectiveAssistant); value != "" {
		merged.EffectiveAssistant = value
	}
	if value := strings.TrimSpace(detail.ChatAssistantOverride); value != "" {
		merged.ChatAssistantOverride = value
	}
	if value := strings.TrimSpace(detail.NameSource); value != "" {
		merged.NameSource = value
	}
	if detail.OverridesGlobal {
		merged.OverridesGlobal = true
	}
	if value := strings.TrimSpace(detail.PersonaID); value != "" {
		merged.PersonaID = value
	}
	if value := strings.TrimSpace(detail.PersonaLabel); value != "" {
		merged.PersonaLabel = value
	}
	if detail.PersonaPinned {
		merged.PersonaPinned = true
	}
	if value := strings.TrimSpace(detail.WorkspacePath); value != "" {
		merged.WorkspacePath = value
	}
	if len(detail.KnownUserIDs) != 0 {
		merged.KnownUserIDs = detail.KnownUserIDs
	}
	if len(detail.KnownUsers) != 0 {
		merged.KnownUsers = detail.KnownUsers
	}
	if len(detail.History) != 0 {
		merged.History = detail.History
	}
	if detail.HistoryTotalCount != 0 {
		merged.HistoryTotalCount = detail.HistoryTotalCount
	}
	if detail.HistoryTruncated {
		merged.HistoryTruncated = true
	}
	if len(detail.Transcript) != 0 {
		merged.Transcript = detail.Transcript
	}
	if detail.TranscriptTruncated {
		merged.TranscriptTruncated = true
	}
	return merged
}

func adminNavSections(
	active adminView,
	showConfig bool,
	showRuntimeControl bool,
) []adminNavSection {
	controlItems := []adminNavItem{
		{Label: "Overview", Path: routeOverview},
	}
	if showConfig {
		controlItems = append(
			controlItems,
			adminNavItem{Label: "Config", Path: routeConfigPage},
		)
	}
	controlItems = append(
		controlItems,
		adminNavItem{Label: "Skills", Path: routeSkillsPage},
		adminNavItem{Label: "Prompts", Path: routePrompts},
		adminNavItem{Label: "Identity", Path: routeIdentity},
		adminNavItem{Label: "Personas", Path: routePersonas},
		adminNavItem{Label: "Chats", Path: routeChats},
		adminNavItem{Label: "Memory", Path: routeMemory},
		adminNavItem{Label: "Automation", Path: routeAutomation},
	)
	sections := []adminNavSection{
		{
			Label: "Control",
			Items: controlItems,
		},
		{
			Label: "Diagnostics",
			Items: runtimeDiagnosticsNavItems(showRuntimeControl),
		},
	}
	for i := range sections {
		for j := range sections[i].Items {
			sections[i].Items[j].Active =
				navViewForPath(sections[i].Items[j].Path) == active
		}
	}
	return sections
}

func runtimeDiagnosticsNavItems(
	showRuntimeControl bool,
) []adminNavItem {
	items := []adminNavItem{}
	if showRuntimeControl {
		items = append(
			items,
			adminNavItem{
				Label: "Runtime Control",
				Path:  routeRuntimeControlPage,
			},
		)
	}
	items = append(
		items,
		adminNavItem{Label: "Runtime Sessions", Path: routeSessions},
		adminNavItem{Label: "Debug", Path: routeDebug},
		adminNavItem{Label: "Browser", Path: routeBrowser},
	)
	return items
}

func pageTitle(view adminView) string {
	switch view {
	case viewConfig:
		return "Config"
	case viewRuntimeControl:
		return "Runtime Control"
	case viewSkills:
		return "Skills"
	case viewPrompts:
		return "Prompts"
	case viewIdentity:
		return "Identity"
	case viewPersonas:
		return "Personas"
	case viewChats:
		return "Chats"
	case viewMemory:
		return "Memory"
	case viewAutomation:
		return "Automation"
	case viewSessions:
		return "Runtime Sessions"
	case viewDebug:
		return "Debug"
	case viewBrowser:
		return "Browser"
	default:
		return "Overview"
	}
}

func pageSummary(view adminView) string {
	switch view {
	case viewConfig:
		return pageSummaryConfig
	case viewRuntimeControl:
		return pageSummaryRuntimeControl
	case viewSkills:
		return "Discover installed skills, refresh folders from disk, and manage config-backed enablement."
	case viewPrompts:
		return pageSummaryPrompts
	case viewIdentity:
		return pageSummaryIdentity
	case viewPersonas:
		return pageSummaryPersonas
	case viewChats:
		return pageSummaryChats
	case viewMemory:
		return "Inspect durable memory storage, expand file-backed " +
			"MEMORY.md scopes, and edit full files in place."
	case viewAutomation:
		return "Inspect scheduled jobs, trigger one-off runs, and clear automation state."
	case viewSessions:
		return "Review exec sessions, upload sessions, and recently persisted files."
	case viewDebug:
		return "Browse debug session indexes, recent traces, and Langfuse readiness."
	case viewBrowser:
		return "Inspect browser providers, managed browser-server state, nodes, and profiles."
	default:
		return "Runtime summary, gateway surfaces, and entry points into the rest of the admin."
	}
}

func pageRefreshWatch(view adminView) bool {
	switch view {
	case viewOverview,
		viewConfig,
		viewRuntimeControl,
		viewSkills,
		viewChats,
		viewMemory,
		viewAutomation,
		viewSessions,
		viewDebug,
		viewBrowser:
		return true
	default:
		return false
	}
}

func buildPageRefreshData(
	r *http.Request,
	view adminView,
	input pageRefreshInput,
) pageRefreshData {
	return pageRefreshData{
		CurrentPath:     pageRefreshCurrentPath(r),
		StatePath:       pageRefreshStatePath(r, view),
		Token:           pageRefreshToken(view, input),
		UpdatedAt:       input.Snapshot.GeneratedAt,
		IntervalSeconds: refreshSeconds,
		Watch:           pageRefreshWatch(view),
	}
}

func pageRefreshCurrentPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return routeOverview
	}
	values := r.URL.Query()
	values.Del(queryNotice)
	values.Del(queryError)
	path := strings.TrimSpace(r.URL.Path)
	if path == "" {
		path = routeOverview
	}
	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func pageRefreshStatePath(
	r *http.Request,
	view adminView,
) string {
	values := url.Values{}
	values.Set(queryView, string(view))
	if view == viewChats {
		if chatID := selectedChatID(r); chatID != "" {
			values.Set(queryChatID, chatID)
		}
	}
	if view == viewRuntimeControl && r != nil && r.URL != nil {
		version := strings.TrimSpace(
			r.URL.Query().Get(queryRuntimeVersion),
		)
		if version != "" {
			values.Set(queryRuntimeVersion, version)
		}
	}
	return routePageStateJSON + "?" + values.Encode()
}

func pageRefreshToken(
	view adminView,
	input pageRefreshInput,
) string {
	switch view {
	case viewConfig:
		return refreshTokenForValue(input.Config)
	case viewRuntimeControl:
		return refreshTokenForValue(input.RuntimeControl)
	case viewPrompts:
		return refreshTokenForValue(input.Prompts)
	case viewIdentity:
		return refreshTokenForValue(input.Identity)
	case viewPersonas:
		return refreshTokenForValue(input.Personas)
	case viewChats:
		return refreshTokenForValue(struct {
			Chats             ChatsStatus `json:"chats"`
			SelectedChat      *ChatView   `json:"selected_chat,omitempty"`
			SelectedChatError string      `json:"selected_chat_error,omitempty"`
		}{
			Chats:             input.Chats,
			SelectedChat:      compactChatView(input.SelectedChat),
			SelectedChatError: input.SelectedChatError,
		})
	default:
		snapshot := input.Snapshot
		snapshot.GeneratedAt = time.Time{}
		return refreshTokenForValue(snapshot)
	}
}

func compactChatView(chat *ChatView) *ChatView {
	if chat == nil {
		return nil
	}
	copy := *chat
	copy.History = nil
	copy.Transcript = nil
	return &copy
}

func refreshTokenForValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func (s *Service) pageRefreshInput(
	r *http.Request,
	view adminView,
) pageRefreshInput {
	input := pageRefreshInput{
		Snapshot: s.snapshotForView(view),
		Config:   s.runtimeConfigStatus(),
		Prompts:  s.promptsStatus(),
		Identity: s.identityStatus(),
		Personas: s.personasStatus(),
		Chats:    s.chatsStatus(),
	}
	if view == viewRuntimeControl {
		input.RuntimeControl = s.runtimeLifecycleRefreshStatus(r)
	} else {
		input.RuntimeControl = s.runtimeLifecycleStatus(r)
	}
	if view == viewChats {
		input.SelectedChat, input.SelectedChatError = resolveSelectedChat(
			input.Chats,
			s.cfg.Chats,
			selectedChatID(r),
		)
	}
	return input
}

func (s *Service) handleStatusJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.Snapshot())
}

func (s *Service) handlePageStateJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view := adminView(strings.TrimSpace(r.URL.Query().Get(queryView)))
	if view == "" {
		view = viewOverview
	}
	input := s.pageRefreshInput(r, view)
	writeJSON(w, http.StatusOK, pageStateStatus{
		Token:     pageRefreshToken(view, input),
		UpdatedAt: input.Snapshot.GeneratedAt,
	})
}

func (s *Service) handleSkillsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.skillsStatus())
}

func (s *Service) handleMemoryFilesJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.memoryStatus())
}

func (s *Service) handleMemoryFileAPI(
	w http.ResponseWriter,
	r *http.Request,
) {
	switch r.Method {
	case http.MethodGet:
		s.handleMemoryFileJSON(w, r)
	case http.MethodPost:
		s.handleSaveMemoryFile(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Service) handleMemoryFileJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	root, configured, err := configuredMemoryRoot(s.cfg.MemoryFiles)
	if err != nil || !configured {
		http.Error(
			w,
			"memory file store is not configured",
			http.StatusNotFound,
		)
		return
	}
	detail, err := readMemoryFileDetailWithResolver(
		root,
		strings.TrimSpace(r.URL.Query().Get(queryPath)),
		s.cfg.MemoryUserLabels,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Service) handleSaveMemoryFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	_, configured, err := configuredMemoryRoot(s.cfg.MemoryFiles)
	if err != nil || !configured {
		http.Error(
			w,
			"memory file store is not configured",
			http.StatusNotFound,
		)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	path := strings.TrimSpace(r.FormValue(queryPath))
	if path == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"path is required",
			returnTo,
		)
		return
	}
	if err := saveMemoryFile(
		r.Context(),
		s.cfg.MemoryFiles,
		path,
		r.FormValue(formPromptContent),
	); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Saved memory file.",
		returnTo,
	)
}

func (s *Service) handleMemoryFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root, configured, err := configuredMemoryRoot(s.cfg.MemoryFiles)
	if err != nil || !configured {
		http.Error(
			w,
			"memory file store is not configured",
			http.StatusNotFound,
		)
		return
	}
	filePath, err := resolveMemoryFile(
		root,
		strings.TrimSpace(r.URL.Query().Get(queryPath)),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, filePath)
}

func (s *Service) handleRefreshSkills(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	if s.cfg.Skills == nil || !s.cfg.Skills.SkillsRefreshable() {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"live skills repository is not available",
			returnTo,
		)
		return
	}
	if err := s.cfg.Skills.RefreshSkills(); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Refreshed skills. New or removed skill folders will be available on the next turn.",
		returnTo,
	)
}

func (s *Service) handleToggleSkill(
	w http.ResponseWriter,
	r *http.Request,
) {
	configKey, enabled, skillName, returnTo, ok := s.requireSkillTogglePOST(w, r)
	if !ok {
		return
	}

	if err := s.cfg.Skills.SetSkillEnabled(configKey, enabled); err != nil {
		s.redirectWithMessageAt(w, r, queryError, err.Error(), returnTo)
		return
	}

	name := strings.TrimSpace(skillName)
	if name == "" {
		name = strings.TrimSpace(configKey)
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	message := fmt.Sprintf(
		"Saved %s as %s. Restart %s to apply runtime changes.",
		name,
		state,
		adminBrandName,
	)
	if s.cfg.Skills != nil && s.cfg.Skills.SkillsRefreshable() {
		message = fmt.Sprintf(
			"Saved %s as %s. Changes apply on the next turn.",
			name,
			state,
		)
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		message,
		returnTo,
	)
}

func (s *Service) redirectWithMessageAt(
	w http.ResponseWriter,
	r *http.Request,
	key string,
	message string,
	fragment string,
) {
	target := &url.URL{
		Path:     redirectPathFromRequest(r),
		Fragment: strings.TrimSpace(fragment),
	}
	values := url.Values{}
	values.Set(key, message)
	target.RawQuery = values.Encode()
	http.Redirect(
		w,
		r,
		target.String(),
		http.StatusSeeOther,
	)
}

func redirectPathFromRequest(r *http.Request) string {
	if r == nil {
		return routeIndex
	}
	if path := navPath(strings.TrimSpace(r.FormValue(formReturnPath))); path != "" {
		return path
	}
	return routeIndex
}

func navPath(raw string) string {
	switch strings.TrimSpace(raw) {
	case routeIndex, routeOverview:
		return routeOverview
	case routeConfigPage:
		return routeConfigPage
	case routeRuntimeControlPage:
		return routeRuntimeControlPage
	case routeSkillsPage:
		return routeSkillsPage
	case routePrompts:
		return routePrompts
	case routeIdentity:
		return routeIdentity
	case routePersonas:
		return routePersonas
	case routeChats:
		return routeChats
	case routeMemory:
		return routeMemory
	case routeAutomation:
		return routeAutomation
	case routeSessions:
		return routeSessions
	case routeDebug:
		return routeDebug
	case routeBrowser:
		return routeBrowser
	default:
		return ""
	}
}

func navViewForPath(path string) adminView {
	switch strings.TrimSpace(path) {
	case routeIndex, routeOverview:
		return viewOverview
	case routeConfigPage:
		return viewConfig
	case routeRuntimeControlPage:
		return viewRuntimeControl
	case routeSkillsPage:
		return viewSkills
	case routePrompts:
		return viewPrompts
	case routeIdentity:
		return viewIdentity
	case routePersonas:
		return viewPersonas
	case routeChats:
		return viewChats
	case routeMemory:
		return viewMemory
	case routeAutomation:
		return viewAutomation
	case routeSessions:
		return viewSessions
	case routeDebug:
		return viewDebug
	case routeBrowser:
		return viewBrowser
	default:
		return viewOverview
	}
}

func (s *Service) handleJobsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.cronStatus().Jobs)
}

func (s *Service) handleExecSessionsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.execStatus().Sessions)
}

func (s *Service) handleUploadsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filters := uploadFiltersFromRequest(r)
	writeJSON(
		w,
		http.StatusOK,
		s.uploadsStatusFiltered(filters, 0, 0),
	)
}

func (s *Service) handleUploadSessionsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filters := uploadFiltersFromRequest(r)
	writeJSON(
		w,
		http.StatusOK,
		s.uploadsStatusFiltered(filters, 0, 0).Sessions,
	)
}

func (s *Service) handleUploadFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	root := resolveUploadsRoot(s.cfg.StateDir)
	filePath, err := resolveUploadFile(
		root,
		strings.TrimSpace(r.URL.Query().Get(queryPath)),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get(queryDownload)) != "" {
		w.Header().Set(
			"Content-Disposition",
			fmt.Sprintf(
				"attachment; filename=%q",
				filepath.Base(filePath),
			),
		)
	}
	http.ServeFile(w, r, filePath)
}

func uploadFiltersFromRequest(r *http.Request) uploadFilters {
	if r == nil || r.URL == nil {
		return uploadFilters{}
	}
	values := r.URL.Query()
	return uploadFilters{
		Channel:   strings.TrimSpace(values.Get(queryChannel)),
		UserID:    strings.TrimSpace(values.Get(queryUserID)),
		SessionID: strings.TrimSpace(values.Get(querySessionID)),
		Kind:      strings.TrimSpace(values.Get(queryKind)),
		MimeType:  strings.TrimSpace(values.Get(queryMimeType)),
		Source:    strings.TrimSpace(values.Get(querySource)),
	}
}

func (s *Service) handleDebugSessionsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.debugStatus().Sessions)
}

func (s *Service) handleDebugTracesJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get(querySessionID))
	writeJSON(
		w,
		http.StatusOK,
		s.debugStatusForSession(sessionID).RecentTraces,
	)
}

func (s *Service) handleDebugFile(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tracePath := strings.TrimSpace(r.URL.Query().Get(queryTrace))
	name := strings.TrimSpace(r.URL.Query().Get(queryName))
	relPath := strings.TrimSpace(r.URL.Query().Get(queryPath))
	if relPath == "" && name == debugEventsFileName {
		traceDir, err := s.resolveDebugTraceDir(tracePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, err := debugrecorder.ReadEventsFile(traceDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set(headerContentType, debugEventsMIMEType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	filePath, err := s.resolveDebugFile(tracePath, name, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, filePath)
}

func resolveUploadFile(root string, rel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("uploads are not enabled")
	}
	clean := filepath.Clean(strings.TrimSpace(rel))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("missing upload path")
	}
	if strings.HasPrefix(clean, "..") ||
		filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid upload path")
	}
	if uploads.IsMetadataPath(clean) {
		return "", fmt.Errorf("invalid upload path")
	}

	filePath := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", fmt.Errorf("resolve upload path: %w", err)
	}
	if relative == "." || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("invalid upload path")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("upload path is a directory")
	}
	return filePath, nil
}

func (s *Service) handleRunJob(
	w http.ResponseWriter,
	r *http.Request,
) {
	jobID, ok := s.requireJobAction(w, r)
	if !ok {
		return
	}
	if _, err := s.cfg.Cron.RunNow(jobID); err != nil {
		s.redirectWithMessage(w, r, queryError, err.Error())
		return
	}
	s.redirectWithMessage(
		w,
		r,
		queryNotice,
		"Scheduled job run requested.",
	)
}

func (s *Service) handleRemoveJob(
	w http.ResponseWriter,
	r *http.Request,
) {
	jobID, ok := s.requireJobAction(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Cron.Remove(jobID); err != nil {
		s.redirectWithMessage(w, r, queryError, err.Error())
		return
	}
	s.redirectWithMessage(
		w,
		r,
		queryNotice,
		"Scheduled job removed.",
	)
}

func (s *Service) handleClearJobs(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !s.requireCronPOST(w, r) {
		return
	}

	removed := 0
	for _, job := range s.cfg.Cron.List() {
		if job == nil || strings.TrimSpace(job.ID) == "" {
			continue
		}
		if err := s.cfg.Cron.Remove(job.ID); err != nil {
			s.redirectWithMessage(w, r, queryError, err.Error())
			return
		}
		removed++
	}
	s.redirectWithMessage(
		w,
		r,
		queryNotice,
		fmt.Sprintf("Cleared %d scheduled job(s).", removed),
	)
}

func (s *Service) requireJobAction(
	w http.ResponseWriter,
	r *http.Request,
) (string, bool) {
	if !s.requireCronPOST(w, r) {
		return "", false
	}
	jobID := strings.TrimSpace(r.FormValue(formJobID))
	if jobID == "" {
		s.redirectWithMessage(w, r, queryError, "job_id is required")
		return "", false
	}
	return jobID, true
}

func (s *Service) requireCronPOST(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if s.cfg.Cron == nil {
		http.Error(
			w,
			"scheduled jobs are not enabled",
			http.StatusNotFound,
		)
		return false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Service) requireSkillTogglePOST(
	w http.ResponseWriter,
	r *http.Request,
) (string, bool, string, string, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", false, "", "", false
	}
	if s.cfg.Skills == nil {
		http.Error(w, "skills are not enabled", http.StatusNotFound)
		return "", false, "", "", false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false, "", "", false
	}

	configKey := strings.TrimSpace(r.FormValue(formSkillKey))
	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	if configKey == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"skill_key is required",
			returnTo,
		)
		return "", false, "", "", false
	}

	rawEnabled := strings.TrimSpace(r.FormValue(formEnabled))
	enabled := rawEnabled == "true" || rawEnabled == "1"
	if rawEnabled != "true" &&
		rawEnabled != "false" &&
		rawEnabled != "1" &&
		rawEnabled != "0" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"enabled must be true or false",
			returnTo,
		)
		return "", false, "", "", false
	}

	if strings.TrimSpace(s.cfg.Skills.SkillsConfigPath()) == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"skill toggles require a config-backed runtime",
			returnTo,
		)
		return "", false, "", "", false
	}

	return configKey,
		enabled,
		strings.TrimSpace(r.FormValue(formSkillName)),
		returnTo,
		true
}

func (s *Service) redirectWithMessage(
	w http.ResponseWriter,
	r *http.Request,
	key string,
	message string,
) {
	s.redirectWithMessageAt(w, r, key, message, "")
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data = append(data, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

func jobViewFromJob(job *cron.Job) jobView {
	return jobView{
		ID:             strings.TrimSpace(job.ID),
		Name:           fallbackJobName(job),
		Schedule:       cron.ScheduleSummary(job.Schedule),
		UserID:         strings.TrimSpace(job.UserID),
		Channel:        strings.TrimSpace(job.Delivery.Channel),
		Target:         strings.TrimSpace(job.Delivery.Target),
		MessagePreview: summarizeText(job.Message, maxRunesPreview),
		LastOutput:     summarizeText(job.LastOutput, maxJobOutputRunes),
		Enabled:        job.Enabled,
		CreatedAt:      job.CreatedAt,
		UpdatedAt:      job.UpdatedAt,
		LastRunAt:      cloneTime(job.LastRunAt),
		NextRunAt:      cloneTime(job.NextRunAt),
		LastStatus:     strings.TrimSpace(job.LastStatus),
		LastError:      strings.TrimSpace(job.LastError),
	}
}

func fallbackJobName(job *cron.Job) string {
	if job == nil {
		return ""
	}
	if name := strings.TrimSpace(job.Name); name != "" {
		return name
	}
	return strings.TrimSpace(job.ID)
}

const maxRunesPreview = 96

func summarizeText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = maxRunesPreview
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

func cloneTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	next := *src
	return &next
}

func intFromMap(raw any) int {
	value, ok := raw.(int)
	if ok {
		return value
	}
	return 0
}

func stringSliceFromMap(raw any) []string {
	items, ok := raw.([]string)
	if !ok || len(items) == 0 {
		return nil
	}
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}

func formatTime(raw any) string {
	switch value := raw.(type) {
	case time.Time:
		if value.IsZero() {
			return "-"
		}
		return value.Local().Format(formatTimeLayout)
	case *time.Time:
		if value == nil || value.IsZero() {
			return "-"
		}
		return value.Local().Format(formatTimeLayout)
	default:
		return "-"
	}
}

func formatUptime(startedAt time.Time, now time.Time) string {
	if startedAt.IsZero() {
		return "-"
	}
	if now.Before(startedAt) {
		now = startedAt
	}
	return now.Sub(startedAt).Round(time.Second).String()
}

func compactPortLabel(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err == nil && strings.TrimSpace(port) != "" {
		return ":" + strings.TrimSpace(port)
	}
	return trimmed
}

func browserEndpointSummary(view browserEndpointView) string {
	if strings.TrimSpace(view.URL) == "" {
		return "-"
	}
	if !view.Reachable {
		if strings.TrimSpace(view.Error) != "" {
			return "down: " + strings.TrimSpace(view.Error)
		}
		return "down"
	}
	if len(view.Profiles) == 0 {
		return "reachable"
	}
	parts := make([]string, 0, len(view.Profiles))
	for i := range view.Profiles {
		profile := view.Profiles[i]
		name := strings.TrimSpace(profile.Name)
		state := strings.TrimSpace(profile.State)
		if name == "" && state == "" {
			continue
		}
		if state == "" {
			parts = append(parts, name)
			continue
		}
		if name == "" {
			parts = append(parts, state)
			continue
		}
		parts = append(parts, name+"="+state)
	}
	if len(parts) == 0 {
		return "reachable"
	}
	return strings.Join(parts, ", ")
}

func displayAdminAppName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if lower == "openclaw" {
		return adminRuntimePrefix
	}
	if strings.HasPrefix(lower, "openclaw-") {
		return adminRuntimePrefix + trimmed[len("openclaw"):]
	}
	return trimmed
}

func (s *Service) resolveDebugFile(
	tracePath string,
	name string,
	relPath string,
) (string, error) {
	root := strings.TrimSpace(s.cfg.DebugDir)
	if root == "" {
		return "", fmt.Errorf("debug recorder is not configured")
	}
	if strings.TrimSpace(relPath) != "" {
		return resolveDebugRootFile(root, relPath)
	}
	if !isAllowedDebugFile(name) {
		return "", fmt.Errorf("unsupported debug file: %s", name)
	}
	traceDir, err := s.resolveDebugTraceDir(tracePath)
	if err != nil {
		return "", err
	}

	candidate := filepath.Join(traceDir, name)
	if name == debugEventsFileName {
		resolved, _, err := debugrecorder.ResolveEventsFilePath(traceDir)
		if err == nil {
			return resolved, nil
		}
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("debug file not found")
	}
	return candidate, nil
}

func (s *Service) resolveDebugTraceDir(tracePath string) (string, error) {
	root := strings.TrimSpace(s.cfg.DebugDir)
	if root == "" {
		return "", fmt.Errorf("debug recorder is not configured")
	}
	if strings.TrimSpace(tracePath) == "" {
		return "", fmt.Errorf("trace path is required")
	}

	candidate := filepath.Clean(filepath.Join(
		root,
		filepath.FromSlash(tracePath),
	))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve debug root: %w", err)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve debug file: %w", err)
	}
	if absCandidate != absRoot &&
		!strings.HasPrefix(
			absCandidate,
			absRoot+string(os.PathSeparator),
		) {
		return "", fmt.Errorf("debug trace escapes debug root")
	}
	info, err := os.Stat(absCandidate)
	if err != nil {
		return "", fmt.Errorf("debug trace not found")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("debug trace path is not a directory")
	}
	return absCandidate, nil
}

func resolveDebugRootFile(root string, relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("debug path is required")
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid debug path")
	}

	candidate := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve debug root: %w", err)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve debug file: %w", err)
	}
	if absCandidate != absRoot &&
		!strings.HasPrefix(
			absCandidate,
			absRoot+string(os.PathSeparator),
		) {
		return "", fmt.Errorf("debug file escapes debug root")
	}

	info, err := os.Stat(absCandidate)
	if err != nil {
		return "", fmt.Errorf("debug file not found")
	}
	if info.IsDir() {
		return "", fmt.Errorf("debug path is a directory")
	}
	return absCandidate, nil
}

func isAllowedDebugFile(name string) bool {
	switch strings.TrimSpace(name) {
	case debugMetaFileName, debugEventsFileName, debugResultFileName:
		return true
	default:
		return false
	}
}

var adminPage = template.Must(
	template.New("admin").Funcs(template.FuncMap{
		"formatTime":                 formatTime,
		"browserEndpointSummary":     browserEndpointSummary,
		"displayAdminAppName":        displayAdminAppName,
		"promptSections":             promptSections,
		"promptBlockCount":           promptBlockCount,
		"hasPromptValue":             hasPromptValue,
		"promptValuesDiffer":         promptValuesDiffer,
		"promptCollapsedSummary":     promptCollapsedSummary,
		"promptInlineEditorTitle":    promptInlineEditorTitle,
		"promptInlineEditorSummary":  promptInlineEditorSummary,
		"promptRuntimeEditorSummary": promptRuntimeEditorSummary,
		"personaStoreTitle":          personaStoreTitle,
		"personaStoreUsageLabels":    personaStoreUsageLabels,
		"personaCustomPersonas":      personaCustomPersonas,
		"personaBuiltInPersonas":     personaBuiltInPersonas,
		"personaStoreBuiltInCount":   personaStoreBuiltInCount,
		"personaStoreCustomCount":    personaStoreCustomCount,
		"personaDisplayName":         personaDisplayName,
		"personaKindLabel":           personaKindLabel,
		"personaSummaryText":         personaSummaryText,
		"chatDisplayLabel":           chatDisplayLabel,
		"chatHistoryAPIPath":         chatHistoryAPIPath,
		"chatHiddenHistory":          chatHiddenHistory,
		"chatHiddenTranscript":       chatHiddenTranscript,
		"chatHiddenTurns":            chatHiddenTurns,
		"chatKnownUsers":             chatKnownUsers,
		"chatHasTranscript":          chatHasTranscript,
		"chatHistorySummary":         chatHistorySummary,
		"chatNameSourceLabel":        chatNameSourceLabel,
		"chatTranscriptLabel":        chatTranscriptLabel,
		"chatTranscriptSummary":      chatTranscriptSummary,
		"chatTurnSpeaker":            chatTurnSpeaker,
		"chatOverrideSample":         chatOverrideSample,
		"chatVisibleHistory":         chatVisibleHistory,
		"chatVisibleTranscript":      chatVisibleTranscript,
		"chatVisibleTurns":           chatVisibleTurns,
		"hasTime":                    hasTime,
	}).Parse(
		adminPageHTML +
			promptsPageTemplateHTML +
			chatsPageTemplateHTML +
			identityPageTemplateHTML +
			personasPageTemplateHTML,
	),
)

const adminPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TRPC-CLAW admin</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f3eee7;
      --panel: rgba(255, 252, 247, 0.92);
      --panel-strong: #fffdf8;
      --line: #d7cfc2;
      --ink: #1d1a16;
      --muted: #5f574d;
      --accent: #0f6f61;
      --warn: #9a2f2f;
      --ok: #2d6d3f;
      --shadow: 0 18px 40px rgba(35, 29, 22, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: "Iowan Old Style", "Palatino Linotype", serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, #fff8ef, transparent 38%),
        linear-gradient(180deg, #efe7dc 0%, var(--bg) 100%);
    }
    .app-shell {
      display: grid;
      grid-template-columns: 272px minmax(0, 1fr);
      min-height: 100vh;
    }
    .sidebar {
      position: sticky;
      top: 0;
      align-self: start;
      height: 100vh;
      padding: 24px 18px 22px;
      border-right: 1px solid rgba(215, 207, 194, 0.92);
      background: rgba(255, 250, 244, 0.78);
      backdrop-filter: blur(16px);
    }
    .sidebar-brand {
      display: flex;
      align-items: center;
      gap: 12px;
      margin-bottom: 28px;
    }
    .sidebar-mark {
      width: 42px;
      height: 42px;
      border-radius: 14px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      background: var(--accent);
      color: white;
      font-weight: 700;
      letter-spacing: 0.04em;
      box-shadow: var(--shadow);
    }
    .sidebar-eyebrow {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .sidebar-title {
      margin-top: 2px;
      font-size: 26px;
      font-weight: 700;
      line-height: 1.1;
    }
    .sidebar-subtle {
      margin-top: 4px;
      color: var(--muted);
      font-size: 14px;
    }
    main {
      margin: 0;
      width: 100%;
      padding: 32px 28px 40px;
    }
    .page-wrap {
      max-width: 1440px;
    }
    .sidebar-nav {
      display: grid;
      gap: 22px;
    }
    .sidebar-section-title {
      margin: 0 0 10px;
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    .sidebar-links {
      display: grid;
      gap: 8px;
    }
    .sidebar-link {
      display: flex;
      align-items: center;
      min-height: 42px;
      padding: 10px 14px;
      border-radius: 14px;
      border: 1px solid transparent;
      color: var(--ink);
      text-decoration: none;
      font-weight: 700;
      transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
    }
    .sidebar-link:hover {
      background: rgba(255, 253, 248, 0.88);
      border-color: rgba(215, 207, 194, 0.88);
    }
    .sidebar-link.active {
      background: rgba(15, 111, 97, 0.1);
      border-color: rgba(15, 111, 97, 0.24);
      color: var(--accent);
      box-shadow: var(--shadow);
    }
    .page-header {
      margin-bottom: 18px;
    }
    .page-toolbar {
      margin-top: 16px;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 12px 16px;
    }
    .page-toolbar-copy {
      display: grid;
      gap: 4px;
      min-width: 0;
    }
    .page-toolbar-updated {
      color: var(--muted);
      font-size: 13px;
      font-weight: 700;
      letter-spacing: 0.02em;
    }
    .page-toolbar-note {
      color: var(--muted);
      font-size: 14px;
      max-width: 720px;
    }
    .page-refresh-link {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-height: 40px;
      padding: 8px 14px;
      border-radius: 999px;
      border: 1px solid var(--line);
      background: rgba(255, 253, 248, 0.92);
      color: var(--ink);
      text-decoration: none;
      font-weight: 700;
      box-shadow: var(--shadow);
    }
    .page-refresh-link:hover {
      border-color: rgba(15, 111, 97, 0.28);
      color: var(--accent);
    }
    .page-refresh-alert {
      margin-top: 16px;
    }
    .page-kicker {
      margin: 0 0 10px;
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
    }
    h1, h2 { margin: 0 0 14px; }
    h1 { font-size: 36px; }
    h2 { font-size: 22px; }
    p, li, td, th, button, code {
      font-size: 15px;
      line-height: 1.5;
    }
    .subtle {
      color: var(--muted);
      max-width: 860px;
    }
    .notice {
      margin: 18px 0 0;
      padding: 12px 14px;
      border-radius: 14px;
      border: 1px solid var(--line);
      background: var(--panel-strong);
      box-shadow: var(--shadow);
    }
    .notice.ok { border-color: rgba(45, 109, 63, 0.3); }
    .notice.err { border-color: rgba(154, 47, 47, 0.3); }
    .stats,
    .panels {
      display: grid;
      gap: 16px;
      margin-top: 24px;
    }
    .stats { grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); }
    .panels { grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); }
    .card {
      border: 1px solid var(--line);
      border-radius: 20px;
      padding: 20px;
      background: var(--panel);
      box-shadow: var(--shadow);
      backdrop-filter: blur(8px);
    }
    .stat-label {
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.08em;
      font-size: 12px;
    }
    .stat-value {
      display: block;
      margin-top: 8px;
      font-size: 28px;
      font-weight: 700;
    }
    .meta {
      margin: 0;
      display: grid;
      grid-template-columns: minmax(110px, 160px) 1fr;
      gap: 8px 12px;
    }
    .meta dt {
      color: var(--muted);
      font-weight: 700;
      min-width: 0;
    }
    .meta dd {
      margin: 0;
      min-width: 0;
      overflow-wrap: anywhere;
      word-break: break-word;
    }
    a { color: var(--accent); }
    code {
      background: rgba(15, 111, 97, 0.08);
      padding: 2px 6px;
      border-radius: 8px;
      word-break: break-all;
    }
    input[type="text"],
    textarea {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 16px;
      padding: 12px 14px;
      font: inherit;
      background: var(--panel-strong);
      color: var(--ink);
    }
    textarea {
      min-height: 160px;
      resize: vertical;
      white-space: pre-wrap;
      font-family: "SFMono-Regular", "SFMono-Regular", monospace;
      line-height: 1.45;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 12px;
    }
    th, td {
      text-align: left;
      vertical-align: top;
      min-width: 0;
      padding: 12px 10px;
      border-top: 1px solid var(--line);
      overflow-wrap: anywhere;
      word-break: break-word;
    }
    th {
      color: var(--muted);
      font-size: 13px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    form { margin: 0; }
    button {
      border: 0;
      border-radius: 999px;
      padding: 8px 14px;
      background: var(--accent);
      color: white;
      cursor: pointer;
    }
    button.secondary {
      background: #c9bca9;
      color: var(--ink);
    }
    button.warn { background: var(--warn); }
    .empty {
      margin-top: 14px;
      color: var(--muted);
    }
    .preview-box {
      max-width: 220px;
    }
    .preview-box img,
    .preview-box video {
      display: block;
      max-width: 220px;
      max-height: 140px;
      border-radius: 12px;
      border: 1px solid var(--line);
      background: white;
    }
    .preview-box audio {
      width: 220px;
      max-width: 100%;
    }
    .filter-tabs {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 14px;
    }
    .filter-tab {
      background: #e6ddcf;
      color: var(--ink);
    }
    .filter-tab.active {
      background: var(--accent);
      color: white;
    }
    .skills-controls {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 12px 16px;
      align-items: center;
      margin-top: 18px;
    }
    .skills-search-wrap {
      min-width: 0;
    }
    .skills-controls input {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 12px 16px;
      font: inherit;
      background: var(--panel-strong);
      color: var(--ink);
    }
    .skills-toolbar-side {
      display: flex;
      align-items: center;
      justify-content: flex-end;
      gap: 12px;
    }
    .skills-shown {
      color: var(--muted);
      font-weight: 700;
      white-space: nowrap;
    }
    .skills-header {
      display: flex;
      flex-wrap: wrap;
      gap: 10px 18px;
      align-items: flex-end;
      justify-content: space-between;
    }
    .skills-header-copy {
      max-width: 820px;
    }
    .skills-lead {
      margin: 8px 0 0;
      color: var(--muted);
      max-width: 700px;
    }
    .skills-ops-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
      gap: 12px;
      margin-top: 16px;
    }
    .skills-op-card {
      border: 1px solid var(--line);
      border-radius: 16px;
      padding: 14px 16px;
      background: rgba(255, 253, 248, 0.72);
    }
    .skills-op-label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.1em;
    }
    .skills-op-value {
      margin-top: 8px;
      font-weight: 700;
      line-height: 1.45;
    }
    .skills-op-value code {
      background: rgba(15, 111, 97, 0.08);
    }
    .skills-op-note {
      margin-top: 8px;
      color: var(--muted);
      font-size: 14px;
    }
    .skills-op-actions {
      margin-top: 12px;
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .skills-op-actions form {
      margin: 0;
    }
    .skills-op-actions button {
      padding: 7px 12px;
      font-size: 14px;
    }
    .config-sections {
      display: grid;
      gap: 18px;
      margin-top: 16px;
    }
    .config-section-card {
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 18px;
      background: rgba(255, 253, 248, 0.8);
      box-shadow: 0 10px 24px rgba(35, 29, 22, 0.04);
    }
    .config-field-list {
      display: grid;
      gap: 14px;
      margin-top: 14px;
    }
    .config-field {
      border: 1px solid rgba(215, 207, 194, 0.9);
      border-radius: 16px;
      background: rgba(255, 252, 247, 0.94);
      overflow: hidden;
      transition: box-shadow 140ms ease, border-color 140ms ease;
    }
    .config-field[open] {
      border-color: rgba(15, 111, 97, 0.28);
      box-shadow: 0 14px 28px rgba(35, 29, 22, 0.08);
    }
    .config-field summary {
      list-style: none;
      cursor: pointer;
      padding: 14px 16px;
    }
    .config-field summary::-webkit-details-marker {
      display: none;
    }
    .config-field-detail {
      padding: 0 16px 16px;
      border-top: 1px solid rgba(215, 207, 194, 0.65);
    }
    .config-field-top {
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 12px;
    }
    .config-field-title {
      margin: 0;
      font-size: 18px;
    }
    .config-badges {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
    }
    .config-badge {
      border-radius: 999px;
      border: 1px solid rgba(15, 111, 97, 0.18);
      background: rgba(15, 111, 97, 0.08);
      color: var(--accent);
      padding: 4px 10px;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .config-badge.warn {
      border-color: rgba(154, 47, 47, 0.18);
      background: rgba(154, 47, 47, 0.08);
      color: var(--warn);
    }
    .config-meta {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      gap: 10px 16px;
      margin-top: 12px;
    }
    .config-meta-block {
      border-radius: 14px;
      background: rgba(243, 238, 231, 0.62);
      padding: 10px 12px;
    }
    .config-meta-label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .config-meta-value {
      margin-top: 6px;
      line-height: 1.5;
      word-break: break-word;
    }
    .config-form {
      display: grid;
      gap: 10px;
      margin-top: 14px;
    }
    .config-form-row {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .config-form input,
    .config-form select {
      min-width: min(100%, 320px);
      padding: 10px 12px;
      border-radius: 12px;
      border: 1px solid var(--line);
      background: rgba(255, 255, 255, 0.96);
      color: var(--ink);
      font: inherit;
    }
    .config-form .subtle {
      margin: 0;
    }
    .runtime-meta-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      gap: 10px 16px;
      margin-top: 12px;
    }
    .runtime-meta-card {
      border-radius: 14px;
      background: rgba(243, 238, 231, 0.62);
      padding: 10px 12px;
    }
    .runtime-meta-label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .runtime-meta-value {
      margin-top: 6px;
      line-height: 1.5;
      word-break: break-word;
    }
    .runtime-pending {
      margin-top: 16px;
      padding-top: 16px;
      border-top: 1px solid rgba(215, 207, 194, 0.65);
    }
    .runtime-pending h3,
    .runtime-version-card h3,
    .runtime-changelog-card h3 {
      margin: 0 0 10px;
      font-size: 16px;
    }
    .runtime-summary-list {
      margin: 12px 0 0;
      padding-left: 20px;
    }
    .runtime-summary-list li {
      color: var(--muted);
    }
    .runtime-version-form,
    .runtime-version-view-form {
      display: grid;
      gap: 10px;
      margin-top: 14px;
    }
    .runtime-version-form select,
    .runtime-version-view-form select {
      min-width: min(100%, 320px);
      padding: 10px 12px;
      border-radius: 12px;
      border: 1px solid var(--line);
      background: rgba(255, 255, 255, 0.96);
      color: var(--ink);
      font: inherit;
    }
    .runtime-version-actions {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .runtime-changelog {
      margin-top: 12px;
      padding: 14px;
      border-radius: 16px;
      border: 1px solid rgba(215, 207, 194, 0.85);
      background: rgba(255, 253, 248, 0.9);
      white-space: pre-wrap;
      font-family: "SFMono-Regular", "SFMono-Regular", monospace;
      line-height: 1.45;
      max-height: 420px;
      overflow: auto;
    }
    .skills-group {
      margin-top: 18px;
    }
    .skills-group h3 {
      margin: 0 0 10px;
      font-size: 16px;
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .skill-card {
      border: 1px solid var(--line);
      border-radius: 18px;
      background: var(--panel-strong);
      margin-top: 12px;
      overflow: hidden;
      transition: box-shadow 140ms ease, border-color 140ms ease;
    }
    .skill-card[open] {
      border-color: rgba(15, 111, 97, 0.28);
      box-shadow: 0 14px 28px rgba(35, 29, 22, 0.08);
    }
    .skill-card summary {
      list-style: none;
      cursor: pointer;
      padding: 16px 18px;
    }
    .skill-card summary::-webkit-details-marker {
      display: none;
    }
    .skill-main {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 18px;
    }
    .skill-copy {
      min-width: 0;
      flex: 1 1 auto;
    }
    .skill-headline {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 10px;
    }
    .skill-name {
      display: flex;
      align-items: center;
      gap: 8px;
      font-weight: 700;
      font-size: 17px;
    }
    .skill-dot {
      width: 10px;
      height: 10px;
      border-radius: 999px;
      background: var(--line);
      flex: 0 0 auto;
    }
    .skill-dot.ready { background: var(--ok); }
    .skill-dot.needs-setup { background: #c27a20; }
    .skill-dot.disabled { background: var(--muted); }
    .skill-badges {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .skill-badges.inline {
      margin-top: 10px;
    }
    .skill-summary-side {
      display: flex;
      flex-direction: column;
      align-items: flex-end;
      justify-content: flex-start;
      gap: 8px;
      flex: 0 0 auto;
      min-width: 72px;
    }
    .skill-badge {
      border-radius: 999px;
      padding: 4px 9px;
      font-size: 12px;
      border: 1px solid var(--line);
      background: rgba(15, 111, 97, 0.08);
      color: var(--ink);
    }
    .skill-badge.status {
      padding: 6px 12px;
      font-size: 13px;
      font-weight: 700;
      letter-spacing: 0.01em;
    }
    .skill-badge.ready {
      color: var(--ok);
      border-color: rgba(45, 109, 63, 0.25);
      background: rgba(45, 109, 63, 0.08);
    }
    .skill-badge.needs-setup {
      color: #9b5f12;
      border-color: rgba(194, 122, 32, 0.25);
      background: rgba(194, 122, 32, 0.08);
    }
    .skill-badge.disabled {
      color: var(--muted);
      border-color: rgba(95, 87, 77, 0.18);
      background: rgba(95, 87, 77, 0.08);
    }
    .skill-description {
      margin-top: 10px;
      color: #3f3932;
      max-width: 820px;
      display: -webkit-box;
      -webkit-line-clamp: 2;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    .skill-card[open] .skill-description {
      display: block;
      overflow: visible;
    }
    .skill-reason {
      margin-top: 8px;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
      color: var(--muted);
      font-size: 14px;
      min-width: 0;
    }
    .skill-reason-label {
      display: inline-flex;
      align-items: center;
      min-height: 22px;
      padding: 2px 8px;
      border-radius: 999px;
      border: 1px solid rgba(95, 87, 77, 0.18);
      background: rgba(95, 87, 77, 0.06);
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.01em;
    }
    .skill-reason-label.needs-setup {
      color: #9b5f12;
      border-color: rgba(194, 122, 32, 0.25);
      background: rgba(194, 122, 32, 0.08);
    }
    .skill-reason-label.disabled {
      color: #655b50;
      border-color: rgba(95, 87, 77, 0.22);
      background: rgba(95, 87, 77, 0.1);
    }
    .skill-reason-text {
      min-width: 0;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .skill-card[open] .skill-reason-text {
      white-space: normal;
    }
    .skill-toggle-group {
      display: inline-flex;
      align-items: center;
      flex: 0 0 auto;
    }
    .skill-inline-toggle-form {
      margin: 0;
      flex: 0 0 auto;
    }
    .skill-inline-toggle {
      display: inline-flex;
      align-items: center;
      border: 0;
      border-radius: 999px;
      padding: 0;
      background: transparent;
      color: var(--ink);
      cursor: pointer;
      font: inherit;
    }
    .skill-inline-toggle:focus-visible {
      outline: 2px solid rgba(15, 111, 97, 0.42);
      outline-offset: 3px;
    }
    .skill-inline-toggle-track {
      position: relative;
      width: 54px;
      height: 32px;
      border-radius: 999px;
      background: rgba(95, 87, 77, 0.28);
      border: 1px solid rgba(95, 87, 77, 0.18);
      transition: background 120ms ease, border-color 120ms ease;
    }
    .skill-inline-toggle-track::after {
      content: "";
      position: absolute;
      top: 3px;
      left: 3px;
      width: 24px;
      height: 24px;
      border-radius: 999px;
      background: white;
      box-shadow: 0 4px 10px rgba(35, 29, 22, 0.18);
      transition: transform 120ms ease;
    }
    .skill-inline-toggle.enabled .skill-inline-toggle-track {
      background: rgba(45, 109, 63, 0.9);
      border-color: rgba(45, 109, 63, 0.35);
    }
    .skill-inline-toggle.enabled .skill-inline-toggle-track::after {
      transform: translateX(22px);
    }
    .skill-details {
      margin-top: 14px;
      padding: 14px 18px 18px;
      border-top: 1px solid var(--line);
      background: rgba(15, 111, 97, 0.02);
    }
    .skill-details-head {
      display: flex;
      flex-wrap: wrap;
      align-items: flex-start;
      gap: 12px;
      margin-bottom: 14px;
    }
    .skill-details-grid {
      display: grid;
      gap: 12px;
      grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
    }
    .skill-list {
      margin: 8px 0 0;
      padding-left: 18px;
    }
    .memory-preview {
      max-width: 540px;
      color: #3f3932;
      overflow-wrap: anywhere;
      white-space: pre-wrap;
    }
    .prompt-detail {
      overflow: hidden;
    }
    .prompt-detail[open] {
      border-color: rgba(15, 111, 97, 0.28);
      box-shadow: 0 14px 28px rgba(35, 29, 22, 0.08);
    }
    .prompt-detail summary {
      list-style: none;
      cursor: pointer;
    }
    .prompt-detail summary::-webkit-details-marker {
      display: none;
    }
    .prompt-detail-copy,
    .prompt-detail-hint {
      margin: 8px 0 0;
    }
    .prompt-detail-body {
      margin-top: 14px;
      padding-top: 14px;
      border-top: 1px solid var(--line);
    }
    .memory-scope {
      display: grid;
      gap: 4px;
    }
    .memory-user-label {
      color: var(--muted);
      font-size: 0.92rem;
      font-weight: 700;
    }
    .memory-controls {
      margin: 18px 0 12px;
    }
    .memory-filter-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 10px;
    }
    .memory-filter-head label {
      color: var(--muted);
      font-size: 0.92rem;
      font-weight: 700;
    }
    .memory-filter-grid {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      gap: 12px;
      min-width: 0;
    }
    .memory-filter-field {
      min-width: 0;
    }
    .memory-filter-field label {
      display: block;
      margin-bottom: 6px;
      color: var(--muted);
      font-size: 0.92rem;
      font-weight: 700;
    }
    .memory-filter-field input {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 12px 16px;
      font: inherit;
      background: var(--panel-strong);
      color: var(--ink);
    }
    .memory-shown {
      color: var(--muted);
      font-weight: 700;
      white-space: nowrap;
    }
    .memory-list {
      display: grid;
      gap: 14px;
      margin-top: 16px;
    }
    .memory-card-head {
      display: flex;
      flex-wrap: wrap;
      justify-content: space-between;
      gap: 14px;
      align-items: flex-start;
    }
    .memory-card-meta {
      display: grid;
      gap: 6px;
      color: var(--muted);
      font-size: 0.9rem;
      text-align: right;
      white-space: nowrap;
    }
    .memory-path {
      margin-top: 8px;
      color: var(--muted);
      overflow-wrap: anywhere;
    }
    .memory-card-toolbar {
      display: flex;
      flex-wrap: wrap;
      justify-content: space-between;
      gap: 10px 14px;
      align-items: center;
      margin-bottom: 12px;
    }
    .memory-editor-form {
      margin-top: 12px;
    }
    .memory-editor-form textarea {
      width: 100%;
      min-height: 240px;
      margin-top: 8px;
      font: inherit;
      resize: vertical;
    }
    .memory-editor-status {
      margin-top: 8px;
      color: var(--muted);
      font-size: 0.92rem;
    }
    .memory-editor-status.error {
      color: #b6544d;
    }
    .chat-list {
      display: grid;
      gap: 14px;
      margin-top: 16px;
    }
    .chat-card {
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 16px 18px;
      background: rgba(255, 253, 248, 0.72);
    }
    .chat-card-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      flex-wrap: wrap;
    }
    .chat-card-copy {
      min-width: 0;
      flex: 1 1 280px;
    }
    .chat-card-title {
      font-size: 18px;
      font-weight: 700;
      line-height: 1.35;
      overflow-wrap: anywhere;
      word-break: break-word;
    }
    .chat-card-kind {
      margin-top: 6px;
      color: var(--muted);
    }
    .chat-card-link {
      flex: 0 0 auto;
      white-space: nowrap;
    }
    .chat-card-grid {
      display: grid;
      gap: 10px 18px;
      grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
      margin-top: 14px;
    }
    .chat-card-meta {
      min-width: 0;
    }
    .chat-card-label {
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
    .chat-card-value {
      margin-top: 6px;
      line-height: 1.45;
      overflow-wrap: anywhere;
      word-break: break-word;
    }
    .chat-detail-section + .chat-detail-section {
      margin-top: 24px;
      padding-top: 22px;
      border-top: 1px solid var(--line);
    }
    .chat-detail-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 12px;
    }
    .chat-detail-head h3,
    .chat-action-card h4,
    .chat-transcript-title {
      margin: 0;
    }
    .chat-disclosure,
    .chat-disclosure-more {
      border: 1px solid var(--line);
      border-radius: 16px;
      background: rgba(255, 253, 248, 0.72);
      overflow: hidden;
    }
    .chat-disclosure[open],
    .chat-disclosure-more[open] {
      border-color: rgba(15, 111, 97, 0.28);
      box-shadow: 0 14px 28px rgba(35, 29, 22, 0.08);
    }
    .chat-disclosure summary,
    .chat-disclosure-more summary {
      list-style: none;
      cursor: pointer;
      padding: 14px 16px;
    }
    .chat-disclosure summary::-webkit-details-marker,
    .chat-disclosure-more summary::-webkit-details-marker {
      display: none;
    }
    .chat-disclosure-meta {
      margin: 8px 0 0;
    }
    .chat-disclosure-body {
      margin-top: 0;
      padding: 0 16px 16px;
      border-top: 1px solid var(--line);
    }
    .chat-disclosure-body > :first-child {
      margin-top: 14px;
    }
    .chat-disclosure-body > :last-child {
      margin-bottom: 0;
    }
    .chat-disclosure-more {
      margin-top: 12px;
    }
    .chat-history-shell {
      margin-top: 14px;
    }
    .chat-history-status {
      margin: 0;
    }
    .chat-history-toolbar {
      margin: 14px 0 0;
    }
    .chat-history-more {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 16px;
      padding: 12px 14px;
      background: rgba(255, 253, 248, 0.82);
      color: var(--ink);
      font: inherit;
      text-align: left;
      cursor: pointer;
      transition: border-color 120ms ease, box-shadow 120ms ease;
    }
    .chat-history-more:hover,
    .chat-history-more:focus {
      border-color: rgba(15, 111, 97, 0.38);
      box-shadow: 0 12px 24px rgba(35, 29, 22, 0.08);
      outline: none;
    }
    .chat-history-bounded {
      margin: 12px 0 0;
    }
    .chat-timeline {
      display: grid;
      gap: 12px;
      margin-top: 14px;
    }
    .chat-timeline-session {
      padding-top: 12px;
      border-top: 1px solid var(--line);
    }
    .chat-timeline-session:first-child {
      padding-top: 0;
      border-top: 0;
    }
    .chat-timeline-session-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      flex-wrap: wrap;
    }
    .chat-timeline-session-label {
      margin: 0;
      font-size: 16px;
      font-weight: 700;
    }
    .chat-timeline-session-meta {
      margin-top: 6px;
    }
    .chat-transcript-list {
      display: grid;
      gap: 14px;
      margin-top: 12px;
    }
    .chat-transcript-card,
    .chat-action-card {
      border: 1px solid var(--line);
      border-radius: 16px;
      padding: 14px 16px;
      background: rgba(255, 253, 248, 0.72);
      min-width: 0;
    }
    .chat-transcript-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      flex-wrap: wrap;
    }
    .chat-turn-list {
      display: grid;
      gap: 10px;
      margin-top: 14px;
    }
    .chat-turn {
      border-left: 3px solid rgba(15, 111, 97, 0.2);
      padding-left: 12px;
      min-width: 0;
    }
    .chat-turn-head {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 12px;
      flex-wrap: wrap;
    }
    .chat-turn-speaker {
      font-weight: 700;
    }
    .chat-turn-quote {
      margin: 8px 0 0;
      padding-left: 12px;
      border-left: 2px solid var(--line);
      color: var(--muted);
    }
    .chat-turn-text {
      margin-top: 8px;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
      word-break: break-word;
    }
    .chat-action-grid {
      display: grid;
      gap: 12px;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      margin-top: 12px;
    }
    @media (max-width: 760px) {
      .app-shell {
        grid-template-columns: 1fr;
      }
      .sidebar {
        position: static;
        height: auto;
        border-right: 0;
        border-bottom: 1px solid rgba(215, 207, 194, 0.92);
      }
      main {
        padding: 24px 16px 32px;
      }
      h1 { font-size: 30px; }
      .meta { grid-template-columns: 1fr; }
      .skills-controls {
        grid-template-columns: 1fr;
      }
      .memory-filter-grid {
        grid-template-columns: 1fr;
      }
      .skills-toolbar-side {
        justify-content: space-between;
      }
      .memory-filter-head {
        align-items: flex-start;
        flex-direction: column;
      }
      .skills-header {
        align-items: flex-start;
      }
      .skill-main {
        flex-direction: column;
      }
      .skill-summary-side {
        width: 100%;
        justify-content: flex-start;
        align-items: flex-start;
      }
      table, thead, tbody, th, td, tr { display: block; }
      thead { display: none; }
      td {
        padding: 10px 0;
        border-top: 0;
      }
      tr {
        padding: 14px 0;
        border-top: 1px solid var(--line);
      }
    }
  </style>
</head>
<body>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="sidebar-brand">
        <div class="sidebar-mark">TC</div>
        <div>
          <div class="sidebar-eyebrow">control</div>
          <div class="sidebar-title">TRPC-CLAW</div>
          {{if .Snapshot.AppName}}
          <div class="sidebar-subtle">{{displayAdminAppName .Snapshot.AppName}}</div>
          {{end}}
        </div>
      </div>
      <nav class="sidebar-nav" aria-label="Admin sections">
        {{range .NavSections}}
        <section>
          <div class="sidebar-section-title">{{.Label}}</div>
          <div class="sidebar-links">
            {{range .Items}}
            <a class="sidebar-link{{if .Active}} active{{end}}" href="{{.Path}}">
              {{.Label}}
            </a>
            {{end}}
          </div>
        </section>
        {{end}}
      </nav>
    </aside>
    <main>
      <div class="page-wrap">
        <header class="page-header">
          <p class="page-kicker">TRPC-CLAW admin</p>
          <h1>{{.PageTitle}}</h1>
          <p class="subtle">{{.PageSummary}}</p>
          <div class="page-toolbar">
            <div class="page-toolbar-copy">
              <div class="page-toolbar-updated">
                Updated {{formatTime .PageRefresh.UpdatedAt}}
              </div>
              {{if .PageRefresh.Watch}}
              <div class="page-toolbar-note">
                This page watches for newer runtime state without
                interrupting your reading or editing.
              </div>
              {{end}}
            </div>
            <a class="page-refresh-link" href="{{.PageRefresh.CurrentPath}}">
              Refresh page
            </a>
          </div>
        </header>
        {{if .PageRefresh.Watch}}
        <div
          class="notice page-refresh-alert"
          hidden
          data-page-stale-root
          data-page-state-path="{{.PageRefresh.StatePath}}"
          data-page-state-token="{{.PageRefresh.Token}}"
          data-page-refresh-interval="{{.PageRefresh.IntervalSeconds}}"
        >
          New runtime state is available for this page.
          <a href="{{.PageRefresh.CurrentPath}}">Refresh page</a>
        </div>
        {{end}}
        {{if .Notice}}<div class="notice ok">{{.Notice}}</div>{{end}}
        {{if .Error}}<div class="notice err">{{.Error}}</div>{{end}}

    {{if eq .View "overview"}}
    <section class="stats">
      <article class="card">
        <span class="stat-label">Instance</span>
        <span class="stat-value">{{.Snapshot.InstanceID}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Gateway</span>
        <span class="stat-value">{{if .Snapshot.GatewayLabel}}{{.Snapshot.GatewayLabel}}{{else}}{{.Snapshot.GatewayAddr}}{{end}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Jobs</span>
        <span class="stat-value">{{.Snapshot.Cron.JobCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Skills</span>
        <span class="stat-value">{{.Snapshot.Skills.TotalCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Memory Files</span>
        <span class="stat-value">{{.Snapshot.Memory.FileCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Exec Sessions</span>
        <span class="stat-value">{{.Snapshot.Exec.SessionCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Uploads</span>
        <span class="stat-value">{{.Snapshot.Uploads.FileCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Browser Profiles</span>
        <span class="stat-value">{{.Snapshot.Browser.ProfileCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Debug Sessions</span>
        <span class="stat-value">{{.Snapshot.Debug.SessionCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Recent Traces</span>
        <span class="stat-value">{{.Snapshot.Debug.TraceCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Langfuse</span>
        <span class="stat-value">
          {{if .Snapshot.Langfuse.Ready}}ready
          {{else if .Snapshot.Langfuse.Enabled}}error
          {{else}}off{{end}}
        </span>
      </article>
    </section>

    <section class="panels">
      <article class="card">
        <h2>Runtime</h2>
        <dl class="meta">
          <dt>App</dt>
          <dd>{{displayAdminAppName .Snapshot.AppName}}</dd>
          <dt>Agent Type</dt>
          <dd>
            {{if .Snapshot.AgentType}}
              {{.Snapshot.AgentType}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Model</dt>
          <dd>
            {{if .Snapshot.ModelName}}
              {{.Snapshot.ModelMode}} / {{.Snapshot.ModelName}}
            {{else if .Snapshot.ModelMode}}
              {{.Snapshot.ModelMode}}
            {{else}}-{{end}}
          </dd>
          <dt>Session Backend</dt>
          <dd>
            {{if .Snapshot.SessionBackend}}
              {{.Snapshot.SessionBackend}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Memory Backend</dt>
          <dd>
            {{if .Snapshot.MemoryBackend}}
              {{.Snapshot.MemoryBackend}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Host</dt>
          <dd>
            {{if .Snapshot.Hostname}}
              {{.Snapshot.Hostname}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>PID</dt>
          <dd>{{if .Snapshot.PID}}{{.Snapshot.PID}}{{else}}-{{end}}</dd>
          <dt>Started</dt>
          <dd>{{formatTime .Snapshot.StartedAt}}</dd>
          <dt>Uptime</dt>
          <dd>{{.Snapshot.Uptime}}</dd>
          <dt>Gateway URL</dt>
          <dd>
            <a href="{{.Snapshot.GatewayURL}}">
              {{.Snapshot.GatewayURL}}
            </a>
          </dd>
          <dt>Admin URL</dt>
          <dd><a href="{{.Snapshot.AdminURL}}">{{.Snapshot.AdminURL}}</a></dd>
          <dt>Admin Auto Port</dt>
          <dd>{{.Snapshot.AdminAutoPort}}</dd>
          <dt>State Dir</dt>
          <dd><code>{{.Snapshot.StateDir}}</code></dd>
          <dt>Debug Dir</dt>
          <dd><code>{{.Snapshot.DebugDir}}</code></dd>
          <dt>Generated</dt>
          <dd>{{formatTime .Snapshot.GeneratedAt}}</dd>
        </dl>
      </article>

      <article class="card">
        <h2>Gateway Surface</h2>
        <dl class="meta">
          <dt>Health</dt>
          <dd><code>{{.Snapshot.Routes.HealthPath}}</code></dd>
          <dt>Messages</dt>
          <dd><code>{{.Snapshot.Routes.MessagesPath}}</code></dd>
          <dt>Status</dt>
          <dd><code>{{.Snapshot.Routes.StatusPath}}</code></dd>
          <dt>Cancel</dt>
          <dd><code>{{.Snapshot.Routes.CancelPath}}</code></dd>
          <dt>Channels</dt>
          <dd>
            {{if .Snapshot.Channels}}
              {{range $i, $ch := .Snapshot.Channels}}
                {{if $i}}, {{end}}{{$ch}}
              {{end}}
            {{else}}none{{end}}
          </dd>
          <dt>JSON</dt>
          <dd>
            <a href="/api/status">status</a> ·
            <a href="/api/skills/status">skills</a> ·
            <a href="/api/memory/files">memory</a> ·
            <a href="/api/cron/jobs">jobs</a> ·
            <a href="/api/exec/sessions">exec</a> ·
            <a href="/api/uploads">uploads</a> ·
            <a href="/api/uploads/sessions">upload sessions</a> ·
            <a href="/api/debug/sessions">debug sessions</a> ·
            <a href="/api/debug/traces">debug traces</a>
          </dd>
        </dl>
      </article>

      <article class="card">
        <h2>Automation</h2>
        {{if .Snapshot.Cron.Enabled}}
        <p class="subtle">
          Persisted jobs continue after gateway restarts. Telegram quick
          commands like <code>/jobs</code> and <code>/jobs_clear</code> remain
          useful, but the admin surface is the place for global inspection and
          one-off management.
        </p>
        <div class="actions">
          <form method="post" action="/api/cron/jobs/clear">
            <input type="hidden" name="return_path" value="/overview">
            <button
              class="warn"
              type="submit"
              onclick="return confirm('Clear all scheduled jobs?');"
            >
              Clear All Jobs
            </button>
          </form>
        </div>
        {{else}}
        <p class="empty">Scheduled jobs are not enabled for this runtime.</p>
        {{end}}
      </article>

      <article class="card">
        <h2>Skills Surface</h2>
        <p class="subtle">
          Read-only management view for bundled, local, and external skills.
          It highlights disabled skills, setup gaps, and the config/env
          requirements you still need to satisfy before a skill becomes usable.
        </p>
        <dl class="meta">
          <dt>Total</dt>
          <dd>{{.Snapshot.Skills.TotalCount}}</dd>
          <dt>Ready</dt>
          <dd>{{.Snapshot.Skills.ReadyCount}}</dd>
          <dt>Needs Setup</dt>
          <dd>{{.Snapshot.Skills.NeedsSetupCount}}</dd>
          <dt>Disabled</dt>
          <dd>{{.Snapshot.Skills.DisabledCount}}</dd>
          <dt>Bundled</dt>
          <dd>{{.Snapshot.Skills.BundledCount}}</dd>
          <dt>JSON</dt>
          <dd><a href="/api/skills/status">/api/skills/status</a></dd>
        </dl>
        <p class="subtle" style="margin-top: 12px;">
          Open <a href="/skills">Skills Inventory</a>.
        </p>
      </article>

      <article class="card">
        <h2>Sessions</h2>
        <p class="subtle">
          Exec sessions and persisted uploads live on their own page so the
          overview stays scannable.
        </p>
        <dl class="meta">
          <dt>Exec Sessions</dt>
          <dd>{{.Snapshot.Exec.SessionCount}}</dd>
          <dt>Running Exec</dt>
          <dd>{{.Snapshot.Exec.RunningCount}}</dd>
          <dt>Uploads</dt>
          <dd>{{.Snapshot.Uploads.FileCount}}</dd>
          <dt>Upload Sessions</dt>
          <dd>{{len .Snapshot.Uploads.Sessions}}</dd>
          <dt>Open</dt>
          <dd><a href="/sessions">Sessions</a></dd>
        </dl>
      </article>

      <article class="card">
        <h2>Memory Surface</h2>
        <p class="subtle">
          Durable memory can use structured backends or file-backed
          <code>MEMORY.md</code> scopes. The Memory page inventories the
          file-backed scopes when this runtime uses the file backend.
        </p>
        <dl class="meta">
          <dt>Backend</dt>
          <dd>{{if .Snapshot.Memory.Backend}}{{.Snapshot.Memory.Backend}}{{else}}-{{end}}</dd>
          <dt>File Memory</dt>
          <dd>{{.Snapshot.Memory.FileEnabled}}</dd>
          <dt>Files</dt>
          <dd>{{.Snapshot.Memory.FileCount}}</dd>
          <dt>Total Bytes</dt>
          <dd>{{.Snapshot.Memory.TotalBytes}}</dd>
          <dt>JSON</dt>
          <dd><a href="/api/memory/files">/api/memory/files</a></dd>
          <dt>Open</dt>
          <dd><a href="/memory">Memory</a></dd>
        </dl>
      </article>

      <article class="card">
        <h2>Debug</h2>
        <p class="subtle">
          Trace browsing and Langfuse drill-down live on a separate page.
        </p>
        <dl class="meta">
          <dt>Debug Sessions</dt>
          <dd>{{.Snapshot.Debug.SessionCount}}</dd>
          <dt>Recent Traces</dt>
          <dd>{{.Snapshot.Debug.TraceCount}}</dd>
          <dt>Langfuse</dt>
          <dd>
            {{if .Snapshot.Langfuse.Ready}}ready
            {{else if .Snapshot.Langfuse.Enabled}}starting
            {{else}}off{{end}}
          </dd>
          <dt>Status</dt>
          <dd>
            {{if .Snapshot.Debug.Error}}
              <span class="subtle">{{.Snapshot.Debug.Error}}</span>
            {{else if .Snapshot.Debug.Enabled}}
              ready
            {{else}}
              idle
            {{end}}
          </dd>
          <dt>Open</dt>
          <dd><a href="/debug">Debug</a></dd>
        </dl>
      </article>

      <article class="card">
        <h2>Browser</h2>
        <p class="subtle">
          Browser providers, managed browser-server state, and profile details
          are grouped on their own page.
        </p>
        <dl class="meta">
          <dt>Providers</dt>
          <dd>{{.Snapshot.Browser.ProviderCount}}</dd>
          <dt>Profiles</dt>
          <dd>{{.Snapshot.Browser.ProfileCount}}</dd>
          <dt>Nodes</dt>
          <dd>{{.Snapshot.Browser.NodeCount}}</dd>
          <dt>Status</dt>
          <dd>
            {{if .Snapshot.Browser.Managed.Enabled}}
              {{if .Snapshot.Browser.Managed.State}}
                {{.Snapshot.Browser.Managed.State}}
              {{else}}
                configured
              {{end}}
            {{else if .Snapshot.Browser.Enabled}}
              ready
            {{else}}
              idle
            {{end}}
          </dd>
          <dt>Open</dt>
          <dd><a href="/browser">Browser</a></dd>
        </dl>
      </article>
    </section>
    {{end}}

    {{if eq .View "config"}}
    <section class="card" style="margin-top: 24px;" id="config-admin">
      <div class="skills-header">
        <div class="skills-header-copy">
          <h2>Runtime Config</h2>
          <p class="skills-lead">
            This page writes directly to the main runtime config file and keeps
            unset fields distinct from explicit values.
          </p>
        </div>
      </div>
      <div class="skills-ops-grid">
        <div class="skills-op-card">
          <div class="skills-op-label">Config file</div>
          <div class="skills-op-value">
            {{if .Config.ConfigPath}}
              <code>{{.Config.ConfigPath}}</code>
            {{else}}
              Runtime config path is not available
            {{end}}
          </div>
          <div class="skills-op-note">
            Reset deletes the YAML key so the runtime falls back to inherited
            behavior.
          </div>
        </div>
        <div class="skills-op-card">
          <div class="skills-op-label">Apply model</div>
          <div class="skills-op-value">Restart required by default</div>
          <div class="skills-op-note">
            The current runtime stays unchanged until you restart, so this page
            shows both the saved config and the live runtime side by side.
          </div>
        </div>
        {{if .ConfigCanRestart}}
        <div class="skills-op-card">
          <div class="skills-op-label">Runtime control</div>
          <div class="skills-op-value">Restart and version actions</div>
          <div class="skills-op-note">
            Use the runtime controls page for graceful restarts, forced
            restarts, and target-version actions.
          </div>
          <div class="skills-op-actions">
            <a class="page-refresh-link" href="/runtime-control">
              Open Runtime Control
            </a>
          </div>
        </div>
        {{end}}
      </div>
      {{if gt .ConfigPending 0}}
      <div class="skills-ops-grid">
        <div class="skills-op-card">
          <div class="skills-op-label">Pending restart</div>
          <div class="skills-op-value">
            {{.ConfigPending}} saved field{{if ne .ConfigPending 1}}s{{end}}
            still need a restart before the running runtime will use them.
          </div>
          <div class="skills-op-note">
            Saved config changes are already on disk. The live runtime stays on
            the previous values until the next restart.
          </div>
          {{if .ConfigCanRestart}}
          <div class="skills-op-actions">
            <a class="page-refresh-link" href="/runtime-control">
              Open Runtime Control
            </a>
          </div>
          {{end}}
        </div>
      </div>
      {{end}}
      {{if .Config.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Config.Error}}
      </div>
      {{end}}
      {{if .Config.Sections}}
      <div class="config-sections">
        {{range .Config.Sections}}
        <section class="config-section-card" id="config-section-{{.Key}}">
          <h3>{{.Title}}</h3>
          {{if .Summary}}
          <p class="subtle">{{.Summary}}</p>
          {{end}}
          <div class="config-field-list">
            {{range .Fields}}
            <details class="config-field" id="config-field-{{.Key}}">
              {{$field := .}}
              <summary class="config-field-summary">
                <div class="config-field-top">
                  <div>
                    <h4 class="config-field-title">{{.Title}}</h4>
                    {{if .Summary}}
                    <p class="subtle">{{.Summary}}</p>
                    {{end}}
                  </div>
                  <div class="config-badges">
                    <span class="config-badge">
                      {{if eq .ApplyMode "hot"}}Hot apply{{else if eq .ApplyMode "next_turn"}}Next turn{{else}}Restart{{end}}
                    </span>
                    {{if .PendingRestart}}
                    <span class="config-badge warn">Pending restart</span>
                    {{end}}
                  </div>
                </div>
              </summary>
              <div class="config-field-detail">
                <div class="config-meta">
                  <div class="config-meta-block">
                    <div class="config-meta-label">Configured</div>
                    <div class="config-meta-value">
                      {{if eq .ConfiguredSource "explicit"}}
                        {{if .ConfiguredValue}}<code>{{.ConfiguredValue}}</code>{{else}}<code>(empty)</code>{{end}}
                      {{else}}
                        <span class="subtle">Inherited</span>
                      {{end}}
                    </div>
                    {{if .ConfiguredSourceLabel}}
                    <div class="subtle">{{.ConfiguredSourceLabel}}</div>
                    {{end}}
                  </div>
                  <div class="config-meta-block">
                    <div class="config-meta-label">Current runtime</div>
                    <div class="config-meta-value">
                      {{if .RuntimeValue}}<code>{{.RuntimeValue}}</code>{{else}}<span class="subtle">-</span>{{end}}
                    </div>
                    {{if .RuntimeSourceLabel}}
                    <div class="subtle">{{.RuntimeSourceLabel}}</div>
                    {{end}}
                  </div>
                </div>
                {{if eq .InputType "readonly"}}
                <p class="subtle" style="margin-top: 12px;">
                  Read-only runtime diagnostics.
                </p>
                {{else}}
                <form method="post" action="/api/config/save" class="config-form">
                  <input type="hidden" name="field_key" value="{{.Key}}">
                  <input type="hidden" name="return_to" value="config-field-{{.Key}}">
                  <input type="hidden" name="return_path" value="/config">
                  {{if eq .InputType "select"}}
                  <select name="value">
                    {{range $field.Options}}
                    <option value="{{.Value}}" {{if eq $field.EditorValue .Value}}selected{{end}}>
                      {{.Label}}
                    </option>
                    {{end}}
                  </select>
                  {{else if eq .InputType "number"}}
                  <input
                    type="number"
                    name="value"
                    value="{{.EditorValue}}"
                    {{if .Placeholder}}placeholder="{{.Placeholder}}"{{end}}
                  >
                  {{else}}
                  <input
                    type="text"
                    name="value"
                    value="{{.EditorValue}}"
                    {{if .Placeholder}}placeholder="{{.Placeholder}}"{{end}}
                  >
                  {{end}}
                  <div class="config-form-row">
                    <button type="submit">Save</button>
                    {{if .Resettable}}
                    <button class="secondary" type="submit" formaction="/api/config/reset">
                      Reset
                    </button>
                    {{end}}
                  </div>
                </form>
                {{end}}
              </div>
            </details>
            {{end}}
          </div>
        </section>
        {{end}}
      </div>
      {{else if not .Config.Error}}
      <p class="subtle" style="margin-top: 12px;">
        Runtime config editing is not available for this runtime.
      </p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "runtime_control"}}
    {{if .RuntimeControl.Error}}
    <div class="notice err" style="margin-top: 24px;">
      {{.RuntimeControl.Error}}
    </div>
    {{end}}
    {{if .RuntimeControl.Enabled}}
    <section class="panels" style="margin-top: 24px;">
      <article class="card" id="runtime-control-state">
        <h2>Runtime State</h2>
        <p class="subtle">
          Review the current version, queue depth, and any lifecycle action
          that is already in flight.
        </p>
        <div class="runtime-meta-grid">
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">Current Version</div>
            <div class="runtime-meta-value">
              {{if .RuntimeControl.Status.CurrentVersion}}
                <code>{{.RuntimeControl.Status.CurrentVersion}}</code>
              {{else}}
                <span class="subtle">-</span>
              {{end}}
            </div>
          </div>
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">Latest Version</div>
            <div class="runtime-meta-value">
              {{if .RuntimeControl.Index.LatestVersion}}
                <code>{{.RuntimeControl.Index.LatestVersion}}</code>
              {{else}}
                <span class="subtle">Unavailable</span>
              {{end}}
            </div>
          </div>
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">State</div>
            <div class="runtime-meta-value">
              {{if .RuntimeControl.Status.State}}
                {{.RuntimeControl.Status.State}}
              {{else}}
                <span class="subtle">Unknown</span>
              {{end}}
            </div>
          </div>
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">Exit Code</div>
            <div class="runtime-meta-value">
              {{.RuntimeControl.Status.ExitCode}}
            </div>
          </div>
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">Running Requests</div>
            <div class="runtime-meta-value">
              {{.RuntimeControl.Status.RunningRequests}}
            </div>
          </div>
          <div class="runtime-meta-card">
            <div class="runtime-meta-label">Queued Requests</div>
            <div class="runtime-meta-value">
              {{.RuntimeControl.Status.QueuedRequests}}
            </div>
          </div>
        </div>
        {{if .RuntimeControl.Status.Pending}}
        <div class="runtime-pending">
          <h3>Pending Action</h3>
          <dl class="meta">
            <dt>Kind</dt>
            <dd>{{.RuntimeControl.Status.Pending.Kind}}</dd>
            <dt>Mode</dt>
            <dd>{{.RuntimeControl.Status.Pending.Mode}}</dd>
            {{if .RuntimeControl.Status.Pending.TargetVersion}}
            <dt>Target Version</dt>
            <dd>
              <code>{{.RuntimeControl.Status.Pending.TargetVersion}}</code>
            </dd>
            {{end}}
            {{if .RuntimeControl.Status.Pending.Actor}}
            <dt>Actor</dt>
            <dd>{{.RuntimeControl.Status.Pending.Actor}}</dd>
            {{end}}
            {{if .RuntimeControl.Status.Pending.Source}}
            <dt>Source</dt>
            <dd>{{.RuntimeControl.Status.Pending.Source}}</dd>
            {{end}}
            {{if hasTime .RuntimeControl.Status.Pending.RequestedAt}}
            <dt>Requested</dt>
            <dd>{{formatTime .RuntimeControl.Status.Pending.RequestedAt}}</dd>
            {{end}}
            {{if hasTime .RuntimeControl.Status.UpdatedAt}}
            <dt>Updated</dt>
            <dd>{{formatTime .RuntimeControl.Status.UpdatedAt}}</dd>
            {{end}}
          </dl>
          {{if .RuntimeControl.Status.Pending.Summary}}
          <ul class="runtime-summary-list">
            {{range .RuntimeControl.Status.Pending.Summary}}
            <li>{{.}}</li>
            {{end}}
          </ul>
          {{end}}
        </div>
        {{else}}
        <p class="subtle" style="margin-top: 14px;">
          No restart or version switch is currently pending.
        </p>
        {{end}}
      </article>

      <article class="card" id="runtime-control-quick-actions">
        <h2>Quick Actions</h2>
        <p class="subtle">
          These controls line up with the runtime lifecycle card behavior:
          graceful actions wait for in-flight work, forced actions exit after
          the forced-shutdown window.
        </p>
        <div class="skills-ops-grid">
          <div class="skills-op-card">
            <div class="skills-op-label">Restart</div>
            <div class="skills-op-value">Graceful restart</div>
            <div class="skills-op-note">
              Stop admitting new work, drain active requests, and then restart.
            </div>
            <div class="skills-op-actions">
              <form method="post" action="/api/runtime/control/action">
                <input type="hidden" name="kind" value="restart">
                <input type="hidden" name="mode" value="graceful">
                <input type="hidden" name="return_path" value="/runtime-control">
                <input type="hidden" name="return_to" value="runtime-control-quick-actions">
                <button type="submit">Graceful Restart</button>
              </form>
            </div>
          </div>
          <div class="skills-op-card">
            <div class="skills-op-label">Restart</div>
            <div class="skills-op-value">Force restart</div>
            <div class="skills-op-note">
              Request a restart and exit once the forced shutdown timeout lands.
            </div>
            <div class="skills-op-actions">
              <form method="post" action="/api/runtime/control/action">
                <input type="hidden" name="kind" value="restart">
                <input type="hidden" name="mode" value="force">
                <input type="hidden" name="return_path" value="/runtime-control">
                <input type="hidden" name="return_to" value="runtime-control-quick-actions">
                <button class="warn" type="submit">Force Restart</button>
              </form>
            </div>
          </div>
          <div class="skills-op-card">
            <div class="skills-op-label">Upgrade</div>
            <div class="skills-op-value">Latest version</div>
            <div class="skills-op-note">
              Ask the runtime to switch to the latest published release.
            </div>
            <div class="skills-op-actions">
              <form method="post" action="/api/runtime/control/action">
                <input type="hidden" name="kind" value="upgrade">
                <input type="hidden" name="mode" value="graceful">
                <input type="hidden" name="return_path" value="/runtime-control">
                <input type="hidden" name="return_to" value="runtime-control-quick-actions">
                <button type="submit">Upgrade to Latest</button>
              </form>
            </div>
          </div>
          <div class="skills-op-card">
            <div class="skills-op-label">Upgrade</div>
            <div class="skills-op-value">Force latest upgrade</div>
            <div class="skills-op-note">
              Request the latest version and force the handoff after the drain
              window ends.
            </div>
            <div class="skills-op-actions">
              <form method="post" action="/api/runtime/control/action">
                <input type="hidden" name="kind" value="upgrade">
                <input type="hidden" name="mode" value="force">
                <input type="hidden" name="return_path" value="/runtime-control">
                <input type="hidden" name="return_to" value="runtime-control-quick-actions">
                <button class="warn" type="submit">Force Upgrade to Latest</button>
              </form>
            </div>
          </div>
        </div>
      </article>
    </section>

    <section class="panels" style="margin-top: 16px;">
      <article class="card runtime-version-card" id="runtime-control-version">
        <h2>Target Version</h2>
        <p class="subtle">
          Pick a release to inspect notes or request a direct switch to that
          version.
        </p>
        {{if .RuntimeControl.Index.MinSupportedTarget}}
        <p class="subtle">
          Minimum supported target:
          <code>{{.RuntimeControl.Index.MinSupportedTarget}}</code>
        </p>
        {{end}}
        {{if .RuntimeControl.Index.Versions}}
        <form method="get" action="/runtime-control" class="runtime-version-view-form">
          <select name="version">
            {{range .RuntimeControl.Index.Versions}}
            <option value="{{.Version}}" {{if eq $.RuntimeControl.SelectedVersion .Version}}selected{{end}}>
              {{.Version}}
            </option>
            {{end}}
          </select>
          <div class="runtime-version-actions">
            <button class="secondary" type="submit">View Release Notes</button>
          </div>
        </form>
        <form method="post" action="/api/runtime/control/action" class="runtime-version-form">
          <input type="hidden" name="kind" value="upgrade">
          <input type="hidden" name="return_path" value="/runtime-control">
          <input type="hidden" name="return_to" value="runtime-control-version">
          <select name="target_version">
            {{range .RuntimeControl.Index.Versions}}
            <option value="{{.Version}}" {{if eq $.RuntimeControl.SelectedVersion .Version}}selected{{end}}>
              {{.Version}}
            </option>
            {{end}}
          </select>
          <div class="runtime-version-actions">
            <button type="submit" name="mode" value="graceful">
              Switch Gracefully
            </button>
            <button class="warn" type="submit" name="mode" value="force">
              Switch Forcefully
            </button>
          </div>
        </form>
        {{else}}
        <p class="subtle" style="margin-top: 14px;">
          No published versions are available from the configured release
          source.
        </p>
        {{end}}
      </article>

      <article class="card runtime-changelog-card" id="runtime-control-changelog">
        <h2>Release Notes</h2>
        {{if .RuntimeControl.Changelog.Version}}
        <p class="subtle">
          Notes for <code>{{.RuntimeControl.Changelog.Version}}</code>.
        </p>
        {{else if .RuntimeControl.SelectedVersion}}
        <p class="subtle">
          Notes for <code>{{.RuntimeControl.SelectedVersion}}</code> are not
          available yet.
        </p>
        {{else}}
        <p class="subtle">
          Pick a version to inspect release notes.
        </p>
        {{end}}
        {{if .RuntimeControl.Changelog.Summary}}
        <ul class="runtime-summary-list">
          {{range .RuntimeControl.Changelog.Summary}}
          <li>{{.}}</li>
          {{end}}
        </ul>
        {{end}}
        {{if .RuntimeControl.Changelog.Changelog}}
        <div class="runtime-changelog">
          {{.RuntimeControl.Changelog.Changelog}}
        </div>
        {{else}}
        <p class="subtle" style="margin-top: 14px;">
          The selected version does not currently have release notes.
        </p>
        {{end}}
      </article>
    </section>
    {{else if not .RuntimeControl.Error}}
    <section class="card" style="margin-top: 24px;">
      <h2>Runtime Control</h2>
      <p class="subtle">
        Runtime lifecycle controls are not available for this runtime.
      </p>
    </section>
    {{end}}
    {{end}}

    {{if eq .View "skills"}}
    <section class="card" style="margin-top: 24px;" id="skills-admin" data-skills-root>
      <div class="skills-header">
        <div class="skills-header-copy">
          <h2>Skills Inventory</h2>
          <p class="skills-lead">
            Bundled, local, project, and external skills discovered by this
            runtime.
          </p>
        </div>
      </div>
      <div class="skills-ops-grid">
        <div class="skills-op-card">
          <div class="skills-op-label">
            {{if .Snapshot.Skills.Writable}}Config-backed changes{{else}}Runtime state{{end}}
          </div>
          <div class="skills-op-value">
            {{if .Snapshot.Skills.Writable}}
              Writes to <code>{{.Snapshot.Skills.ConfigPath}}</code>
            {{else}}
              Read-only runtime view
            {{end}}
          </div>
          <div class="skills-op-note">
            {{if .Snapshot.Skills.Refreshable}}
              Enabled changes apply on the next turn.
            {{else if .Snapshot.Skills.Writable}}
              Enabled changes are saved, but runtime updates still require a restart.
            {{else}}
              Enable and disable controls are unavailable for this runtime.
            {{end}}
          </div>
        </div>
        {{if .Snapshot.Skills.Refreshable}}
        <div class="skills-op-card">
          <div class="skills-op-label">Refresh from disk</div>
          <div class="skills-op-value">
            Rescan skill folders and update this inventory.
          </div>
          <div class="skills-op-note">
            Use this after adding or removing skill folders on disk.
          </div>
          <div class="skills-op-actions">
            <form method="post" action="/api/skills/refresh">
              <input type="hidden" name="return_to" value="skills-admin">
              <input type="hidden" name="return_path" value="/skills">
              <button class="secondary" type="submit">Refresh inventory</button>
            </form>
          </div>
        </div>
        {{end}}
      </div>
      {{if .Snapshot.Skills.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Snapshot.Skills.Error}}
      </div>
      {{end}}
      {{if .Snapshot.Skills.Groups}}
      <div class="filter-tabs">
        <button class="filter-tab active" type="button" data-skill-tab="all">
          All {{.Snapshot.Skills.TotalCount}}
        </button>
        <button class="filter-tab" type="button" data-skill-tab="ready">
          Ready {{.Snapshot.Skills.ReadyCount}}
        </button>
        <button class="filter-tab" type="button" data-skill-tab="needs-setup">
          Needs Setup {{.Snapshot.Skills.NeedsSetupCount}}
        </button>
        <button class="filter-tab" type="button" data-skill-tab="disabled">
          Disabled {{.Snapshot.Skills.DisabledCount}}
        </button>
      </div>
      <div class="skills-controls">
        <div class="skills-search-wrap">
          <input
            type="search"
            placeholder="Search skills by name, path, key, env, or reason"
            data-skills-filter
          >
        </div>
        <div class="skills-toolbar-side">
          <span class="skills-shown"><span data-skills-shown>{{.Snapshot.Skills.TotalCount}}</span> shown</span>
        </div>
      </div>

      {{range .Snapshot.Skills.Groups}}
      <div class="skills-group" data-skills-group id="skills-group-{{.ID}}">
        <h3>{{.Label}}</h3>
        {{range .Skills}}
        <details
          class="skill-card"
          id="skill-card-{{.ConfigKey}}"
          data-skill-card
          data-skill-status="{{.Status}}"
          data-skill-search="{{.SearchText}}"
        >
          <summary>
            <div class="skill-main">
              <div class="skill-copy">
                <div class="skill-headline">
                  <div class="skill-name">
                    <span class="skill-dot {{.Status}}"></span>
                    {{if .Emoji}}<span>{{.Emoji}}</span>{{end}}
                    <span>{{.Name}}</span>
                  </div>
                </div>
                <div class="skill-badges inline">
                  {{if .Bundled}}<span class="skill-badge">bundled</span>{{end}}
                  {{if .BlockedByAllowlist}}<span class="skill-badge">allowlist</span>{{end}}
                  {{if .Always}}<span class="skill-badge">always</span>{{end}}
                  {{if .PrimaryEnv}}<span class="skill-badge">{{.PrimaryEnv}}</span>{{end}}
                </div>
                <div class="skill-description">{{.Description}}</div>
                {{if .Reason}}
                <div class="skill-reason">
                  <span class="skill-reason-label {{.Status}}">
                    {{if eq .Status "needs-setup"}}Setup Required{{else if eq .Status "disabled"}}Disabled{{else}}Reason{{end}}
                  </span>
                  <span class="skill-reason-text">{{.Reason}}</span>
                </div>
                {{end}}
              </div>
              <div class="skill-summary-side">
                {{if $.Snapshot.Skills.Writable}}
                <div class="skill-toggle-group">
                  <form
                    method="post"
                    action="/api/skills/toggle"
                    class="skill-inline-toggle-form"
                    data-skill-inline-toggle
                    data-skill-config="{{.ConfigKey}}"
                  >
                    <input type="hidden" name="skill_key" value="{{.ConfigKey}}">
                    <input type="hidden" name="skill_name" value="{{.Name}}">
                    <input type="hidden" name="enabled" value="{{if .Disabled}}true{{else}}false{{end}}">
                    <input type="hidden" name="return_to" value="skill-card-{{.ConfigKey}}">
                    <input type="hidden" name="return_path" value="/skills">
                    <button
                      class="skill-inline-toggle {{if not .Disabled}}enabled{{end}}"
                      type="submit"
                      data-skill-toggle-button
                      data-skill-switch="{{.ConfigKey}}"
                      role="switch"
                      aria-checked="{{if .Disabled}}false{{else}}true{{end}}"
                      aria-label="Enabled for {{.Name}}"
                      title="{{if .Disabled}}Enable{{else}}Disable{{end}} {{.Name}}"
                    >
                      <span class="skill-inline-toggle-track" aria-hidden="true"></span>
                    </button>
                  </form>
                </div>
                {{end}}
              </div>
            </div>
          </summary>
          <div class="skill-details">
            <div class="skill-details-head">
              <div class="subtle">
                {{if and $.Snapshot.Skills.Writable $.Snapshot.Skills.Refreshable}}
                The row-level Enabled switch saves
                <code>skills.entries.{{.ConfigKey}}.enabled</code> and refreshes
                this runtime for the next turn.
                {{else if $.Snapshot.Skills.Writable}}
                The row-level Enabled switch saves
                <code>skills.entries.{{.ConfigKey}}.enabled</code> for this
                skill.
                {{else}}
                Enable/disable controls are unavailable for this runtime.
                {{end}}
              </div>
            </div>
            <div class="skill-details-grid">
              <div>
                <strong>Skill Key</strong>
                <div><code>{{.SkillKey}}</code></div>
              </div>
              <div>
                <strong>Config Key</strong>
                <div><code>{{.ConfigKey}}</code></div>
              </div>
              <div>
                <strong>Source</strong>
                <div>{{if .Source}}{{.Source}}{{else}}unknown{{end}}</div>
              </div>
              <div>
                <strong>Primary Env</strong>
                <div>{{if .PrimaryEnv}}<code>{{.PrimaryEnv}}</code>{{else}}-{{end}}</div>
              </div>
              <div>
                <strong>Path</strong>
                <div><code>{{.FilePath}}</code></div>
              </div>
              <div>
                <strong>Homepage</strong>
                <div>
                  {{if .Homepage}}
                  <a href="{{.Homepage}}" target="_blank" rel="noopener noreferrer">{{.Homepage}}</a>
                  {{else}}-{{end}}
                </div>
              </div>
            </div>

            {{if or .Missing.Bins .Missing.AnyBins .Missing.Env .Missing.Config .Missing.OS}}
            <div style="margin-top: 14px;">
              <strong>Missing Requirements</strong>
              <ul class="skill-list">
                {{if .Missing.Bins}}
                <li>bins:
                  {{range $i, $item := .Missing.Bins}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Missing.AnyBins}}
                <li>one of:
                  {{range $i, $item := .Missing.AnyBins}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Missing.Env}}
                <li>env:
                  {{range $i, $item := .Missing.Env}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Missing.Config}}
                <li>config:
                  {{range $i, $item := .Missing.Config}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Missing.OS}}
                <li>os:
                  {{range $i, $item := .Missing.OS}}{{if $i}}, {{end}}{{$item}}{{end}}
                </li>
                {{end}}
              </ul>
            </div>
            {{end}}

            {{if or .Requirements.Bins .Requirements.AnyBins .Requirements.Env .Requirements.Config .Requirements.OS}}
            <div style="margin-top: 14px;">
              <strong>Declared Requirements</strong>
              <ul class="skill-list">
                {{if .Requirements.Bins}}
                <li>bins:
                  {{range $i, $item := .Requirements.Bins}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Requirements.AnyBins}}
                <li>one of:
                  {{range $i, $item := .Requirements.AnyBins}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Requirements.Env}}
                <li>env:
                  {{range $i, $item := .Requirements.Env}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Requirements.Config}}
                <li>config:
                  {{range $i, $item := .Requirements.Config}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}}
                </li>
                {{end}}
                {{if .Requirements.OS}}
                <li>os:
                  {{range $i, $item := .Requirements.OS}}{{if $i}}, {{end}}{{$item}}{{end}}
                </li>
                {{end}}
              </ul>
            </div>
            {{end}}

            {{if .Install}}
            <div style="margin-top: 14px;">
              <strong>Suggested Installers</strong>
              <ul class="skill-list">
                {{range .Install}}
                <li>
                  <code>{{.Label}}</code>
                  {{if .Bins}}
                  <span class="subtle">
                    (provides {{range $i, $item := .Bins}}{{if $i}}, {{end}}<code>{{$item}}</code>{{end}})
                  </span>
                  {{end}}
                </li>
                {{end}}
              </ul>
            </div>
            {{end}}
          </div>
        </details>
        {{end}}
      </div>
      {{end}}
      {{else if not .Snapshot.Skills.Error}}
      <p class="empty">No skills discovered.</p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "prompts"}}
    {{template "promptsPage" .}}
    {{end}}

    {{if eq .View "identity"}}
    {{template "identityPage" .}}
    {{end}}

    {{if eq .View "personas"}}
    {{template "personasPage" .}}
    {{end}}

    {{if eq .View "chats"}}
    {{template "chatsPage" .}}
    {{end}}

    {{if eq .View "memory"}}
    <section class="panels">
      <article class="card">
        <h2>Memory Backend</h2>
        <dl class="meta">
          <dt>Backend</dt>
          <dd>{{if .Snapshot.Memory.Backend}}{{.Snapshot.Memory.Backend}}{{else}}-{{end}}</dd>
          <dt>Storage Mode</dt>
          <dd>
            {{if eq .Snapshot.Memory.Backend "file"}}
              {{if .Snapshot.Memory.FileEnabled}}
                File-backed <code>MEMORY.md</code>
              {{else}}
                file backend not configured
              {{end}}
            {{else if .Snapshot.Memory.FileEnabled}}
              File-backed <code>MEMORY.md</code>
            {{else if .Snapshot.Memory.Enabled}}
              Structured memory service
            {{else}}
              unavailable
            {{end}}
          </dd>
          <dt>Structured Memory</dt>
          <dd>
            {{if eq .Snapshot.Memory.Backend "file"}}
              not used by file backend
            {{else if and .Snapshot.Memory.Enabled (not .Snapshot.Memory.FileEnabled)}}
              enabled
            {{else}}
              unavailable
            {{end}}
          </dd>
          <dt>File Inventory</dt>
          <dd>
            {{if eq .Snapshot.Memory.Backend "file"}}
              {{if .Snapshot.Memory.FileEnabled}}available{{else}}not configured{{end}}
            {{else}}
              not available
            {{end}}
          </dd>
          <dt>JSON</dt>
          <dd><a href="/api/memory/files">/api/memory/files</a></dd>
        </dl>
      </article>
      <article class="card">
        <h2>File Inventory</h2>
        <dl class="meta">
          <dt>Root</dt>
          <dd>
            {{if .Snapshot.Memory.Root}}
              <code>{{.Snapshot.Memory.Root}}</code>
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Files</dt>
          <dd>{{.Snapshot.Memory.FileCount}}</dd>
          <dt>Total Bytes</dt>
          <dd>{{.Snapshot.Memory.TotalBytes}}</dd>
          <dt>Last Modified</dt>
          <dd>{{formatTime .Snapshot.Memory.LastModified}}</dd>
        </dl>
      </article>
    </section>

    <section class="card" style="margin-top: 24px;">
      <h2>Memory Files</h2>
      <p class="subtle">
        File-backed memory stores one visible <code>MEMORY.md</code> per
        app/user scope. Use this inventory to inspect what durable memory the
        runtime can inject into future turns.
      </p>
      {{if .Snapshot.Memory.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Snapshot.Memory.Error}}
      </div>
      {{end}}
      {{if .Snapshot.Memory.Files}}
      <div class="memory-controls" data-memory-root>
        <div class="memory-filter-head">
          <label for="memory-search">Search memory</label>
          <span class="memory-shown">
            <span data-memory-shown>{{.Snapshot.Memory.FileCount}}</span> shown
          </span>
        </div>
        <div class="memory-filter-grid">
          <div class="memory-filter-field">
            <input
              id="memory-search"
              type="search"
              placeholder="Search app, user, path, or preview"
              data-memory-search
            >
          </div>
        </div>
      </div>
      <div class="memory-list">
        {{range .Snapshot.Memory.Files}}
        <details
          class="card prompt-detail"
          id="{{.CardID}}"
          data-memory-card
          data-memory-path="{{.RelativePath}}"
          data-memory-load-url="{{.LoadURL}}"
          data-memory-search="{{.SearchValue}}"
        >
          <summary>
            <div class="memory-card-head">
              <div>
                <div class="memory-scope">
                  <span>app <code>{{.AppName}}</code></span>
                  <span>
                    user <code>{{.UserID}}</code>
                    {{if .UserLabel}}
                    <span class="memory-user-label">{{.UserLabel}}</span>
                    {{end}}
                  </span>
                </div>
                <div class="memory-path">
                  <code>{{.RelativePath}}</code>
                </div>
                <p class="subtle prompt-detail-hint">
                  {{if .Preview}}
                    {{.Preview}}
                  {{else}}
                    No visible memory content yet.
                  {{end}}
                </p>
              </div>
              <div class="memory-card-meta">
                <span>{{.SizeBytes}} bytes</span>
                <span>{{formatTime .ModifiedAt}}</span>
              </div>
            </div>
          </summary>
          <div class="prompt-detail-body">
            <div class="memory-card-toolbar">
              <div class="subtle">
                Edit the full <code>MEMORY.md</code> file for this scope.
              </div>
              <a
                href="{{.OpenURL}}"
                target="_blank"
                rel="noopener noreferrer"
              >
                Open Raw File
              </a>
            </div>
            <form
              class="memory-editor-form"
              method="post"
              action="/api/memory/file"
              data-memory-form
            >
              <input type="hidden" name="path" value="{{.RelativePath}}">
              <input type="hidden" name="return_path" value="/memory">
              <input type="hidden" name="return_to" value="{{.CardID}}">
              <label for="editor-{{.CardID}}">
                Full file content
              </label>
              <textarea
                id="editor-{{.CardID}}"
                name="content"
                data-memory-editor
                placeholder="Open this scope to load MEMORY.md"
                disabled
              ></textarea>
              <div class="memory-editor-status" data-memory-status>
                Open this scope to load its full file.
              </div>
              <div
                class="notice err"
                style="margin-top: 12px;"
                data-memory-error
                hidden
              ></div>
              <div class="actions" style="margin-top: 12px;">
                <button type="submit" data-memory-save disabled>
                  Save File
                </button>
                <button
                  class="secondary"
                  type="button"
                  data-memory-reset
                  disabled
                >
                  Revert
                </button>
              </div>
            </form>
          </div>
        </details>
        {{end}}
      </div>
      <p class="empty" data-memory-empty hidden>No matching memory files.</p>
      {{else if not .Snapshot.Memory.Error}}
      <p class="empty">
        {{if eq .Snapshot.Memory.Backend "file"}}
          {{if .Snapshot.Memory.FileEnabled}}
            No file-backed memory files discovered yet.
          {{else}}
            File-backed memory store is not configured for this runtime.
          {{end}}
        {{else if .Snapshot.Memory.FileEnabled}}
          No file-backed memory files discovered yet.
        {{else}}
          File-backed memory inventory is only available when the runtime uses
          the <code>file</code> memory backend.
        {{end}}
      </p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "automation"}}
    <section class="panels">
      <article class="card">
        <h2>Automation</h2>
        {{if .Snapshot.Cron.Enabled}}
        <p class="subtle">
          Persisted jobs continue after gateway restarts. Use this page for
          scheduling, one-off runs, and cleanup.
        </p>
        <div class="actions">
          <form method="post" action="/api/cron/jobs/clear">
            <input type="hidden" name="return_path" value="/automation">
            <button
              class="warn"
              type="submit"
              onclick="return confirm('Clear all scheduled jobs?');"
            >
              Clear All Jobs
            </button>
          </form>
        </div>
        {{else}}
        <p class="empty">Scheduled jobs are not enabled for this runtime.</p>
        {{end}}
      </article>
    </section>
    <section class="card" style="margin-top: 24px;">
      <h2>Scheduled Jobs</h2>
      {{if .Snapshot.Cron.Jobs}}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Schedule</th>
            <th>Delivery</th>
            <th>Status</th>
            <th>Timing</th>
            <th>Task</th>
            <th>Last Output</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Cron.Jobs}}
          <tr>
            <td>
              <strong>{{.Name}}</strong><br>
              <code>{{.ID}}</code><br>
              owner {{.UserID}}
            </td>
            <td>{{.Schedule}}</td>
            <td>
              {{if .Channel}}
                {{.Channel}} → {{.Target}}
              {{else}}
                no delivery target
              {{end}}
            </td>
            <td>
              {{if .LastStatus}}{{.LastStatus}}{{else}}idle{{end}}
              {{if .LastError}}
                <br>
                <span class="subtle">{{.LastError}}</span>
              {{end}}
            </td>
            <td>
              next {{formatTime .NextRunAt}}<br>
              last {{formatTime .LastRunAt}}
            </td>
            <td>{{.MessagePreview}}</td>
            <td>{{if .LastOutput}}{{.LastOutput}}{{else}}-{{end}}</td>
            <td>
              <div class="actions">
                <form method="post" action="/api/cron/jobs/run">
                  <input type="hidden" name="job_id" value="{{.ID}}">
                  <input type="hidden" name="return_path" value="/automation">
                  <button type="submit">Run Now</button>
                </form>
                <form method="post" action="/api/cron/jobs/remove">
                  <input type="hidden" name="job_id" value="{{.ID}}">
                  <input type="hidden" name="return_path" value="/automation">
                  <button
                    class="secondary"
                    type="submit"
                    onclick="return confirm('Remove this job?');"
                  >
                    Remove
                  </button>
                </form>
              </div>
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No scheduled jobs.</p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "sessions"}}
    <section class="panels">
      <article class="card">
        <h2>Exec Surface</h2>
        <dl class="meta">
          <dt>Enabled</dt>
          <dd>{{.Snapshot.Exec.Enabled}}</dd>
          <dt>Sessions</dt>
          <dd>{{.Snapshot.Exec.SessionCount}}</dd>
          <dt>Running</dt>
          <dd>{{.Snapshot.Exec.RunningCount}}</dd>
          <dt>JSON</dt>
          <dd><a href="/api/exec/sessions">/api/exec/sessions</a></dd>
        </dl>
      </article>
      <article class="card">
        <h2>Uploads</h2>
        <dl class="meta">
          <dt>Enabled</dt>
          <dd>{{.Snapshot.Uploads.Enabled}}</dd>
          <dt>Root</dt>
          <dd><code>{{.Snapshot.Uploads.Root}}</code></dd>
          <dt>Files</dt>
          <dd>{{.Snapshot.Uploads.FileCount}}</dd>
          <dt>Total Bytes</dt>
          <dd>{{.Snapshot.Uploads.TotalBytes}}</dd>
        </dl>
      </article>
    </section>
    <section class="card" style="margin-top: 24px;">
      <h2>Exec Sessions</h2>
      {{if .Snapshot.Exec.Sessions}}
      <table>
        <thead>
          <tr>
            <th>Session</th>
            <th>Status</th>
            <th>Command</th>
            <th>Timing</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Exec.Sessions}}
          <tr>
            <td><code>{{.SessionID}}</code></td>
            <td>{{.Status}}{{if .ExitCode}}<br>exit {{.ExitCode}}{{end}}</td>
            <td><code>{{.Command}}</code></td>
            <td>
              started {{.StartedAt}}
              {{if .DoneAt}}<br>done {{.DoneAt}}{{end}}
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No exec sessions.</p>
      {{end}}
    </section>

    <section class="card" style="margin-top: 24px;">
      <h2>Upload Sessions</h2>
      {{if .Snapshot.Uploads.Sessions}}
      <table>
        <thead>
          <tr>
            <th>Channel</th>
            <th>User</th>
            <th>Session</th>
            <th>Files</th>
            <th>Total Bytes</th>
            <th>Last Modified</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Uploads.Sessions}}
          <tr>
            <td>{{.Channel}}</td>
            <td><code>{{.UserID}}</code></td>
            <td>
              <a href="/api/uploads?session_id={{urlquery .SessionID}}">
                <code>{{.SessionID}}</code>
              </a>
            </td>
            <td>{{.FileCount}}</td>
            <td>{{.TotalBytes}}</td>
            <td>{{formatTime .LastModified}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No upload sessions indexed yet.</p>
      {{end}}
    </section>

    <section class="card" style="margin-top: 24px;">
      <h2>Recent Uploads</h2>
      {{if .Snapshot.Uploads.Files}}
      <table>
        <thead>
          <tr>
            <th>Channel</th>
            <th>User</th>
            <th>Session</th>
            <th>Name</th>
            <th>Kind</th>
            <th>MIME</th>
            <th>Source</th>
            <th>Preview</th>
            <th>Relative Path</th>
            <th>Size</th>
            <th>Modified</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Uploads.Files}}
          <tr>
            <td>{{.Channel}}</td>
            <td><code>{{.UserID}}</code></td>
            <td>
              <a href="/api/uploads?session_id={{urlquery .SessionID}}">
                <code>{{.SessionID}}</code>
              </a>
            </td>
            <td>
              <a href="{{.OpenURL}}" target="_blank"
                rel="noopener noreferrer">{{.Name}}</a>
              <br>
              <a href="{{.DownloadURL}}">download</a>
            </td>
            <td>
              <a href="/api/uploads?kind={{urlquery .Kind}}">
                {{.Kind}}
              </a>
            </td>
            <td>
              {{if .MimeType}}
              <a href="/api/uploads?mime_type={{urlquery .MimeType}}">
                <code>{{.MimeType}}</code>
              </a>
              {{else}}
              <span class="subtle">-</span>
              {{end}}
            </td>
            <td>
              {{if .Source}}
              <a href="/api/uploads?source={{urlquery .Source}}">
                {{.Source}}
              </a>
              {{else}}
              <span class="subtle">-</span>
              {{end}}
            </td>
            <td>
              <div class="preview-box">
                {{if eq .Kind "image"}}
                <a href="{{.OpenURL}}" target="_blank"
                  rel="noopener noreferrer">
                  <img src="{{.OpenURL}}" alt="{{.Name}}">
                </a>
                {{else if eq .Kind "audio"}}
                <audio controls preload="none" src="{{.OpenURL}}">
                  Your browser does not support audio preview.
                </audio>
                {{else if eq .Kind "video"}}
                <video controls preload="metadata" muted src="{{.OpenURL}}">
                  Your browser does not support video preview.
                </video>
                {{else if eq .Kind "pdf"}}
                <a href="{{.OpenURL}}" target="_blank"
                  rel="noopener noreferrer">open preview</a>
                {{else}}
                <span class="subtle">n/a</span>
                {{end}}
              </div>
            </td>
            <td><code>{{.RelativePath}}</code></td>
            <td>{{.SizeBytes}}</td>
            <td>{{formatTime .ModifiedAt}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No uploads indexed yet.</p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "debug"}}
    <section class="panels">
      <article class="card">
        <h2>Debug Index</h2>
        <dl class="meta">
          <dt>Indexed Dir</dt>
          <dd><code>{{.Snapshot.Debug.BySessionDir}}</code></dd>
          <dt>Session Count</dt>
          <dd>{{.Snapshot.Debug.SessionCount}}</dd>
          <dt>Trace Count</dt>
          <dd>{{.Snapshot.Debug.TraceCount}}</dd>
          <dt>Status</dt>
          <dd>
            {{if .Snapshot.Debug.Error}}
              <span class="subtle">{{.Snapshot.Debug.Error}}</span>
            {{else if .Snapshot.Debug.Enabled}}
              ready
            {{else}}
              idle
            {{end}}
          </dd>
        </dl>
      </article>
      <article class="card">
        <h2>Langfuse</h2>
        <dl class="meta">
          <dt>Enabled</dt>
          <dd>{{.Snapshot.Langfuse.Enabled}}</dd>
          <dt>Ready</dt>
          <dd>{{.Snapshot.Langfuse.Ready}}</dd>
          <dt>Status</dt>
          <dd>
            {{if .Snapshot.Langfuse.Error}}
              <span class="subtle">{{.Snapshot.Langfuse.Error}}</span>
            {{else if .Snapshot.Langfuse.Ready}}
              ready
            {{else if .Snapshot.Langfuse.Enabled}}
              starting
            {{else}}
              idle
            {{end}}
          </dd>
        </dl>
      </article>
    </section>
    <section class="card" style="margin-top: 24px;">
      <h2>Debug Sessions</h2>
      {{if .Snapshot.Debug.Sessions}}
      <table>
        <thead>
          <tr>
            <th>Session</th>
            <th>Trace Count</th>
            <th>Last Seen</th>
            <th>Latest Trace</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Debug.Sessions}}
          <tr>
            <td><code>{{.SessionID}}</code></td>
            <td>{{.TraceCount}}</td>
            <td>
              {{formatTime .LastTraceAt}}<br>
              {{if .Channel}}{{.Channel}}{{end}}
              {{if .RequestID}}
                <br>
                <span class="subtle">{{.RequestID}}</span>
              {{end}}
              {{if .TraceID}}
                <br>
                <span class="subtle">trace {{.TraceID}}</span>
              {{end}}
            </td>
            <td>
              {{if .LangfuseURL}}
                <a href="{{.LangfuseURL}}" target="_blank"
                  rel="noopener noreferrer">langfuse</a> ·
              {{end}}
              {{if .MetaURL}}
                <a href="{{.MetaURL}}" target="_blank">meta</a>
              {{end}}
              {{if .EventsURL}}
                · <a href="{{.EventsURL}}" target="_blank">events</a>
              {{end}}
              {{if .ResultURL}}
                · <a href="{{.ResultURL}}" target="_blank">result</a>
              {{end}}
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No debug sessions indexed yet.</p>
      {{end}}
    </section>

    <section class="card" style="margin-top: 24px;">
      <h2>Recent Traces</h2>
      {{if .Snapshot.Debug.RecentTraces}}
      <table>
        <thead>
          <tr>
            <th>Session</th>
            <th>Started</th>
            <th>Request</th>
            <th>Files</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Debug.RecentTraces}}
          <tr>
            <td><code>{{.SessionID}}</code></td>
            <td>{{formatTime .StartedAt}}</td>
            <td>
              {{if .Channel}}{{.Channel}}{{else}}-{{end}}
              {{if .RequestID}}
                <br>
                <span class="subtle">{{.RequestID}}</span>
              {{end}}
              {{if .MessageID}}
                <br>
                <span class="subtle">msg {{.MessageID}}</span>
              {{end}}
              {{if .TraceID}}
                <br>
                <span class="subtle">trace {{.TraceID}}</span>
              {{end}}
            </td>
            <td>
              {{if .LangfuseURL}}
                <a href="{{.LangfuseURL}}" target="_blank"
                  rel="noopener noreferrer">langfuse</a> ·
              {{end}}
              {{if .MetaURL}}
                <a href="{{.MetaURL}}" target="_blank">meta</a>
              {{end}}
              {{if .EventsURL}}
                · <a href="{{.EventsURL}}" target="_blank">events</a>
              {{end}}
              {{if .ResultURL}}
                · <a href="{{.ResultURL}}" target="_blank">result</a>
              {{end}}
              {{if .TracePath}}
                <br>
                <span class="subtle">
                  <code>{{.TracePath}}</code>
                </span>
              {{end}}
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No recent traces.</p>
      {{end}}
    </section>
    {{end}}

    {{if eq .View "browser"}}
    <section class="card" style="margin-top: 24px;">
      <h2>Browser Surface</h2>
      <p class="subtle">
        Native browser tool wiring, including host browser-server routing,
        sandbox targets, node targets, and profile inventory.
      </p>
      <dl class="meta">
        <dt>Enabled</dt>
        <dd>{{.Snapshot.Browser.Enabled}}</dd>
        <dt>Providers</dt>
        <dd>{{.Snapshot.Browser.ProviderCount}}</dd>
        <dt>Profiles</dt>
        <dd>{{.Snapshot.Browser.ProfileCount}}</dd>
        <dt>Nodes</dt>
        <dd>{{.Snapshot.Browser.NodeCount}}</dd>
        <dt>Status</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.Enabled}}
            {{if .Snapshot.Browser.Managed.State}}
              {{.Snapshot.Browser.Managed.State}}
            {{else}}
              configured
            {{end}}
          {{else if .Snapshot.Browser.Enabled}}
            ready
          {{else}}
            idle
          {{end}}
        </dd>
      </dl>
      {{if .Snapshot.Browser.Managed.Enabled}}
      <h3 style="margin: 16px 0 8px;">Local browser-server</h3>
      <dl class="meta">
        <dt>Managed</dt>
        <dd>{{.Snapshot.Browser.Managed.Managed}}</dd>
        <dt>URL</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.URL}}
            <code>{{.Snapshot.Browser.Managed.URL}}</code>
          {{else}}
            -
          {{end}}
        </dd>
        <dt>PID</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.PID}}
            {{.Snapshot.Browser.Managed.PID}}
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Work Dir</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.WorkDir}}
            <code>{{.Snapshot.Browser.Managed.WorkDir}}</code>
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Command</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.Command}}
            <code>{{.Snapshot.Browser.Managed.Command}}</code>
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Log</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.LogURL}}
            <a
              href="{{.Snapshot.Browser.Managed.LogURL}}"
              target="_blank"
              rel="noopener noreferrer"
            >
              open log
            </a>
            <br><code>{{.Snapshot.Browser.Managed.LogPath}}</code>
          {{else if .Snapshot.Browser.Managed.LogPath}}
            <code>{{.Snapshot.Browser.Managed.LogPath}}</code>
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Started</dt>
        <dd>{{formatTime .Snapshot.Browser.Managed.StartedAt}}</dd>
        <dt>Stopped</dt>
        <dd>{{formatTime .Snapshot.Browser.Managed.StoppedAt}}</dd>
        <dt>Exit</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.ExitCode}}
            {{.Snapshot.Browser.Managed.ExitCode}}
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Error</dt>
        <dd>
          {{if .Snapshot.Browser.Managed.LastError}}
            {{.Snapshot.Browser.Managed.LastError}}
          {{else}}
            -
          {{end}}
        </dd>
      </dl>
      {{if .Snapshot.Browser.Managed.RecentLogs}}
      <pre
        style="margin-top: 12px; white-space: pre-wrap;"
      >{{range .Snapshot.Browser.Managed.RecentLogs}}
{{.}}
{{end}}</pre>
      {{end}}
      {{end}}
      {{if .Snapshot.Browser.Providers}}
      <table>
        <thead>
          <tr>
            <th>Provider</th>
            <th>Default</th>
            <th>Host</th>
            <th>Sandbox</th>
            <th>Guards</th>
            <th>Profiles</th>
            <th>Nodes</th>
          </tr>
        </thead>
        <tbody>
          {{range .Snapshot.Browser.Providers}}
          <tr>
            <td>{{if .Name}}{{.Name}}{{else}}browser{{end}}</td>
            <td>
              {{if .DefaultProfile}}
                {{.DefaultProfile}}
              {{else}}
                -
              {{end}}
            </td>
            <td>
              {{if .Host.URL}}
                <code>{{.Host.URL}}</code><br>
                <span class="subtle">{{browserEndpointSummary .Host}}</span>
              {{else}}-{{end}}
            </td>
            <td>
              {{if .Sandbox.URL}}
                <code>{{.Sandbox.URL}}</code><br>
                <span class="subtle">
                  {{browserEndpointSummary .Sandbox}}
                </span>
              {{else}}-{{end}}
            </td>
            <td>
              loopback={{.AllowLoopback}},
              private={{.AllowPrivateNet}},
              file={{.AllowFileURLs}}
            </td>
            <td>
              {{if .Profiles}}
                {{range $i, $profile := .Profiles}}
                  {{if $i}}, {{end}}{{$profile.Name}}
                {{end}}
              {{else}}-{{end}}
            </td>
            <td>
              {{if .Nodes}}
                {{range $i, $node := .Nodes}}
                  {{if $i}}<br>{{end}}{{$node.ID}}
                  {{if $node.Status.URL}}
                    <br>
                    <span class="subtle">
                      {{browserEndpointSummary $node.Status}}
                    </span>
                  {{end}}
                {{end}}
              {{else}}-{{end}}
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">Browser tool is not configured for this runtime.</p>
      {{end}}
    </section>
    {{end}}
  <script>
    const resolveRequestURL = (reference) => {
      const trimmed = typeof reference === "string"
        ? reference.trim()
        : "";
      if (!trimmed) {
        return null;
      }
      try {
        return new URL(trimmed, window.location.href);
      } catch (_) {
        return null;
      }
    };

    (function () {
      const root = document.querySelector("[data-page-stale-root]");
      if (!root) return;

      const stateURL = resolveRequestURL(
        root.getAttribute("data-page-state-path") || ""
      );
      const initialToken =
        root.getAttribute("data-page-state-token") || "";
      const intervalValue = Number(
        root.getAttribute("data-page-refresh-interval") || "0"
      );
      if (!stateURL || !initialToken || intervalValue <= 0) {
        return;
      }

      let currentToken = initialToken;
      let stopped = false;

      const poll = async () => {
        if (stopped || document.hidden) {
          return;
        }
        try {
          const response = await fetch(stateURL.toString(), {
            headers: { Accept: "application/json" },
          });
          if (!response.ok) {
            return;
          }
          const payload = await response.json();
          const nextToken =
            payload && typeof payload.token === "string"
              ? payload.token
              : "";
          if (!nextToken || nextToken === currentToken) {
            return;
          }
          root.hidden = false;
          stopped = true;
        } catch (_) {}
      };

      window.setInterval(poll, intervalValue * 1000);
    })();

    (function () {
      const root = document.querySelector("[data-skills-root]");
      if (!root) return;

      const search = root.querySelector("[data-skills-filter]");
      const shown = root.querySelector("[data-skills-shown]");
      const tabs = Array.from(root.querySelectorAll("[data-skill-tab]"));
      const cards = Array.from(root.querySelectorAll("[data-skill-card]"));
      const groups = Array.from(root.querySelectorAll("[data-skills-group]"));
      const inlineToggleForms = Array.from(root.querySelectorAll("[data-skill-inline-toggle]"));
      const inlineToggleButtons = Array.from(root.querySelectorAll("[data-skill-toggle-button]"));
      const scrollRestoreKey = "openclaw-admin-skills-scroll";
      let active = "all";

      const saveScrollRestore = (form) => {
        if (!form || !window.sessionStorage) return;
        const button = form.querySelector("[data-skill-toggle-button]");
        const card = form.closest("[data-skill-card]");
        const returnField = form.querySelector('input[name="return_to"]');
        const payload = {
          path: window.location.pathname,
          configKey: form.getAttribute("data-skill-config") || "",
          cardId: card ? card.id : "",
          viewportTop: button ? button.getBoundingClientRect().top : 0,
          scrollY: window.scrollY || window.pageYOffset || 0,
          active,
          searchValue: search ? search.value : "",
          ts: Date.now()
        };
        try {
          window.sessionStorage.setItem(
            scrollRestoreKey,
            JSON.stringify(payload)
          );
        } catch (_) {}
        if (returnField) {
          returnField.value = "";
        }
      };

      const restoreScrollPosition = () => {
        if (!window.sessionStorage) return;
        let raw = "";
        try {
          raw = window.sessionStorage.getItem(scrollRestoreKey) || "";
        } catch (_) {
          return;
        }
        if (!raw) return;

        let payload = null;
        try {
          payload = JSON.parse(raw);
        } catch (_) {
          payload = null;
        }
        try {
          window.sessionStorage.removeItem(scrollRestoreKey);
        } catch (_) {}
        if (!payload || payload.path !== window.location.pathname) {
          return;
        }
        if (typeof payload.searchValue === "string" && search) {
          search.value = payload.searchValue;
        }
        if (typeof payload.active === "string" && payload.active) {
          active = payload.active;
        }
        refresh();

        const apply = () => {
          const savedY = Number(payload.scrollY);
          if (Number.isFinite(savedY)) {
            window.scrollTo(0, savedY);
          }

          let target = null;
          if (payload.configKey) {
            target = root.querySelector(
              '[data-skill-switch="' + payload.configKey + '"]'
            );
          }
          if (!target && payload.cardId) {
            target = document.getElementById(payload.cardId);
          }
          const savedTop = Number(payload.viewportTop);
          if (!target || !Number.isFinite(savedTop)) {
            return;
          }
          const rect = target.getBoundingClientRect();
          if (!rect || rect.height === 0) {
            return;
          }
          window.scrollBy(0, rect.top - savedTop);
        };

        window.requestAnimationFrame(() => {
          window.requestAnimationFrame(apply);
        });
      };

      const matches = (card) => {
        const status = card.getAttribute("data-skill-status") || "";
        if (active !== "all" && status !== active) return false;
        const needle = (search && search.value ? search.value : "").trim().toLowerCase();
        if (!needle) return true;
        const haystack = (card.getAttribute("data-skill-search") || "").toLowerCase();
        return haystack.indexOf(needle) >= 0;
      };

      const refresh = () => {
        let visibleCount = 0;
        cards.forEach((card) => {
          const visible = matches(card);
          card.hidden = !visible;
          if (visible) visibleCount += 1;
        });
        groups.forEach((group) => {
          const visibleCards = group.querySelectorAll("[data-skill-card]:not([hidden])");
          group.hidden = visibleCards.length === 0;
        });
        if (shown) shown.textContent = String(visibleCount);
        tabs.forEach((tab) => {
          tab.classList.toggle("active", (tab.getAttribute("data-skill-tab") || "") === active);
        });
      };

      tabs.forEach((tab) => {
        tab.addEventListener("click", () => {
          active = tab.getAttribute("data-skill-tab") || "all";
          refresh();
        });
      });
      if (search) {
        search.addEventListener("input", refresh);
      }
      inlineToggleForms.forEach((form) => {
        form.addEventListener("click", (event) => {
          event.stopPropagation();
        });
        form.addEventListener("keydown", (event) => {
          event.stopPropagation();
        });
        form.addEventListener("submit", () => {
          saveScrollRestore(form);
        });
      });
      inlineToggleButtons.forEach((button) => {
        button.addEventListener("click", (event) => {
          event.preventDefault();
          event.stopPropagation();
          const form = button.form;
          if (!form) return;
          if (typeof form.requestSubmit === "function") {
            form.requestSubmit();
            return;
          }
          form.submit();
        });
      });
      refresh();
      restoreScrollPosition();
    })();

    (function () {
      const revealHashDetails = () => {
        if (!window.location.hash) {
          return;
        }
        const target = document.querySelector(window.location.hash);
        if (!target) {
          return;
        }
        const disclosure = target.matches("details")
          ? target
          : target.closest("details");
        if (!disclosure) {
          return;
        }
        disclosure.open = true;
      };

      window.addEventListener("hashchange", revealHashDetails);
      revealHashDetails();
    })();

    (function () {
      const roots = Array.from(
        document.querySelectorAll("[data-chat-history-root]")
      );
      if (!roots.length) return;

      const itemKindSession = "session";
      const itemKindTurn = "turn";
      const fallbackHistoryError = "Unable to load chat history right now.";
      const fallbackHistoryEmpty =
        "No recent chat transcript is available in this runtime " +
        "for this chat right now.";
      const historyLoadingText = "Loading recent messages...";
      const historyMoreText = "Load older messages";
      const historyBoundedText =
        "This admin view only loads the most recent tracked " +
        "session lines for this chat.";

      const formatDateTime = (value) => {
        if (!value) return "";
        const parsed = new Date(value);
        if (Number.isNaN(parsed.getTime())) return value;
        return parsed.toLocaleString(undefined, {
          year: "numeric",
          month: "2-digit",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
          hour12: false,
          timeZoneName: "short",
        });
      };

      const speakerLabel = (item) => {
        if (item && typeof item.speaker === "string" && item.speaker.trim()) {
          return item.speaker.trim();
        }
        const role = item && typeof item.role === "string"
          ? item.role.trim()
          : "";
        switch (role) {
          case "user":
            return "User";
          case "assistant":
            return "Assistant";
          case "system":
            return "System";
          default:
            return "Turn";
        }
      };

      const setText = (node, text) => {
        if (!node) return;
        node.textContent = text;
      };

      const createDiv = (className, text) => {
        const node = document.createElement("div");
        if (className) {
          node.className = className;
        }
        if (typeof text === "string") {
          node.textContent = text;
        }
        return node;
      };

      const updateMeta = (root, page) => {
        const meta = root.querySelector("[data-chat-history-meta]");
        if (!meta || !page) return;
        const loaded = Number(root.getAttribute("data-chat-history-loaded") || "0");
        const total = Number(page.turn_count || 0);
        const sessions = Number(page.session_line_count || 0);
        if (total <= 0) {
          meta.textContent = fallbackHistoryEmpty;
          return;
        }
        const parts = [];
        parts.push(
          "Showing " + String(loaded) + " of " + String(total) +
          " messages"
        );
        if (sessions > 0) {
          parts.push("from " + String(sessions) + " recent session lines");
        }
        meta.textContent = parts.join(" ");
      };

      const updateBoundedNote = (root, bounded) => {
        const note = root.querySelector("[data-chat-history-bounded]");
        if (!note) return;
        note.hidden = !bounded;
        if (bounded) {
          note.textContent = historyBoundedText;
        }
      };

      const updateMoreButton = (root, nextCursor) => {
        const toolbar = root.querySelector("[data-chat-history-toolbar]");
        const button = root.querySelector("[data-chat-history-more]");
        if (!toolbar || !button) return;
        const hasMore = typeof nextCursor === "string" && nextCursor !== "";
        toolbar.hidden = !hasMore;
        button.hidden = !hasMore;
        button.disabled = false;
        button.textContent = historyMoreText;
        if (hasMore) {
          button.setAttribute("data-chat-history-next", nextCursor);
        } else {
          button.removeAttribute("data-chat-history-next");
        }
      };

      const clearMessages = (root) => {
        const error = root.querySelector("[data-chat-history-error]");
        const empty = root.querySelector("[data-chat-history-empty]");
        if (error) {
          error.hidden = true;
          error.textContent = "";
        }
        if (empty) {
          empty.hidden = true;
          empty.textContent = "";
        }
      };

      const showError = (root, message) => {
        const error = root.querySelector("[data-chat-history-error]");
        const empty = root.querySelector("[data-chat-history-empty]");
        if (empty) {
          empty.hidden = true;
          empty.textContent = "";
        }
        if (!error) return;
        error.hidden = false;
        error.textContent = message || fallbackHistoryError;
      };

      const showEmpty = (root, message) => {
        const empty = root.querySelector("[data-chat-history-empty]");
        const error = root.querySelector("[data-chat-history-error]");
        if (error) {
          error.hidden = true;
          error.textContent = "";
        }
        if (!empty) return;
        empty.hidden = false;
        empty.textContent = message || fallbackHistoryEmpty;
      };

      const renderSessionItem = (item) => {
        const wrapper = createDiv("chat-timeline-session");
        const head = createDiv("chat-timeline-session-head");
        const copy = document.createElement("div");
        const title = createDiv(
          "chat-timeline-session-label",
          item.session_label || "Recent session"
        );
        const meta = createDiv("subtle chat-timeline-session-meta");
        const code = document.createElement("code");
        code.textContent = item.session_id || "";
        meta.appendChild(code);
        copy.appendChild(title);
        copy.appendChild(meta);
        head.appendChild(copy);
        if (item.last_activity) {
          head.appendChild(
            createDiv("subtle", formatDateTime(item.last_activity))
          );
        }
        wrapper.appendChild(head);
        return wrapper;
      };

      const renderTurnItem = (item) => {
        const article = document.createElement("article");
        article.className = "chat-turn";
        const head = createDiv("chat-turn-head");
        head.appendChild(
          createDiv("chat-turn-speaker", speakerLabel(item))
        );
        if (item.timestamp) {
          head.appendChild(
            createDiv("subtle", formatDateTime(item.timestamp))
          );
        }
        article.appendChild(head);
        if (typeof item.quote_text === "string" && item.quote_text.trim()) {
          article.appendChild(
            createDiv("chat-turn-quote", item.quote_text)
          );
        }
        article.appendChild(
          createDiv("chat-turn-text", item.text || "")
        );
        return article;
      };

      const renderHistoryItem = (item) => {
        if (!item || typeof item.kind !== "string") {
          return null;
        }
        if (item.kind === itemKindSession) {
          return renderSessionItem(item);
        }
        if (item.kind === itemKindTurn) {
          return renderTurnItem(item);
        }
        return null;
      };

      const updateLoading = (root, loading) => {
        root.setAttribute(
          "data-chat-history-loading",
          loading ? "true" : "false"
        );
        const button = root.querySelector("[data-chat-history-more]");
        if (!button) return;
        button.disabled = loading;
        if (loading) {
          button.textContent = historyLoadingText;
        } else if (button.hidden !== true) {
          button.textContent = historyMoreText;
        }
      };

      const fetchHistory = async (root, cursor) => {
        const chatID = root.getAttribute("data-chat-id") || "";
        const url = resolveRequestURL(
          root.getAttribute("data-chat-history-path") || ""
        );
        if (!url) {
          throw new Error(fallbackHistoryError);
        }
        url.searchParams.set("chat_id", chatID);
        if (cursor) {
          url.searchParams.set("cursor", cursor);
        }
        const response = await fetch(url.toString(), {
          headers: { Accept: "application/json" },
        });
        if (!response.ok) {
          const text = (await response.text()).trim();
          throw new Error(text || fallbackHistoryError);
        }
        return response.json();
      };

      const loadHistory = async (root, cursor) => {
        const items = root.querySelector("[data-chat-history-items]");
        if (!items) return;
        if (root.getAttribute("data-chat-history-loading") === "true") {
          return;
        }
        const prepend = typeof cursor === "string" && cursor !== "";
        const anchor = prepend ? items.firstElementChild : null;
        const anchorTop = anchor ? anchor.getBoundingClientRect().top : 0;
        updateLoading(root, true);
        clearMessages(root);
        try {
          const page = await fetchHistory(root, cursor);
          const fragment = document.createDocumentFragment();
          const pageItems = Array.isArray(page.items) ? page.items : [];
          pageItems.forEach((item) => {
            const node = renderHistoryItem(item);
            if (node) {
              fragment.appendChild(node);
            }
          });
          if (!prepend) {
            items.innerHTML = "";
          }
          if (prepend) {
            items.prepend(fragment);
          } else {
            items.appendChild(fragment);
          }

          const loaded = Number(
            root.getAttribute("data-chat-history-loaded") || "0"
          ) + Number(page.returned_turn_count || 0);
          root.setAttribute(
            "data-chat-history-loaded",
            String(loaded)
          );
          updateMeta(root, page);
          updateBoundedNote(root, Boolean(page.bounded));
          updateMoreButton(root, page.next_cursor || "");

          if (!pageItems.length && Number(page.turn_count || 0) === 0) {
            showEmpty(root, fallbackHistoryEmpty);
          }
          if (prepend && anchor) {
            window.scrollBy(
              0,
              anchor.getBoundingClientRect().top - anchorTop
            );
          }
          root.setAttribute("data-chat-history-loaded-once", "true");
        } catch (err) {
          showError(
            root,
            err && typeof err.message === "string"
              ? err.message
              : fallbackHistoryError
          );
        } finally {
          updateLoading(root, false);
        }
      };

      roots.forEach((root) => {
        const disclosure = root.closest("details");
        const button = root.querySelector("[data-chat-history-more]");
        if (button) {
          button.addEventListener("click", () => {
            const nextCursor =
              button.getAttribute("data-chat-history-next") || "";
            if (!nextCursor) return;
            loadHistory(root, nextCursor);
          });
        }
        const ensureLoaded = () => {
          if (
            root.getAttribute("data-chat-history-loaded-once") ===
            "true"
          ) {
            return;
          }
          loadHistory(root, "");
        };
        if (disclosure) {
          disclosure.addEventListener("toggle", () => {
            if (disclosure.open) {
              ensureLoaded();
            }
          });
          if (disclosure.open) {
            ensureLoaded();
          }
          return;
        }
        ensureLoaded();
      });
    })();

    (function () {
      const root = document.querySelector("[data-memory-root]");
      if (!root) return;

      const search = root.querySelector("[data-memory-search]");
      const shown = root.querySelector("[data-memory-shown]");
      const empty = document.querySelector("[data-memory-empty]");
      const cards = Array.from(
        document.querySelectorAll("[data-memory-card]")
      );

      const readControls = (card) => ({
        editor: card.querySelector("[data-memory-editor]"),
        save: card.querySelector("[data-memory-save]"),
        reset: card.querySelector("[data-memory-reset]"),
        error: card.querySelector("[data-memory-error]"),
        status: card.querySelector("[data-memory-status]"),
      });

      const setStatus = (card, message, isError) => {
        const status = readControls(card).status;
        if (!status) return;
        status.textContent = message;
        status.classList.toggle("error", Boolean(isError));
      };

      const clearError = (card) => {
        const error = readControls(card).error;
        if (!error) return;
        error.hidden = true;
        error.textContent = "";
      };

      const showError = (card, message) => {
        const error = readControls(card).error;
        if (!error) return;
        error.hidden = false;
        error.textContent = message;
        setStatus(card, message, true);
      };

      const updateDirtyState = (card) => {
        const controls = readControls(card);
        const editor = controls.editor;
        if (!editor) return;
        const original = editor.dataset.original || "";
        const loaded = card.dataset.memoryLoaded === "true";
        const dirty = !editor.disabled && editor.value !== original;
        if (controls.save) {
          controls.save.disabled = editor.disabled || !dirty;
        }
        if (controls.reset) {
          controls.reset.disabled = editor.disabled || !dirty;
        }
        if (editor.disabled) {
          setStatus(
            card,
            "Open this scope to load its full file.",
            false
          );
          return;
        }
        if (dirty) {
          setStatus(card, "Unsaved changes.", false);
          return;
        }
        if (loaded) {
          setStatus(
            card,
            "Loaded from disk. Edit the file to enable save.",
            false
          );
        }
      };

      const loadCard = async (card) => {
        if (
          card.dataset.memoryLoaded === "true" ||
          card.dataset.memoryLoading === "true"
        ) {
          updateDirtyState(card);
          return;
        }
        const controls = readControls(card);
        const editor = controls.editor;
        card.dataset.memoryLoading = "true";
        clearError(card);
        setStatus(card, "Loading full file...", false);
        if (editor) {
          editor.disabled = true;
        }
        if (controls.save) {
          controls.save.disabled = true;
        }
        if (controls.reset) {
          controls.reset.disabled = true;
        }
        try {
          const url = resolveRequestURL(
            card.getAttribute("data-memory-load-url") || ""
          );
          if (!url) {
            throw new Error("memory file endpoint is unavailable");
          }
          const response = await fetch(url.toString(), {
            headers: { Accept: "application/json" },
          });
          if (!response.ok) {
            const text = (await response.text()).trim();
            throw new Error(text || "failed to load memory file");
          }
          const payload = await response.json();
          const content =
            typeof payload.content === "string" ? payload.content : "";
          if (editor) {
            editor.value = content;
            editor.dataset.original = content;
            editor.disabled = false;
          }
          card.dataset.memoryLoaded = "true";
          updateDirtyState(card);
        } catch (err) {
          showError(
            card,
            err && typeof err.message === "string"
              ? err.message
              : "failed to load memory file"
          );
        } finally {
          delete card.dataset.memoryLoading;
        }
      };

      const matches = (card) => {
        const needle =
          (search && search.value ? search.value : "")
            .trim()
            .toLowerCase();
        if (!needle) return true;
        const haystack = (
          card.getAttribute("data-memory-search") || ""
        ).toLowerCase();
        if (haystack.indexOf(needle) === -1) return false;
        return true;
      };

      const refresh = () => {
        let visibleCount = 0;
        cards.forEach((card) => {
          const visible = matches(card);
          card.hidden = !visible;
          if (visible) visibleCount += 1;
        });
        if (shown) shown.textContent = String(visibleCount);
        if (empty) empty.hidden = visibleCount !== 0;
      };

      cards.forEach((card) => {
        const disclosure = card.matches("details")
          ? card
          : card.closest("details");
        const controls = readControls(card);
        if (controls.editor) {
          controls.editor.addEventListener("input", () => {
            updateDirtyState(card);
          });
        }
        if (controls.reset) {
          controls.reset.addEventListener("click", () => {
            if (!controls.editor) return;
            controls.editor.value =
              controls.editor.dataset.original || "";
            updateDirtyState(card);
          });
        }
        if (disclosure) {
          disclosure.addEventListener("toggle", () => {
            if (disclosure.open) {
              loadCard(card);
            }
          });
          if (disclosure.open) {
            loadCard(card);
          }
        }
      });

      if (search) {
        search.addEventListener("input", refresh);
      }
      refresh();
    })();
  </script>
      </div>
    </main>
  </div>
</body>
</html>`
