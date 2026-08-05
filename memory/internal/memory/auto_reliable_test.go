//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionstate"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionnoop "trpc.group/trpc-go/trpc-agent-go/session/noop"
)

func TestAutoMemoryWorker_PreserveHistoryResumesPendingOperations(t *testing.T) {
	firstArgs, err := json.Marshal(map[string]any{
		"memory": "User likes tea.",
		"topics": []string{"tea"},
	})
	require.NoError(t, err)
	secondArgs, err := json.Marshal(map[string]any{
		"memory": "User likes coffee.",
		"topics": []string{"coffee"},
	})
	require.NoError(t, err)
	mdl := &countingModel{mockModel: newMockModelWithToolCalls([]model.ToolCall{
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      memory.AddToolName,
				Arguments: firstArgs,
			},
		},
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      memory.AddToolName,
				Arguments: secondArgs,
			},
		},
	})}
	checkerCalls := 0
	ext := extractor.NewExtractor(
		mdl,
		extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
		extractor.WithChecker(func(*extractor.ExtractionContext) bool {
			checkerCalls++
			return checkerCalls == 1
		}),
	)
	operator := &failOnceAddOperator{
		mockOperator: newMockOperator(),
		failMemory:   "User likes coffee.",
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	appendSessionMessage(sess, eventTime, model.NewUserMessage("I like tea and coffee."))

	err = worker.EnqueueJob(context.Background(), sess)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, 1, mdl.calls)
	assert.Equal(t, []string{
		"User likes tea.",
		"User likes coffee.",
	}, operator.attempted)
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)
	pending, err := readPendingAutoMemoryBatch(sess)
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, 1, pending.Next)

	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	assert.Equal(t, 1, mdl.calls)
	assert.Equal(t, 1, checkerCalls)
	assert.Equal(t, []string{
		"User likes tea.",
		"User likes coffee.",
		"User likes coffee.",
	}, operator.attempted)
	_, ok = sess.GetState(pendingAutoMemoryBatchStateKey)
	assert.False(t, ok)
	assert.True(t, readLastExtractAt(sess).Equal(eventTime))
}

func TestAutoMemoryWorker_PendingBatchSurvivesSessionReload(t *testing.T) {
	firstArgs, err := json.Marshal(map[string]any{"memory": "User likes tea."})
	require.NoError(t, err)
	secondArgs, err := json.Marshal(map[string]any{"memory": "User likes coffee."})
	require.NoError(t, err)
	mdl := &countingModel{mockModel: newMockModelWithToolCalls([]model.ToolCall{
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name: memory.AddToolName, Arguments: firstArgs,
			},
		},
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name: memory.AddToolName, Arguments: secondArgs,
			},
		},
	})}
	ext := extractor.NewExtractor(
		mdl,
		extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
	)
	operator := &failOnceAddOperator{
		mockOperator: newMockOperator(),
		failMemory:   "User likes coffee.",
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	service := sessioninmemory.NewSessionService()
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	sess, key := newPersistedAutoMemorySession(
		t, service, eventTime,
		model.NewUserMessage("I like tea and coffee."),
	)
	ctx := sessionstate.ContextWithService(context.Background(), service)

	err = worker.EnqueueJob(ctx, sess)
	require.ErrorIs(t, err, assert.AnError)
	reloaded, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	pending, err := readPendingAutoMemoryBatch(reloaded)
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, 1, pending.Next)
	assert.Equal(t, 1, mdl.calls)

	require.NoError(t, worker.EnqueueJob(ctx, reloaded))
	finalSession, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	pending, err = readPendingAutoMemoryBatch(finalSession)
	require.NoError(t, err)
	assert.Nil(t, pending)
	assert.True(t, readLastExtractAt(finalSession).Equal(eventTime))
	assert.Equal(t, 1, mdl.calls)
	assert.Equal(t, []string{
		"User likes tea.",
		"User likes coffee.",
		"User likes coffee.",
	}, operator.attempted)
}

func TestAutoMemoryWorker_DoesNotExecuteBeforePendingBatchIsPersisted(t *testing.T) {
	mdl, ext := newCountingAddExtractor(
		t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	baseService := sessioninmemory.NewSessionService()
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	sess, key := newPersistedAutoMemorySession(
		t, baseService, eventTime, model.NewUserMessage("I like tea."),
	)
	service := &failingUpdateSessionService{
		Service: baseService,
		err:     assert.AnError,
	}
	ctx := sessionstate.ContextWithService(context.Background(), service)

	err := worker.EnqueueJob(ctx, sess)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, 1, mdl.calls)
	assert.Zero(t, operator.addCalls)
	reloaded, err := baseService.GetSession(context.Background(), key)
	require.NoError(t, err)
	_, ok := reloaded.GetState(pendingAutoMemoryBatchStateKey)
	assert.False(t, ok)
	_, ok = reloaded.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)
}

