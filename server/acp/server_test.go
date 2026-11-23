//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func withSessionIDGenerator(generator func() string) Option {
	return func(options *options) {
		options.sessionIDGenerator = generator
	}
}

func TestServerPromptOverACP(t *testing.T) {
	fakeRunner := &testRunner{
		run: func(_ context.Context) <-chan *event.Event {
			finishReason := "stop"
			events := make(chan *event.Event, 4)
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					ID:        "response",
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.Message{Content: "hello"},
					}},
				},
			}
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					ID: "response",
					Choices: []model.Choice{{
						Message:      model.Message{Content: "hello"},
						FinishReason: &finishReason,
					}},
					Usage: &model.Usage{
						PromptTokens:     2,
						CompletionTokens: 1,
						TotalTokens:      3,
					},
				},
			}
			close(events)
			return events
		},
	}
	server, err := New(
		fakeRunner,
		WithUserID("editor-user"),
		WithImplementation("test-agent", "1.0.0"),
		withSessionIDGenerator(func() string { return "session-1" }),
		WithRunOptionsFunc(func(session Session) []agent.RunOption {
			return []agent.RunOption{agent.WithRuntimeState(map[string]any{
				"cwd": session.CWD,
			})}
		}),
	)
	require.NoError(t, err)

	client := &testClient{}
	clientConnection, cleanup := connectTestClient(t, server, client)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initializeResponse, err := clientConnection.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		acpsdk.ProtocolVersion(acpsdk.ProtocolVersionNumber),
		initializeResponse.ProtocolVersion,
	)
	require.NotNil(t, initializeResponse.AgentInfo)
	assert.Equal(t, "test-agent", initializeResponse.AgentInfo.Name)
	require.NotNil(t, initializeResponse.AgentCapabilities.SessionCapabilities.Close)
	assert.False(t, initializeResponse.AgentCapabilities.McpCapabilities.Http)
	assert.False(t, initializeResponse.AgentCapabilities.McpCapabilities.Sse)

	session, err := clientConnection.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.SessionId("session-1"), session.SessionId)

	promptResponse, err := clientConnection.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, promptResponse.StopReason)
	require.NotNil(t, promptResponse.Usage)
	assert.Equal(t, 3, promptResponse.Usage.TotalTokens)

	call := fakeRunner.lastCall()
	assert.Equal(t, "editor-user", call.userID)
	assert.Equal(t, "session-1", call.sessionID)
	assert.Equal(t, "hi", call.message.Content)
	assert.NotEmpty(t, call.options.RequestID)
	assert.Equal(t, "/workspace", call.options.RuntimeState["cwd"])

	updates := client.allUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, "hello", updates[0].Update.AgentMessageChunk.Content.Text.Text)

	_, err = clientConnection.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: session.SessionId,
	})
	require.NoError(t, err)
}

func TestServerPromptReassignsReusedProviderToolCallID(t *testing.T) {
	const providerID = "auto_call_0"
	fakeRunner := &testRunner{
		run: func(_ context.Context) <-chan *event.Event {
			events := make(chan *event.Event, 4)
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					ID: "response-1",
					Choices: []model.Choice{{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID: providerID,
								Function: model.FunctionDefinitionParam{
									Name: "search",
								},
							}},
						},
					}},
				},
			}
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleTool,
							ToolID:  providerID,
							Content: "first result",
						},
					}},
				},
			}
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					ID: "response-2",
					Choices: []model.Choice{{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID: providerID,
								Function: model.FunctionDefinitionParam{
									Name: "search",
								},
							}},
						},
					}},
				},
			}
			events <- &event.Event{
				InvocationID: "invocation",
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleTool,
							ToolID:  providerID,
							Content: "second result",
						},
					}},
				},
			}
			close(events)
			return events
		},
	}
	server, err := New(
		fakeRunner,
		withSessionIDGenerator(func() string { return "session-1" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	client := &testClient{}
	protocolAgent.connection = client
	ctx := context.Background()
	_, err = protocolAgent.NewSession(
		ctx,
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)

	_, err = protocolAgent.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: "session-1",
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("run tools")},
	})
	require.NoError(t, err)

	updates := client.allUpdates()
	require.Len(t, updates, 4)
	assert.Equal(t, acpsdk.ToolCallId("tool-1"), updates[0].Update.ToolCall.ToolCallId)
	assert.Equal(t, acpsdk.ToolCallId("tool-1"), updates[1].Update.ToolCallUpdate.ToolCallId)
	assert.Equal(t, acpsdk.ToolCallId("tool-2"), updates[2].Update.ToolCall.ToolCallId)
	assert.Equal(t, acpsdk.ToolCallId("tool-2"), updates[3].Update.ToolCallUpdate.ToolCallId)
}

func TestServerCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	producerFinished := make(chan struct{})
	fakeRunner := &testRunner{
		run: func(ctx context.Context) <-chan *event.Event {
			events := make(chan *event.Event)
			close(started)
			go func() {
				defer close(producerFinished)
				<-ctx.Done()
				close(cancelObserved)
				events <- &event.Event{}
				close(events)
			}()
			return events
		},
	}
	server, err := New(
		fakeRunner,
		withSessionIDGenerator(func() string { return "session-cancel" }),
	)
	require.NoError(t, err)
	clientConnection, cleanup := connectTestClient(t, server, &testClient{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := clientConnection.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	type promptResult struct {
		response acpsdk.PromptResponse
		err      error
	}
	result := make(chan promptResult, 1)
	go func() {
		response, promptErr := clientConnection.Prompt(ctx, acpsdk.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("wait")},
		})
		result <- promptResult{response: response, err: promptErr}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("runner did not start")
	}
	require.NoError(t, clientConnection.Cancel(ctx, acpsdk.CancelNotification{
		SessionId: session.SessionId,
	}))

	select {
	case <-cancelObserved:
	case <-ctx.Done():
		t.Fatal("runner context was not canceled")
	}
	select {
	case got := <-result:
		require.NoError(t, got.err)
		assert.Equal(t, acpsdk.StopReasonCancelled, got.response.StopReason)
	case <-ctx.Done():
		t.Fatal("prompt did not return")
	}
	select {
	case <-producerFinished:
	case <-ctx.Done():
		t.Fatal("runner event producer was not drained")
	}
}

func TestServeCleansUpOnExit(t *testing.T) {
	tests := []struct {
		name       string
		disconnect bool
		wantErr    error
	}{
		{name: "context canceled", wantErr: context.Canceled},
		{name: "peer disconnected", disconnect: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			cancelObserved := make(chan struct{})
			fakeRunner := &testRunner{
				run: func(ctx context.Context) <-chan *event.Event {
					events := make(chan *event.Event)
					close(started)
					go func() {
						<-ctx.Done()
						close(cancelObserved)
						close(events)
					}()
					return events
				},
			}
			server, err := New(
				fakeRunner,
				withSessionIDGenerator(func() string { return "session-serve-exit" }),
			)
			require.NoError(t, err)

			serverTransport, clientTransport := net.Pipe()
			defer serverTransport.Close()
			defer clientTransport.Close()
			serveCtx, cancelServe := context.WithCancel(context.Background())
			defer cancelServe()
			serveErr := make(chan error, 1)
			go func() {
				serveErr <- server.Serve(serveCtx, serverTransport, serverTransport)
			}()

			clientConnection := acpsdk.NewClientSideConnection(
				&testClient{},
				clientTransport,
				clientTransport,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			session, err := clientConnection.NewSession(ctx, acpsdk.NewSessionRequest{
				Cwd:        "/workspace",
				McpServers: []acpsdk.McpServer{},
			})
			require.NoError(t, err)

			type promptResult struct {
				response acpsdk.PromptResponse
				err      error
			}
			promptDone := make(chan promptResult, 1)
			go func() {
				response, promptErr := clientConnection.Prompt(ctx, acpsdk.PromptRequest{
					SessionId: session.SessionId,
					Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("wait")},
				})
				promptDone <- promptResult{response: response, err: promptErr}
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("runner did not start")
			}

			if test.disconnect {
				require.NoError(t, clientTransport.Close())
			} else {
				cancelServe()
			}

			select {
			case <-cancelObserved:
			case <-ctx.Done():
				t.Fatal("runner context was not canceled")
			}
			select {
			case err := <-serveErr:
				if test.wantErr == nil {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, test.wantErr)
				}
			case <-ctx.Done():
				t.Fatal("Serve did not return")
			}
			select {
			case <-promptDone:
			case <-ctx.Done():
				t.Fatal("prompt did not return")
			}
		})
	}
}

