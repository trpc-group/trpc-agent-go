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

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	reply   string

	startOnce sync.Once
}

func (r *blockingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.startOnce.Do(func() {
		close(r.started)
	})

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case <-r.release:
		}
		ch <- &event.Event{
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(r.reply),
				}},
			},
		}
	}()
	return ch, nil
}

func (r *blockingRunner) Close() error { return nil }

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

func TestServiceStartRunsDueJobsOnTicker(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &stubRunner{reply: "tick done"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithClock(func() time.Time { return now }),
		WithTickInterval(10*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	_, err = svc.Add(&Job{
		Name:    "due-now",
		Enabled: true,
		Schedule: Schedule{
			Kind: ScheduleKindAt,
			At:   now.Add(-time.Second).Format(time.RFC3339),
		},
		Message: "collect system resources",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	waitFor(t, func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return sender.text == "tick done"
	})

	status := svc.Status()
	require.Equal(t, true, status["running"])
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

func TestServiceScopesAndHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC)
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "user-1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	_, err = svc.Add(&Job{
		Name:    "other",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect system resources",
		UserID:  "user-2",
	})
	require.NoError(t, err)

	require.NotNil(t, svc.Get(job.ID))
	require.Nil(t, svc.Get(" "))
	require.Len(
		t,
		svc.ListForUser(
			"user-1",
			outbound.DeliveryTarget{Channel: "telegram"},
		),
		1,
	)

	removed, err := svc.RemoveForUser(
		"user-1",
		outbound.DeliveryTarget{Channel: "telegram", Target: "100"},
	)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Len(t, svc.ListForUser("user-1", outbound.DeliveryTarget{}), 0)
}

func TestServiceRemoveCancelsRunningDelivery(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		reply:   "should stay muted",
	}
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
		Message: "collect cpu",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	<-runner.started
	require.NoError(t, svc.Remove(job.ID))

	waitFor(t, func() bool {
		return svc.Status()["jobs_running"] == 0
	})

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Empty(t, sender.text)
	require.Nil(t, svc.Get(job.ID))
}

func TestServiceRemoveForUserCancelsRunningDelivery(t *testing.T) {
	t.Parallel()

	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
		reply:   "should stay muted",
	}
	svc, err := NewService(t.TempDir(), runner, router)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	_, err = svc.Add(&Job{
		Name:    "report",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	jobs := svc.ListForUser(
		"telegram:user",
		outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	)
	require.Len(t, jobs, 1)

	_, err = svc.RunNow(jobs[0].ID)
	require.NoError(t, err)

	<-runner.started
	removed, err := svc.RemoveForUser(
		"telegram:user",
		outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	waitFor(t, func() bool {
		return svc.Status()["jobs_running"] == 0
	})

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Empty(t, sender.text)
	require.Empty(
		t,
		svc.ListForUser(
			"telegram:user",
			outbound.DeliveryTarget{
				Channel: "telegram",
				Target:  "100",
			},
		),
	)
}

func TestServiceNormalizeAndAccumulatorHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC)

	loaded, err := normalizeLoadedJob(&Job{
		ID:      "job-1",
		Message: "report",
		UserID:  "user-1",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		LastStatus: StatusRunning,
	}, now)
	require.NoError(t, err)
	require.Equal(t, StatusIdle, loaded.LastStatus)

	created, err := normalizeNewJob(&Job{
		Message: "report",
		UserID:  "user-1",
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
	}, now)
	require.NoError(t, err)
	require.True(t, created.Enabled)

	require.True(t, matchesJobScope(&Job{
		UserID: "user-1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "1",
		},
	}, "user-1", outbound.DeliveryTarget{Channel: "telegram"}))
	require.False(t, matchesJobScope(nil, "user-1", outbound.DeliveryTarget{}))

	runtimeState := scheduledRunRuntimeState(&Job{
		ID: "job-1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "1",
		},
	})
	require.Equal(t, true, runtimeState[runtimeStateScheduledRun])
	require.Equal(t, "job-1", runtimeState[runtimeStateJobID])
	require.Contains(
		t,
		buildScheduledRunMessage(" collect cpu "),
		"collect cpu",
	)

	acc := cronReplyAccumulator{}
	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "hello"},
			}},
		},
	})
	require.Equal(t, "hello", acc.text)

	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("done"),
			}},
		},
	})
	require.Equal(t, "done", acc.text)

	acc.consume(event.NewErrorEvent("inv", "assistant", "tool", "boom"))
	require.EqualError(t, acc.err, "boom")

	require.True(t, IsRunSessionID("cron:job-1:123"))
	require.False(t, IsRunSessionID("telegram:dm:1"))
	require.Equal(t, "every 1m", ScheduleSummary(Schedule{
		Kind:  ScheduleKindEvery,
		Every: "1m",
	}))
	require.Equal(t, "cron 0 * * * *", ScheduleSummary(Schedule{
		Kind:     ScheduleKindCron,
		CronExpr: "0 * * * *",
	}))
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
