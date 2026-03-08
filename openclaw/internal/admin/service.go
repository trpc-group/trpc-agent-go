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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	routeIndex = "/"

	routeStatusJSON        = "/api/status"
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

	refreshSeconds = 15

	debugBySessionDir   = "by-session"
	debugMetaFileName   = "meta.json"
	debugEventsFileName = "events.jsonl"
	debugResultFileName = "result.json"

	maxDebugSessionRows = 12
	maxDebugTraceRows   = 18
	maxJobOutputRunes   = 120

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

	StateDir string
	DebugDir string

	Channels      []string
	GatewayRoutes Routes

	Cron *cron.Service
	Exec *octool.Manager
}

type Service struct {
	cfg Config
	now func() time.Time
}

type Option func(*Service)

func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if s != nil && fn != nil {
			s.now = fn
		}
	}
}

func New(cfg Config, opts ...Option) *Service {
	svc := &Service{
		cfg: cfg,
		now: time.Now,
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

	GatewayAddr   string `json:"gateway_addr,omitempty"`
	GatewayURL    string `json:"gateway_url,omitempty"`
	AdminAddr     string `json:"admin_addr,omitempty"`
	AdminURL      string `json:"admin_url,omitempty"`
	AdminAutoPort bool   `json:"admin_auto_port"`

	StateDir string `json:"state_dir,omitempty"`
	DebugDir string `json:"debug_dir,omitempty"`

	Channels []string      `json:"channels,omitempty"`
	Routes   Routes        `json:"routes,omitempty"`
	Exec     execStatus    `json:"exec"`
	Uploads  uploadsStatus `json:"uploads"`
	Cron     cronStatus    `json:"cron"`
	Debug    debugStatus   `json:"debug"`
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
		StateDir:       strings.TrimSpace(s.cfg.StateDir),
		DebugDir:       strings.TrimSpace(s.cfg.DebugDir),
		Routes:         s.cfg.GatewayRoutes,
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
	filePath, err := s.resolveDebugFile(tracePath, name)
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

func (s *Service) redirectWithMessage(
	w http.ResponseWriter,
	r *http.Request,
	key string,
	message string,
) {
	values := url.Values{}
	values.Set(key, message)
	http.Redirect(
		w,
		r,
		routeIndex+"?"+values.Encode(),
		http.StatusSeeOther,
	)
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

func (s *Service) resolveDebugFile(
	tracePath string,
	name string,
) (string, error) {
	root := strings.TrimSpace(s.cfg.DebugDir)
	if root == "" {
		return "", fmt.Errorf("debug recorder is not configured")
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
		"formatTime": formatTime,
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
    @media (max-width: 760px) {
      h1 { font-size: 30px; }
      .meta { grid-template-columns: 1fr; }
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
        <span class="stat-label">Exec Sessions</span>
        <span class="stat-value">{{.Snapshot.Exec.SessionCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Uploads</span>
        <span class="stat-value">{{.Snapshot.Uploads.FileCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Debug Sessions</span>
        <span class="stat-value">{{.Snapshot.Debug.SessionCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Recent Traces</span>
        <span class="stat-value">{{.Snapshot.Debug.TraceCount}}</span>
      </article>
    </section>

    <section class="panels">
      <article class="card">
        <h2>Runtime</h2>
        <dl class="meta">
          <dt>App</dt>
          <dd>{{.Snapshot.AppName}}</dd>
          <dt>Agent Type</dt>
          <dd>{{if .Snapshot.AgentType}}{{.Snapshot.AgentType}}{{else}}-{{end}}</dd>
          <dt>Model</dt>
          <dd>
            {{if .Snapshot.ModelName}}
              {{.Snapshot.ModelMode}} / {{.Snapshot.ModelName}}
            {{else if .Snapshot.ModelMode}}
              {{.Snapshot.ModelMode}}
            {{else}}-{{end}}
          </dd>
          <dt>Session Backend</dt>
          <dd>{{if .Snapshot.SessionBackend}}{{.Snapshot.SessionBackend}}{{else}}-{{end}}</dd>
          <dt>Memory Backend</dt>
          <dd>{{if .Snapshot.MemoryBackend}}{{.Snapshot.MemoryBackend}}{{else}}-{{end}}</dd>
          <dt>Host</dt>
          <dd>{{if .Snapshot.Hostname}}{{.Snapshot.Hostname}}{{else}}-{{end}}</dd>
          <dt>PID</dt>
          <dd>{{if .Snapshot.PID}}{{.Snapshot.PID}}{{else}}-{{end}}</dd>
          <dt>Started</dt>
          <dd>{{formatTime .Snapshot.StartedAt}}</dd>
          <dt>Uptime</dt>
          <dd>{{.Snapshot.Uptime}}</dd>
          <dt>Gateway URL</dt>
          <dd><a href="{{.Snapshot.GatewayURL}}">{{.Snapshot.GatewayURL}}</a></dd>
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
              {{range $i, $ch := .Snapshot.Channels}}{{if $i}}, {{end}}{{$ch}}{{end}}
            {{else}}none{{end}}
          </dd>
          <dt>JSON</dt>
          <dd>
            <a href="/api/status">status</a> ·
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
        <h2>Debug Index</h2>
        <p class="subtle">
          Session-indexed trace browsing for recent gateway activity. This is
          especially useful when a Telegram or cron flow behaves strangely and
          you want the exact recorded request and event stream.
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
              {{range $i, $item := .Snapshot.Uploads.KindCounts}}{{if $i}}, {{end}}{{$item.Kind}} {{$item.Count}}{{end}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>By Source</dt>
          <dd>
            {{if .Snapshot.Uploads.SourceCounts}}
              {{range $i, $item := .Snapshot.Uploads.SourceCounts}}{{if $i}}, {{end}}{{$item.Source}} {{$item.Count}}{{end}}
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
              {{if .Channel}}{{.Channel}} → {{.Target}}{{else}}no delivery target{{end}}
            </td>
            <td>
              {{if .LastStatus}}{{.LastStatus}}{{else}}idle{{end}}
              {{if .LastError}}<br><span class="subtle">{{.LastError}}</span>{{end}}
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
              {{if .RequestID}}<br><span class="subtle">{{.RequestID}}</span>{{end}}
            </td>
            <td>
              {{if .MetaURL}}<a href="{{.MetaURL}}" target="_blank">meta</a>{{end}}
              {{if .EventsURL}} · <a href="{{.EventsURL}}" target="_blank">events</a>{{end}}
              {{if .ResultURL}} · <a href="{{.ResultURL}}" target="_blank">result</a>{{end}}
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
              {{if .RequestID}}<br><span class="subtle">{{.RequestID}}</span>{{end}}
              {{if .MessageID}}<br><span class="subtle">msg {{.MessageID}}</span>{{end}}
            </td>
            <td>
              {{if .MetaURL}}<a href="{{.MetaURL}}" target="_blank">meta</a>{{end}}
              {{if .EventsURL}} · <a href="{{.EventsURL}}" target="_blank">events</a>{{end}}
              {{if .ResultURL}} · <a href="{{.ResultURL}}" target="_blank">result</a>{{end}}
              {{if .TracePath}}<br><span class="subtle"><code>{{.TracePath}}</code></span>{{end}}
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
</body>
</html>`
