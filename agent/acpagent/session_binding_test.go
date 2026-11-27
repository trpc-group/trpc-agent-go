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
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
	require.Same(t, firstBinding.process, secondBinding.process)
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

func TestCloseSessionIsIdempotent(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "close-session",
	}
	invocation := newSessionInvocation(t, service, key)
	_, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)

	require.NoError(t, acpAgent.CloseSession(context.Background(), key))
	require.NoError(t, acpAgent.CloseSession(context.Background(), key))
	require.Len(t, acpAgent.sessions.byKey, 0)
	require.Equal(t, lifecycleRunning,
		snapshotConnectionManager(acpAgent.connections).state)
}

func TestCloseSessionErrorKeepsMapping(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"close_error",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "close-error",
	}
	invocation := newSessionInvocation(t, service, key)
	_, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)

	err = acpAgent.CloseSession(context.Background(), key)
	require.ErrorContains(t, err, "session close called")
	require.Len(t, acpAgent.sessions.byKey, 1)
}

func TestCloseSessionHonorsContextWhileWaitingForPrompt(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "close-wait",
	}
	invocation := newSessionInvocation(t, service, key)
	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.NoError(t, binding.acquirePrompt(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = acpAgent.CloseSession(ctx, key)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, acpAgent.sessions.byKey, 1)
	binding.releasePrompt()
}

func TestIdleTimeoutStopsProcessAndRecoversSession(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithConfig(
		t,
		"load_success",
		io.Discard,
		false,
		func(config *processConfig) {
			config.IdleTimeout = 100 * time.Millisecond
		},
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "idle",
	})
	first, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.generation)

	require.Eventually(t, func() bool {
		return snapshotConnectionManager(acpAgent.connections).state == lifecycleStopped
	}, 3*time.Second, 10*time.Millisecond)
	require.Len(t, acpAgent.sessions.byKey, 1)

	second, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, first.id, second.id)
	require.Equal(t, uint64(2), second.generation)
}

func TestIdleTimeoutDoesNotReclaimActivePrompt(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithConfig(
		t,
		"prompt_cancel",
		io.Discard,
		false,
		func(config *processConfig) {
			config.IdleTimeout = 50 * time.Millisecond
		},
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "idle-active",
	})
	invocation.Message = model.NewUserMessage("hello")
	ctx, cancel := context.WithCancel(context.Background())
	events, err := acpAgent.Run(ctx, invocation)
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)
	require.Equal(t, lifecycleRunning,
		snapshotConnectionManager(acpAgent.connections).state)
	cancel()
	for range events {
	}
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

	base := processConfig{
		Name:    "acp-test",
		Command: "agent",
		WorkDir: t.TempDir(),
	}
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

func TestACPSessionPromptGateSerializesOnlyMatchingSession(t *testing.T) {
	t.Parallel()

	first := newGatedACPSession()
	second := newGatedACPSession()
	require.NoError(t, first.acquirePrompt(context.Background()))

	blockedCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, first.acquirePrompt(blockedCtx), context.DeadlineExceeded)

	otherCtx, otherCancel := context.WithTimeout(context.Background(), time.Second)
	defer otherCancel()
	require.NoError(t, second.acquirePrompt(otherCtx))
	second.releasePrompt()

	first.releasePrompt()
	reacquiredCtx, reacquiredCancel := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer reacquiredCancel()
	require.NoError(t, first.acquirePrompt(reacquiredCtx))
	first.releasePrompt()
}

func TestAcquireSessionRestoresWithResumeWhenSupported(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(t, "resume_success", io.Discard, false)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "resume-session",
	})
	persistedID := acpsdk.SessionId("persisted-session")
	setPersistedSessionState(t, acpAgent, invocation.Session, persistedID)

	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, persistedID, binding.id)
}

func TestAcquireSessionRestoresWithLoadWhenResumeUnsupported(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(t, "load_success", io.Discard, false)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "load-session",
	})
	persistedID := acpsdk.SessionId("persisted-session")
	setPersistedSessionState(t, acpAgent, invocation.Session, persistedID)

	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, persistedID, binding.id)
}

