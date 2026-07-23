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
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunner_Run_SessionContextMessages_PersistBeforeCurrentUser(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithSessionContextMessages([]model.Message{
			model.NewUserMessage("ctx A"),
		}),
		agent.WithSessionContextMessagesFunc(func(
			context.Context,
			*agent.SessionContextMessagesArgs,
		) ([]model.Message, error) {
			return []model.Message{model.NewUserMessage("ctx F")}, nil
		}),
		agent.WithSessionContextMessages([]model.Message{
			model.NewUserMessage("ctx B"),
		}),
	)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, model.NewUserMessage("hello"), ag.invocationMessage)

	sess, err := svc.GetSession(
		context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 4)
	require.Equal(t, "ctx A", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "ctx F", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "ctx B", sess.Events[2].Choices[0].Message.Content)
	require.Equal(t, "hello", sess.Events[3].Choices[0].Message.Content)
}

func TestRunner_Run_SessionContextMessages_NormalizesNonUserRoles(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithSessionContextMessages([]model.Message{
			model.NewSystemMessage("system ctx"),
			model.NewAssistantMessage("assistant ctx"),
		}),
	)
	require.NoError(t, err)
	for range ch {
	}

	sess, err := svc.GetSession(
		context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 3)
	require.Equal(t, model.RoleUser, sess.Events[0].Choices[0].Message.Role)
	require.Equal(t, "system ctx", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, model.RoleUser, sess.Events[1].Choices[0].Message.Role)
	require.Equal(t, "assistant ctx", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "hello", sess.Events[2].Choices[0].Message.Content)
}

func TestRunner_Run_SessionContextMessagesFunc_WrapsError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	buildErr := errors.New("build failed")
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithSessionContextMessagesFunc(func(
			context.Context,
			*agent.SessionContextMessagesArgs,
		) ([]model.Message, error) {
			return nil, buildErr
		}),
	)
	require.Nil(t, ch)
	require.ErrorIs(t, err, buildErr)
	require.Contains(t, err.Error(), "runner: build session context messages")
}

func TestRunner_Run_SessionContextSource_WrapsError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	buildErr := errors.New("source failed")
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithSessionContextSource("policy", func(
			context.Context,
			*agent.SessionContextSourceArgs,
		) (*agent.SessionContextSourceResult, error) {
			return nil, buildErr
		}),
	)
	require.Nil(t, ch)
	require.ErrorIs(t, err, buildErr)
	require.Contains(t, err.Error(), `runner: build session context source "policy"`)
}

func TestRunner_Run_SessionContextSource_SnapshotUnchangedUpdate(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ctx := context.Background()

	type policyState struct {
		Revision  string `json:"revision"`
		Workspace string `json:"workspace"`
	}
	var calls []agent.SessionContextSourceArgs
	sourceFor := func(current policyState) agent.SessionContextSourceFunc {
		return func(
			ctx context.Context,
			args *agent.SessionContextSourceArgs,
		) (*agent.SessionContextSourceResult, error) {
			copiedArgs := *args
			copiedArgs.PreviousState = append([]byte(nil), args.PreviousState...)
			calls = append(calls, copiedArgs)

			stateBytes, err := json.Marshal(current)
			require.NoError(t, err)
			if args.NeedsSnapshot() {
				return &agent.SessionContextSourceResult{
					Version: current.Revision,
					State:   stateBytes,
					Messages: []model.Message{
						model.NewUserMessage("policy snapshot " + current.Revision),
					},
				}, nil
			}

			var previous policyState
			require.NoError(t, json.Unmarshal(args.PreviousState, &previous))
			if previous == current {
				return &agent.SessionContextSourceResult{
					Version: current.Revision,
					State:   stateBytes,
				}, nil
			}
			return &agent.SessionContextSourceResult{
				Version: current.Revision,
				State:   stateBytes,
				Messages: []model.Message{
					model.NewUserMessage(
						"policy update " + previous.Revision + " -> " + current.Revision,
					),
				},
			}, nil
		}
	}

	for _, turn := range []struct {
		user  string
		state policyState
	}{
		{user: "first", state: policyState{Revision: "v1", Workspace: "read"}},
		{user: "second", state: policyState{Revision: "v1", Workspace: "read"}},
		{user: "third", state: policyState{Revision: "v2", Workspace: "write"}},
	} {
		ch, err := r.Run(
			ctx,
			"u",
			"s",
			model.NewUserMessage(turn.user),
			agent.WithSessionContextSource("policy", sourceFor(turn.state)),
		)
		require.NoError(t, err)
		for range ch {
		}
	}

	sess, err := svc.GetSession(
		ctx,
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 5)
	require.Equal(t, "policy snapshot v1", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "first", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "second", sess.Events[2].Choices[0].Message.Content)
	require.Equal(t, "policy update v1 -> v2", sess.Events[3].Choices[0].Message.Content)
	require.Equal(t, "third", sess.Events[4].Choices[0].Message.Content)

	require.Len(t, calls, 3)
	require.True(t, calls[0].NeedsSnapshot())
	require.False(t, calls[1].NeedsSnapshot())
	require.Equal(t, "v1", calls[1].PreviousVersion)
	require.Contains(t, string(calls[1].PreviousState), `"revision":"v1"`)
	require.False(t, calls[2].NeedsSnapshot())
	require.Equal(t, "v1", calls[2].PreviousVersion)
	require.Contains(t, string(calls[2].PreviousState), `"workspace":"read"`)
}

