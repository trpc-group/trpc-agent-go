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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

type stubRunner struct {
	reply string
}

type stubBMP struct {
	status BrowserManagedService
}

type stubSkillsProvider struct {
	report        ocskills.StatusReport
	err           error
	statusCount   int
	configPath    string
	refreshable   bool
	refreshCount  int
	refreshErr    error
	lastConfigKey string
	lastEnabled   bool
	setCount      int
	setErr        error
}

func (p stubBMP) BrowserManagedStatus() BrowserManagedService {
	return p.status
}

func (p *stubSkillsProvider) SkillsStatus() (ocskills.StatusReport, error) {
	if p != nil {
		p.statusCount++
	}
	return p.report, p.err
}

func (p *stubSkillsProvider) SkillsConfigPath() string {
	if p == nil {
		return ""
	}
	return p.configPath
}

func (p *stubSkillsProvider) SkillsRefreshable() bool {
	if p == nil {
		return false
	}
	return p.refreshable
}

func (p *stubSkillsProvider) RefreshSkills() error {
	if p == nil {
		return nil
	}
	p.refreshCount++
	return p.refreshErr
}

func (p *stubSkillsProvider) SetSkillEnabled(
	configKey string,
	enabled bool,
) error {
	if p == nil {
		return nil
	}
	p.setCount++
	p.lastConfigKey = configKey
	p.lastEnabled = enabled
	return p.setErr
}

func (r *stubRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(r.reply),
			}},
			Done: true,
		},
	}
	close(ch)
	return ch, nil
}

func (r *stubRunner) Close() error { return nil }

func writeDebugTraceFixture(
	t *testing.T,
	root string,
	sessionID string,
	requestID string,
	startedAt time.Time,
	traceID string,
) string {
	t.Helper()

	traceDir := filepath.Join(
		root,
		startedAt.Format("20060102"),
		startedAt.Format("150405")+"_"+requestID,
	)
	require.NoError(t, os.MkdirAll(traceDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugMetaFileName),
		[]byte(
			`{"request_id":"`+requestID+`","trace_id":"`+
				traceID+`"}`+"\n",
		),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugEventsFileName),
		[]byte(`{"kind":"trace.start"}`+"\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(traceDir, debugResultFileName),
		[]byte(`{"status":"ok"}`+"\n"),
		0o600,
	))

	indexDir := filepath.Join(
		root,
		debugBySessionDir,
		sessionID,
		startedAt.Format("20060102"),
		startedAt.Format("150405")+"_"+requestID,
	)
	require.NoError(t, os.MkdirAll(indexDir, 0o755))
	rel, err := filepath.Rel(indexDir, traceDir)
	require.NoError(t, err)
	ref := `{"trace_dir":"` + filepath.ToSlash(rel) + `",` +
		`"started_at":"` + startedAt.Format(time.RFC3339Nano) + `",` +
		`"channel":"telegram","request_id":"` + requestID + `",` +
		`"message_id":"msg-` + requestID + `","trace_id":"` +
		traceID + `"}`
	require.NoError(t, os.WriteFile(
		filepath.Join(indexDir, debugMetaTraceRefName),
		[]byte(ref),
		0o600,
	))
	return traceDir
}

func TestNormalizeLangfuseStatus_TrimsValues(t *testing.T) {
	t.Parallel()

	got := normalizeLangfuseStatus(LangfuseStatus{
		Error:            " boom ",
		UIBaseURL:        " http://127.0.0.1:3000/ ",
		TraceURLTemplate: " http://127.0.0.1:3000/traces/{{trace_id}} ",
	})

	require.Equal(t, "boom", got.Error)
	require.Equal(t, "http://127.0.0.1:3000", got.UIBaseURL)
	require.Equal(
		t,
		"http://127.0.0.1:3000/traces/{{trace_id}}",
		got.TraceURLTemplate,
	)
}

func TestService_LangfuseTraceURL_Guards(t *testing.T) {
	t.Parallel()

	var nilSvc *Service
	require.Empty(t, nilSvc.langfuseTraceURL("trace-1"))

	svc := New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Equal(
		t,
		"http://127.0.0.1:3000/traces/trace-1",
		svc.langfuseTraceURL(" trace-1 "),
	)

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          false,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            false,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/{{trace_id}}",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))

	svc = New(Config{
		Langfuse: LangfuseStatus{
			Enabled:          true,
			Ready:            true,
			TraceURLTemplate: "http://127.0.0.1:3000/traces/static",
		},
	})
	require.Empty(t, svc.langfuseTraceURL("trace-1"))
}

func TestServiceHandlerRendersOverview(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu and mem",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(Config{
		AppName:     "openclaw",
		InstanceID:  "abcd1234",
		GatewayAddr: "127.0.0.1:8080",
		GatewayURL:  "http://127.0.0.1:8080",
		AdminAddr:   "127.0.0.1:18789",
		AdminURL:    "http://127.0.0.1:18789/",
		StateDir:    "/tmp/openclaw",
		DebugDir:    "/tmp/openclaw/debug",
		Channels:    []string{"telegram"},
		Cron:        cronSvc,
	})

	req := httptest.NewRequest(http.MethodGet, routeIndex, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "TRPC-CLAW admin")
	require.Contains(t, body, "TRPC-CLAW")
	require.Contains(t, body, "trpc-claw")
	require.Contains(t, body, "/skills")
	require.Contains(t, body, "127.0.0.1:8080")
	require.Contains(t, body, "telegram")
}