func TestAcquireSessionPrefersResumeWhenBothCapabilitiesExist(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(t, "resume_both_success", io.Discard, false)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "resume-both",
	})
	persistedID := acpsdk.SessionId("persisted-session")
	setPersistedSessionState(t, acpAgent, invocation.Session, persistedID)

	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, persistedID, binding.id)
}

func TestAcquireSessionCreatesNewWhenRecoveryUnsupported(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"new_only",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "new-session",
	})
	setPersistedSessionState(
		t,
		acpAgent,
		invocation.Session,
		acpsdk.SessionId("unusable-session"),
	)

	binding, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, acpsdk.SessionId("helper-session-1"), binding.id)
}

func TestAcquireSessionUnknownRecoveryClearsAndCreatesOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
	}{
		{name: "resume", mode: "resume_unknown"},
		{name: "load", mode: "load_unknown"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			acpAgent := newHelperAgentWithOptions(
				t,
				test.mode,
				io.Discard,
				false,
				WithAuthMethodSelectorFunc(agentLoginSelector()),
			)
			service := inmemory.NewSessionService()
			invocation := newSessionInvocation(t, service, session.Key{
				AppName:   "app",
				UserID:    "user",
				SessionID: test.name,
			})
			staleID := acpsdk.SessionId("stale-session")
			setPersistedSessionState(t, acpAgent, invocation.Session, staleID)

			binding, err := acpAgent.acquireSession(context.Background(), invocation)
			require.NoError(t, err)
			require.Equal(t, acpsdk.SessionId("helper-session-1"), binding.id)
			keys := newACPSessionStateKeys("acp-helper")
			raw, ok := invocation.Session.GetState(keys.sessionID)
			require.True(t, ok)
			require.Equal(t, string(binding.id), string(raw))
		})
	}
}

func TestAcquireSessionRecreatesSameKeyForNewSessionGeneration(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"wait",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "recreated",
	}
	firstInvocation := newSessionInvocation(t, service, key)
	first, err := acpAgent.acquireSession(context.Background(), firstInvocation)
	require.NoError(t, err)

	recreated := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionCreatedAt(firstInvocation.Session.CreatedAt.Add(time.Second)),
	)
	secondInvocation := &agent.Invocation{
		Session:        recreated,
		SessionService: service,
	}
	second, err := acpAgent.acquireSession(context.Background(), secondInvocation)
	require.NoError(t, err)
	require.NotEqual(t, first.id, second.id)
	require.Equal(t, acpsdk.SessionId("helper-session-2"), second.id)
	require.Len(t, acpAgent.sessions.byKey, 1)
}

func TestAcquireSessionTTLReplacementAttemptsOldSessionClose(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"close_error",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "close-old",
	}
	firstInvocation := newSessionInvocation(t, service, key)
	_, err := acpAgent.acquireSession(context.Background(), firstInvocation)
	require.NoError(t, err)

	recreated := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionCreatedAt(firstInvocation.Session.CreatedAt.Add(time.Second)),
	)
	secondInvocation := &agent.Invocation{
		Session:        recreated,
		SessionService: service,
	}
	binding, err := acpAgent.acquireSession(context.Background(), secondInvocation)
	require.Nil(t, binding)
	require.ErrorContains(t, err, "session close called")
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

func setPersistedSessionState(
	t *testing.T,
	acpAgent *ACPAgent,
	target *session.Session,
	sessionID acpsdk.SessionId,
) {
	t.Helper()
	keys := newACPSessionStateKeys(acpAgent.config.Name)
	target.SetState(keys.sessionID, []byte(sessionID))
	target.SetState(keys.launchFingerprint, []byte(acpAgent.launchFingerprint))
	target.SetState(keys.agentVersion, []byte("1.2.3"))
}

func newGatedACPSession() *acpSession {
	session := &acpSession{
		promptGate: make(chan struct{}, 1),
	}
	session.promptGate <- struct{}{}
	return session
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
