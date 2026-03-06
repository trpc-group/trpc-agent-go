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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
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