func TestServiceHandlerRendersSkillsInventory(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		AppName: "openclaw",
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			report: ocskills.StatusReport{
				Skills: []ocskills.StatusEntry{{
					Name:        "weather-probe",
					Description: "Probe weather prerequisites",
					SkillKey:    "weather-api",
					ConfigKey:   "weather-api",
					FilePath:    "/tmp/skills/weather-probe",
					Source:      "bundled",
					Reason:      "missing env: OPENAI_API_KEY",
					PrimaryEnv:  "OPENAI_API_KEY",
					Bundled:     true,
				}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Skills Inventory")
	require.Contains(t, body, "weather-probe")
	require.Contains(t, body, "/api/skills/refresh")
	require.Contains(t, body, "/api/skills/toggle")
	require.Contains(t, body, "/overview")
	require.Contains(t, body, "/skills")
	require.Contains(t, body, "/tmp/openclaw.yaml")
	require.Contains(t, body, "OPENAI_API_KEY")
}

func TestServiceRenderPageScopesSnapshotToActiveView(t *testing.T) {
	t.Parallel()

	var browserProfilesHits int32
	browserServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		atomic.AddInt32(&browserProfilesHits, 1)
		require.Equal(t, "/profiles", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"profiles":[]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(browserServer.Close)

	provider := &stubSkillsProvider{
		report: ocskills.StatusReport{
			Skills: []ocskills.StatusEntry{{
				Name:      "weather-probe",
				ConfigKey: "weather-probe",
				Eligible:  true,
			}},
		},
	}
	svc := New(
		Config{
			Skills: provider,
			Browser: BrowserConfig{
				Providers: []BrowserProvider{{
					Name:          "browser",
					HostServerURL: browserServer.URL,
				}},
			},
		},
		WithBrowserHTTPClient(browserServer.Client()),
	)

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, provider.statusCount)
	require.Zero(t, atomic.LoadInt32(&browserProfilesHits))

	req = httptest.NewRequest(http.MethodGet, routeBrowser, nil)
	rr = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, provider.statusCount)
	require.NotZero(t, atomic.LoadInt32(&browserProfilesHits))
}

func TestServiceHandlerRendersAdminPages(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	handler := svc.Handler()

	cases := []struct {
		name    string
		path    string
		title   string
		summary string
	}{
		{
			name:    "automation",
			path:    routeAutomation,
			title:   "Automation",
			summary: "Inspect scheduled jobs, trigger one-off runs, and clear automation state.",
		},
		{
			name:    "sessions",
			path:    routeSessions,
			title:   "Sessions",
			summary: "Review exec sessions, upload sessions, and recently persisted files.",
		},
		{
			name:    "debug",
			path:    routeDebug,
			title:   "Debug",
			summary: "Browse debug session indexes, recent traces, and Langfuse readiness.",
		},
		{
			name:    "browser",
			path:    routeBrowser,
			title:   "Browser",
			summary: "Inspect browser providers, managed browser-server state, nodes, and profiles.",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			require.Contains(t, rr.Body.String(), tc.title)
			require.Contains(t, rr.Body.String(), tc.summary)
		})
	}
}

func TestServiceRenderPageRejectsNonGET(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, routeOverview, nil)
	rr := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestServiceSkillsStatusErrorRetainsRecoveryFields(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			err:         errors.New("skills status boom"),
		},
	})

	status := svc.skillsStatus()
	require.True(t, status.Enabled)
	require.True(t, status.Writable)
	require.True(t, status.Refreshable)
	require.Equal(t, "/tmp/openclaw.yaml", status.ConfigPath)
	require.Equal(t, "skills status boom", status.Error)
}

func TestServiceHandlerSuppressesSkillsEmptyStateOnError(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			configPath:  "/tmp/openclaw.yaml",
			refreshable: true,
			err:         errors.New("skills status boom"),
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsPage, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "skills status boom")
	require.NotContains(t, body, "No skills discovered.")
}

