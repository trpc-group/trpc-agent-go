//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRunnerDeduplicatesEventPersistence(t *testing.T) {
	service := &mockSessionService{}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	loop := &eventLoopContext{
		sess:             sess,
		invocation:       invocation,
		processedEventCh: make(chan *event.Event, 2),
		streamFilter:     graph.NewStreamModeFilter(false, nil),
	}

	require.NoError(t, r.processSingleAgentEvent(context.Background(), loop, evt))
	require.NoError(t, r.processSingleAgentEvent(context.Background(), loop, evt))
	require.Len(t, service.appendEventCallsSnapshot(), 1)
	require.Len(t, service.enqueueSummaryJobCallsSnapshot(), 1)
}

func TestRunnerDoesNotDeduplicateAcrossEventLoops(t *testing.T) {
	service := &mockSessionService{}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()

	require.True(t, r.handleEventPersistenceOnce(
		context.Background(),
		invocation,
		sess,
		sess,
		evt,
		&eventPersistenceDeduper{},
	))
	require.True(t, r.handleEventPersistenceOnce(
		context.Background(),
		invocation,
		sess,
		sess,
		evt,
		&eventPersistenceDeduper{},
	))
	require.Len(t, service.appendEventCallsSnapshot(), 2)
	require.Len(t, service.enqueueSummaryJobCallsSnapshot(), 2)
}

func TestRunnerReleasesCompletedEventPersistenceRecord(t *testing.T) {
	service := &mockSessionService{}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	deduper := &eventPersistenceDeduper{}

	require.True(t, r.handleEventPersistenceOnce(
		context.Background(), invocation, sess, sess, evt, deduper,
	))
	deduper.mu.Lock()
	record, ok := deduper.records[evt.ID]
	deduper.mu.Unlock()
	require.True(t, ok)
	require.Nil(t, record)
}

func TestRunnerDeduplicatesEventWhileAppendIsInFlight(t *testing.T) {
	service := &blockingPersistenceSessionService{
		mockSessionService: &mockSessionService{},
		appendStarted:      make(chan struct{}),
		appendRelease:      make(chan struct{}),
	}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	deduper := &eventPersistenceDeduper{}
	firstDone := make(chan bool, 1)
	secondDone := make(chan bool, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(service.appendRelease)
		})
	}
	defer release()

	go func() {
		firstDone <- r.handleEventPersistenceOnce(
			context.Background(), invocation, sess, sess, evt, deduper,
		)
	}()
	select {
	case <-service.appendStarted:
	case <-time.After(time.Second):
		t.Fatal("first append did not start")
	}
	go func() {
		secondDone <- r.handleEventPersistenceOnce(
			context.Background(), invocation, sess, sess, evt, deduper,
		)
	}()

	select {
	case <-secondDone:
		t.Fatal("duplicate persistence completed before the in-flight append")
	case <-time.After(50 * time.Millisecond):
	}
	release()

	require.True(t, receivePersistenceResult(t, firstDone))
	require.False(t, receivePersistenceResult(t, secondDone))
	require.EqualValues(t, 1, service.appendCalls.Load())
	require.EqualValues(t, 1, service.enqueueCalls.Load())
}

func TestRunnerDuplicateWaiterHonorsCancellation(t *testing.T) {
	service := &blockingPersistenceSessionService{
		mockSessionService: &mockSessionService{},
		appendStarted:      make(chan struct{}),
		appendRelease:      make(chan struct{}),
	}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	deduper := &eventPersistenceDeduper{}
	ownerDone := make(chan bool, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(service.appendRelease)
		})
	}
	defer release()

	go func() {
		ownerDone <- r.handleEventPersistenceOnce(
			context.Background(), invocation, sess, sess, evt, deduper,
		)
	}()
	select {
	case <-service.appendStarted:
	case <-time.After(time.Second):
		t.Fatal("owner append did not start")
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	duplicateDone := make(chan bool, 1)
	go func() {
		duplicateDone <- r.handleEventPersistenceOnce(
			canceledCtx, invocation, sess, sess, evt, deduper,
		)
	}()

	require.False(t, receivePersistenceResult(t, duplicateDone))
	require.EqualValues(t, 1, service.appendCalls.Load())
	release()
	require.True(t, receivePersistenceResult(t, ownerDone))
	require.EqualValues(t, 1, service.appendCalls.Load())
	require.EqualValues(t, 1, service.enqueueCalls.Load())
}

