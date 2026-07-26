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
		WithSessionIDGenerator(func() string { return "session-1" }),
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

	session, err := clientConnection.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.SessionId("session-1"), session.SessionId)

	messageID := "9a27ea91-a754-40cc-a557-105011523714"
	promptResponse, err := clientConnection.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		MessageId: &messageID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, promptResponse.StopReason)
	assert.Equal(t, &messageID, promptResponse.UserMessageId)
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
		WithSessionIDGenerator(func() string { return "session-cancel" }),
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

func TestProtocolAgentRejectsConcurrentPromptUntilRunFinishes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &testRunner{
		run: func(ctx context.Context) <-chan *event.Event {
			events := make(chan *event.Event)
			close(started)
			go func() {
				<-ctx.Done()
				<-release
				close(events)
			}()
			return events
		},
	}
	server, err := New(
		runner,
		WithSessionIDGenerator(func() string { return "session-serialized" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)

	firstResult := make(chan error, 1)
	go func() {
		_, promptErr := protocolAgent.Prompt(
			context.Background(),
			acpsdk.PromptRequest{
				SessionId: session.SessionId,
				Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("first")},
			},
		)
		firstResult <- promptErr
	}()
	<-started

	_, err = protocolAgent.Prompt(
		context.Background(),
		acpsdk.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("second")},
		},
	)
	assert.ErrorContains(t, err, "prompt already in progress")
	assert.Equal(t, 1, runner.callCount())

	require.NoError(t, protocolAgent.Cancel(
		context.Background(),
		acpsdk.CancelNotification{SessionId: session.SessionId},
	))
	select {
	case err := <-firstResult:
		t.Fatalf("prompt returned before the runner finished: %v", err)
	default:
	}
	close(release)
	require.NoError(t, <-firstResult)
}

func TestServerSessionIDsAreUniqueAcrossConnectionsAndLifetime(t *testing.T) {
	server, err := New(
		&testRunner{},
		WithSessionIDGenerator(func() string { return "shared-session" }),
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
	_, err = newProtocolAgent(server).NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	assert.ErrorContains(t, err, "duplicate session ID")
}

func TestServerValidation(t *testing.T) {
	_, err := New(nil)
	assert.ErrorContains(t, err, "runner is required")

	server, err := New(&testRunner{})
	require.NoError(t, err)
	_, err = server.Connect(nil, nil)
	assert.ErrorContains(t, err, "input is required")
	_, err = server.Connect(&net.TCPConn{}, nil)
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
			option: WithSessionIDGenerator(nil),
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
		WithSessionIDGenerator(func() string {
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
	assert.ErrorContains(t, err, "dynamic MCP servers")
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
	assert.ErrorContains(t, err, "unknown session")
	require.NoError(t, protocolAgent.Cancel(ctx, acpsdk.CancelNotification{
		SessionId: "unknown",
	}))
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
	serverConnection, err := server.Connect(serverTransport, serverTransport)
	require.NoError(t, err)
	clientConnection := acpsdk.NewClientSideConnection(
		client,
		clientTransport,
		clientTransport,
	)
	return clientConnection, func() {
		require.NoError(t, clientTransport.Close())
		select {
		case <-serverConnection.Done():
		case <-time.After(time.Second):
			t.Error("server connection did not close")
		}
	}
}
