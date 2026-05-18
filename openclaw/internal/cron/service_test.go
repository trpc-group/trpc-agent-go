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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stubRunner struct {
	mu        sync.Mutex
	userID    string
	sessionID string
	message   model.Message
	runOpts   agent.RunOptions
	messages  []string
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
	s.messages = append(s.messages, message.Content)
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

func (s *stubRunner) messagesSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, len(s.messages))
	copy(out, s.messages)
	return out
}

type toolCallingRunner struct {
	mu         sync.Mutex
	router     *outbound.Router
	args       []byte
	reply      string
	toolResult any
	toolErr    error
}

func (r *toolCallingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	var runOpts agent.RunOptions
	for _, opt := range opts {
		opt(&runOpts)
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", userID, sessionID),
		),
		agent.WithInvocationRunOptions(runOpts),
	)
	toolCtx := agent.NewInvocationContext(ctx, inv)
	result, err := outbound.NewTool(r.router).Call(toolCtx, r.args)

	r.mu.Lock()
	r.toolResult = result
	r.toolErr = err
	r.mu.Unlock()

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(r.reply),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (r *toolCallingRunner) Close() error { return nil }

func (r *toolCallingRunner) resultSnapshot() (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.toolResult, r.toolErr
}

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

type replaceAwareRunner struct {
	mu      sync.Mutex
	started []chan struct{}
	ctxs    []context.Context
	release chan struct{}
	reply   string
	callCnt int
}

func (r *replaceAwareRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	started := make(chan struct{})
	r.started = append(r.started, started)
	r.ctxs = append(r.ctxs, ctx)
	r.callCnt++
	r.mu.Unlock()
	close(started)

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case <-r.release:
		}
		select {
		case <-ctx.Done():
			return
		default:
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

func (r *replaceAwareRunner) Close() error { return nil }

func (r *replaceAwareRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCnt
}

func (r *replaceAwareRunner) ctxAt(index int) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.ctxs) {
		return nil
	}
	return r.ctxs[index]
}

type stubSender struct {
	mu      sync.Mutex
	target  string
	text    string
	files   []channel.OutboundFile
	err     error
	errs    []error
	count   int
	records []sendRecord
}

type sendRecord struct {
	target string
	text   string
	files  []channel.OutboundFile
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
	s.records = append(s.records, sendRecord{
		target: target,
		text:   text,
	})
	s.count++
	return s.nextErrLocked()
}

func (s *stubSender) SendMessage(
	_ context.Context,
	target string,
	msg channel.OutboundMessage,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = target
	s.text = msg.Text
	s.files = append([]channel.OutboundFile(nil), msg.Files...)
	s.records = append(s.records, sendRecord{
		target: target,
		text:   msg.Text,
		files:  append([]channel.OutboundFile(nil), msg.Files...),
	})
	s.count++
	return s.nextErrLocked()
}

func (s *stubSender) nextErrLocked() error {
	if len(s.errs) == 0 {
		return s.err
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}

func (s *stubSender) countSnapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.count
}

func (s *stubSender) recordsSnapshot() []sendRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]sendRecord, len(s.records))
	for i, record := range s.records {
		records[i] = sendRecord{
			target: record.target,
			text:   record.text,
			files:  append([]channel.OutboundFile(nil), record.files...),
		}
	}
	return records
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

func TestServiceRunNowSuppressesFinalAfterMessageToolSend(
	t *testing.T,
) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)

	runner := &toolCallingRunner{
		router: router,
		args: []byte(
			`{"text":"提醒 wineguo：现在喝点水。"}`,
		),
		reply: "已在当前聊天中提醒 wineguo：现在喝点水。",
	}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithDebugRecorder(rec),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "drink water",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "remind wineguo to drink water",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	waitForJobStatus(t, svc, job.ID, StatusSucceeded)

	result, toolErr := runner.resultSnapshot()
	require.NoError(t, toolErr)
	require.Equal(t, true, result.(map[string]any)["ok"])

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Equal(t, "100", sender.target)
	require.Equal(t, "提醒 wineguo：现在喝点水。", sender.text)
	require.Equal(t, 1, sender.count)

	traceDir := waitForCronTrace(t, rec.Dir())
	events, err := debugrecorder.ReadEventsFile(traceDir)
	require.NoError(t, err)
	require.Contains(t, string(events), "duplicate_message_tool_text")
	require.Contains(t, string(events), cronDeliverySkipMessageToolTarget)
}

