//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestAcquireSessionMapsFullKeyAndPersistsState(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	first := newSessionInvocation(t, service, session.Key{
		AppName:   "app-one",
		UserID:    "user-one",
		SessionID: "shared-id",
	})
	second := newSessionInvocation(t, service, session.Key{
		AppName:   "app-one",
		UserID:    "user-two",
		SessionID: "shared-id",
	})

	firstBinding, err := acpAgent.acquireSession(context.Background(), first)
	require.NoError(t, err)
	secondBinding, err := acpAgent.acquireSession(context.Background(), second)
	require.NoError(t, err)
	require.NotSame(t, firstBinding, secondBinding)
	require.NotEqual(t, firstBinding.id, secondBinding.id)
	require.Equal(t, snapshotConnectionManager(acpAgent.connections).generation,
		firstBinding.generation)
	require.Equal(t, firstBinding.generation, secondBinding.generation)
	require.Len(t, acpAgent.sessions.byKey, 2)

	stateKeys := newACPSessionStateKeys("acp-helper")
	firstState := loadSessionState(t, service, first.Session)
	require.Len(t, firstState, 3)
	require.Equal(t, string(firstBinding.id), string(firstState[stateKeys.sessionID]))
	require.Equal(t, acpAgent.launchFingerprint,
		string(firstState[stateKeys.launchFingerprint]))
	require.Equal(t, "1.2.3", string(firstState[stateKeys.agentVersion]))

	evt := &event.Event{}
	firstBinding.applyStateDelta(evt)
	require.Len(t, evt.StateDelta, 3)
	require.Equal(t, firstState[stateKeys.sessionID], evt.StateDelta[stateKeys.sessionID])
	require.Equal(t, firstState[stateKeys.launchFingerprint],
		evt.StateDelta[stateKeys.launchFingerprint])
	require.Equal(t, firstState[stateKeys.agentVersion],
		evt.StateDelta[stateKeys.agentVersion])

	again, err := acpAgent.acquireSession(context.Background(), first)
	require.NoError(t, err)
	require.Same(t, firstBinding, again)
	require.Len(t, acpAgent.sessions.byKey, 2)
}

func TestAcquireSessionConcurrentSameKeyCreatesOneACPSession(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "shared",
	})

	const callers = 8
	bindings := make([]*acpSession, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			bindings[index], errs[index] = acpAgent.acquireSession(
				context.Background(),
				invocation,
			)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err)
		require.Same(t, bindings[0], bindings[i])
	}
	require.Equal(t, acpsdk.SessionId("helper-session-1"), bindings[0].id)
	require.Len(t, acpAgent.sessions.byKey, 1)
}

func TestAcquireSessionPersistenceFailureClosesNewACPSession(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"close_error",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	underlying := inmemory.NewSessionService()
	persistErr := errors.New("persist failed")
	service := &failingSessionStateService{
		Service: underlying,
		err:     persistErr,
	}
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	})

	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.Nil(t, binding)
	require.ErrorIs(t, err, persistErr)
	require.ErrorContains(t, err, "session close called")
	require.Len(t, acpAgent.sessions.byKey, 0)

	stateKeys := newACPSessionStateKeys("acp-helper")
	_, ok := invocation.Session.GetState(stateKeys.sessionID)
	require.False(t, ok)
}

func TestComputeLaunchFingerprintExcludesEnvValues(t *testing.T) {
	t.Parallel()

	base := validACPConfig(t)
	base.Args = []string{"--mode", "acp"}
	base.Env = []string{"TOKEN=secret", "MODE=test"}
	first, err := computeLaunchFingerprint(base)
	require.NoError(t, err)
	require.NotContains(t, first, "secret")

	sameNames := base
	sameNames.Env = []string{"MODE=changed", "TOKEN=also-secret"}
	second, err := computeLaunchFingerprint(sameNames)
	require.NoError(t, err)
	require.Equal(t, first, second)

	differentEnvName := base
	differentEnvName.Env = []string{"TOKEN=secret", "REGION=test"}
	third, err := computeLaunchFingerprint(differentEnvName)
	require.NoError(t, err)
	require.NotEqual(t, first, third)

	differentArgs := base
	differentArgs.Args = []string{"--mode", "different"}
	fourth, err := computeLaunchFingerprint(differentArgs)
	require.NoError(t, err)
	require.NotEqual(t, first, fourth)
}

func newSessionInvocation(
	t *testing.T,
	service session.Service,
	key session.Key,
) *agent.Invocation {
	t.Helper()
	sess, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	return &agent.Invocation{
		Session:        sess,
		SessionService: service,
	}
}

func loadSessionState(
	t *testing.T,
	service session.Service,
	sess *session.Session,
) session.StateMap {
	t.Helper()
	stored, err := service.GetSession(context.Background(), session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	})
	require.NoError(t, err)
	return stored.State
}

type failingSessionStateService struct {
	session.Service
	err error
}

func (s *failingSessionStateService) UpdateSessionState(
	context.Context,
	session.Key,
	session.StateMap,
) error {
	return s.err
}
