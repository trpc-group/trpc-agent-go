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
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
)

const (
	routeIndex = "/"

	routeStatusJSON = "/api/status"
	routeJobsJSON   = "/api/cron/jobs"
	routeJobRun     = "/api/cron/jobs/run"
	routeJobRemove  = "/api/cron/jobs/remove"
	routeJobsClear  = "/api/cron/jobs/clear"

	queryNotice = "notice"
	queryError  = "error"
	formJobID   = "job_id"

	refreshSeconds = 15
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

	GatewayAddr string
	GatewayURL  string
	AdminAddr   string
	AdminURL    string

	StateDir string
	DebugDir string

	Channels      []string
	GatewayRoutes Routes

	Cron *cron.Service
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
	return mux
}

type snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`

	AppName    string `json:"app_name,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`

	GatewayAddr string `json:"gateway_addr,omitempty"`
	GatewayURL  string `json:"gateway_url,omitempty"`
	AdminAddr   string `json:"admin_addr,omitempty"`
	AdminURL    string `json:"admin_url,omitempty"`

	StateDir string `json:"state_dir,omitempty"`
	DebugDir string `json:"debug_dir,omitempty"`

	Channels []string   `json:"channels,omitempty"`
	Routes   Routes     `json:"routes,omitempty"`
	Cron     cronStatus `json:"cron"`
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

	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

type pageData struct {
	Snapshot snapshot
	Notice   string
	Error    string
}

func (s *Service) Snapshot() snapshot {
	out := snapshot{
		GeneratedAt: s.now(),
		AppName:     strings.TrimSpace(s.cfg.AppName),
		InstanceID:  strings.TrimSpace(s.cfg.InstanceID),
		GatewayAddr: strings.TrimSpace(s.cfg.GatewayAddr),
		GatewayURL:  strings.TrimSpace(s.cfg.GatewayURL),
		AdminAddr:   strings.TrimSpace(s.cfg.AdminAddr),
		AdminURL:    strings.TrimSpace(s.cfg.AdminURL),
		StateDir:    strings.TrimSpace(s.cfg.StateDir),
		DebugDir:    strings.TrimSpace(s.cfg.DebugDir),
		Routes:      s.cfg.GatewayRoutes,
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
		Snapshot: s.Snapshot(),
		Notice:   strings.TrimSpace(r.URL.Query().Get(queryNotice)),
		Error:    strings.TrimSpace(r.URL.Query().Get(queryError)),
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
		MessagePreview: summarizeMessage(job.Message),
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

func summarizeMessage(text string) string {
	runes := []rune(strings.TrimSpace(text))
	const maxRunes = 96
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
		return value.Local().Format("2006-01-02 15:04:05 MST")
	case *time.Time:
		if value == nil || value.IsZero() {
			return "-"
		}
		return value.Local().Format("2006-01-02 15:04:05 MST")
	default:
		return "-"
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
  <meta http-equiv="refresh" content="15">
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
        <span class="stat-label">Running Jobs</span>
        <span class="stat-value">{{.Snapshot.Cron.RunningJobs}}</span>
      </article>
    </section>

    <section class="panels">
      <article class="card">
        <h2>Runtime</h2>
        <dl class="meta">
          <dt>App</dt>
          <dd>{{.Snapshot.AppName}}</dd>
          <dt>Gateway URL</dt>
          <dd><a href="{{.Snapshot.GatewayURL}}">{{.Snapshot.GatewayURL}}</a></dd>
          <dt>Admin URL</dt>
          <dd><a href="{{.Snapshot.AdminURL}}">{{.Snapshot.AdminURL}}</a></dd>
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
            <a href="/api/cron/jobs">jobs</a>
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
  </main>
</body>
</html>`
