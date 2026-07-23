//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type sessionContextReplyAgent struct {
	name string
}

func (a *sessionContextReplyAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *sessionContextReplyAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *sessionContextReplyAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *sessionContextReplyAgent) Tools() []tool.Tool {
	return nil
}

func (a *sessionContextReplyAgent) Run(
	_ context.Context,
	_ *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("assistant"),
			}},
		},
	}
	close(ch)
	return ch, nil
}

func newSessionContextPromptTestGateway(
	t *testing.T,
	svc *sessioninmemory.SessionService,
) *Server {
	t.Helper()

	r := runner.NewRunner(
		"gateway-session-context",
		&sessionContextReplyAgent{name: "test"},
		runner.WithSessionService(svc),
	)
	srv, err := New(r, WithAppName("gateway-session-context"))
	require.NoError(t, err)
	return srv
}

func sessionContextPromptTranscript(
	t *testing.T,
	svc *sessioninmemory.SessionService,
	userID, sessionID string,
) []string {
	t.Helper()

	sess, err := svc.GetSession(
		context.Background(),
		session.Key{
			AppName:   "gateway-session-context",
			UserID:    userID,
			SessionID: sessionID,
		},
	)
	require.NoError(t, err)

	contents := make([]string, 0, len(sess.Events))
	for _, evt := range sess.Events {
		if len(evt.Choices) == 0 {
			continue
		}
		content := strings.TrimSpace(evt.Choices[0].Message.Content)
		if content == "" {
			continue
		}
		contents = append(contents, content)
	}
	return contents
}

func TestGatewayRequestSessionContextPrompt_PersistsBeforeUser(
	t *testing.T,
) {
	t.Parallel()

	const (
		userID    = "u1"
		sessionID = "telegram:dm:u1"
	)
	svc := sessioninmemory.NewSessionService()
	srv := newSessionContextPromptTestGateway(t, svc)

	send := func(text, sessionContext, lateContext string) {
		t.Helper()
		req := gwproto.MessageRequest{
			Channel:                     "telegram",
			From:                        userID,
			SessionID:                   sessionID,
			Text:                        text,
			RequestSessionContextPrompt: sessionContext,
			RequestLateContextPrompt:    lateContext,
		}
		rsp, status := srv.ProcessMessage(context.Background(), req)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "assistant", rsp.Reply)
	}

	send("user1", "session context one", "late context one")
	send("user2", "session context two", "late context two")

	require.Equal(
		t,
		[]string{
			"session context one",
			"user1",
			"assistant",
			"session context two",
			"user2",
			"assistant",
		},
		sessionContextPromptTranscript(t, svc, userID, sessionID),
	)
}

func TestGatewayRequestLateContextPrompt_DoesNotPersist(
	t *testing.T,
) {
	t.Parallel()

	const (
		userID    = "u1"
		sessionID = "telegram:dm:u1"
	)
	svc := sessioninmemory.NewSessionService()
	srv := newSessionContextPromptTestGateway(t, svc)

	req := gwproto.MessageRequest{
		Channel:                  "telegram",
		From:                     userID,
		SessionID:                sessionID,
		Text:                     "user1",
		RequestLateContextPrompt: "late context one",
	}
	rsp, status := srv.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "assistant", rsp.Reply)

	require.Equal(
		t,
		[]string{
			"user1",
			"assistant",
		},
		sessionContextPromptTranscript(t, svc, userID, sessionID),
	)
}

func TestGatewayRunOptionResolver_SessionContextSourceAppendOnlyDiff(
	t *testing.T,
) {
	t.Parallel()

	const (
		userID       = "u1"
		sessionID    = "telegram:dm:u1"
		extensionKey = "openclaw.runtime_state"
	)
	type runtimeState struct {
		Revision string `json:"revision"`
		Mode     string `json:"mode"`
	}
	sourceFor := func(current runtimeState) agent.SessionContextSourceFunc {
		return func(
			_ context.Context,
			args *agent.SessionContextSourceArgs,
		) (*agent.SessionContextSourceResult, error) {
			stateBytes, err := json.Marshal(current)
			if err != nil {
				return nil, err
			}
			if args.NeedsSnapshot() {
				return &agent.SessionContextSourceResult{
					Version: current.Revision,
					State:   stateBytes,
					Messages: []model.Message{
						model.NewUserMessage("<runtime_state>snapshot " + current.Mode + "</runtime_state>"),
					},
				}, nil
			}
			var previous runtimeState
			if err := json.Unmarshal(args.PreviousState, &previous); err != nil {
				return nil, err
			}
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
					model.NewUserMessage("<runtime_state>update " + previous.Mode + " -> " + current.Mode + "</runtime_state>"),
				},
			}, nil
		}
	}

	svc := sessioninmemory.NewSessionService()
	r := runner.NewRunner(
		"gateway-session-context",
		&sessionContextReplyAgent{name: "test"},
		runner.WithSessionService(svc),
	)
	srv, err := New(
		r,
		WithAppName("gateway-session-context"),
		WithRunOptionResolver(func(
			ctx context.Context,
			input RunOptionInput,
		) (context.Context, []agent.RunOption, error) {
			raw := input.Extensions[extensionKey]
			if len(raw) == 0 {
				return ctx, nil, nil
			}
			var state runtimeState
			if err := json.Unmarshal(raw, &state); err != nil {
				return ctx, nil, err
			}
			return ctx, []agent.RunOption{
				agent.WithSessionContextSource("runtime_state", sourceFor(state)),
			}, nil
		}),
	)
	require.NoError(t, err)

	send := func(text string, state runtimeState) {
		t.Helper()
		raw, err := json.Marshal(state)
		require.NoError(t, err)
		rsp, status := srv.ProcessMessage(
			context.Background(),
			gwproto.MessageRequest{
				Channel:   "telegram",
				From:      userID,
				SessionID: sessionID,
				Text:      text,
				Extensions: map[string]json.RawMessage{
					extensionKey: raw,
				},
			},
		)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "assistant", rsp.Reply)
	}

	send("user1", runtimeState{Revision: "v1", Mode: "read"})
	send("user2", runtimeState{Revision: "v1", Mode: "read"})
	send("user3", runtimeState{Revision: "v2", Mode: "write"})

	require.Equal(
		t,
		[]string{
			"<runtime_state>snapshot read</runtime_state>",
			"user1",
			"assistant",
			"user2",
			"assistant",
			"<runtime_state>update read -> write</runtime_state>",
			"user3",
			"assistant",
		},
		sessionContextPromptTranscript(t, svc, userID, sessionID),
	)
}
