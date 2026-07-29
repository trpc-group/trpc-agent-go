//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	legacya2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	corerunner "trpc.group/trpc-go/trpc-agent-go/runner"
	legacya2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	a2aE2EInput         = "hello from the compatibility matrix"
	a2aE2EPrefix        = "echo: "
	a2aE2EUserID        = "a2a-e2e-user"
	a2aE2EMetadataKey   = "a2a_e2e_metadata"
	a2aE2EMetadataValue = "metadata-value"
)

type a2aProtocolGeneration string

const (
	a2aProtocolV0 a2aProtocolGeneration = "v0"
	a2aProtocolV1 a2aProtocolGeneration = "v1"
)

type a2aE2EObservation struct {
	StreamingText string
	FinalText     string
	Done          bool
}

type a2aE2ERichObservation struct {
	Text                   string
	Reasoning              string
	Tag                    string
	StateDelta             map[string][]byte
	ContentParts           []model.ContentPart
	ToolCall               model.ToolCall
	ToolResult             model.Message
	Code                   string
	CodeTag                string
	CodeResult             string
	CodeResultTag          string
	MetadataOnlyTag        string
	MetadataOnlyStateDelta map[string][]byte
	FinalText              string
	Done                   bool
}

type a2aE2ECall struct {
	UserID     string
	SessionID  string
	Message    model.Message
	RunOptions agent.RunOptions
}

type a2aE2ERunner struct {
	mu        sync.Mutex
	streaming bool
	rich      bool
	calls     []a2aE2ECall
}

func (r *a2aE2ERunner) Run(
	_ context.Context,
	userID string,
	sessionID string,
	message model.Message,
	options ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.calls = append(r.calls, a2aE2ECall{
		UserID:     userID,
		SessionID:  sessionID,
		Message:    message,
		RunOptions: agent.NewRunOptions(options...),
	})
	r.mu.Unlock()

	if r.rich {
		return richA2AE2EEvents(r.streaming), nil
	}
	events := make(chan *event.Event, 3)
	if r.streaming {
		events <- &event.Event{Response: &model.Response{
			ID:        "a2a-e2e-response",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleAssistant, Content: a2aE2EPrefix},
			}},
		}}
		events <- &event.Event{Response: &model.Response{
			ID:        "a2a-e2e-response",
			Object:    model.ObjectTypeChatCompletionChunk,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleAssistant, Content: message.Content},
			}},
		}}
	} else {
		events <- &event.Event{Response: &model.Response{
			ID:     "a2a-e2e-response",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(a2aE2EPrefix + message.Content),
			}},
		}}
	}
	events <- &event.Event{Response: &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	}}
	close(events)
	return events, nil
}

func (*a2aE2ERunner) Close() error {
	return nil
}

func (r *a2aE2ERunner) Calls() []a2aE2ECall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]a2aE2ECall(nil), r.calls...)
}