func TestRunner_Run_SessionContextSource_NeedsSnapshotWhenMaterializedEventTrimmed(t *testing.T) {
	svc := sessioninmemory.NewSessionService(sessioninmemory.WithSessionEventLimit(2))
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ctx := context.Background()
	var needSnapshotCalls []bool

	source := func(
		ctx context.Context,
		args *agent.SessionContextSourceArgs,
	) (*agent.SessionContextSourceResult, error) {
		needSnapshotCalls = append(needSnapshotCalls, args.NeedsSnapshot())
		state := []byte(`{"revision":"v1"}`)
		if args.NeedsSnapshot() {
			return &agent.SessionContextSourceResult{
				Version:  "v1",
				State:    state,
				Messages: []model.Message{model.NewUserMessage("policy snapshot v1")},
			}, nil
		}
		return &agent.SessionContextSourceResult{Version: "v1", State: state}, nil
	}

	for _, userText := range []string{"first", "second", "third"} {
		ch, err := r.Run(
			ctx,
			"u",
			"s",
			model.NewUserMessage(userText),
			agent.WithSessionContextSource("policy", source),
		)
		require.NoError(t, err)
		for range ch {
		}
	}

	require.Equal(t, []bool{true, false, true}, needSnapshotCalls)
	sess, err := svc.GetSession(
		ctx,
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 2)
	require.Equal(t, "policy snapshot v1", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "third", sess.Events[1].Choices[0].Message.Content)
}

type sessionContextSourceSummaryCoverSummarizer struct{}

func (s sessionContextSourceSummaryCoverSummarizer) ShouldSummarize(
	*session.Session,
) bool {
	return true
}

func (s sessionContextSourceSummaryCoverSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	return "covered", nil
}

func (s sessionContextSourceSummaryCoverSummarizer) SetPrompt(string) {}

func (s sessionContextSourceSummaryCoverSummarizer) SetModel(model.Model) {}

func (s sessionContextSourceSummaryCoverSummarizer) Metadata() map[string]any {
	return nil
}

func TestRunner_Run_SessionContextSource_NeedsSnapshotWhenMaterializedEventSummarized(
	t *testing.T,
) {
	svc := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(sessionContextSourceSummaryCoverSummarizer{}),
	)
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ctx := context.Background()
	var needSnapshotCalls []bool

	source := func(
		ctx context.Context,
		args *agent.SessionContextSourceArgs,
	) (*agent.SessionContextSourceResult, error) {
		needSnapshotCalls = append(needSnapshotCalls, args.NeedsSnapshot())
		state := []byte(`{"revision":"v1"}`)
		if args.NeedsSnapshot() {
			return &agent.SessionContextSourceResult{
				Version:  "v1",
				State:    state,
				Messages: []model.Message{model.NewUserMessage("policy snapshot v1")},
			}, nil
		}
		return &agent.SessionContextSourceResult{Version: "v1", State: state}, nil
	}

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.NewUserMessage("first"),
		agent.WithSessionContextSource("policy", source),
	)
	require.NoError(t, err)
	for range ch {
	}

	sess, err := svc.GetSession(
		ctx,
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.NoError(t, svc.CreateSessionSummary(ctx, sess, "", true))

	ch, err = r.Run(
		ctx,
		"u",
		"s",
		model.NewUserMessage("second"),
		agent.WithSessionContextSource("policy", source),
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, []bool{true, true}, needSnapshotCalls)
	sess, err = svc.GetSession(
		ctx,
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 4)
	require.Equal(t, "policy snapshot v1", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "first", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "policy snapshot v1", sess.Events[2].Choices[0].Message.Content)
	require.Equal(t, "second", sess.Events[3].Choices[0].Message.Content)
}

