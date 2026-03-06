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
	require.Equal(t, "collect system resources", runner.message.Content)
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