func TestA2AProtocolVersionMatrixE2E(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "unary"
		if streaming {
			mode = "streaming"
		}
		t.Run(mode, func(t *testing.T) {
			legacyBackend := &a2aE2ERunner{streaming: streaming}
			legacyServer := newLegacyA2AE2EServer(t, legacyBackend)
			v1Backend := &a2aE2ERunner{streaming: streaming}
			v1Server := newV1A2AE2EServer(t, v1Backend, true)
			v1OnlyBackend := &a2aE2ERunner{streaming: streaming}
			v1OnlyServer := newV1A2AE2EServer(t, v1OnlyBackend, false)

			results := make(map[string]a2aE2EObservation)
			successCases := []struct {
				name       string
				client     a2aProtocolGeneration
				serverURL  string
				serverCall *a2aE2ERunner
			}{
				{
					name:       "v0_client_to_v0_server",
					client:     a2aProtocolV0,
					serverURL:  legacyServer.URL,
					serverCall: legacyBackend,
				},
				{
					name:       "v1_client_to_v1_server",
					client:     a2aProtocolV1,
					serverURL:  v1Server.URL,
					serverCall: v1Backend,
				},
				{
					name:       "v0_client_to_v1_compatible_server",
					client:     a2aProtocolV0,
					serverURL:  v1Server.URL,
					serverCall: v1Backend,
				},
			}
			for _, test := range successCases {
				t.Run(test.name, func(t *testing.T) {
					sessionID := mode + "-" + test.name
					callsBefore := len(test.serverCall.Calls())
					got, err := runA2AE2EClient(
						test.client,
						test.serverURL,
						streaming,
						sessionID,
					)
					require.NoError(t, err)
					require.Equal(t, a2aE2EPrefix+a2aE2EInput, got.FinalText)
					require.True(t, got.Done)
					if streaming {
						require.Equal(t, a2aE2EPrefix+a2aE2EInput, got.StreamingText)
					} else {
						require.Empty(t, got.StreamingText)
					}

					calls := test.serverCall.Calls()
					require.Len(t, calls, callsBefore+1)
					call := calls[len(calls)-1]
					require.Equal(t, a2aE2EUserID, call.UserID)
					require.Equal(t, sessionID, call.SessionID)
					require.Equal(t, a2aE2EInput, call.Message.Content)
					results[test.name] = got
				})
			}
			require.Equal(
				t,
				results["v1_client_to_v1_server"],
				results["v0_client_to_v1_compatible_server"],
				"v0 and v1 clients should observe the same text behavior from the v1 server",
			)

			v1Method := "SendMessage"
			v0Method := "message/send"
			if streaming {
				v1Method = "SendStreamingMessage"
				v0Method = "message/stream"
			}
			failureCases := []struct {
				name           string
				client         a2aProtocolGeneration
				serverURL      string
				serverCall     *a2aE2ERunner
				expectedMethod string
			}{
				{
					name:           "v1_client_to_v0_server",
					client:         a2aProtocolV1,
					serverURL:      legacyServer.URL,
					serverCall:     legacyBackend,
					expectedMethod: v1Method,
				},
				{
					name:           "v0_client_to_v1_server_without_compatibility",
					client:         a2aProtocolV0,
					serverURL:      v1OnlyServer.URL,
					serverCall:     v1OnlyBackend,
					expectedMethod: v0Method,
				},
			}
			for _, test := range failureCases {
				t.Run(test.name, func(t *testing.T) {
					callsBefore := len(test.serverCall.Calls())
					_, err := runA2AE2EClient(
						test.client,
						test.serverURL,
						streaming,
						mode+"-"+test.name,
					)
					require.ErrorContains(t, err, "Method not found")
					require.ErrorContains(t, err, test.expectedMethod)
					require.Len(t, test.serverCall.Calls(), callsBefore)
				})
			}
		})
	}
}

func TestA2ARichEventCompatibilityE2E(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "unary"
		if streaming {
			mode = "streaming"
		}
		t.Run(mode, func(t *testing.T) {
			legacyBackend := &a2aE2ERunner{streaming: streaming, rich: true}
			legacyServer := newLegacyA2AE2EServer(t, legacyBackend)
			v1Backend := &a2aE2ERunner{streaming: streaming, rich: true}
			v1Server := newV1A2AE2EServer(t, v1Backend, true)

			testCases := []struct {
				name                 string
				client               a2aProtocolGeneration
				serverURL            string
				serverCall           *a2aE2ERunner
				expectedContentParts []model.ContentPart
			}{
				{
					name:       "v0_client_to_v0_server",
					client:     a2aProtocolV0,
					serverURL:  legacyServer.URL,
					serverCall: legacyBackend,
				},
				{
					name:                 "v1_client_to_v1_server",
					client:               a2aProtocolV1,
					serverURL:            v1Server.URL,
					serverCall:           v1Backend,
					expectedContentParts: richA2AE2EContentParts(),
				},
				{
					name:                 "v0_client_to_v1_compatible_server",
					client:               a2aProtocolV0,
					serverURL:            v1Server.URL,
					serverCall:           v1Backend,
					expectedContentParts: richA2AE2EContentParts(),
				},
			}

			results := make(map[string]a2aE2ERichObservation)
			for _, test := range testCases {
				t.Run(test.name, func(t *testing.T) {
					sessionID := mode + "-rich-" + test.name
					input := model.NewUserMessage(a2aE2EInput)
					input.ContentParts = richA2AE2EContentParts()
					callsBefore := len(test.serverCall.Calls())
					events, err := runA2AE2EClientEvents(
						test.client,
						test.serverURL,
						streaming,
						sessionID,
						input,
						agent.WithRuntimeState(map[string]any{
							a2aE2EMetadataKey: a2aE2EMetadataValue,
						}),
					)
					require.NoError(t, err)

					calls := test.serverCall.Calls()
					require.Len(t, calls, callsBefore+1)
					call := calls[len(calls)-1]
					require.Equal(t, a2aE2EUserID, call.UserID)
					require.Equal(t, sessionID, call.SessionID)
					require.Equal(
						t,
						normalizeA2AE2EMessage(input),
						normalizeA2AE2EMessage(call.Message),
					)
					require.Equal(
						t,
						a2aE2EMetadataValue,
						call.RunOptions.RuntimeState[a2aE2EMetadataKey],
					)
					results[test.name] = observeA2AE2ERichEvents(
						t,
						events,
						test.expectedContentParts,
					)
				})
			}

			legacyBaseline := results["v0_client_to_v0_server"]
			legacyCompatible := results["v0_client_to_v1_compatible_server"]
			v1 := results["v1_client_to_v1_server"]
			legacyBaseline.ContentParts = nil
			legacyCompatible.ContentParts = nil
			v1.ContentParts = nil
			require.Equal(
				t,
				legacyBaseline,
				legacyCompatible,
				"v0 client common behavior should match its v0 server baseline",
			)
			require.Equal(
				t,
				v1,
				legacyCompatible,
				"v0 and v1 clients should observe the same common rich events from the v1 server",
			)
		})
	}
}