func TestServiceRunNowDeliversFinalTextWhenMessageToolFails(
	t *testing.T,
) {
	router := outbound.NewRouter()
	sender := &stubSender{
		errs: []error{context.DeadlineExceeded, nil},
	}
	router.RegisterSender(sender)

	runner := &toolCallingRunner{
		router: router,
		args:   []byte(`{"text":"report ready"}`),
		reply:  "report ready",
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
		Message: "send report",
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
		return sender.countSnapshot() == 2
	})

	result, toolErr := runner.resultSnapshot()
	require.Nil(t, result)
	require.ErrorIs(t, toolErr, context.DeadlineExceeded)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Equal(t, "100", sender.target)
	require.Equal(t, "report ready", sender.text)
	require.Equal(t, 2, sender.count)
}

func TestServiceRunNowDeliversSameTextWhenMessageToolSendsFile(
	t *testing.T,
) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)
	router.RegisterMessageSender(sender)

	runner := &toolCallingRunner{
		router: router,
		args: []byte(
			`{"text":"report ready","file":"report.pdf"}`,
		),
		reply: "report ready",
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
		Message: "send report",
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
		return sender.countSnapshot() == 2
	})

	result, toolErr := runner.resultSnapshot()
	require.NoError(t, toolErr)
	require.Equal(t, true, result.(map[string]any)["ok"])

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Equal(t, "100", sender.target)
	require.Equal(t, "report ready", sender.text)
	require.Equal(t, 2, sender.count)
}

func TestServiceRunNowSuppressesWhitespaceDifferentFinalText(
	t *testing.T,
) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &toolCallingRunner{
		router: router,
		args:   []byte(`{"text":"report ready"}`),
		reply:  "report ready\n",
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
		Message: "send report",
		UserID:  "telegram:user",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	waitForJobStatus(t, svc, job.ID, StatusSucceeded)

	result, toolErr := runner.resultSnapshot()
	require.NoError(t, toolErr)
	require.Equal(t, true, result.(map[string]any)["ok"])

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Equal(t, "100", sender.target)
	require.Equal(t, "report ready", sender.text)
	require.Equal(t, 1, sender.count)
}

func TestServiceRunNowDeliversFinalAfterDifferentTargetToolSend(
	t *testing.T,
) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	runner := &toolCallingRunner{
		router: router,
		args: []byte(
			`{"text":"preparing report","channel":"telegram","target":"200"}`,
		),
		reply: "report ready",
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
		Message: "prepare report",
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
		return sender.countSnapshot() == 2
	})

	result, toolErr := runner.resultSnapshot()
	require.NoError(t, toolErr)
	require.Equal(t, true, result.(map[string]any)["ok"])

	sender.mu.Lock()
	require.Equal(t, "100", sender.target)
	require.Equal(t, "report ready", sender.text)
	require.Equal(t, 2, sender.count)
	sender.mu.Unlock()

	records := sender.recordsSnapshot()
	require.Len(t, records, 2)
	require.Equal(t, "200", records[0].target)
	require.Equal(t, "preparing report", records[0].text)
	require.Equal(t, "100", records[1].target)
	require.Equal(t, "report ready", records[1].text)
}

func TestServiceRunNowWritesDebugTrace(t *testing.T) {
	router := outbound.NewRouter()
	sender := &stubSender{}
	router.RegisterSender(sender)

	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)

	runner := &stubRunner{reply: "resource report"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithDebugRecorder(rec),
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

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	traceDir := waitForCronTrace(t, rec.Dir())
	events, err := debugrecorder.ReadEventsFile(traceDir)
	require.NoError(t, err)
	rawEvents := string(events)
	require.Contains(t, rawEvents, debugrecorder.KindCronRun)
	require.Contains(t, rawEvents, debugrecorder.KindRunnerEvent)
	require.Contains(t, rawEvents, debugrecorder.KindCronDelivery)
	require.Contains(t, rawEvents, "resource report")

	meta := readCronTraceMeta(t, traceDir)
	require.Equal(t, "cron", meta.Start.Source)
	require.Equal(t, "telegram", meta.Start.Channel)
	require.Equal(t, "telegram:user", meta.Start.UserID)
	require.True(t, strings.HasPrefix(meta.Start.SessionID, cronSessionPrefix))
	require.Empty(t, meta.Start.TraceID)

	result := readCronTraceResult(t, traceDir)
	require.Equal(t, StatusSucceeded, result.Status)
	require.Empty(t, result.Error)
}