func TestServiceSkillsJSONEndpoint(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		Skills: &stubSkillsProvider{
			report: ocskills.StatusReport{
				Skills: []ocskills.StatusEntry{{
					Name:        "weather-probe",
					Description: "Probe weather prerequisites",
					Bundled:     true,
					Eligible:    true,
				}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, routeSkillsJSON, nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"total_count": 1`)
	require.Contains(t, rr.Body.String(), `"label": "Bundled Skills"`)
	require.Contains(t, rr.Body.String(), `"name": "weather-probe"`)
}

func TestServiceSkillsJSONEndpointRejectsMethod(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, routeSkillsJSON, nil)
	rr := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestServiceToggleSkillEndpoint(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{
		configPath:  "/tmp/openclaw.yaml",
		refreshable: true,
	}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillToggle,
		strings.NewReader(
			"skill_key=weather-api&skill_name=weather-probe&enabled=false&return_to=skill-card-weather-api&return_path=%2Fskills",
		),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, "weather-api", provider.lastConfigKey)
	require.False(t, provider.lastEnabled)

	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, routeSkillsPage, loc.Path)
	require.Equal(t, "skill-card-weather-api", loc.Fragment)
	require.Contains(
		t,
		loc.Query().Get(queryNotice),
		"Changes apply on the next turn.",
	)
}

func TestServiceRefreshSkillsEndpoint(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{refreshable: true}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, 1, provider.refreshCount)

	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, routeSkillsPage, loc.Path)
	require.Equal(t, "skills-admin", loc.Fragment)
	require.Contains(
		t,
		loc.Query().Get(queryNotice),
		"Refreshed skills.",
	)
}

func TestServiceRefreshSkillsEndpointRequiresLiveRepo(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, routeSkillsPage, loc.Path)
	require.Equal(t, "skills-admin", loc.Fragment)
	require.Zero(t, provider.refreshCount)
	require.Equal(
		t,
		"live skills repository is not available",
		loc.Query().Get(queryError),
	)
}

func TestServiceRefreshSkillsEndpointReportsProviderError(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{
		refreshable: true,
		refreshErr:  errors.New("refresh boom"),
	}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillsRefresh,
		strings.NewReader("return_to=skills-admin&return_path=%2Fskills"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, 1, provider.refreshCount)

	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, routeSkillsPage, loc.Path)
	require.Equal(t, "skills-admin", loc.Fragment)
	require.Equal(t, "refresh boom", loc.Query().Get(queryError))
}

func TestAdminHelpers_PageMetadataAndNavigation(t *testing.T) {
	t.Parallel()

	type pageCase struct {
		path    string
		view    adminView
		title   string
		summary string
	}

	cases := []pageCase{
		{
			path:    routeOverview,
			view:    viewOverview,
			title:   "Overview",
			summary: "Runtime summary, gateway surfaces, and entry points into the rest of the admin.",
		},
		{
			path:    routeSkillsPage,
			view:    viewSkills,
			title:   "Skills",
			summary: "Discover installed skills, refresh folders from disk, and manage config-backed enablement.",
		},
		{
			path:    routeAutomation,
			view:    viewAutomation,
			title:   "Automation",
			summary: "Inspect scheduled jobs, trigger one-off runs, and clear automation state.",
		},
		{
			path:    routeSessions,
			view:    viewSessions,
			title:   "Sessions",
			summary: "Review exec sessions, upload sessions, and recently persisted files.",
		},
		{
			path:    routeDebug,
			view:    viewDebug,
			title:   "Debug",
			summary: "Browse debug session indexes, recent traces, and Langfuse readiness.",
		},
		{
			path:    routeBrowser,
			view:    viewBrowser,
			title:   "Browser",
			summary: "Inspect browser providers, managed browser-server state, nodes, and profiles.",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.title, pageTitle(tc.view))
			require.Equal(t, tc.summary, pageSummary(tc.view))
			require.Equal(t, tc.path, navPath(tc.path))
			require.Equal(t, tc.view, navViewForPath(tc.path))
		})
	}

	require.Equal(t, routeOverview, navPath(routeIndex))
	require.Equal(t, viewOverview, navViewForPath(routeIndex))
	require.Empty(t, navPath(" /unknown "))
	require.Equal(t, viewOverview, navViewForPath(" /unknown "))
}

func TestSkillInstallViewsFromStatus_TrimsValues(t *testing.T) {
	t.Parallel()

	out := skillInstallViewsFromStatus([]ocskills.StatusInstallOption{
		{
			ID:    " brew-jq ",
			Kind:  " brew ",
			Label: " brew install jq ",
			Bins:  []string{" jq ", " yq "},
		},
		{
			ID:    " custom ",
			Kind:  " custom ",
			Label: " custom installer ",
		},
	})
	require.Equal(
		t,
		[]skillInstallView{
			{
				ID:    "brew-jq",
				Kind:  "brew",
				Label: "brew install jq",
				Bins:  []string{" jq ", " yq "},
			},
			{
				ID:    "custom",
				Kind:  "custom",
				Label: "custom installer",
			},
		},
		out,
	)
	require.Nil(t, skillInstallViewsFromStatus(nil))
}

func TestServiceToggleSkillEndpointRequiresConfigPath(t *testing.T) {
	t.Parallel()

	provider := &stubSkillsProvider{}
	svc := New(Config{Skills: provider})

	req := httptest.NewRequest(
		http.MethodPost,
		routeSkillToggle,
		strings.NewReader("skill_key=weather-api&enabled=true"),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Zero(t, provider.setCount)
	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(
		t,
		"skill toggles require a config-backed runtime",
		loc.Query().Get(queryError),
	)
}

func TestServiceJobEndpoints(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	job, err := cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(Config{Cron: cronSvc})
	handler := svc.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeJobsJSON, nil)
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), job.ID)

	runReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRun,
		strings.NewReader("job_id="+job.ID),
	)
	runReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	runRR := httptest.NewRecorder()
	handler.ServeHTTP(runRR, runReq)
	require.Equal(t, http.StatusSeeOther, runRR.Code)

	removeReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRemove,
		strings.NewReader("job_id="+job.ID),
	)
	removeReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	removeRR := httptest.NewRecorder()
	handler.ServeHTTP(removeRR, removeReq)
	require.Equal(t, http.StatusSeeOther, removeRR.Code)
	require.Nil(t, cronSvc.Get(job.ID))
}

func TestBrowserEndpointSummary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		view browserEndpointView
		want string
	}{
		{
			name: "empty",
			want: "-",
		},
		{
			name: "down with error",
			view: browserEndpointView{
				URL:   "http://127.0.0.1:19790",
				Error: "connection refused",
			},
			want: "down: connection refused",
		},
		{
			name: "down",
			view: browserEndpointView{
				URL: "http://127.0.0.1:19790",
			},
			want: "down",
		},
		{
			name: "reachable without profiles",
			view: browserEndpointView{
				URL:       "http://127.0.0.1:19790",
				Reachable: true,
			},
			want: "reachable",
		},
		{
			name: "reachable with profiles",
			view: browserEndpointView{
				URL:       "http://127.0.0.1:19790",
				Reachable: true,
				Profiles: []browserRemoteProbe{
					{Name: "chrome", State: "ready"},
					{Name: "", State: "busy"},
					{Name: "edge"},
					{},
				},
			},
			want: "chrome=ready, busy, edge",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, browserEndpointSummary(tc.view))
		})
	}
}

func TestServiceProbeBrowserEndpoint_CachesAndHandlesErrors(
	t *testing.T,
) {
	t.Parallel()

	t.Run("caches reachable result", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			requestCount++
			require.Equal(t, "/profiles", r.URL.Path)
			writeJSON(w, http.StatusOK, map[string]any{
				"profiles": []map[string]any{{
					"name": "chrome",
				}, {
					"name": "alpha",
				}},
			})
		}))
		t.Cleanup(server.Close)

		svc := New(Config{})
		cache := make(map[string]browserEndpointView)

		view := svc.probeBrowserEndpoint(server.URL, cache)
		require.True(t, view.Reachable)
		require.Len(t, view.Profiles, 2)
		require.Equal(t, "alpha", view.Profiles[0].Name)
		require.Equal(t, "chrome", view.Profiles[1].Name)

		cached := svc.probeBrowserEndpoint(server.URL, cache)
		require.Equal(t, view, cached)
		require.Equal(t, 1, requestCount)
	})

	t.Run("bad status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)

		view := New(Config{}).probeBrowserEndpoint(
			server.URL,
			make(map[string]browserEndpointView),
		)
		require.False(t, view.Reachable)
		require.Contains(t, view.Error, "unexpected status")
	})

	t.Run("bad json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			_, _ = w.Write([]byte("{"))
		}))
		t.Cleanup(server.Close)

		view := New(Config{}).probeBrowserEndpoint(
			server.URL,
			make(map[string]browserEndpointView),
		)
		require.False(t, view.Reachable)
		require.Contains(t, view.Error, "decode profiles")
	})
}

func TestResolveDebugRootFile_ErrorPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	serviceDir := filepath.Join(root, "services")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))

	logPath := filepath.Join(serviceDir, "browser-server.log")
	require.NoError(t, os.WriteFile(logPath, []byte("ready\n"), 0o600))

	got, err := resolveDebugRootFile(root, "services/browser-server.log")
	require.NoError(t, err)
	require.Equal(t, logPath, got)

	_, err = resolveDebugRootFile(root, ".")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")

	_, err = resolveDebugRootFile(root, "/tmp/browser-server.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid debug path")

	_, err = resolveDebugRootFile(root, "services/missing.log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	_, err = resolveDebugRootFile(root, "services")
	require.Error(t, err)
	require.Contains(t, err.Error(), "directory")
}

func TestServiceSnapshotIncludesCronSummary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
		cron.WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "5m",
		},
		Message: "collect cpu and mem",
		UserID:  "u1",
	})
	require.NoError(t, err)

	svc := New(
		Config{Cron: cronSvc},
		WithClock(func() time.Time { return now }),
	)
	snap := svc.Snapshot()
	require.True(t, snap.Cron.Enabled)
	require.Equal(t, 1, snap.Cron.JobCount)
	require.Len(t, snap.Cron.Jobs, 1)
	require.Equal(t, "every 5m", snap.Cron.Jobs[0].Schedule)
}

func TestServiceSnapshotIncludesBrowserSummary(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(1700000000, 0)

	svc := New(Config{
		Browser: BrowserConfig{
			Managed: stubBMP{
				status: BrowserManagedService{
					Enabled:         true,
					Managed:         true,
					State:           "running",
					URL:             "http://127.0.0.1:19790",
					PID:             4321,
					LogPath:         "/tmp/debug/services/browser-server.log",
					LogRelativePath: "services/browser-server.log",
					StartedAt:       &startedAt,
					RecentLogs: []string{
						"OpenClaw browser server listening",
					},
				},
			},
			Providers: []BrowserProvider{{
				Name:             "primary",
				DefaultProfile:   "openclaw",
				HostServerURL:    "http://127.0.0.1:19790",
				SandboxServerURL: "http://127.0.0.1:20790",
				AllowLoopback:    true,
				Profiles: []BrowserProfile{{
					Name:      "openclaw",
					Transport: "stdio",
				}, {
					Name:             "chrome",
					BrowserServerURL: "http://127.0.0.1:19790",
				}},
				Nodes: []BrowserNode{{
					ID:        "edge",
					ServerURL: "http://node.example:7777",
				}},
			}},
		},
	})

	snap := svc.Snapshot()
	require.True(t, snap.Browser.Enabled)
	require.Equal(t, 1, snap.Browser.ProviderCount)
	require.Equal(t, 2, snap.Browser.ProfileCount)
	require.Equal(t, 1, snap.Browser.NodeCount)
	require.Len(t, snap.Browser.Providers, 1)
	require.Equal(t, "primary", snap.Browser.Providers[0].Name)
	require.Equal(
		t,
		"http://127.0.0.1:19790",
		snap.Browser.Providers[0].HostServerURL,
	)
	require.True(t, snap.Browser.Managed.Enabled)
	require.True(t, snap.Browser.Managed.Managed)
	require.Equal(t, "running", snap.Browser.Managed.State)
	require.Equal(
		t,
		"/debug/file?path=services%2Fbrowser-server.log",
		snap.Browser.Managed.LogURL,
	)
	require.Len(t, snap.Browser.Managed.RecentLogs, 1)
}

func TestServiceSnapshotProbesBrowserEndpoints(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "/profiles", r.URL.Path)
		writeJSON(w, http.StatusOK, map[string]any{
			"profiles": []map[string]any{{
				"name":   "openclaw",
				"state":  "ready",
				"driver": "playwright",
				"tabs":   1,
			}},
		})
	}))
	t.Cleanup(server.Close)

	svc := New(
		Config{
			Browser: BrowserConfig{
				Providers: []BrowserProvider{{
					Name:           "primary",
					DefaultProfile: "openclaw",
					HostServerURL:  server.URL,
					Nodes: []BrowserNode{{
						ID:        "edge",
						ServerURL: server.URL,
					}},
				}},
			},
		},
		WithBrowserHTTPClient(server.Client()),
	)

	snap := svc.Snapshot()
	require.True(t, snap.Browser.Enabled)
	require.Len(t, snap.Browser.Providers, 1)
	require.True(t, snap.Browser.Providers[0].Host.Reachable)
	require.Len(t, snap.Browser.Providers[0].Host.Profiles, 1)
	require.Equal(
		t,
		"openclaw",
		snap.Browser.Providers[0].Host.Profiles[0].Name,
	)
	require.Len(t, snap.Browser.Providers[0].Nodes, 1)
	require.True(t, snap.Browser.Providers[0].Nodes[0].Status.Reachable)
}

func TestServiceSnapshotIncludesUploadSourceCounts(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.SaveWithInfo(
		context.Background(),
		scope,
		"voice.ogg",
		uploads.FileMetadata{
			MimeType: "audio/ogg",
			Source:   uploads.SourceInbound,
		},
		[]byte("voice"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithInfo(
		context.Background(),
		scope,
		"page-1.pdf",
		uploads.FileMetadata{
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	snap := New(Config{StateDir: stateDir}).Snapshot()
	require.True(t, snap.Uploads.Enabled)
	require.Len(t, snap.Uploads.SourceCounts, 2)
	require.Equal(t, "derived", snap.Uploads.SourceCounts[0].Source)
	require.Equal(t, 1, snap.Uploads.SourceCounts[0].Count)
	require.Equal(t, "inbound", snap.Uploads.SourceCounts[1].Source)
	require.Equal(t, 1, snap.Uploads.SourceCounts[1].Count)
}

func TestServiceDebugEndpoints(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	now := time.Date(2026, 3, 6, 18, 10, 0, 0, time.UTC)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:1",
		"req-1",
		now,
		"trace-1",
	)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:2",
		"req-2",
		now.Add(-time.Minute),
		"trace-2",
	)

	svc := New(
		Config{
			AppName:        "openclaw",
			InstanceID:     "inst-1",
			StartedAt:      now.Add(-time.Hour),
			Hostname:       "host-1",
			PID:            4321,
			GoVersion:      "go1.test",
			AgentType:      "llm",
			ModelMode:      "openai",
			ModelName:      "gpt-5",
			SessionBackend: "sqlite",
			MemoryBackend:  "inmemory",
			DebugDir:       debugRoot,
			Langfuse: LangfuseStatus{
				Enabled:   true,
				Ready:     true,
				UIBaseURL: "http://127.0.0.1:3000",
				TraceURLTemplate: "http://127.0.0.1:3000/project/" +
					"local-dev/traces/{{trace_id}}",
			},
		},
		WithClock(func() time.Time { return now }),
	)
	handler := svc.Handler()

	snap := svc.Snapshot()
	require.True(t, snap.Debug.Enabled)
	require.Equal(t, 2, snap.Debug.SessionCount)
	require.Equal(t, 2, snap.Debug.TraceCount)
	require.Len(t, snap.Debug.Sessions, 2)
	require.Len(t, snap.Debug.RecentTraces, 2)
	require.True(t, snap.Langfuse.Enabled)
	require.True(t, snap.Langfuse.Ready)
	require.Equal(
		t,
		"trace-1",
		snap.Debug.RecentTraces[0].TraceID,
	)
	require.Equal(
		t,
		"http://127.0.0.1:3000/project/local-dev/traces/trace-1",
		snap.Debug.RecentTraces[0].LangfuseURL,
	)

	sessionsRR := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugSessionsJSON,
		nil,
	)
	handler.ServeHTTP(sessionsRR, sessionsReq)
	require.Equal(t, http.StatusOK, sessionsRR.Code)
	require.Contains(t, sessionsRR.Body.String(), "telegram:dm:1")
	require.Contains(t, sessionsRR.Body.String(), "trace-1")

	traceQuery := routeDebugTracesJSON + "?" +
		querySessionID + "=" +
		url.QueryEscape("telegram:dm:1")
	tracesRR := httptest.NewRecorder()
	tracesReq := httptest.NewRequest(http.MethodGet, traceQuery, nil)
	handler.ServeHTTP(tracesRR, tracesReq)
	require.Equal(t, http.StatusOK, tracesRR.Code)
	require.Contains(t, tracesRR.Body.String(), "req-1")
	require.Contains(t, tracesRR.Body.String(), "trace-1")
	require.Contains(
		t,
		tracesRR.Body.String(),
		"http://127.0.0.1:3000/project/local-dev/traces/trace-1",
	)
	require.NotContains(t, tracesRR.Body.String(), "req-2")

	metaURL := snap.Debug.RecentTraces[0].MetaURL
	metaRR := httptest.NewRecorder()
	metaReq := httptest.NewRequest(http.MethodGet, metaURL, nil)
	handler.ServeHTTP(metaRR, metaReq)
	require.Equal(t, http.StatusOK, metaRR.Code)
	require.Contains(t, metaRR.Body.String(), "req-1")
}

func TestServiceClearAndValidationPaths(t *testing.T) {
	t.Parallel()

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&stubRunner{reply: "done"},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	for _, name := range []string{"job-a", "job-b"} {
		_, err = cronSvc.Add(&cron.Job{
			Name:    name,
			Enabled: true,
			Schedule: cron.Schedule{
				Kind:  cron.ScheduleKindEvery,
				Every: "1m",
			},
			Message: "collect cpu",
			UserID:  "u1",
		})
		require.NoError(t, err)
	}

	svc := New(Config{Cron: cronSvc})
	handler := svc.Handler()

	methodRR := httptest.NewRecorder()
	methodReq := httptest.NewRequest(http.MethodGet, routeJobRun, nil)
	handler.ServeHTTP(methodRR, methodReq)
	require.Equal(t, http.StatusMethodNotAllowed, methodRR.Code)

	missingRR := httptest.NewRecorder()
	missingReq := httptest.NewRequest(
		http.MethodPost,
		routeJobRemove,
		strings.NewReader(""),
	)
	missingReq.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	handler.ServeHTTP(missingRR, missingReq)
	require.Equal(t, http.StatusSeeOther, missingRR.Code)
	require.Contains(
		t,
		missingRR.Header().Get("Location"),
		"job_id+is+required",
	)

	clearRR := httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPost, routeJobsClear, nil)
	handler.ServeHTTP(clearRR, clearReq)
	require.Equal(t, http.StatusSeeOther, clearRR.Code)
	require.Empty(t, cronSvc.List())
}

func TestServiceWithoutCron(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	handler := svc.Handler()

	statusRR := httptest.NewRecorder()
	statusReq := httptest.NewRequest(http.MethodGet, routeStatusJSON, nil)
	handler.ServeHTTP(statusRR, statusReq)
	require.Equal(t, http.StatusOK, statusRR.Code)
	require.Contains(t, statusRR.Body.String(), `"enabled": false`)

	clearRR := httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPost, routeJobsClear, nil)
	handler.ServeHTTP(clearRR, clearReq)
	require.Equal(t, http.StatusNotFound, clearRR.Code)
}

func TestServiceSnapshotIncludesUploadsAndExec(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	uploadsRoot := filepath.Join(stateDir, defaultUploadsDir)
	relPath := filepath.ToSlash(
		filepath.Join("telegram", "u1", "session-1", "clip.mp4"),
	)
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(filepath.Join(uploadsRoot, relPath)), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(uploadsRoot, relPath),
			[]byte("video"),
			0o600,
		),
	)

	svc := New(Config{
		StateDir: stateDir,
		Exec:     octool.NewManager(),
	})
	snap := svc.Snapshot()
	require.True(t, snap.Exec.Enabled)
	require.Equal(t, 0, snap.Exec.SessionCount)
	require.True(t, snap.Uploads.Enabled)
	require.Equal(t, 1, snap.Uploads.FileCount)
	require.Equal(t, "clip.mp4", snap.Uploads.Files[0].Name)
	require.Equal(t, "telegram", snap.Uploads.Files[0].Channel)
	require.Equal(t, "u1", snap.Uploads.Files[0].UserID)
	require.Equal(t, "session-1", snap.Uploads.Files[0].SessionID)
	require.Len(t, snap.Uploads.Sessions, 1)
	require.Len(t, snap.Uploads.KindCounts, 1)
	require.Equal(t, "video", snap.Uploads.KindCounts[0].Kind)
	require.Equal(t, 1, snap.Uploads.KindCounts[0].Count)

	handler := svc.Handler()

	execRR := httptest.NewRecorder()
	execReq := httptest.NewRequest(
		http.MethodGet,
		routeExecSessionsJSON,
		nil,
	)
	handler.ServeHTTP(execRR, execReq)
	require.Equal(t, http.StatusOK, execRR.Code)
	require.Contains(t, execRR.Body.String(), "[]")

	uploadsRR := httptest.NewRecorder()
	uploadsReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON,
		nil,
	)
	handler.ServeHTTP(uploadsRR, uploadsReq)
	require.Equal(t, http.StatusOK, uploadsRR.Code)
	require.Contains(t, uploadsRR.Body.String(), "clip.mp4")
	require.Contains(t, uploadsRR.Body.String(), `"kind": "video"`)

	openRR := httptest.NewRecorder()
	openReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile+"?"+url.Values{
			queryPath: []string{relPath},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(openRR, openReq)
	require.Equal(t, http.StatusOK, openRR.Code)
	require.Equal(t, "video", openRR.Body.String())

	downloadRR := httptest.NewRecorder()
	downloadReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile+"?"+url.Values{
			queryPath:     []string{relPath},
			queryDownload: []string{"1"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(downloadRR, downloadReq)
	require.Equal(t, http.StatusOK, downloadRR.Code)
	require.Contains(
		t,
		downloadRR.Header().Get("Content-Disposition"),
		"clip.mp4",
	)
}

func TestServiceUploadJSONFilters(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	_, err = store.Save(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "session-1",
		},
		"clip.mp4",
		[]byte("video"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithMetadata(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u2",
			SessionID: "session-2",
		},
		"report.pdf",
		"application/pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithInfo(
		context.Background(),
		uploads.Scope{
			Channel:   "telegram",
			UserID:    "u2",
			SessionID: "session-2",
		},
		"derived-frame.png",
		uploads.FileMetadata{
			MimeType: "image/png",
			Source:   uploads.SourceDerived,
		},
		[]byte("png"),
	)
	require.NoError(t, err)

	svc := New(Config{StateDir: stateDir})
	handler := svc.Handler()

	filesRR := httptest.NewRecorder()
	filesReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON+"?"+url.Values{
			querySessionID: []string{"session-2"},
			queryKind:      []string{"pdf"},
			queryMimeType:  []string{"application/pdf"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(filesRR, filesReq)
	require.Equal(t, http.StatusOK, filesRR.Code)
	require.Contains(t, filesRR.Body.String(), "report.pdf")
	require.NotContains(t, filesRR.Body.String(), "clip.mp4")

	sourceRR := httptest.NewRecorder()
	sourceReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadsJSON+"?"+url.Values{
			queryUserID: []string{"u2"},
			querySource: []string{uploads.SourceDerived},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(sourceRR, sourceReq)
	require.Equal(t, http.StatusOK, sourceRR.Code)
	require.Contains(t, sourceRR.Body.String(), "derived-frame.png")
	require.NotContains(t, sourceRR.Body.String(), "report.pdf")

	sessionsRR := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadSessions+"?"+url.Values{
			queryUserID: []string{"u2"},
		}.Encode(),
		nil,
	)
	handler.ServeHTTP(sessionsRR, sessionsReq)
	require.Equal(t, http.StatusOK, sessionsRR.Code)
	require.Contains(t, sessionsRR.Body.String(), "session-2")
	require.NotContains(t, sessionsRR.Body.String(), "session-1")
}

func TestAdminRuntimeHelpers(t *testing.T) {
	t.Parallel()

	exitCode := 7
	view := execSessionViewFromSession(octool.ProcessSession{
		SessionID: " sess-1 ",
		Command:   " echo hi ",
		Status:    " running ",
		StartedAt: " start ",
		DoneAt:    " done ",
		ExitCode:  &exitCode,
	})
	require.Equal(t, "sess-1", view.SessionID)
	require.Equal(t, "echo hi", view.Command)
	require.Equal(t, "running", view.Status)
	require.Equal(t, "start", view.StartedAt)
	require.Equal(t, "done", view.DoneAt)
	require.NotNil(t, view.ExitCode)
	require.Equal(t, exitCode, *view.ExitCode)

	require.Equal(t, "image", uploadKindFromName("frame.PNG"))
	require.Equal(t, "audio", uploadKindFromName("voice.ogg"))
	require.Equal(t, "video", uploadKindFromName("clip.MOV"))
	require.Equal(t, "pdf", uploadKindFromName("doc.pdf"))
	require.Equal(t, "file", uploadKindFromName("notes.txt"))
	require.Equal(
		t,
		"video",
		uploadKindFromFile(uploads.ListedFile{
			Name:     "video-note",
			MimeType: "video/mp4",
		}),
	)
}

func TestServiceUploadAndDebugValidationErrors(t *testing.T) {
	t.Parallel()

	svc := New(Config{StateDir: t.TempDir()})
	handler := svc.Handler()

	uploadRR := httptest.NewRecorder()
	uploadReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile,
		nil,
	)
	handler.ServeHTTP(uploadRR, uploadReq)
	require.Equal(t, http.StatusBadRequest, uploadRR.Code)

	debugRR := httptest.NewRecorder()
	debugReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugFile,
		nil,
	)
	handler.ServeHTTP(debugRR, debugReq)
	require.Equal(t, http.StatusBadRequest, debugRR.Code)
}

func TestResolveUploadFile_InvalidPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "telegram", "u1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	filePath := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))

	_, err := resolveUploadFile(root, "../clip.mp4")
	require.Error(t, err)

	_, err = resolveUploadFile(root, "telegram/u1")
	require.Error(t, err)

	_, err = resolveUploadFile(
		root,
		"telegram/u1/clip.mp4"+uploads.MetadataSuffix,
	)
	require.Error(t, err)

	got, err := resolveUploadFile(root, "telegram/u1/clip.mp4")
	require.NoError(t, err)
	require.Equal(t, filePath, got)
}

func TestServiceUploadEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	svc := New(Config{StateDir: t.TempDir()})
	handler := svc.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, routeUploadFile, nil)
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestDebugHelpers(t *testing.T) {
	t.Parallel()

	svc := New(Config{DebugDir: t.TempDir()})
	require.Empty(t, svc.debugFileURL("", debugMetaFileName))
	require.Empty(t, svc.debugFileURL("x/y", "notes.txt"))

	items := []debugTraceView{
		{SessionID: "s1"},
		{SessionID: "s2"},
	}
	limited := limitDebugTraces(items, 1)
	require.Len(t, limited, 1)
	require.Equal(t, "s1", limited[0].SessionID)

	require.False(t, fileExists(filepath.Join(t.TempDir(), "missing")))
}

func TestReadDebugTrace_RejectsEscapeAndBadJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bySession := filepath.Join(root, debugBySessionDir, "session-1")
	traceDir := filepath.Join(root, "20260307", "trace")
	require.NoError(t, os.MkdirAll(bySession, 0o755))
	require.NoError(t, os.MkdirAll(traceDir, 0o755))

	refPath := filepath.Join(bySession, debugMetaTraceRefName)
	svc := New(Config{DebugDir: root})

	require.NoError(t, os.WriteFile(refPath, []byte("{"), 0o600))
	_, ok, err := svc.readDebugTrace(
		root,
		filepath.Join(root, debugBySessionDir),
		refPath,
		"",
	)
	require.Error(t, err)
	require.False(t, ok)

	ref := `{
  "trace_dir":"../../../../escape",
  "started_at":"2026-03-07T00:00:00Z"
}`
	require.NoError(t, os.WriteFile(refPath, []byte(ref), 0o600))
	_, ok, err = svc.readDebugTrace(
		root,
		filepath.Join(root, debugBySessionDir),
		refPath,
		"",
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestHandleIndex_RendersUploadPreviews(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.Save(context.Background(), scope, "frame.png", []byte("png"))
	require.NoError(t, err)
	_, err = store.Save(context.Background(), scope, "note.mp3", []byte("mp3"))
	require.NoError(t, err)
	_, err = store.Save(context.Background(), scope, "clip.mp4", []byte("mp4"))
	require.NoError(t, err)
	_, err = store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)
	_, err = store.SaveWithMetadata(
		context.Background(),
		scope,
		"video-note",
		"video/mp4",
		[]byte("mp4"),
	)
	require.NoError(t, err)

	svc := New(Config{
		StateDir:   stateDir,
		GatewayURL: "http://127.0.0.1:8080",
		AdminURL:   "http://127.0.0.1:19789",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeSessions, nil)

	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "<img src=\"/uploads/file?")
	require.Contains(t, rr.Body.String(), "<audio controls")
	require.Contains(t, rr.Body.String(), "<video controls")
	require.Contains(t, rr.Body.String(), ">open preview</a>")
	require.Contains(t, rr.Body.String(), "<code>video/mp4</code>")
}

func TestServiceUploadsJSON_RewritesGeneratedNames(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.SaveWithMetadata(
		context.Background(),
		scope,
		"file_10.mp4",
		"video/mp4",
		[]byte("mp4"),
	)
	require.NoError(t, err)

	svc := New(Config{StateDir: stateDir})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeUploadsJSON, nil)

	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "\"name\": \"video.mp4\"")
	require.NotContains(t, rr.Body.String(), "\"name\": \"file_10.mp4\"")
}

func TestDebugStatusForSession_SkipsBadTraceRefs(t *testing.T) {
	t.Parallel()

	debugRoot := t.TempDir()
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:1",
		"req-1",
		now,
		"trace-1",
	)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:2",
		"req-2",
		now.Add(-time.Minute),
		"trace-2",
	)

	badRefDir := filepath.Join(
		debugRoot,
		debugBySessionDir,
		"telegram:dm:1",
		now.Format("20060102"),
		"bad-ref",
	)
	require.NoError(t, os.MkdirAll(badRefDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(badRefDir, debugMetaTraceRefName),
		[]byte("{"),
		0o600,
	))

	status := New(Config{DebugDir: debugRoot}).debugStatusForSession(
		"telegram:dm:1",
	)
	require.True(t, status.Enabled)
	require.Equal(t, 1, status.SessionCount)
	require.Equal(t, 1, status.TraceCount)
	require.Len(t, status.Sessions, 1)
	require.Equal(t, "telegram:dm:1", status.Sessions[0].SessionID)
	require.NotEmpty(t, status.Error)
}

func TestUploadRuntimeHelpers_LimitsAndFilters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	listed := []uploads.ListedFile{
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u2",
				SessionID: "s2",
			},
			Name:         "file_10.mp4",
			RelativePath: "telegram/u2/s2/file_10.mp4",
			MimeType:     "video/mp4",
			SizeBytes:    7,
			ModifiedAt:   now,
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u1",
				SessionID: "s1",
			},
			Name:         "report.pdf",
			RelativePath: "telegram/u1/s1/report.pdf",
			MimeType:     "application/pdf",
			Source:       uploads.SourceDerived,
			SizeBytes:    5,
			ModifiedAt:   now.Add(-time.Minute),
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u1",
				SessionID: "s1",
			},
			Name:         "voice.ogg",
			RelativePath: "telegram/u1/s1/voice.ogg",
			MimeType:     "audio/ogg",
			Source:       uploads.SourceInbound,
			SizeBytes:    3,
			ModifiedAt:   now.Add(-2 * time.Minute),
		},
		{
			Scope: uploads.Scope{
				Channel:   "telegram",
				UserID:    "u0",
				SessionID: "s3",
			},
			Name:         "clip.mp4",
			RelativePath: "telegram/u0/s3/clip.mp4",
			MimeType:     "video/mp4",
			Source:       uploads.SourceDerived,
			SizeBytes:    9,
			ModifiedAt:   now,
		},
	}

	views, totalBytes := uploadViewsFromList(listed, 1)
	require.Equal(t, int64(24), totalBytes)
	require.Len(t, views, 1)
	require.Equal(t, "video.mp4", views[0].Name)
	require.Contains(t, views[0].OpenURL, queryPath+"=")
	require.Contains(t, views[0].DownloadURL, queryDownload+"=1")

	sessions := uploadSessionsFromList(listed, 0)
	require.Len(t, sessions, 3)
	require.Equal(t, "u0", sessions[0].UserID)
	require.Equal(t, "u2", sessions[1].UserID)
	require.Equal(t, "u1", sessions[2].UserID)

	limitedSessions := uploadSessionsFromList(listed, 2)
	require.Len(t, limitedSessions, 2)

	kindCounts := uploadKindCountsFromList(listed)
	require.Len(t, kindCounts, 3)
	require.Equal(t, "video", kindCounts[0].Kind)
	require.Equal(t, 2, kindCounts[0].Count)

	sourceCounts := uploadSourceCountsFromList(listed)
	require.Len(t, sourceCounts, 3)
	require.Equal(t, uploads.SourceDerived, sourceCounts[0].Source)
	require.Equal(t, 2, sourceCounts[0].Count)
	require.Equal(t, "unknown", sourceCounts[2].Source)

	filtered := filterUploadList(
		listed,
		uploadFilters{
			UserID:   "u1",
			Kind:     "pdf",
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
	)
	require.Len(t, filtered, 1)
	require.Equal(t, "report.pdf", filtered[0].Name)

	require.Equal(t, listed, filterUploadList(listed, uploadFilters{}))
	require.Nil(t, filterUploadList(nil, uploadFilters{}))
	require.Nil(t, uploadKindCountsFromList(nil))
	require.Nil(t, uploadSourceCountsFromList(nil))
}

func TestUploadsStatusFiltered_StateErrors(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		uploadsStatus{},
		New(Config{}).uploadsStatusFiltered(uploadFilters{}, 0, 0),
	)

	badState := filepath.Join(t.TempDir(), "state-file")
	require.NoError(t, os.WriteFile(badState, []byte("x"), 0o600))

	status := New(Config{StateDir: badState}).uploadsStatusFiltered(
		uploadFilters{},
		0,
		0,
	)
	require.False(t, status.Enabled)
	require.NotEmpty(t, status.Error)
}

func TestServiceHelperFunctions(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusCreated, map[string]string{"ok": "yes"})
	require.Equal(t, http.StatusCreated, rr.Code)
	require.Contains(t, rr.Body.String(), "\"ok\": \"yes\"")

	errRR := httptest.NewRecorder()
	writeJSON(
		errRR,
		http.StatusOK,
		map[string]any{"bad": make(chan int)},
	)
	require.Equal(t, http.StatusInternalServerError, errRR.Code)

	require.Empty(t, fallbackJobName(nil))
	require.Equal(
		t,
		"job-1",
		fallbackJobName(&cron.Job{ID: " job-1 "}),
	)
	require.Equal(
		t,
		"named",
		fallbackJobName(&cron.Job{Name: " named ", ID: "job-2"}),
	)
	require.Equal(t, "short", summarizeText(" short ", 0))
	require.Equal(t, "abc...", summarizeText("abcdef", 3))
	require.Equal(t, 5, intFromMap(5))
	require.Zero(t, intFromMap("bad"))
	require.Nil(t, stringSliceFromMap(nil))
	require.Equal(
		t,
		[]string{"a", "b"},
		stringSliceFromMap([]string{"b", "a"}),
	)
	require.Nil(t, stringSliceFromMap("bad"))

	now := time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC)
	require.Equal(t, "-", formatTime(time.Time{}))
	require.Equal(t, "-", formatTime((*time.Time)(nil)))
	require.Equal(
		t,
		now.Local().Format(formatTimeLayout),
		formatTime(now),
	)
	require.Equal(t, "-", formatTime("bad"))
	require.Equal(t, "-", formatUptime(time.Time{}, now))
	require.Equal(t, "0s", formatUptime(now, now.Add(-time.Minute)))

	require.Equal(t, uploadFilters{}, uploadFiltersFromRequest(nil))
	req := httptest.NewRequest(
		http.MethodGet,
		"/?"+url.Values{
			queryChannel:   []string{" telegram "},
			queryUserID:    []string{" u1 "},
			querySessionID: []string{" s1 "},
			queryKind:      []string{" pdf "},
			queryMimeType:  []string{" application/pdf "},
			querySource:    []string{" derived "},
		}.Encode(),
		nil,
	)
	require.Equal(
		t,
		uploadFilters{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "s1",
			Kind:      "pdf",
			MimeType:  "application/pdf",
			Source:    "derived",
		},
		uploadFiltersFromRequest(req),
	)
}

func TestServiceResolveDebugFileAndMethodChecks(t *testing.T) {
	t.Parallel()

	debugDir := t.TempDir()
	traceDir := filepath.Join(debugDir, "20260307", "trace")
	require.NoError(t, os.MkdirAll(traceDir, 0o755))
	metaPath := filepath.Join(traceDir, debugMetaFileName)
	require.NoError(t, os.WriteFile(metaPath, []byte("{}"), 0o600))

	svc := New(Config{DebugDir: debugDir})
	got, err := svc.resolveDebugFile(
		"20260307/trace",
		debugMetaFileName,
		"",
	)
	require.NoError(t, err)
	require.Equal(t, metaPath, got)

	serviceLog := filepath.Join(debugDir, "services", "browser-server.log")
	require.NoError(
		t,
		os.MkdirAll(filepath.Dir(serviceLog), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(serviceLog, []byte("ready\n"), 0o600),
	)

	got, err = svc.resolveDebugFile("", "", "services/browser-server.log")
	require.NoError(t, err)
	require.Equal(t, serviceLog, got)

	_, err = svc.resolveDebugFile("", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("20260307/trace", "notes.txt", "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("../escape", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("20260307/missing", debugMetaFileName, "")
	require.Error(t, err)
	_, err = svc.resolveDebugFile("", "", "../escape")
	require.Error(t, err)

	handler := svc.Handler()
	routes := []string{
		routeStatusJSON,
		routeJobsJSON,
		routeExecSessionsJSON,
		routeUploadsJSON,
		routeUploadSessions,
		routeDebugSessionsJSON,
		routeDebugTracesJSON,
	}
	for _, route := range routes {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, route, nil)
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	}
}
