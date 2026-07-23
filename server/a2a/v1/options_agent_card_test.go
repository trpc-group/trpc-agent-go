//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type cardTestTool struct {
	declaration *tool.Declaration
}

func (t *cardTestTool) Declaration() *tool.Declaration {
	return t.declaration
}

type optionMessageConverter struct{}

func (*optionMessageConverter) ConvertToAgentMessage(
	context.Context,
	protocol.Message,
) (*model.Message, error) {
	message := model.NewUserMessage("converted")
	return &message, nil
}

type optionEventConverter struct{}

func (*optionEventConverter) ConvertStreamingToA2AMessage(
	context.Context,
	*event.Event,
	EventToA2AStreamingOptions,
) (protocol.StreamEvent, error) {
	return nil, nil
}

func TestAgentCardValidationToolsAndHandler(t *testing.T) {
	if _, err := NewAgentCard("", "description", "localhost:8080", false); err == nil {
		t.Fatal("NewAgentCard accepted an empty name")
	}
	if _, err := NewAgentCard("agent", "description", "", false); err == nil {
		t.Fatal("NewAgentCard accepted an empty host")
	}

	streaming := true
	card, err := NewAgentCard(
		"agent",
		"description",
		"localhost:8080",
		streaming,
		WithCardTools(
			nil,
			&cardTestTool{},
			&cardTestTool{declaration: &tool.Declaration{
				Name:        "lookup",
				Description: "look things up",
			}},
		),
	)
	if err != nil {
		t.Fatalf("NewAgentCard failed: %v", err)
	}
	if len(card.Skills) != 2 || card.Skills[0].Name != "agent" ||
		card.Skills[1].Name != "lookup" {
		t.Fatalf("skills = %#v", card.Skills)
	}
	if card.Capabilities.Streaming == nil || !*card.Capabilities.Streaming {
		t.Fatalf("streaming capability = %#v", card.Capabilities.Streaming)
	}
	if len(card.Capabilities.Extensions) != 1 {
		t.Fatalf("extensions = %#v", card.Capabilities.Extensions)
	}

	current := card
	handler := NewAgentCardHandler(func() a2aserver.AgentCard { return current })
	for _, test := range []struct {
		name       string
		method     string
		wantStatus int
	}{
		{name: "get", method: http.MethodGet, wantStatus: http.StatusOK},
		{name: "options", method: http.MethodOptions, wantStatus: http.StatusOK},
		{name: "post", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, "/", nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if test.method == http.MethodGet {
				var got a2aserver.AgentCard
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatalf("response unmarshal failed: %v", err)
				}
				if got.Name != card.Name {
					t.Fatalf("card name = %q, want %q", got.Name, card.Name)
				}
			}
		})
	}
	recorder := httptest.NewRecorder()
	NewAgentCardHandler(nil).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nil getter status = %d, want 500", recorder.Code)
	}
}