func TestServiceRunNowDebugTraceRecordsDeliveryFailure(t *testing.T) {
	router := outbound.NewRouter()
	sender := &stubSender{err: context.DeadlineExceeded}
	router.RegisterSender(sender)

	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)

	runner := &stubRunner{reply: "resource report"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		router,
		WithDebugRecorder(rec),
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

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	waitFor(t, func() bool {
		got := svc.Get(job.ID)
		return got != nil && got.LastStatus == StatusDeliveryFailed
	})

	traceDir := waitForCronTrace(t, rec.Dir())
	events, err := debugrecorder.ReadEventsFile(traceDir)
	require.NoError(t, err)
	rawEvents := string(events)
	require.Contains(t, rawEvents, debugrecorder.KindCronDelivery)
	require.Contains(t, rawEvents, context.DeadlineExceeded.Error())

	result := readCronTraceResult(t, traceDir)
	require.Equal(t, StatusDeliveryFailed, result.Status)
	require.Equal(t, context.DeadlineExceeded.Error(), result.Error)
}

func TestServiceRunNowAppliesRuntimeProfile(t *testing.T) {
	runner := &stubRunner{reply: "ok"}
	resolver := runtimeprofile.NewMapResolver(runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			"retail": {
				AppName: "retail-app",
				Version: "v2",
				Prompt: runtimeprofile.Prompt{
					Instruction: "retail instruction",
				},
			},
		},
	})
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithRuntimeProfileResolver(resolver),
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
		Profile: &RuntimeProfileRef{
			ID: "retail",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)
	waitFor(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.messages) > 0
	})

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, "retail-app", runner.runOpts.AppName)
	require.Equal(t, "retail instruction", runner.runOpts.Instruction)
	require.Equal(
		t,
		"retail",
		runner.runOpts.RuntimeState[runtimeprofile.RuntimeStateProfileID],
	)
	require.Equal(
		t,
		"v2",
		runner.runOpts.RuntimeState[runtimeprofile.RuntimeStateProfileVersion],
	)
}

func TestServiceRunNowResolvesRuntimeProfileWithRequestMetadata(
	t *testing.T,
) {
	runner := &stubRunner{reply: "ok"}
	requests := make(chan runtimeprofile.Request, 1)
	resolver := runtimeprofile.ResolverFunc(func(
		ctx context.Context,
		req runtimeprofile.Request,
	) (runtimeprofile.Profile, error) {
		requests <- req
		return runtimeprofile.Profile{
			ID:      req.ProfileID,
			AppName: "retail-app",
		}, nil
	})
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithRuntimeProfileResolver(resolver),
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
		Profile: &RuntimeProfileRef{
			ID:        "retail",
			Channel:   "wecom",
			TenantID:  "tenant-a",
			SessionID: "session-a",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)

	var got runtimeprofile.Request
	select {
	case got = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime profile request was not observed")
	}
	require.Equal(t, "retail", got.ProfileID)
	require.Equal(t, "wecom", got.Channel)
	require.Equal(t, "tenant-a", got.TenantID)
	require.Equal(t, "telegram:user", got.UserID)
	require.Equal(t, "session-a", got.SessionID)
}

func TestJobProfileJSONOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Job{ID: "job-1"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"profile"`)
}

func TestServiceRunNowFailsClosedForMissingRuntimeProfile(
	t *testing.T,
) {
	runner := &stubRunner{reply: "ok"}
	svc, err := NewService(t.TempDir(), runner, nil)
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
		Profile: &RuntimeProfileRef{
			ID:      "retail",
			Version: "v1",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)
	waitFor(t, func() bool {
		got := svc.Get(job.ID)
		return got != nil && got.LastStatus == StatusFailed
	})

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Empty(t, runner.messages)
}

func TestServiceRunNowFailsOnRuntimeProfileVersionMismatch(
	t *testing.T,
) {
	runner := &stubRunner{reply: "ok"}
	resolver := runtimeprofile.NewMapResolver(runtimeprofile.Config{
		Profiles: map[string]runtimeprofile.Profile{
			"retail": {
				AppName: "retail-app",
				Version: "v2",
			},
		},
	})
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithRuntimeProfileResolver(resolver),
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
		Profile: &RuntimeProfileRef{
			ID:      "retail",
			Version: "v1",
		},
	})
	require.NoError(t, err)

	_, err = svc.RunNow(job.ID)
	require.NoError(t, err)
	waitFor(t, func() bool {
		got := svc.Get(job.ID)
		return got != nil && got.LastStatus == StatusFailed &&
			strings.Contains(got.LastError, "version mismatch")
	})
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

func TestServiceDisablesJobAfterMaxRuns(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 20, 0, 0, 0, time.UTC)
	runner := &stubRunner{reply: "tick"}
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "limited",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1s",
		},
		Policy: ExecutionPolicy{
			MaxRuns: 2,
		},
		Message: "say hi",
		UserID:  "user-1",
	})
	require.NoError(t, err)

	now = now.Add(time.Second)
	svc.triggerDue(context.Background())
	waitFor(t, func() bool {
		got := svc.Get(job.ID)
		return got != nil && got.LastStatus == StatusSucceeded
	})

	first := svc.Get(job.ID)
	require.NotNil(t, first)
	require.True(t, first.Enabled)
	require.Equal(t, 1, first.Stats.RunCount)
	require.Equal(t, 1, first.Stats.SuccessCount)
	require.NotNil(t, first.NextRunAt)
	firstMessages := runner.messagesSnapshot()
	require.Len(t, firstMessages, 1)
	require.Contains(t, firstMessages[0], "- run_index: 1")
	require.Contains(t, firstMessages[0], "- max_runs: 2")
	require.Contains(t, firstMessages[0], "- remaining_runs: 1")
	require.Contains(t, firstMessages[0], "- is_final_run: false")

	now = now.Add(time.Second)
	svc.triggerDue(context.Background())
	waitFor(t, func() bool {
		got := svc.Get(job.ID)
		return got != nil &&
			got.LastStatus == StatusSucceeded &&
			!got.Enabled
	})

	second := svc.Get(job.ID)
	require.NotNil(t, second)
	require.False(t, second.Enabled)
	require.Nil(t, second.NextRunAt)
	require.Equal(t, 2, second.Stats.RunCount)
	require.Equal(t, 2, second.Stats.SuccessCount)
	secondMessages := runner.messagesSnapshot()
	require.Len(t, secondMessages, 2)
	require.Contains(t, secondMessages[1], "- run_index: 2")
	require.Contains(t, secondMessages[1], "- max_runs: 2")
	require.Contains(t, secondMessages[1], "- remaining_runs: 0")
	require.Contains(t, secondMessages[1], "- is_final_run: true")
}

func TestServiceReplaceOverlapPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 21, 0, 0, 0, time.UTC)
	runner := &replaceAwareRunner{
		release: make(chan struct{}),
		reply:   "done",
	}
	svc, err := NewService(
		t.TempDir(),
		runner,
		nil,
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	job, err := svc.Add(&Job{
		Name:    "replace",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1s",
		},
		Policy: ExecutionPolicy{
			OverlapPolicy: OverlapPolicyReplace,
		},
		Message: "report",
		UserID:  "user-1",
	})
	require.NoError(t, err)

	now = now.Add(time.Second)
	svc.triggerDue(context.Background())
	waitFor(t, func() bool {
		return runner.calls() == 1
	})

	firstCtx := runner.ctxAt(0)
	require.NotNil(t, firstCtx)

	now = now.Add(time.Second)
	svc.triggerDue(context.Background())
	waitFor(t, func() bool {
		return runner.calls() == 2
	})

	waitFor(t, func() bool {
		select {
		case <-firstCtx.Done():
			return true
		default:
			return false
		}
	})

	close(runner.release)
	waitFor(t, func() bool {
		return svc.Status()["jobs_running"] == 0
	})

	got := svc.Get(job.ID)
	require.NotNil(t, got)
	require.True(t, got.Enabled)
	require.Equal(t, 2, got.Stats.RunCount)
	require.Equal(t, 1, got.Stats.SuccessCount)
	require.Equal(t, StatusSucceeded, got.LastStatus)
}

func TestServiceRunNow_DeliveryFailureWithoutRouter(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{reply: "done"}
	svc, err := NewService(t.TempDir(), runner, nil)
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
		got := svc.Get(job.ID)
		return got != nil &&
			got.LastStatus == StatusDeliveryFailed
	})

	got := svc.Get(job.ID)
	require.NotNil(t, got)
	require.Equal(t, 1, got.Stats.RunCount)
	require.Equal(t, 1, got.Stats.DeliveryFailureCount)
	require.Equal(t, StatusDeliveryFailed, got.LastStatus)
	require.Contains(t, got.LastError, "nil outbound router")
}

func TestService_RunStateHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC)
	svc := &Service{
		clock:   func() time.Time { return now },
		jobs:    make(map[string]*Job),
		running: make(map[string]*jobRun),
	}

	_, _, _, err := svc.markRunning("missing", context.Background())
	require.ErrorContains(t, err, "unknown job")

	job := &Job{
		ID:      "job-1",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
	}
	svc.jobs[job.ID] = job
	svc.running[job.ID] = &jobRun{}
	_, _, _, err = svc.markRunning(job.ID, context.Background())
	require.ErrorContains(t, err, "already running")

	svc.running = make(map[string]*jobRun)
	job.Policy.MaxRuns = 1
	job.Stats.RunCount = 1
	_, _, _, err = svc.markRunning(job.ID, context.Background())
	require.ErrorContains(t, err, "no longer schedulable")
	require.False(t, job.Enabled)

	job.Enabled = true
	job.Stats.RunCount = 0
	job.Policy.MaxRuns = 0
	clone, runCtx, token, err := svc.markRunning(job.ID, nil)
	require.NoError(t, err)
	require.Equal(t, job.ID, clone.ID)
	require.NotNil(t, runCtx)
	require.NotEmpty(t, token)
	require.Equal(t, StatusRunning, job.LastStatus)
	require.Equal(t, 1, job.Stats.RunCount)
	require.True(t, svc.deliveryAllowed(job.ID, token))

	svc.mu.Lock()
	svc.suppressRunLocked(job.ID)
	svc.mu.Unlock()
	require.False(t, svc.deliveryAllowed(job.ID, token))

	svc.mu.Lock()
	svc.removeJobLocked(job.ID, false)
	require.Nil(t, svc.jobs[job.ID])
	require.NotNil(t, svc.running[job.ID])
	svc.removeJobLocked(job.ID, true)
	svc.mu.Unlock()
	require.ErrorIs(t, runCtx.Err(), context.Canceled)
}

func TestService_RetireAndRunPolicyHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	require.False(t, retireJobLocked(nil, now))
	require.False(t, retireJobLocked(&Job{Enabled: false}, now))

	job := &Job{
		Enabled: true,
		Policy: ExecutionPolicy{
			MaxRuns: 2,
			EndsAt:  &future,
		},
		Stats: ExecutionStats{
			RunCount: 2,
		},
	}
	require.True(t, executionLimitReached(job))
	require.True(t, retireJobLocked(job, now))
	require.False(t, job.Enabled)
	require.Nil(t, job.NextRunAt)

	next := now.Add(30 * time.Minute)
	job = &Job{
		Enabled: true,
		Policy: ExecutionPolicy{
			EndsAt: &future,
		},
	}
	require.False(t, executionWindowClosed(job, now))
	require.True(t, nextRunAllowed(job, &next, now))
	applyNextRunPolicy(job, &next, now)
	require.NotNil(t, job.NextRunAt)
	require.Equal(t, next, *job.NextRunAt)

	job.Policy.EndsAt = timePointer(now)
	require.False(t, nextRunAllowed(job, &next, now))
	applyNextRunPolicy(job, &next, now)
	require.False(t, job.Enabled)
	require.Nil(t, job.NextRunAt)

	require.Equal(t, now, scheduledRunBase(nil, now))
	require.NotNil(t, scheduledRunRuntimeState(&Job{}))
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
		Policy: ExecutionPolicy{
			MaxRuns: 3,
		},
		Stats: ExecutionStats{
			RunCount: 2,
		},
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "1",
		},
	})
	require.Equal(t, true, runtimeState[runtimeStateScheduledRun])
	require.Equal(t, "telegram", runtimeState["openclaw.delivery.channel"])
	require.Equal(t, "1", runtimeState["openclaw.delivery.target"])
	require.Equal(t, "job-1", runtimeState[runtimeStateJobID])
	require.Equal(t, 2, runtimeState[runtimeStateRunIndex])
	require.Equal(t, true, runtimeState[runtimeStateHasMaxRuns])
	require.Equal(t, 3, runtimeState[runtimeStateMaxRuns])
	require.Equal(t, 1, runtimeState[runtimeStateRemaining])
	require.Equal(t, false, runtimeState[runtimeStateIsFinalRun])
	require.Contains(
		t,
		buildScheduledRunMessage(&Job{
			Message: "collect {{.Cron.RunIndex}}/" +
				"{{.Cron.MaxRuns}}{{if .Cron.IsFinalRun}}" +
				" final{{end}}",
			Policy: ExecutionPolicy{
				MaxRuns: 3,
			},
			Stats: ExecutionStats{
				RunCount: 2,
			},
		}),
		"collect 2/3",
	)
	require.Contains(
		t,
		buildScheduledRunMessage(&Job{
			Message: "collect {{.Cron.RunIndex}}/" +
				"{{.Cron.MaxRuns}}{{if .Cron.IsFinalRun}}" +
				" final{{end}}",
			Policy: ExecutionPolicy{
				MaxRuns: 3,
			},
			Stats: ExecutionStats{
				RunCount: 3,
			},
		}),
		"collect 3/3 final",
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
	require.Equal(
		t,
		"report {{.Cron.Missing}}",
		renderScheduledRunTask(
			"report {{.Cron.Missing}}",
			cronRunTemplateData{},
		),
	)
	require.Equal(
		t,
		"report {{",
		renderScheduledRunTask(
			"report {{",
			cronRunTemplateData{},
		),
	)

	job := &Job{
		Policy: ExecutionPolicy{
			MaxRuns: 1,
			EndsAt:  cloneTimePtr(&now),
		},
		Stats: ExecutionStats{
			RunCount: 1,
		},
	}
	require.True(t, executionLimitReached(job))
	require.True(t, executionWindowClosed(job, now))
	require.False(
		t,
		nextRunAllowed(
			job,
			cloneTimePtr(&now),
			now,
		),
	)

	applyNextRunPolicy(nil, nil, now)
	applyNextRunPolicy(&Job{}, nil, now)

	require.Equal(
		t,
		"",
		sanitizeStoredOutput(" \n\t "),
	)
}

func TestRunContextPromptRequiresScheduledExecution(t *testing.T) {
	t.Parallel()

	require.Contains(
		t,
		runContextPrompt,
		"Do not return only a statement of what you will do",
	)
	require.Contains(
		t,
		runContextPrompt,
		"perform the scheduled task and report the result",
	)
	require.Contains(
		t,
		runContextPrompt,
		"counts as the job delivery",
	)
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

func waitForJobStatus(
	t *testing.T,
	svc *Service,
	jobID string,
	status string,
) {
	t.Helper()

	waitFor(t, func() bool {
		got := svc.Get(jobID)
		return got != nil && got.LastStatus == status
	})
}

type cronTraceMeta struct {
	Start debugrecorder.TraceStart `json:"start"`
}

func waitForCronTrace(t *testing.T, root string) string {
	t.Helper()

	var matches []string
	waitFor(t, func() bool {
		var err error
		matches, err = filepath.Glob(
			filepath.Join(root, "*", "*", "result.json"),
		)
		require.NoError(t, err)
		if len(matches) == 0 {
			return false
		}
		_, err = debugrecorder.ReadEventsFile(filepath.Dir(matches[0]))
		return err == nil
	})
	return filepath.Dir(matches[0])
}

func readCronTraceMeta(t *testing.T, traceDir string) cronTraceMeta {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(traceDir, "meta.json"))
	require.NoError(t, err)

	var meta cronTraceMeta
	require.NoError(t, json.Unmarshal(data, &meta))
	return meta
}

func readCronTraceResult(
	t *testing.T,
	traceDir string,
) debugrecorder.TraceEnd {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(traceDir, "result.json"))
	require.NoError(t, err)

	var result debugrecorder.TraceEnd
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func timePointer(ts time.Time) *time.Time {
	return &ts
}
