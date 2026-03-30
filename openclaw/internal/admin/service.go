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
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	routeIndex = "/"

	routeStatusJSON        = "/api/status"
	routeSkillsJSON        = "/api/skills/status"
	routeSkillToggle       = "/api/skills/toggle"
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

	queryNotice    = "notice"
	queryError     = "error"
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
	formJobID      = "job_id"
	formSkillKey   = "skill_key"
	formSkillName  = "skill_name"
	formEnabled    = "enabled"
	formReturnTo   = "return_to"

	refreshSeconds = 15

	debugBySessionDir   = "by-session"
	debugMetaFileName   = "meta.json"
	debugEventsFileName = "events.jsonl"
	debugResultFileName = "result.json"

	maxDebugSessionRows = 12
	maxDebugTraceRows   = 18
	maxJobOutputRunes   = 120
	browserProbeTimeout = 1500 * time.Millisecond

	formatTimeLayout = "2006-01-02 15:04:05 MST"
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

	Channels      []string
	GatewayRoutes Routes
	Skills        SkillsStatusProvider
	Browser       BrowserConfig

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
	mux.HandleFunc(routeIndex, s.handleIndex)
	mux.HandleFunc(routeStatusJSON, s.handleStatusJSON)
	mux.HandleFunc(routeSkillsJSON, s.handleSkillsJSON)
	mux.HandleFunc(routeSkillToggle, s.handleToggleSkill)
	mux.HandleFunc(routeJobsJSON, s.handleJobsJSON)
	mux.HandleFunc(routeJobRun, s.handleRunJob)
	mux.HandleFunc(routeJobRemove, s.handleRemoveJob)
	mux.HandleFunc(routeJobsClear, s.handleClearJobs)
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
	Snapshot       snapshot
	Notice         string
	Error          string
	RefreshSeconds int
}

func (s *Service) Snapshot() snapshot {
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
		GatewayURL:     strings.TrimSpace(s.cfg.GatewayURL),
		AdminAddr:      strings.TrimSpace(s.cfg.AdminAddr),
		AdminURL:       strings.TrimSpace(s.cfg.AdminURL),
		AdminAutoPort:  s.cfg.AdminAutoPort,
		Langfuse:       normalizeLangfuseStatus(s.cfg.Langfuse),
		StateDir:       strings.TrimSpace(s.cfg.StateDir),
		DebugDir:       strings.TrimSpace(s.cfg.DebugDir),
		Routes:         s.cfg.GatewayRoutes,
		Skills:         s.skillsStatus(),
		Browser:        s.browserStatus(),
		Exec:           s.execStatus(),
		Uploads:        s.uploadsStatus(),
		Debug:          s.debugStatus(),
	}

	if len(s.cfg.Channels) > 0 {
		out.Channels = append([]string(nil), s.cfg.Channels...)
		sort.Strings(out.Channels)
	}

	if s.cfg.Cron == nil {
		return out
	}

	status := s.cfg.Cron.Status()
	jobs := s.cfg.Cron.List()
	out.Cron = cronStatus{
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
		out.Cron.Jobs = append(out.Cron.Jobs, jobViewFromJob(job))
	}
	return out
}

func (s *Service) skillsStatus() skillsStatus {
	if s == nil || s.cfg.Skills == nil {
		return skillsStatus{}
	}

	report, err := s.cfg.Skills.SkillsStatus()
	if err != nil {
		return skillsStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}

	out := skillsStatus{
		Enabled:    true,
		Writable:   strings.TrimSpace(s.cfg.Skills.SkillsConfigPath()) != "",
		ConfigPath: strings.TrimSpace(s.cfg.Skills.SkillsConfigPath()),
		TotalCount: len(report.Skills),
	}

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

func (s *Service) handleIndex(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := pageData{
		Snapshot:       s.Snapshot(),
		Notice:         strings.TrimSpace(r.URL.Query().Get(queryNotice)),
		Error:          strings.TrimSpace(r.URL.Query().Get(queryError)),
		RefreshSeconds: refreshSeconds,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminPage.Execute(w, data); err != nil {
		http.Error(
			w,
			fmt.Sprintf("render admin page: %v", err),
			http.StatusInternalServerError,
		)
	}
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
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		fmt.Sprintf(
			"Saved %s as %s. Restart OpenClaw to apply runtime changes.",
			name,
			state,
		),
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
		Path:     routeIndex,
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

func (s *Service) handleJobsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.Snapshot().Cron.Jobs)
}

func (s *Service) handleExecSessionsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.Snapshot().Exec.Sessions)
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
	if strings.TrimSpace(tracePath) == "" {
		return "", fmt.Errorf("trace path is required")
	}
	if !isAllowedDebugFile(name) {
		return "", fmt.Errorf("unsupported debug file: %s", name)
	}

	candidate := filepath.Clean(filepath.Join(
		root,
		filepath.FromSlash(tracePath),
		name,
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
		return "", fmt.Errorf("debug file escapes debug root")
	}
	if _, err := os.Stat(absCandidate); err != nil {
		return "", fmt.Errorf("debug file not found")
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
		"formatTime":             formatTime,
		"browserEndpointSummary": browserEndpointSummary,
	}).Parse(adminPageHTML),
)

const adminPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta http-equiv="refresh" content="{{.RefreshSeconds}}">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>OpenClaw Admin</title>
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
      font-family: "Iowan Old Style", "Palatino Linotype", serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, #fff8ef, transparent 38%),
        linear-gradient(180deg, #efe7dc 0%, var(--bg) 100%);
    }
    main {
      max-width: 1180px;
      margin: 0 auto;
      padding: 32px 20px 40px;
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
    }
    .meta dd { margin: 0; }
    a { color: var(--accent); }
    code {
      background: rgba(15, 111, 97, 0.08);
      padding: 2px 6px;
      border-radius: 8px;
      word-break: break-all;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 12px;
    }
    th, td {
      text-align: left;
      vertical-align: top;
      padding: 12px 10px;
      border-top: 1px solid var(--line);
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
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 12px;
      margin-top: 16px;
    }
    .skills-controls input {
      flex: 1 1 260px;
      min-width: 220px;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 10px 14px;
      font: inherit;
      background: var(--panel-strong);
      color: var(--ink);
    }
    .skills-config-note {
      margin-top: 12px;
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
      padding: 14px 16px;
      margin-top: 12px;
    }
    .skill-card summary {
      list-style: none;
      cursor: pointer;
    }
    .skill-card summary::-webkit-details-marker {
      display: none;
    }
    .skill-title {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 14px;
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
      justify-content: flex-end;
      gap: 8px;
    }
    .skill-summary-side {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: flex-end;
      gap: 14px;
      flex: 0 0 auto;
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
    .skill-note {
      margin-top: 8px;
      color: var(--muted);
    }
    .skill-toggle-group {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      flex: 0 0 auto;
    }
    .skill-toggle-label {
      font-size: 12px;
      font-weight: 700;
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.06em;
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
      padding-top: 14px;
      border-top: 1px solid var(--line);
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
    @media (max-width: 760px) {
      h1 { font-size: 30px; }
      .meta { grid-template-columns: 1fr; }
      .skill-title {
        align-items: flex-start;
      }
      .skill-summary-side {
        width: 100%;
        justify-content: flex-start;
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
  <main>
    <h1>OpenClaw Admin</h1>
    <p class="subtle">
      Local control surface for the gateway runtime. This page is generic on
      purpose: it starts with system overview and scheduled job operations,
      and can grow into a wider management plane without going back through
      Telegram commands.
    </p>
    {{if .Notice}}<div class="notice ok">{{.Notice}}</div>{{end}}
    {{if .Error}}<div class="notice err">{{.Error}}</div>{{end}}

    <section class="stats">
      <article class="card">
        <span class="stat-label">Instance</span>
        <span class="stat-value">{{.Snapshot.InstanceID}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Gateway</span>
        <span class="stat-value">{{.Snapshot.GatewayAddr}}</span>
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
          <dd>{{.Snapshot.AppName}}</dd>
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
          Jump to <a href="#skills-admin">Skills Inventory</a>.
        </p>
      </article>

      <article class="card">
        <h2>Debug Index</h2>
        <p class="subtle">
          Session-indexed trace browsing for recent gateway activity. This is
          especially useful when a Telegram or cron flow behaves strangely and
          you want the exact recorded request and event stream before jumping
          into a full trace backend.
        </p>
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
        <p class="subtle">
          OpenTelemetry spans are pushed to Langfuse when the exporter starts
          successfully. The admin surface keeps local request indexes and uses
          trace links only as a drill-down path.
        </p>
        <dl class="meta">
          <dt>Enabled</dt>
          <dd>{{.Snapshot.Langfuse.Enabled}}</dd>
          <dt>Ready</dt>
          <dd>{{.Snapshot.Langfuse.Ready}}</dd>
          <dt>UI</dt>
          <dd>
            {{if .Snapshot.Langfuse.UIBaseURL}}
              <a href="{{.Snapshot.Langfuse.UIBaseURL}}" target="_blank"
                rel="noopener noreferrer">{{.Snapshot.Langfuse.UIBaseURL}}</a>
            {{else}}-{{end}}
          </dd>
          <dt>Trace Links</dt>
          <dd>
            {{if .Snapshot.Langfuse.TraceURLTemplate}}configured{{else}}-{{end}}
          </dd>
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

      <article class="card">
        <h2>Exec Surface</h2>
        <p class="subtle">
          Live view of host <code>exec_command</code> sessions. This makes it
          easier to understand long-running interactive jobs without digging
          through runner logs first.
        </p>
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
        <p class="subtle">
          Recent persisted chat uploads. This helps debug multi-turn file,
          PDF, audio, and video workflows without exposing host paths in
          the user conversation.
        </p>
        <dl class="meta">
          <dt>Enabled</dt>
          <dd>{{.Snapshot.Uploads.Enabled}}</dd>
          <dt>Root</dt>
          <dd><code>{{.Snapshot.Uploads.Root}}</code></dd>
          <dt>Files</dt>
          <dd>{{.Snapshot.Uploads.FileCount}}</dd>
          <dt>Total Bytes</dt>
          <dd>{{.Snapshot.Uploads.TotalBytes}}</dd>
          <dt>By Kind</dt>
          <dd>
            {{if .Snapshot.Uploads.KindCounts}}
              {{range $i, $item := .Snapshot.Uploads.KindCounts}}
                {{if $i}}, {{end}}{{$item.Kind}} {{$item.Count}}
              {{end}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>By Source</dt>
          <dd>
            {{if .Snapshot.Uploads.SourceCounts}}
              {{range $i, $item := .Snapshot.Uploads.SourceCounts}}
                {{if $i}}, {{end}}{{$item.Source}} {{$item.Count}}
              {{end}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Status</dt>
          <dd>
            {{if .Snapshot.Uploads.Error}}
              <span class="subtle">{{.Snapshot.Uploads.Error}}</span>
            {{else if .Snapshot.Uploads.Enabled}}
              ready
            {{else}}
              idle
            {{end}}
          </dd>
        </dl>
      </article>

      <article class="card">
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
      </article>
    </section>

    <section class="card" style="margin-top: 24px;" id="skills-admin" data-skills-root>
      <h2>Skills Inventory</h2>
      <p class="subtle">
        Inventory of bundled, local, project, and external skills discovered
        by this runtime. Use it to see which skills are ready, which are
        blocked or disabled, and which requirement is still missing before
        they can run.
      </p>
      {{if .Snapshot.Skills.Writable}}
      <p class="subtle skills-config-note">
        Enable and disable writes are persisted to
        <code>{{.Snapshot.Skills.ConfigPath}}</code>. Restart OpenClaw after
        saving to apply runtime changes.
      </p>
      {{else}}
      <p class="subtle skills-config-note">
        This runtime was started without a writable config file, so
        enable/disable is unavailable here.
      </p>
      {{end}}
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
        <input
          type="search"
          placeholder="Search skills by name, path, key, env, or reason"
          data-skills-filter
        >
        <span class="subtle"><span data-skills-shown>{{.Snapshot.Skills.TotalCount}}</span> shown</span>
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
            <div class="skill-title">
              <div>
                <div class="skill-name">
                  <span class="skill-dot {{.Status}}"></span>
                  {{if .Emoji}}<span>{{.Emoji}}</span>{{end}}
                  <span>{{.Name}}</span>
                </div>
                <div class="skill-note">{{.Description}}</div>
                {{if .Reason}}
                <div class="skill-note">Reason: {{.Reason}}</div>
                {{end}}
              </div>
              <div class="skill-summary-side">
                <div class="skill-badges">
                  <span class="skill-badge status {{.Status}}">
                    {{if eq .Status "ready"}}Ready{{else if eq .Status "disabled"}}Disabled{{else}}Setup Required{{end}}
                  </span>
                  {{if .Bundled}}<span class="skill-badge">bundled</span>{{end}}
                  {{if .BlockedByAllowlist}}<span class="skill-badge">allowlist</span>{{end}}
                  {{if .Always}}<span class="skill-badge">always</span>{{end}}
                  {{if .PrimaryEnv}}<span class="skill-badge">{{.PrimaryEnv}}</span>{{end}}
                </div>
                {{if $.Snapshot.Skills.Writable}}
                <div class="skill-toggle-group">
                  <span class="skill-toggle-label">Enabled</span>
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
                {{if $.Snapshot.Skills.Writable}}
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
      {{else}}
      <p class="empty">No skills discovered.</p>
      {{end}}
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
                  <button type="submit">Run Now</button>
                </form>
                <form method="post" action="/api/cron/jobs/remove">
                  <input type="hidden" name="job_id" value="{{.ID}}">
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
  </main>
  <script>
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
  </script>
</body>
</html>`