func TestProtocolAgentNewerPromptReplacesActivePrompt(t *testing.T) {
	started := make(chan struct{})
	firstCanceled := make(chan struct{})
	var first sync.Once
	runner := &testRunner{
		run: func(ctx context.Context) <-chan *event.Event {
			events := make(chan *event.Event)
			isFirst := false
			first.Do(func() {
				isFirst = true
				close(started)
			})
			if !isFirst {
				close(events)
				return events
			}
			go func() {
				<-ctx.Done()
				close(firstCanceled)
				close(events)
			}()
			return events
		},
	}
	server, err := New(
		runner,
		withSessionIDGenerator(func() string { return "session-serialized" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)

	firstResult := make(chan struct {
		response acpsdk.PromptResponse
		err      error
	}, 1)
	go func() {
		response, promptErr := protocolAgent.Prompt(
			context.Background(),
			acpsdk.PromptRequest{
				SessionId: session.SessionId,
				Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("first")},
			},
		)
		firstResult <- struct {
			response acpsdk.PromptResponse
			err      error
		}{response: response, err: promptErr}
	}()
	<-started

	second, err := protocolAgent.Prompt(
		context.Background(),
		acpsdk.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("second")},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, second.StopReason)
	select {
	case <-firstCanceled:
	case <-time.After(time.Second):
		t.Fatal("previous prompt was not canceled")
	}
	select {
	case first := <-firstResult:
		require.NoError(t, first.err)
		assert.Equal(t, acpsdk.StopReasonCancelled, first.response.StopReason)
	case <-time.After(time.Second):
		t.Fatal("previous prompt did not return")
	}
	assert.Equal(t, 2, runner.callCount())
}

func TestServerNewerPromptReplacesActivePrompt(t *testing.T) {
	started := make(chan struct{})
	firstCanceled := make(chan struct{})
	var first sync.Once
	runner := &testRunner{
		run: func(ctx context.Context) <-chan *event.Event {
			events := make(chan *event.Event)
			isFirst := false
			first.Do(func() {
				isFirst = true
				close(started)
			})
			if !isFirst {
				close(events)
				return events
			}
			go func() {
				<-ctx.Done()
				close(firstCanceled)
				close(events)
			}()
			return events
		},
	}
	server, err := New(
		runner,
		withSessionIDGenerator(func() string { return "session-overlap" }),
	)
	require.NoError(t, err)
	clientConnection, cleanup := connectTestClient(t, server, &testClient{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = clientConnection.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	require.NoError(t, err)
	session, err := clientConnection.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)

	firstResult := make(chan struct {
		response acpsdk.PromptResponse
		err      error
	}, 1)
	go func() {
		response, promptErr := clientConnection.Prompt(context.Background(), acpsdk.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("first")},
		})
		firstResult <- struct {
			response acpsdk.PromptResponse
			err      error
		}{response: response, err: promptErr}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first prompt did not start")
	}

	second, err := clientConnection.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("second")},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, second.StopReason)
	select {
	case <-firstCanceled:
	case <-ctx.Done():
		t.Fatal("previous prompt was not canceled")
	}
	select {
	case result := <-firstResult:
		require.NoError(t, result.err)
		assert.Equal(t, acpsdk.StopReasonCancelled, result.response.StopReason)
	case <-ctx.Done():
		t.Fatal("previous prompt did not complete")
	}
	assert.Equal(t, 2, runner.callCount())
}

func TestServerSessionIDsAreUniqueAcrossActiveConnections(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "shared-session" }),
	)
	require.NoError(t, err)
	agents := []*protocolAgent{newProtocolAgent(server), newProtocolAgent(server)}
	type result struct {
		agent    *protocolAgent
		response acpsdk.NewSessionResponse
		err      error
	}
	results := make(chan result, len(agents))
	var ready sync.WaitGroup
	ready.Add(len(agents))
	start := make(chan struct{})
	for _, agentInstance := range agents {
		go func(a *protocolAgent) {
			ready.Done()
			<-start
			response, newSessionErr := a.NewSession(
				context.Background(),
				acpsdk.NewSessionRequest{Cwd: "/workspace"},
			)
			results <- result{agent: a, response: response, err: newSessionErr}
		}(agentInstance)
	}
	ready.Wait()
	close(start)

	var succeeded result
	var successCount int
	var duplicateCount int
	for range agents {
		got := <-results
		if got.err == nil {
			succeeded = got
			successCount++
			continue
		}
		assert.ErrorContains(t, got.err, "duplicate session ID")
		duplicateCount++
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, duplicateCount)

	_, err = succeeded.agent.CloseSession(
		context.Background(),
		acpsdk.CloseSessionRequest{SessionId: succeeded.response.SessionId},
	)
	require.NoError(t, err)
	reuseAgent := newProtocolAgent(server)
	reused, err := reuseAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	assert.Equal(t, succeeded.response.SessionId, reused.SessionId)

	_, err = reuseAgent.CloseSession(
		context.Background(),
		acpsdk.CloseSessionRequest{SessionId: reused.SessionId},
	)
	require.NoError(t, err)
	assert.Zero(t, activeSessionIDCount(server))
}