func TestServerContextAuthenticationAndOptions(t *testing.T) {
	if id, ok := UserIDFromContext(nil); ok || id != "" {
		t.Fatalf("nil context user = (%q, %v)", id, ok)
	}
	if id, ok := UserIDFromContext(context.Background()); ok || id != "" {
		t.Fatalf("empty context user = (%q, %v)", id, ok)
	}
	ctx := NewContextWithUserID(context.Background(), "user")
	if id, ok := UserIDFromContext(ctx); !ok || id != "user" {
		t.Fatalf("context user = (%q, %v), want user", id, ok)
	}
	if got := NewContextWithUserID(nil, "user"); got != nil {
		t.Fatalf("nil context result = %#v, want nil", got)
	}

	provider := &defaultAuthProvider{userIDHeader: "X-Custom-User"}
	if _, err := provider.Authenticate(nil); err == nil {
		t.Fatal("Authenticate accepted nil request")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Custom-User", "custom-user")
	user, err := provider.Authenticate(request)
	if err != nil || user.ID != "custom-user" {
		t.Fatalf("Authenticate user = %#v, err = %v", user, err)
	}

	if got := normalizeMetadataKeys(nil); len(got) != 0 || got == nil {
		t.Fatalf("normalized nil keys = %#v, want non-nil empty", got)
	}
	if got := normalizeMetadataKeys([]string{" a ", "", "a", "b"}); len(got) != 2 ||
		got[0] != "a" || got[1] != "b" {
		t.Fatalf("normalized keys = %#v", got)
	}

	runner := &modeTestRunner{}
	card := a2aserver.AgentCard{Name: "agent", URL: "http://localhost:8080"}
	messageConverter := &optionMessageConverter{}
	eventConverter := &optionEventConverter{}
	hookCalled := false
	managerBuilder := func(taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
		return nil, nil
	}
	rewriter := func(context.Context, protocol.StreamEvent) protocol.StreamEvent { return nil }
	partMapper := func(context.Context, *event.Event) ([]*protocol.Part, error) { return nil, nil }
	handler := func(context.Context, *protocol.Message, error) (*protocol.Message, error) {
		return nil, errors.New("handled")
	}
	opts := &options{}
	for _, option := range []Option{
		WithRunner(runner),
		WithAgentCard(card),
		WithProcessMessageHook(func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
			hookCalled = true
			return next
		}),
		WithUserIDHeader("X-Custom-User"),
		WithExtraA2AOptions(),
		WithTaskManagerBuilder(managerBuilder),
		WithRunOptions(agent.WithRuntimeState(map[string]any{"key": "value"})),
		WithA2AToAgentConverter(messageConverter),
		WithEventToA2AConverter(eventConverter),
		WithGraphEventObjectAllowlist(" graph.* ", "", "graph.*"),
		WithResponseRewriter(rewriter),
		WithADKCompatibility(true),
		WithEventToA2APartMapper(nil),
		WithEventToA2APartMapper(partMapper),
		WithDebugLogging(true),
		WithErrorHandler(handler),
	} {
		option(opts)
	}
	if opts.runner != runner || opts.agentCard == nil || opts.agentCard.Name != card.Name ||
		opts.userIDHeader != "X-Custom-User" ||
		opts.taskManagerBuilder == nil || len(opts.runOptions) != 1 ||
		opts.a2aToAgentConverter != messageConverter ||
		opts.eventToA2AConverter != eventConverter ||
		len(opts.graphEventObjectAllowlist) != 1 ||
		opts.responseRewriter == nil || !opts.adkCompatibility ||
		len(opts.eventPartMappers) != 1 || !opts.debugLogging ||
		opts.errorHandler == nil {
		t.Fatalf("options not applied: %#v", opts)
	}
	opts.processorHook(nil)
	if !hookCalled {
		t.Fatal("processor hook was not installed")
	}
	WithUserIDHeader("")(opts)
	if opts.userIDHeader != "X-Custom-User" {
		t.Fatal("empty user ID header overwrote configured value")
	}

	message := protocol.NewMessage(protocol.MessageRoleUser, nil)
	response, err := defaultErrorHandler(context.Background(), &message, errors.New("failed"))
	if err != nil || response == nil || len(response.Parts) != 1 {
		t.Fatalf("defaultErrorHandler response = %#v, err = %v", response, err)
	}
}

func TestStateDeltaRoundTripAndInvalidInput(t *testing.T) {
	state := map[string][]byte{"key": []byte(`{"nested":true}`)}
	encoded := EncodeStateDeltaMetadata(state)
	decoded := DecodeStateDeltaMetadata(encoded)
	if string(decoded["key"]) != `{"nested":true}` {
		t.Fatalf("decoded state = %#v", decoded)
	}
	if got := DecodeStateDeltaMetadata(make(chan int)); got != nil {
		t.Fatalf("invalid state delta = %#v, want nil", got)
	}
}
