//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestA2AAgent_CandidateCookieRotationIsPrivateAndCommitsWinner(t *testing.T) {
	var (
		serverURL       string
		calls           atomic.Int32
		receivedCookies []string
		receivedMu      sync.Mutex
	)
	oldCookieValue := anonymousTestCookieValue(99)
	handlerErrors := make(chan error, 1)
	reportHandlerError := func(err error) {
		select {
		case handlerErrors <- err:
		default:
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/agent-card.json" {
			if err := json.NewEncoder(w).Encode(server.AgentCard{
				Name:        "candidate-cookie-agent",
				Description: "candidate cookie test",
				URL:         serverURL,
			}); err != nil {
				reportHandlerError(err)
			}
			return
		}
		var request struct {
			ID any `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			reportHandlerError(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requestCookieValue := ""
		if requestCookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			requestCookieValue = requestCookie.Value
		}
		receivedMu.Lock()
		receivedCookies = append(receivedCookies, requestCookieValue)
		receivedMu.Unlock()
		call := calls.Add(1)
		responseCookie := anonymousTestCookieValue(int(call))
		http.SetCookie(w, &http.Cookie{
			Name:    anonymousUserIDCookieName,
			Value:   responseCookie,
			Path:    "/",
			Expires: time.Now().Add(time.Hour),
		})
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "candidate-response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("candidate response")},
			},
		}); err != nil {
			reportHandlerError(err)
		}
	}))
	defer srv.Close()
	serverURL = srv.URL

	a, err := New(WithAgentCardURL(serverURL), WithEnableStreaming(false))
	require.NoError(t, err)
	service := sessionmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	stateKey := anonymousCookieStateKey(serverURL)
	scope := anonymousCookieURLScopeFromAgentURL(serverURL)
	oldRecord := anonymousCookieRecord{
		value:   oldCookieValue,
		path:    "/",
		expires: time.Now().Add(time.Hour).UTC(),
	}
	oldCanonical, err := encodeAnonymousCookieRecord(oldRecord)
	require.NoError(t, err)
	initialState := anonymousCookieRecordStateMap(stateKey, oldRecord)
	initialState[stateKey+anonymousCookieRecordStateKeySuffix] = oldCanonical
	_, err = service.CreateSession(
		context.Background(),
		session.Key{AppName: "app", UserID: "user", SessionID: "session"},
		initialState,
	)
	require.NoError(t, err)

	selector := &recordingCandidateSelector{winner: 0}
	r := runner.NewRunner(
		"app",
		anonymousSessionAgentWrapper{inner: a},
		runner.WithSessionService(service),
		runner.WithCandidateSelector(selector, runner.WithCandidateAttempts(2)),
	)
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	ch, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("question"),
	)
	require.NoError(t, err)
	var emitted []*event.Event
	for evt := range ch {
		emitted = append(emitted, evt)
	}
	require.EqualValues(t, 2, calls.Load())
	receivedMu.Lock()
	gotReceivedCookies := append([]string(nil), receivedCookies...)
	receivedMu.Unlock()
	require.Equal(t, []string{oldCookieValue, oldCookieValue}, gotReceivedCookies)
	select {
	case err := <-handlerErrors:
		require.NoError(t, err)
	default:
	}
	require.NotNil(t, selector.request)
	require.Len(t, selector.request.Attempts, 2)
	for _, attempt := range selector.request.Attempts {
		assertAnonymousCookieKeysAbsent(t, stateKey, attempt.Events)
	}
	assertAnonymousCookieKeysAbsent(t, stateKey, emitted)

	persisted, err := service.GetSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	canonical, ok := persisted.GetState(stateKey + anonymousCookieRecordStateKeySuffix)
	require.True(t, ok)
	got, ok := decodeAnonymousCookieRecord(canonical, scope)
	require.True(t, ok)
	require.Equal(t, anonymousTestCookieValue(1), got.value)
	legacy, ok := loadAnonymousCookieStateFromSession(persisted, stateKey)
	require.True(t, ok)
	require.True(t, got.equal(legacy))
}

func TestAnonymousCookieState_DirectCanonicalFallbackKeepsLegacyProjection(t *testing.T) {
	ctx := context.Background()
	base := sessionmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	serverURL := "https://agent.example.com/a2a"
	stateKey := anonymousCookieStateKey(serverURL)
	scope := anonymousCookieURLScopeFromAgentURL(serverURL)
	old := anonymousCookieRecord{
		value:   anonymousTestCookieValue(41),
		secure:  true,
		path:    "/a2a",
		domain:  "agent.example.com",
		expires: time.Now().Add(time.Hour).UTC(),
	}
	canonical, err := encodeAnonymousCookieRecord(old)
	require.NoError(t, err)
	initial := anonymousCookieRecordStateMap(stateKey, old)
	initial[stateKey+anonymousCookieRecordStateKeySuffix] = canonical
	persisted, err := base.CreateSession(ctx, key, initial)
	require.NoError(t, err)
	state := newAnonymousCookieState(
		persisted.Clone(),
		persisted,
		&sessionServiceWithoutStateInitialization{Service: base},
		stateKey,
		scope,
	)
	rotated := anonymousCookieRecord{
		value:   anonymousTestCookieValue(42),
		secure:  true,
		path:    "/a2a/tasks",
		domain:  "agent.example.com",
		expires: time.Now().Add(2 * time.Hour).UTC(),
	}
	require.NoError(t, state.persist(ctx, rotated))
	stored, err := base.GetSession(ctx, key)
	require.NoError(t, err)
	canonicalValue, present := stored.GetState(stateKey + anonymousCookieRecordStateKeySuffix)
	require.True(t, present)
	canonicalRecord, ok := decodeAnonymousCookieRecord(canonicalValue, scope)
	require.True(t, ok)
	require.True(t, rotated.equal(canonicalRecord))
	legacy, ok := loadAnonymousCookieStateFromSession(stored, stateKey)
	require.True(t, ok)
	require.True(t, rotated.equal(legacy))

	require.NoError(t, state.clear(ctx))
	stored, err = base.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok = loadAnonymousCookieStateFromSession(stored, stateKey)
	require.False(t, ok)
	for _, legacyKey := range anonymousCookieLegacyStateKeys(stateKey) {
		value, present := stored.GetState(legacyKey)
		require.True(t, present)
		require.Nil(t, value)
	}
	tombstone, ok := stored.GetState(stateKey + anonymousCookieRecordStateKeySuffix)
	require.True(t, ok)
	require.Equal(t, anonymousCookieTombstoneValue(), tombstone)
}

func anonymousCookieLegacyStateKeys(stateKey string) []string {
	return []string{
		stateKey,
		stateKey + anonymousUserIDCookieSecureKeySuffix,
		stateKey + anonymousUserIDCookiePathKeySuffix,
		stateKey + anonymousUserIDCookieDomainKeySuffix,
		stateKey + anonymousUserIDCookieExpiryKeySuffix,
	}
}

type recordingCandidateSelector struct {
	winner  int
	request *runner.CandidateSelectRequest
}

func (s *recordingCandidateSelector) Select(
	_ context.Context,
	request *runner.CandidateSelectRequest,
) (int, error) {
	s.request = request
	return s.winner, nil
}

func assertAnonymousCookieKeysAbsent(
	t *testing.T,
	stateKey string,
	events []*event.Event,
) {
	t.Helper()
	keys := append(
		anonymousCookieLegacyStateKeys(stateKey),
		stateKey+anonymousCookieRecordStateKeySuffix,
	)
	for _, evt := range events {
		if evt == nil || evt.StateDelta == nil {
			continue
		}
		for _, key := range keys {
			require.NotContains(t, evt.StateDelta, key)
		}
	}
}
