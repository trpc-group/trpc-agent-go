//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	a2aprotocol "trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestA2ASessionIsListedAndReplayableThroughAGUI(t *testing.T) {
	const (
		userID        = "a2a-user"
		threadID      = "a2a-thread"
		userText      = "hello through A2A"
		assistantText = "hello from the agent"
	)

	sessionService := inmemory.NewSessionService()
	t.Cleanup(func() {
		if err := sessionService.Close(); err != nil {
			t.Errorf("close session service: %v", err)
		}
	})
	baseRunner := &a2aHistoryTestRunner{assistantText: assistantText}
	a2aRunner, err := aguirunner.WrapCoreRunner(
		baseRunner,
		uiAppName,
		sessionService,
	)
	if err != nil {
		t.Fatalf("wrap core runner: %v", err)
	}
	t.Cleanup(func() {
		if err := a2aRunner.Close(); err != nil {
			t.Errorf("close A2A runner: %v", err)
		}
	})

	httpServer := httptest.NewUnstartedServer(nil)
	a2aEndpoint := "http://" + httpServer.Listener.Addr().String() + a2aBasePath + "/"
	agentCard, err := a2aserver.NewAgentCard(
		"test-agent",
		"A test agent",
		agentVersion,
		a2aEndpoint,
		false,
	)
	if err != nil {
		t.Fatalf("create agent card: %v", err)
	}
	a2aServer, err := a2aserver.New(
		a2aserver.WithAgentCard(agentCard),
		a2aserver.WithRunner(a2aRunner),
	)
	if err != nil {
		t.Fatalf("create A2A server: %v", err)
	}
	uiServer, err := agui.New(
		baseRunner,
		agui.WithBasePath(uiBasePath),
		agui.WithPath(uiChatPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath(uiHistoryPath),
		agui.WithAppName(uiAppName),
		agui.WithSessionService(sessionService),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithUserIDResolver(userIDResolver),
		),
	)
	if err != nil {
		t.Fatalf("create AG-UI server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(uiBasePath+"/", userIDMiddleware(uiServer.Handler()))
	mux.Handle(
		sessionListPath,
		userIDMiddleware(newSessionListHandler(sessionService, uiAppName)),
	)
	mux.Handle("/", userIDMiddleware(a2aServer.Handler()))
	httpServer.Config.Handler = mux
	httpServer.Start()
	t.Cleanup(httpServer.Close)

	client, err := a2aclient.NewA2AClient(
		a2aEndpoint,
		a2aclient.WithHTTPClient(httpServer.Client()),
	)
	if err != nil {
		t.Fatalf("create A2A client: %v", err)
	}
	message := a2aprotocol.NewMessageWithContext(
		a2aprotocol.MessageRoleUser,
		[]*a2aprotocol.Part{a2aprotocol.NewTextPart(userText)},
		nil,
		stringPtr(threadID),
	)
	response, err := client.SendMessage(
		context.Background(),
		a2aprotocol.SendMessageParams{Message: message},
		a2aclient.WithRequestHeader(userIDHeader, userID),
	)
	if err != nil {
		t.Fatalf("send A2A message: %v", err)
	}
	if response == nil {
		t.Fatal("A2A response is nil")
	}
	task := response.GetTask()
	if task == nil {
		t.Fatal("A2A response task is nil")
	}
	if task.Status.State != a2aprotocol.TaskStateCompleted {
		t.Fatalf("A2A task state = %q, want %q", task.Status.State, a2aprotocol.TaskStateCompleted)
	}

	listRequest, err := http.NewRequest(
		http.MethodGet,
		httpServer.URL+sessionListPath,
		nil,
	)
	if err != nil {
		t.Fatalf("create session list request: %v", err)
	}
	listRequest.Header.Set(userIDHeader, userID)
	listResponse, err := httpServer.Client().Do(listRequest)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	t.Cleanup(func() {
		if err := listResponse.Body.Close(); err != nil {
			t.Errorf("close session list response: %v", err)
		}
	})
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("session list status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	if got := listResponse.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("session list Cache-Control = %q, want %q", got, "no-store")
	}
	var listed sessionListResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode session list: %v", err)
	}
	if len(listed.Sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(listed.Sessions))
	}
	if listed.Sessions[0].ThreadID != threadID {
		t.Fatalf("listed thread ID = %q, want %q", listed.Sessions[0].ThreadID, threadID)
	}

	historyRequest, err := http.NewRequest(
		http.MethodPost,
		httpServer.URL+uiBasePath+uiHistoryPath,
		strings.NewReader(`{"threadId":"a2a-thread","runId":"history"}`),
	)
	if err != nil {
		t.Fatalf("create history request: %v", err)
	}
	historyRequest.Header.Set("Content-Type", "application/json")
	historyRequest.Header.Set(userIDHeader, userID)
	historyResponse, err := httpServer.Client().Do(historyRequest)
	if err != nil {
		t.Fatalf("get AG-UI history: %v", err)
	}
	t.Cleanup(func() {
		if err := historyResponse.Body.Close(); err != nil {
			t.Errorf("close history response: %v", err)
		}
	})
	if historyResponse.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, want %d", historyResponse.StatusCode, http.StatusOK)
	}
	snapshot := readMessagesSnapshot(t, historyResponse)
	if len(snapshot.Messages) != 2 {
		t.Fatalf("history message count = %d, want 2", len(snapshot.Messages))
	}
	assertMessageContent(t, snapshot.Messages[0], aguitypes.RoleUser, userText)
	assertMessageContent(t, snapshot.Messages[1], aguitypes.RoleAssistant, assistantText)
}

type a2aHistoryTestRunner struct {
	assistantText string
}

func (r *a2aHistoryTestRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event, 2)
	assistant := event.NewResponseEvent("invocation", "test-agent", &model.Response{
		ID:     "assistant-message",
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(r.assistantText),
		}},
	})
	assistant.RequestID = "a2a-run"
	completion := event.NewResponseEvent("invocation", uiAppName, &model.Response{
		ID:     "runner-completion",
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	completion.RequestID = "a2a-run"
	events <- assistant
	events <- completion
	close(events)
	return events, nil
}

func (*a2aHistoryTestRunner) Close() error { return nil }

func readMessagesSnapshot(
	t *testing.T,
	response *http.Response,
) *aguievents.MessagesSnapshotEvent {
	t.Helper()
	var snapshot *aguievents.MessagesSnapshotEvent
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		evt, err := aguievents.EventFromJSON([]byte(payload))
		if err != nil {
			t.Fatalf("decode AG-UI history event: %v", err)
		}
		if current, ok := evt.(*aguievents.MessagesSnapshotEvent); ok {
			snapshot = current
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read AG-UI history response: %v", err)
	}
	if snapshot == nil {
		t.Fatal("AG-UI history did not contain a messages snapshot")
	}
	return snapshot
}

func assertMessageContent(
	t *testing.T,
	message aguitypes.Message,
	role aguitypes.Role,
	content string,
) {
	t.Helper()
	if message.Role != role {
		t.Errorf("message role = %q, want %q", message.Role, role)
	}
	got, ok := message.ContentString()
	if !ok {
		t.Fatal("message content is not a string")
	}
	if got != content {
		t.Errorf("message content = %q, want %q", got, content)
	}
}

func stringPtr(value string) *string { return &value }

var _ trunner.Runner = (*a2aHistoryTestRunner)(nil)