func TestServerSessionIDActiveSetStaysBounded(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "reusable-session" }),
	)
	require.NoError(t, err)
	agentInstance := newProtocolAgent(server)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		response, newSessionErr := agentInstance.NewSession(
			ctx,
			acpsdk.NewSessionRequest{Cwd: "/workspace"},
		)
		require.NoError(t, newSessionErr)
		_, closeErr := agentInstance.CloseSession(
			ctx,
			acpsdk.CloseSessionRequest{SessionId: response.SessionId},
		)
		require.NoError(t, closeErr)
		assert.Zero(t, activeSessionIDCount(server))
	}
}

func TestServerShutdownReleasesSessionIDs(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "shutdown-session" }),
	)
	require.NoError(t, err)
	agentInstance := newProtocolAgent(server)
	_, err = agentInstance.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	require.Equal(t, 1, activeSessionIDCount(server))

	agentInstance.shutdown()
	assert.Zero(t, activeSessionIDCount(server))
}

func activeSessionIDCount(server *Server) int {
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	return len(server.activeSessionIDs)
}

func TestServerValidation(t *testing.T) {
	_, err := New(nil)
	assert.ErrorContains(t, err, "runner is required")

	server, err := New(&testRunner{})
	require.NoError(t, err)
	_, err = server.connect(nil, nil)
	assert.ErrorContains(t, err, "input is required")
	_, err = server.connect(&net.TCPConn{}, nil)
	assert.ErrorContains(t, err, "output is required")

	tests := []struct {
		name   string
		option Option
		err    string
	}{
		{name: "user ID", option: WithUserID(""), err: "user ID is required"},
		{
			name:   "implementation name",
			option: WithImplementation("", "1.0.0"),
			err:    "implementation name is required",
		},
		{
			name:   "implementation version",
			option: WithImplementation("agent", ""),
			err:    "implementation version is required",
		},
		{
			name:   "session ID generator",
			option: withSessionIDGenerator(nil),
			err:    "session ID generator is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(&testRunner{}, test.option)
			assert.ErrorContains(t, err, test.err)
		})
	}
}

func TestProtocolAgentSessionValidation(t *testing.T) {
	sessionIDs := []string{"", "session-1", "session-1"}
	server, err := New(
		&testRunner{},
		WithRunOptions(agent.WithRuntimeState(map[string]any{"static": true})),
		WithReasoningContentEnabled(true),
		withSessionIDGenerator(func() string {
			sessionID := sessionIDs[0]
			sessionIDs = sessionIDs[1:]
			return sessionID
		}),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	ctx := context.Background()

	_, err = protocolAgent.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: "relative"})
	assert.ErrorContains(t, err, "absolute path")
	_, err = protocolAgent.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acpsdk.McpServer{{}},
	})
	assert.ErrorContains(t, err, "only stdio MCP servers are supported")
	_, err = protocolAgent.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:                   "/workspace",
		AdditionalDirectories: []string{"/other"},
	})
	assert.ErrorContains(t, err, "additional directories")
	_, err = protocolAgent.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: "/workspace"})
	assert.ErrorContains(t, err, "empty session ID")

	response, err := protocolAgent.NewSession(
		ctx,
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	assert.Equal(t, acpsdk.SessionId("session-1"), response.SessionId)
	_, err = protocolAgent.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: "/workspace"})
	assert.ErrorContains(t, err, "duplicate session ID")

	_, err = protocolAgent.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: "unknown",
	})
	require.NoError(t, err)
	require.NoError(t, protocolAgent.Cancel(ctx, acpsdk.CancelNotification{
		SessionId: "unknown",
	}))
}

