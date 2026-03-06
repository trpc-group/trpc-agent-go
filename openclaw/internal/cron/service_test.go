//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stubRunner struct {
	mu        sync.Mutex
	userID    string
	sessionID string
	message   model.Message
	runOpts   agent.RunOptions
	reply     string
}

func (s *stubRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	s.mu.Lock()
	s.userID = userID
	s.sessionID = sessionID
	s.message = message
	var runOpts agent.RunOptions
	for _, opt := range opts {
		opt(&runOpts)
	}
	s.runOpts = runOpts
	reply := s.reply
	s.mu.Unlock()

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(reply),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (s *stubRunner) Close() error { return nil }

type stubSender struct {
	mu     sync.Mutex
	target string
	text   string
}

func (s *stubSender) ID() string { return "telegram" }

func (s *stubSender) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *stubSender) SendText(
	ctx context.Context,
	target string,
	text string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = target
	s.text = text
	return nil
}

func TestServiceRunNowSendsToDeliveryTarget(t *testing.T) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &stubRunner{reply: "resource report"}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "report",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	waitFor(t, func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return sender.text == "resource report"
	})

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, "telegram:user", runner.userID)
	require.True(t, strings.HasPrefix(runner.sessionID, "cron:"+job.ID))
	require.Contains(t, runner.message.Content, "collect system resources")
	require.Contains(
		t,
		runner.message.Content,
		"Execute the following existing scheduled job once now",
	)
	require.Equal(
		t,
		"telegram",
		runner.runOpts.RuntimeState["openclaw.delivery.channel"],
	)
	require.Equal(
		t,
		"100",
		runner.runOpts.RuntimeState["openclaw.delivery.target"],
	)
	require.Equal(
		t,
		true,
		runner.runOpts.RuntimeState[runtimeStateScheduledRun],
	)
	require.Equal(
		t,
		job.ID,
		runner.runOpts.RuntimeState[runtimeStateJobID],
	)
}

func TestToolAddUsesCurrentSessionDelivery(t *testing.T) {
	svc, err := NewService(t.TempDir(), &stubRunner{reply: "ok"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tool := NewTool(svc)
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				session.NewSession("app", "telegram:user", "telegram:dm:321"),
			),
		),
	)

	result, err := tool.Call(
		ctx,
		[]byte(`{
			"action":"add",
			"name":"report",
			"message":"report status",
			"schedule_kind":"every",
			"every":"1m"
		}`),
	)
	require.NoError(t, err)

	job := result.(*Job)
	require.Equal(t, outbound.DeliveryTarget{
		Channel: "telegram",
		Target:  "321",
	}, job.Delivery)
}

func TestServiceTriggerDueRunsPastAtJob(t *testing.T) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	runner := &stubRunner{reply: "done"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "once",
		Enabled: true,
		Schedule: Schedule{
			Kind: ScheduleKindAt,
			At:   "2026-03-06T14:59:00Z",
		},
		Message: "say done",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "999",
		},
	})
	require.NoError(t, err)

	svc.triggerDue(context.Background())

	waitFor(t, func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return sender.text == "done"
	})

	jobs := svc.List()
	require.Len(t, jobs, 1)
	require.Equal(t, job.ID, jobs[0].ID)
	require.False(t, jobs[0].Enabled)
	require.Equal(t, StatusSucceeded, jobs[0].LastStatus)
}

func TestServiceRecurringJobsKeepFixedCadence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &stubRunner{reply: "done"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "report",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, job.NextRunAt)

	now = job.NextRunAt.Add(15 * time.Second)
	svc.triggerDue(context.Background())

	waitFor(t, func() bool {
		jobs := svc.List()
		return len(jobs) == 1 &&
			jobs[0].LastStatus == StatusSucceeded
	})

	jobs := svc.List()
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].NextRunAt)
	require.Equal(t, job.NextRunAt.Add(time.Minute), *jobs[0].NextRunAt)
}

func TestNewServicePreservesLoadedNextRunAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	router := outbound.NewRouter()
	runner := &stubRunner{reply: "done"}
	initialNow := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)

	svc, err := NewService(
		dir,
		runner,
		router,
		WithClock(func() time.Time { return initialNow }),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "report",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "telegram:user",
	})
	require.NoError(t, err)
	require.NotNil(t, job.NextRunAt)
	require.NoError(t, svc.Close())

	reloadNow := initialNow.Add(10 * time.Minute)
	reloaded, err := NewService(
		dir,
		runner,
		router,
		WithClock(func() time.Time { return reloadNow }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reloaded.Close())
	})

	jobs := reloaded.List()
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].NextRunAt)
	require.Equal(t, *job.NextRunAt, *jobs[0].NextRunAt)
}

func TestServiceRunNowDoesNotShiftSchedule(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &stubRunner{reply: "done"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "report",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, job.NextRunAt)

	now = now.Add(10 * time.Second)
	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	waitFor(t, func() bool {
		jobs := svc.List()
		return len(jobs) == 1 &&
			jobs[0].LastStatus == StatusSucceeded
	})

	jobs := svc.List()
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].NextRunAt)
	require.Equal(t, *job.NextRunAt, *jobs[0].NextRunAt)
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition was not met")
}