func richA2AE2EEvents(streaming bool) <-chan *event.Event {
	events := make(chan *event.Event, 8)
	contentMessage := model.Message{
		Role:             model.RoleAssistant,
		Content:          "rich content",
		ReasoningContent: "rich reasoning",
		ContentParts:     richA2AE2EContentParts(),
	}
	contentResponse := &model.Response{
		ID:      "rich-content",
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: contentMessage}},
	}
	if streaming {
		contentResponse.Object = model.ObjectTypeChatCompletionChunk
		contentResponse.IsPartial = true
		contentResponse.Choices[0] = model.Choice{Delta: contentMessage}
	}
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag("rich"),
		event.WithTag("text"),
		event.WithStateDelta(map[string][]byte{"rich-state": []byte(`"ready"`)}),
		event.WithResponse(contentResponse),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag("rich-tool-call"),
		event.WithResponse(&model.Response{
			ID:     "rich-tool-call",
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "rich-call",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "lookup",
						Arguments: []byte(`{"city":"Shenzhen"}`),
					},
				}},
			}}},
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag("rich-tool-result"),
		event.WithResponse(&model.Response{
			ID:     "rich-tool-result",
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{Message: model.Message{
				Role:     model.RoleTool,
				ToolID:   "rich-call",
				ToolName: "lookup",
				Content:  `{"temperature":30}`,
			}}},
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag(event.CodeExecutionTag),
		event.WithTag("rich-code"),
		event.WithResponse(&model.Response{
			ID:     "rich-code",
			Object: model.ObjectTypePostprocessingCodeExecution,
			Choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "print(1)",
			}}},
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag(event.CodeExecutionResultTag),
		event.WithTag("rich-code-result"),
		event.WithResponse(&model.Response{
			ID:     "rich-code-result",
			Object: model.ObjectTypePostprocessingCodeExecution,
			Choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "1",
			}}},
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag("metadata-only-a"),
		event.WithTag("metadata-only-b"),
		event.WithStateDelta(map[string][]byte{
			"metadata-state": []byte(`{"enabled":true}`),
			"binary-state":   {0, 1, 2, 255},
			"nil-state":      nil,
		}),
		event.WithResponse(&model.Response{
			ID:     "rich-metadata",
			Object: "custom.metadata",
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithTag("rich-final"),
		event.WithResponse(&model.Response{
			ID:     "rich-final",
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "rich final",
			}}},
		}),
	)
	events <- event.New(
		"rich-invocation",
		"rich-agent",
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		}),
	)
	close(events)
	return events
}

func richA2AE2EContentParts() []model.ContentPart {
	return []model.ContentPart{
		{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				Data:   []byte("image-bytes"),
				Format: "image/png",
			},
		},
		{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				URL:    "https://example.com/image.png",
				Format: "image/png",
			},
		},
		{
			Type: model.ContentTypeAudio,
			Audio: &model.Audio{
				Data:   []byte("audio-bytes"),
				Format: "audio/wav",
			},
		},
		{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     "report.pdf",
				Data:     []byte("file-bytes"),
				MimeType: "application/pdf",
			},
		},
		{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     "remote.txt",
				URL:      "https://example.com/remote.txt",
				MimeType: "text/plain",
			},
		},
	}
}