func TestProtocolAgentCloseSessionIsIdempotent(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "session-close" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	ctx := context.Background()
	session, err := protocolAgent.NewSession(
		ctx,
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)

	for rangeIndex := 0; rangeIndex < 2; rangeIndex++ {
		_, err = protocolAgent.CloseSession(ctx, acpsdk.CloseSessionRequest{
			SessionId: session.SessionId,
		})
		require.NoError(t, err)
	}

	_, err = protocolAgent.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hello")},
	})
	assert.ErrorContains(t, err, "unknown session")
}

func TestProtocolAgentUnsupportedMethods(t *testing.T) {
	server, err := New(&testRunner{})
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	ctx := context.Background()

	_, err = protocolAgent.Authenticate(ctx, acpsdk.AuthenticateRequest{})
	assert.ErrorContains(t, err, "Method not found")
	_, err = protocolAgent.Logout(ctx, acpsdk.LogoutRequest{})
	assert.ErrorContains(t, err, "Method not found")
	_, err = protocolAgent.ListSessions(ctx, acpsdk.ListSessionsRequest{})
	assert.ErrorContains(t, err, "Method not found")
	_, err = protocolAgent.ResumeSession(ctx, acpsdk.ResumeSessionRequest{})
	assert.ErrorContains(t, err, "Method not found")
	_, err = protocolAgent.SetSessionConfigOption(
		ctx,
		acpsdk.SetSessionConfigOptionRequest{},
	)
	assert.ErrorContains(t, err, "Method not found")
	_, err = protocolAgent.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{})
	assert.ErrorContains(t, err, "Method not found")
}

type runnerCall struct {
	userID    string
	sessionID string
	message   model.Message
	options   agent.RunOptions
}

type testRunner struct {
	run   func(context.Context) <-chan *event.Event
	mu    sync.Mutex
	calls []runnerCall
}

func (r *testRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOptions ...agent.RunOption,
) (<-chan *event.Event, error) {
	var options agent.RunOptions
	for _, runOption := range runOptions {
		runOption(&options)
	}
	r.mu.Lock()
	r.calls = append(r.calls, runnerCall{
		userID:    userID,
		sessionID: sessionID,
		message:   message,
		options:   options,
	})
	r.mu.Unlock()
	if r.run == nil {
		events := make(chan *event.Event)
		close(events)
		return events, nil
	}
	return r.run(ctx), nil
}

func (*testRunner) Close() error {
	return nil
}

func (r *testRunner) lastCall() runnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

func (r *testRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type testClient struct {
	mu      sync.Mutex
	updates []acpsdk.SessionNotification
}

func (c *testClient) SessionUpdate(
	_ context.Context,
	update acpsdk.SessionNotification,
) error {
	c.mu.Lock()
	c.updates = append(c.updates, update)
	c.mu.Unlock()
	return nil
}

func (c *testClient) allUpdates() []acpsdk.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpsdk.SessionNotification(nil), c.updates...)
}

func (*testClient) ReadTextFile(
	context.Context,
	acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errors.New("not implemented")
}

func (*testClient) WriteTextFile(
	context.Context,
	acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errors.New("not implemented")
}

func (*testClient) RequestPermission(
	context.Context,
	acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{}, errors.New("not implemented")
}

func (*testClient) CreateTerminal(
	context.Context,
	acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errors.New("not implemented")
}

func (*testClient) KillTerminal(
	context.Context,
	acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errors.New("not implemented")
}

func (*testClient) TerminalOutput(
	context.Context,
	acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errors.New("not implemented")
}

func (*testClient) ReleaseTerminal(
	context.Context,
	acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errors.New("not implemented")
}

func (*testClient) WaitForTerminalExit(
	context.Context,
	acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errors.New("not implemented")
}

func connectTestClient(
	t *testing.T,
	server *Server,
	client acpsdk.Client,
) (*acpsdk.ClientSideConnection, func()) {
	t.Helper()
	serverTransport, clientTransport := net.Pipe()
	serverConnection, err := server.connect(serverTransport, serverTransport)
	require.NoError(t, err)
	clientConnection := acpsdk.NewClientSideConnection(
		client,
		clientTransport,
		clientTransport,
	)
	return clientConnection, func() {
		require.NoError(t, clientTransport.Close())
		serverConnection.agent.shutdown()
		select {
		case <-serverConnection.done():
		case <-time.After(time.Second):
			t.Error("server connection did not close")
		}
	}
}