func TestResolveSessionContextSourceUpdate_DerivesVersionAndNormalizesRole(
	t *testing.T,
) {
	previous := sessionContextSourceRecord{
		Version: "v1",
		State:   []byte(`{"revision":"v1"}`),
	}
	messages, commit, err := resolveSessionContextSourceUpdate(
		"policy",
		previous,
		true,
		&agent.SessionContextSourceResult{
			Messages: []model.Message{model.NewSystemMessage("policy update")},
		},
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, model.RoleUser, messages[0].Role)
	require.Equal(t, "policy update", messages[0].Content)
	require.NotNil(t, commit)
	require.Equal(t, "policy", commit.name)
	require.Equal(t, 1, commit.messageCount)
	require.Contains(t, commit.record.Version, "sha256:messages:")
	require.Equal(t, previous.State, commit.record.State)

	messages, commit, err = resolveSessionContextSourceUpdate(
		"policy",
		sessionContextSourceRecord{},
		false,
		&agent.SessionContextSourceResult{State: []byte(`{"revision":"v2"}`)},
	)
	require.NoError(t, err)
	require.Empty(t, messages)
	require.NotNil(t, commit)
	require.Equal(t, 0, commit.messageCount)
	require.Contains(t, commit.record.Version, "sha256:state:")
	require.Equal(t, []byte(`{"revision":"v2"}`), commit.record.State)
}

func TestSourceContextEventsForCurrentTurnUsesExplicitOffset(t *testing.T) {
	appendedEvents := []event.Event{
		*event.NewResponseEvent("inv", "user", &model.Response{
			Choices: []model.Choice{{Message: model.NewUserMessage("duplicate")}},
		}),
		*event.NewResponseEvent("inv", "user", &model.Response{
			Choices: []model.Choice{{Message: model.NewUserMessage("ctx A")}},
		}),
		*event.NewResponseEvent("inv", "user", &model.Response{
			Choices: []model.Choice{{Message: model.NewUserMessage("ctx B")}},
		}),
		*event.NewResponseEvent("inv", "user", &model.Response{
			Choices: []model.Choice{{Message: model.NewUserMessage("duplicate")}},
		}),
	}

	got := sourceContextEventsForCurrentTurn(appendedEvents, 1, 2)
	require.Len(t, got, 2)
	require.Equal(t, "ctx A", got[0].Choices[0].Message.Content)
	require.Equal(t, "ctx B", got[1].Choices[0].Message.Content)
	require.Nil(t, sourceContextEventsForCurrentTurn(appendedEvents, -1, 1))
	require.Nil(t, sourceContextEventsForCurrentTurn(appendedEvents, 3, 2))
}

func TestSessionContextSourceCoveredByBoundary(t *testing.T) {
	events := []event.Event{
		{ID: "ctx", Timestamp: time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)},
		{ID: "user", Timestamp: time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC)},
	}
	record := sessionContextSourceRecord{
		MaterializedAt: events[0].Timestamp,
	}

	require.False(t, sessionContextSourceCoveredByBoundary(
		events,
		0,
		events[0],
		record,
		nil,
	))
	require.True(t, sessionContextSourceCoveredByBoundary(
		events,
		0,
		events[0],
		record,
		session.NewSummaryBoundaryWithEventID("", time.Time{}, "ctx"),
	))
	require.False(t, sessionContextSourceCoveredByBoundary(
		events,
		1,
		events[1],
		record,
		session.NewSummaryBoundaryWithEventID("", time.Time{}, "ctx"),
	))
	require.True(t, sessionContextSourceCoveredByBoundary(
		events,
		0,
		event.Event{ID: "ctx"},
		record,
		session.NewSummaryBoundary("", events[0].Timestamp.Add(time.Second)),
	))
	require.False(t, sessionContextSourceCoveredByBoundary(
		events,
		0,
		events[0],
		record,
		session.NewSummaryBoundary("", events[0].Timestamp.Add(-time.Second)),
	))
	require.True(t, sessionContextSourceCoveredByBoundary(
		events,
		0,
		event.Event{ID: "ctx"},
		sessionContextSourceRecord{},
		session.NewSummaryBoundary("", events[0].Timestamp),
	))
}

func TestLoadSessionContextSourceLedgerInvalidState(t *testing.T) {
	sess := &session.Session{}
	sess.SetState(sessionContextSourceStateKey, []byte("{"))
	require.Empty(t, loadSessionContextSourceLedger(sess))
	sess.SetState(sessionContextSourceStateKey, []byte("null"))
	require.Empty(t, loadSessionContextSourceLedger(sess))
}