func TestRunnerRetriesEventAfterAppendFailure(t *testing.T) {
	service := &failFirstPersistenceSessionService{
		mockSessionService: &mockSessionService{},
	}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	deduper := &eventPersistenceDeduper{}

	require.False(t, r.handleEventPersistenceOnce(
		context.Background(), invocation, sess, sess, evt, deduper,
	))
	require.True(t, r.handleEventPersistenceOnce(
		context.Background(), invocation, sess, sess, evt, deduper,
	))
	require.EqualValues(t, 2, service.appendCalls.Load())
	require.EqualValues(t, 1, service.enqueueCalls.Load())
}

func TestRunnerDoesNotDeduplicateEmptyEventID(t *testing.T) {
	service := &mockSessionService{}
	r := &runner{sessionService: service}
	sess, invocation, evt := newPersistenceDedupTestInput()
	evt.ID = ""
	deduper := &eventPersistenceDeduper{}

	require.True(t, r.handleEventPersistenceOnce(
		context.Background(), invocation, sess, sess, evt, deduper,
	))
	require.True(t, r.handleEventPersistenceOnce(
		context.Background(), invocation, sess, sess, evt, deduper,
	))
	require.Len(t, service.appendEventCallsSnapshot(), 2)
	require.Len(t, service.enqueueSummaryJobCallsSnapshot(), 2)
}

func newPersistenceDedupTestInput() (
	*session.Session,
	*agent.Invocation,
	*event.Event,
) {
	sess := session.NewSession("app", "user", "session")
	invocation := agent.NewInvocation(agent.WithInvocationSession(sess))
	evt := event.NewResponseEvent(
		invocation.InvocationID,
		"assistant",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	)
	evt.ID = "stable-event-id"
	return sess, invocation, evt
}

func receivePersistenceResult(t *testing.T, result <-chan bool) bool {
	t.Helper()
	select {
	case persisted := <-result:
		return persisted
	case <-time.After(time.Second):
		t.Fatal("event persistence did not complete")
		return false
	}
}

type blockingPersistenceSessionService struct {
	*mockSessionService
	appendStarted chan struct{}
	appendRelease chan struct{}
	startOnce     sync.Once
	appendCalls   atomic.Int32
	enqueueCalls  atomic.Int32
}

func (s *blockingPersistenceSessionService) AppendEvent(
	context.Context,
	*session.Session,
	*event.Event,
	...session.Option,
) error {
	s.appendCalls.Add(1)
	s.startOnce.Do(func() {
		close(s.appendStarted)
	})
	<-s.appendRelease
	return nil
}

func (s *blockingPersistenceSessionService) EnqueueSummaryJob(
	context.Context,
	*session.Session,
	string,
	bool,
) error {
	s.enqueueCalls.Add(1)
	return nil
}

type failFirstPersistenceSessionService struct {
	*mockSessionService
	appendCalls  atomic.Int32
	enqueueCalls atomic.Int32
}

func (s *failFirstPersistenceSessionService) AppendEvent(
	context.Context,
	*session.Session,
	*event.Event,
	...session.Option,
) error {
	if s.appendCalls.Add(1) == 1 {
		return errors.New("append failed")
	}
	return nil
}

func (s *failFirstPersistenceSessionService) EnqueueSummaryJob(
	context.Context,
	*session.Session,
	string,
	bool,
) error {
	s.enqueueCalls.Add(1)
	return nil
}