func observeA2AE2ERichEvents(
	t *testing.T,
	events []*event.Event,
	expectedContentParts []model.ContentPart,
) a2aE2ERichObservation {
	t.Helper()
	byID := make(map[string][]*event.Event)
	var done bool
	for _, evt := range events {
		done = done || evt.Response.Done
		if evt.Response.ID != "" {
			byID[evt.Response.ID] = append(byID[evt.Response.ID], evt)
		}
	}

	contentEvent := requireA2AE2EEvent(t, byID, "rich-content", func(evt *event.Event) bool {
		message := a2aE2EEventMessage(evt)
		return message.Content == "rich content" && message.ReasoningContent == "rich reasoning"
	})
	content := a2aE2EEventMessage(contentEvent)
	toolCallEvent := requireA2AE2EEvent(t, byID, "rich-tool-call", func(evt *event.Event) bool {
		return len(a2aE2EEventMessage(evt).ToolCalls) > 0
	})
	toolCallMessage := a2aE2EEventMessage(toolCallEvent)
	require.Len(t, toolCallMessage.ToolCalls, 1)
	toolResultEvent := requireA2AE2EEvent(t, byID, "rich-tool-result", func(evt *event.Event) bool {
		return a2aE2EEventMessage(evt).Role == model.RoleTool
	})
	codeEvent := requireA2AE2EEvent(t, byID, "rich-code", func(evt *event.Event) bool {
		return evt.ContainsTag(event.CodeExecutionTag)
	})
	codeResultEvent := requireA2AE2EEvent(t, byID, "rich-code-result", func(evt *event.Event) bool {
		return evt.ContainsTag(event.CodeExecutionResultTag)
	})
	metadataEvent := requireA2AE2EEvent(t, byID, "rich-metadata", func(evt *event.Event) bool {
		return evt.ContainsTag("metadata-only-a")
	})
	finalEvent := requireA2AE2EEvent(t, byID, "rich-final", func(evt *event.Event) bool {
		return evt.ContainsTag("rich-final")
	})

	observation := a2aE2ERichObservation{
		Text:                   content.Content,
		Reasoning:              content.ReasoningContent,
		Tag:                    contentEvent.Tag,
		StateDelta:             contentEvent.StateDelta,
		ContentParts:           content.ContentParts,
		ToolCall:               toolCallMessage.ToolCalls[0],
		ToolResult:             a2aE2EEventMessage(toolResultEvent),
		Code:                   a2aE2EEventMessage(codeEvent).Content,
		CodeTag:                codeEvent.Tag,
		CodeResult:             a2aE2EEventMessage(codeResultEvent).Content,
		CodeResultTag:          codeResultEvent.Tag,
		MetadataOnlyTag:        metadataEvent.Tag,
		MetadataOnlyStateDelta: metadataEvent.StateDelta,
		FinalText:              a2aE2EEventMessage(finalEvent).Content,
		Done:                   done,
	}
	require.Equal(t, "rich content", observation.Text)
	require.Equal(t, "rich reasoning", observation.Reasoning)
	require.Equal(t, "rich;text", observation.Tag)
	require.Equal(t, map[string][]byte{
		"rich-state": []byte(`"ready"`),
	}, observation.StateDelta)
	require.Equal(t, expectedContentParts, observation.ContentParts)
	require.Equal(t, model.ToolCall{
		ID:   "rich-call",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "lookup",
			Arguments: []byte(`{"city":"Shenzhen"}`),
		},
	}, observation.ToolCall)
	require.Equal(t, model.Message{
		Role:     model.RoleTool,
		Content:  `{"temperature":30}`,
		ToolID:   "rich-call",
		ToolName: "lookup",
	}, observation.ToolResult)
	require.Equal(t, "print(1)", observation.Code)
	require.Equal(t, event.CodeExecutionTag+";rich-code", observation.CodeTag)
	require.Equal(t, "1", observation.CodeResult)
	require.Equal(
		t,
		event.CodeExecutionResultTag+";rich-code-result",
		observation.CodeResultTag,
	)
	require.Equal(t, "metadata-only-a;metadata-only-b", observation.MetadataOnlyTag)
	require.Equal(t, map[string][]byte{
		"metadata-state": []byte(`{"enabled":true}`),
		"binary-state":   {0, 1, 2, 255},
		"nil-state":      nil,
	}, observation.MetadataOnlyStateDelta)
	require.Equal(t, "rich final", observation.FinalText)
	require.True(t, observation.Done)
	return observation
}

func requireA2AE2EEvent(
	t *testing.T,
	events map[string][]*event.Event,
	responseID string,
	match func(*event.Event) bool,
) *event.Event {
	t.Helper()
	for _, evt := range events[responseID] {
		if match == nil || match(evt) {
			return evt
		}
	}
	require.FailNow(t, "response event was not received", "response ID: %s", responseID)
	return nil
}

func a2aE2EEventMessage(evt *event.Event) model.Message {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}
	}
	choice := evt.Response.Choices[0]
	message := choice.Message
	if message.Role != "" ||
		message.Content != "" ||
		message.ReasoningContent != "" ||
		len(message.ContentParts) > 0 ||
		len(message.ToolCalls) > 0 ||
		message.ToolID != "" ||
		message.ToolName != "" {
		return message
	}
	return choice.Delta
}

