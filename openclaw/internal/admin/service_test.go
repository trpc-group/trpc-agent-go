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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
)

type stubRunner struct {
	reply string
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
		[]byte(`{"request_id":"`+requestID+`"}`+"\n"),
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
		`"message_id":"msg-` + requestID + `"}`
	require.NoError(t, os.WriteFile(
		filepath.Join(indexDir, debugMetaTraceRefName),
		[]byte(ref),
		0o600,
	))
	return traceDir
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
	require.Contains(t, body, "OpenClaw Admin")
	require.Contains(t, body, "cpu report")
	require.Contains(t, body, "127.0.0.1:8080")
	require.Contains(t, body, "telegram")
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
	)
	writeDebugTraceFixture(
		t,
		debugRoot,
		"telegram:dm:2",
		"req-2",
		now.Add(-time.Minute),
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

	sessionsRR := httptest.NewRecorder()
	sessionsReq := httptest.NewRequest(
		http.MethodGet,
		routeDebugSessionsJSON,
		nil,
	)
	handler.ServeHTTP(sessionsRR, sessionsReq)
	require.Equal(t, http.StatusOK, sessionsRR.Code)
	require.Contains(t, sessionsRR.Body.String(), "telegram:dm:1")

	traceQuery := routeDebugTracesJSON + "?" +
		querySessionID + "=" +
		url.QueryEscape("telegram:dm:1")
	tracesRR := httptest.NewRecorder()
	tracesReq := httptest.NewRequest(http.MethodGet, traceQuery, nil)
	handler.ServeHTTP(tracesRR, tracesReq)
	require.Equal(t, http.StatusOK, tracesRR.Code)
	require.Contains(t, tracesRR.Body.String(), "req-1")
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
	require.NoError(
		t,
		os.MkdirAll(filepath.Join(uploadsRoot, "telegram", "u1"), 0o755),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(uploadsRoot, "telegram", "u1", "clip.mp4"),
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

	openRR := httptest.NewRecorder()
	openReq := httptest.NewRequest(
		http.MethodGet,
		routeUploadFile+"?"+url.Values{
			queryPath: []string{"telegram/u1/clip.mp4"},
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
			queryPath:     []string{"telegram/u1/clip.mp4"},
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