func TestAutoMemoryWorker_ReloadKeepsQueuedEventDelta(t *testing.T) {
	mdl, ext := newCountingAddExtractor(
		t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	baseService := sessioninmemory.NewSessionService()
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	sess, _ := newPersistedAutoMemorySession(
		t, baseService, eventTime, model.NewUserMessage("I like tea."),
	)
	service := &stateOnlyReloadSessionService{Service: baseService}
	ctx := sessionstate.ContextWithService(context.Background(), service)

	require.NoError(t, worker.EnqueueJob(ctx, sess))
	assert.Equal(t, 1, mdl.calls)
	assert.Equal(t, 1, operator.addCalls)
}

func TestAutoMemoryWorker_ReloadFailureStopsExtraction(t *testing.T) {
	mdl, ext := newCountingAddExtractor(
		t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	baseService := sessioninmemory.NewSessionService()
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	sess, _ := newPersistedAutoMemorySession(
		t, baseService, eventTime, model.NewUserMessage("I like tea."),
	)
	service := &failingGetSessionService{Service: baseService}
	ctx := sessionstate.ContextWithService(context.Background(), service)

	err := worker.EnqueueJob(ctx, sess)
	require.ErrorIs(t, err, assert.AnError)
	assert.Zero(t, mdl.calls)
	assert.Zero(t, operator.addCalls)
}

func TestAutoMemoryWorker_SupportsTransientSessionService(t *testing.T) {
	mdl, ext := newCountingAddExtractor(
		t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	service := sessionnoop.NewService()
	sess, err := service.CreateSession(context.Background(), session.Key{
		AppName: "app", UserID: "u1", SessionID: "test-session",
	}, nil)
	require.NoError(t, err)
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	appendSessionMessage(sess, eventTime, model.NewUserMessage("I like tea."))
	ctx := sessionstate.ContextWithService(context.Background(), service)

	require.NoError(t, worker.EnqueueJob(ctx, sess))
	assert.Equal(t, 1, mdl.calls)
	assert.Equal(t, 1, operator.addCalls)
	assert.True(t, readLastExtractAt(sess).Equal(eventTime))
}

func TestAutoMemoryWorker_SerializesStaleSessionsForUser(t *testing.T) {
	args, err := json.Marshal(map[string]any{"memory": "User likes tea."})
	require.NoError(t, err)
	mdl := &blockingCountingModel{
		mockModel: newMockModelWithToolCalls([]model.ToolCall{{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name: memory.AddToolName, Arguments: args,
			},
		}}),
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
	ext := extractor.NewExtractor(
		mdl,
		extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	service := sessioninmemory.NewSessionService()
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	first, key := newPersistedAutoMemorySession(
		t, service, eventTime, model.NewUserMessage("I like tea."),
	)
	second, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	ctx := sessionstate.ContextWithService(context.Background(), service)
	errs := make(chan error, 2)

	go func() {
		errs <- worker.processAutoMemoryDelta(
			ctx, reconcileUserKey(), first, eventTime,
			[]model.Message{model.NewUserMessage("I like tea.")},
		)
	}()
	<-mdl.firstStarted
	go func() {
		errs <- worker.processAutoMemoryDelta(
			ctx, reconcileUserKey(), second, eventTime,
			[]model.Message{model.NewUserMessage("I like tea.")},
		)
	}()
	select {
	case <-mdl.secondStarted:
		t.Fatal("second extraction started before the first user-scoped job completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(mdl.releaseFirst)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	assert.Equal(t, 1, mdl.callCount())
	assert.Equal(t, 1, operator.addCalls)
}

func TestReadPendingAutoMemoryBatchRejectsInvalidState(t *testing.T) {
	for _, state := range []string{
		`{"version":1,"next":2,"operations":[]}`,
		`{"version":1,"latest_ts":"2026-08-05T10:00:00Z","operations":[null]}`,
	} {
		sess := newTestSession("app", "u1")
		sess.SetState(pendingAutoMemoryBatchStateKey, []byte(state))
		_, err := readPendingAutoMemoryBatch(sess)
		assert.ErrorContains(t, err, "invalid pending operation batch")
	}

	sess := newTestSession("app", "u1")
	sess.SetState(pendingAutoMemoryBatchStateKey, []byte(`{`))
	_, err := readPendingAutoMemoryBatch(sess)
	assert.ErrorContains(t, err, "decode pending operation batch")
}