func normalizeA2AE2EMessage(message model.Message) model.Message {
	for i := range message.ContentParts {
		file := message.ContentParts[i].File
		if file == nil || file.URL != "" || file.FileID == "" {
			continue
		}
		file.URL = file.FileID
		file.FileID = ""
	}
	return message
}

func newLegacyA2AE2EServer(
	t *testing.T,
	backend corerunner.Runner,
) *httptest.Server {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	card, err := legacya2aserver.NewAgentCard(
		"a2a-e2e-v0",
		"A2A v0 E2E server",
		httpServer.Listener.Addr().String(),
		true,
	)
	require.NoError(t, err)
	server, err := legacya2aserver.New(
		legacya2aserver.WithRunner(backend),
		legacya2aserver.WithAgentCard(card),
	)
	require.NoError(t, err)
	httpServer.Config.Handler = server.Handler()
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer
}

func newV1A2AE2EServer(
	t *testing.T,
	backend corerunner.Runner,
	legacyCompatibility bool,
	extraOptions ...a2aserver.Option,
) *httptest.Server {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	card, err := a2aserver.NewAgentCard(
		"a2a-e2e-v1",
		"A2A v1 E2E server",
		"1.0.0",
		httpServer.Listener.Addr().String(),
		true,
	)
	require.NoError(t, err)
	options := []a2aserver.Option{
		a2aserver.WithRunner(backend),
		a2aserver.WithAgentCard(card),
	}
	if legacyCompatibility {
		options = append(options, a2aserver.WithV0Compatibility())
	}
	options = append(options, extraOptions...)
	server, err := a2aserver.New(options...)
	require.NoError(t, err)
	httpServer.Config.Handler = server.Handler()
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer
}

func runA2AE2EClient(
	generation a2aProtocolGeneration,
	serverURL string,
	streaming bool,
	sessionID string,
) (a2aE2EObservation, error) {
	events, err := runA2AE2EClientEvents(
		generation,
		serverURL,
		streaming,
		sessionID,
		model.NewUserMessage(a2aE2EInput),
	)
	if err != nil {
		return a2aE2EObservation{}, err
	}
	var observation a2aE2EObservation
	for _, evt := range events {
		observation.Done = observation.Done || evt.Response.Done
		if len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if evt.Response.IsPartial {
			observation.StreamingText += choice.Delta.Content
		}
		if choice.Message.Content != "" {
			observation.FinalText = choice.Message.Content
		}
	}
	if observation.FinalText == "" && observation.StreamingText != "" {
		observation.FinalText = observation.StreamingText
	}
	if observation.FinalText == "" {
		return observation, fmt.Errorf(
			"%s client received no response text from %s",
			generation,
			serverURL,
		)
	}
	return observation, nil
}

func runA2AE2EClientEvents(
	generation a2aProtocolGeneration,
	serverURL string,
	streaming bool,
	sessionID string,
	message model.Message,
	options ...agent.RunOption,
) ([]*event.Event, error) {
	remoteAgent, err := newA2AE2EAgent(generation, serverURL, streaming)
	if err != nil {
		return nil, err
	}
	clientRunner := corerunner.NewRunner(
		"a2a-e2e-client",
		remoteAgent,
		corerunner.WithSessionService(inmemory.NewSessionService()),
	)
	defer clientRunner.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := clientRunner.Run(
		ctx,
		a2aE2EUserID,
		sessionID,
		message,
		options...,
	)
	if err != nil {
		return nil, err
	}
	var collected []*event.Event
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return collected, errors.New(evt.Error.Message)
		}
		if evt.Response != nil {
			collected = append(collected, evt)
		}
	}
	return collected, nil
}

func newA2AE2EAgent(
	generation a2aProtocolGeneration,
	serverURL string,
	streaming bool,
) (agent.Agent, error) {
	switch generation {
	case a2aProtocolV0:
		return legacya2aagent.New(
			legacya2aagent.WithAgentCardURL(serverURL),
			legacya2aagent.WithEnableStreaming(streaming),
			legacya2aagent.WithTransferStateKey("*"),
		)
	case a2aProtocolV1:
		return a2aagent.New(
			a2aagent.WithAgentCardURL(serverURL+protocol.AgentCardPath),
			a2aagent.WithEnableStreaming(streaming),
			a2aagent.WithTransferStateKey("*"),
		)
	default:
		return nil, fmt.Errorf("unsupported A2A protocol generation %q", generation)
	}
}
