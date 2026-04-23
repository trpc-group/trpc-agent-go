//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	runnerlog "trpc.group/trpc-go/trpc-agent-go/log"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// mockAgent implements the agent.Agent interface for testing.
type mockAgent struct {
	name string
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Create a mock response event.
	responseEvent := &event.Event{
		Response: &model.Response{
			ID:    "test-response",
			Model: "test-model",
			Done:  true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Hello! I received your message: " + invocation.Message.Content,
					},
				},
			},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "test-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- responseEvent
	close(eventCh)

	return eventCh, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

type capturingRoleAgent struct {
	name         string
	capturedRole model.Role
}

func (m *capturingRoleAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Captures invocation role for testing",
	}
}

func (m *capturingRoleAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *capturingRoleAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *capturingRoleAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	m.capturedRole = invocation.Message.Role
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *capturingRoleAgent) Tools() []tool.Tool {
	return nil
}

type capturingInvocationMessagesAgent struct {
	name              string
	invocationMessage model.Message
}

func (m *capturingInvocationMessagesAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Captures invocation messages for testing",
	}
}

func (m *capturingInvocationMessagesAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *capturingInvocationMessagesAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *capturingInvocationMessagesAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	m.invocationMessage = invocation.Message
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *capturingInvocationMessagesAgent) Tools() []tool.Tool {
	return nil
}

type staticModel struct {
	name    string
	content string
}

type unsupportedSteerRunner struct{}

func (unsupportedSteerRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (unsupportedSteerRunner) Close() error {
	return nil
}

type emptyIDModel struct {
	name    string
	content string
}

const staticModelResponseIDPrefix = "static-model-response-"

func (m *staticModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:        staticModelResponseIDPrefix + m.name,
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *staticModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *emptyIDModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:        "",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *emptyIDModel) Info() model.Info { return model.Info{Name: m.name} }

type runnerStructuredOutputTypedPayload struct {
	Answer string `json:"answer"`
	Score  int    `json:"score"`
}

type realStructuredOutputComplexDetail struct {
	City  string `json:"city"`
	Codes []int  `json:"codes"`
}

type realStructuredOutputComplexPayload struct {
	Answer string                             `json:"answer"`
	Detail *realStructuredOutputComplexDetail `json:"detail"`
	Tags   []string                           `json:"tags"`
	Scores [2]int                             `json:"scores"`
}

type realStructuredOutputMapPayload struct {
	Answer string            `json:"answer"`
	Labels map[string]string `json:"labels"`
}

type capturedModelRequest struct {
	messages         []model.Message
	structuredOutput *model.StructuredOutput
}

type sequentialModel struct {
	name      string
	responses []*model.Response

	mu       sync.Mutex
	requests []*capturedModelRequest
	nextIdx  int
}

func (m *sequentialModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req != nil {
		m.requests = append(
			m.requests,
			cloneCapturedModelRequest(req),
		)
	}

	if m.nextIdx >= len(m.responses) {
		return nil, fmt.Errorf(
			"unexpected model call %d",
			m.nextIdx,
		)
	}

	resp := m.responses[m.nextIdx]
	m.nextIdx++

	ch := make(chan *model.Response, 1)
	ch <- resp
	close(ch)
	return ch, nil
}

func (m *sequentialModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *sequentialModel) Requests() []*capturedModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*capturedModelRequest(nil), m.requests...)
}

type capturingStructuredOutputModel struct {
	name    string
	content string

	mu       sync.Mutex
	requests []*capturedModelRequest
}

func (m *capturingStructuredOutputModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneCapturedModelRequest(req))
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:        staticModelResponseIDPrefix + m.name,
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *capturingStructuredOutputModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *capturingStructuredOutputModel) LatestRequest() *capturedModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

func cloneCapturedModelRequest(req *model.Request) *capturedModelRequest {
	if req == nil {
		return nil
	}
	cloned := &capturedModelRequest{
		messages: append([]model.Message(nil), req.Messages...),
	}
	if req.StructuredOutput != nil {
		structuredOutput := *req.StructuredOutput
		if req.StructuredOutput.JSONSchema != nil {
			jsonSchema := *req.StructuredOutput.JSONSchema
			structuredOutput.JSONSchema = &jsonSchema
		}
		cloned.structuredOutput = &structuredOutput
	}
	return cloned
}

func firstSystemMessageContent(messages []model.Message) string {
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			return msg.Content
		}
	}
	return ""
}

func findMessageIndex(
	messages []model.Message,
	match func(model.Message) bool,
) int {
	for i, message := range messages {
		if match(message) {
			return i
		}
	}
	return -1
}

func collectStructuredOutput(events <-chan *event.Event) any {
	var structured any
	for evt := range events {
		if evt != nil && evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	return structured
}

func TestEnqueueUserMessage_Errors(t *testing.T) {
	err := EnqueueUserMessage(
		unsupportedSteerRunner{},
		"req-1",
		model.NewUserMessage("hello"),
	)
	require.ErrorIs(t, err, ErrQueuedUserMessageUnsupported)

	ag := &mockAgent{name: "runner-agent"}
	r := NewRunner("runner-steer-errors", ag)

	err = EnqueueUserMessage(r, "", model.NewUserMessage("hello"))
	require.EqualError(t, err, "runner: empty request id")

	err = EnqueueUserMessage(
		r,
		"req-1",
		model.NewAssistantMessage("no"),
	)
	require.ErrorIs(t, err, ErrInvalidQueuedUserMessage)

	err = EnqueueUserMessage(
		r,
		"req-1",
		model.Message{Role: model.RoleUser},
	)
	require.ErrorIs(t, err, ErrInvalidQueuedUserMessage)

	err = EnqueueUserMessage(
		r,
		"req-1",
		model.NewUserMessage("hello"),
	)
	require.ErrorIs(t, err, ErrRunNotFound)
}

func TestRunner_EnqueueUserMessage_ConsumesAtSafeBoundary(
	t *testing.T,
) {
	const (
		appName          = "runner-steer-safe-boundary"
		userID           = "user-1"
		sessionID        = "session-1"
		requestID        = "req-steer-1"
		toolName         = "lookup"
		toolDescription  = "Looks up a topic"
		initialQuestion  = "Search alpha"
		steerQuestionOne = "Also compare beta"
		steerQuestionTwo = "Summarize in one sentence"
		finalAnswer      = "Alpha and beta compared."
	)

	type lookupInput struct {
		Topic string `json:"topic"`
	}
	type lookupOutput struct {
		Result string `json:"result"`
	}

	modelStub := &sequentialModel{
		name: "sequential-steer-model",
		responses: []*model.Response{
			{
				ID:   "resp-tool-call",
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "tool-call-1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      toolName,
								Arguments: []byte(`{"topic":"alpha"}`),
							},
						}},
					},
				}},
			},
			{
				ID: "resp-final",
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage(finalAnswer),
				}},
				Done: true,
			},
		},
	}

	var (
		runnerInstance Runner
		enqueueErrs    []error
		enqueueMu      sync.Mutex
	)

	toolImpl := function.NewFunctionTool(
		func(
			_ context.Context,
			input lookupInput,
		) (lookupOutput, error) {
			enqueueMu.Lock()
			enqueueErrs = append(
				enqueueErrs,
				EnqueueUserMessage(
					runnerInstance,
					requestID,
					model.NewUserMessage(steerQuestionOne),
				),
				EnqueueUserMessage(
					runnerInstance,
					requestID,
					model.Message{Content: steerQuestionTwo},
				),
			)
			enqueueMu.Unlock()
			return lookupOutput{
				Result: "tool result for " + input.Topic,
			}, nil
		},
		function.WithName(toolName),
		function.WithDescription(toolDescription),
	)

	ag := llmagent.New(
		"steer-agent",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{toolImpl}),
	)

	runnerInstance = NewRunner(appName, ag)

	events, err := runnerInstance.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage(initialQuestion),
		agent.WithRequestID(requestID),
	)
	require.NoError(t, err)

	var (
		completionEvent *event.Event
		sawFinalAnswer  bool
	)
	for evt := range events {
		if evt != nil && evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == finalAnswer {
			sawFinalAnswer = true
		}
		if evt != nil && evt.IsRunnerCompletion() {
			completionEvent = evt
		}
	}

	require.NotNil(t, completionEvent)
	require.True(t, sawFinalAnswer)

	enqueueMu.Lock()
	require.Len(t, enqueueErrs, 2)
	for _, enqueueErr := range enqueueErrs {
		require.NoError(t, enqueueErr)
	}
	enqueueMu.Unlock()

	requests := modelStub.Requests()
	require.Len(t, requests, 2)

	secondRequest := requests[1]
	require.NotNil(t, secondRequest)

	initialIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleUser &&
				message.Content == initialQuestion
		},
	)
	toolCallIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleAssistant &&
				len(message.ToolCalls) == 1 &&
				message.ToolCalls[0].Function.Name == toolName
		},
	)
	toolResultIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleTool &&
				message.ToolID == "tool-call-1" &&
				message.ToolName == toolName &&
				message.Content != ""
		},
	)
	steerOneIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleUser &&
				message.Content == steerQuestionOne
		},
	)
	steerTwoIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleUser &&
				message.Content == steerQuestionTwo
		},
	)

	require.NotEqual(t, -1, initialIdx)
	require.NotEqual(t, -1, toolCallIdx)
	require.NotEqual(t, -1, toolResultIdx)
	require.NotEqual(t, -1, steerOneIdx)
	require.NotEqual(t, -1, steerTwoIdx)

	require.Less(t, initialIdx, toolCallIdx)
	require.Less(t, toolCallIdx, toolResultIdx)
	require.Less(t, toolResultIdx, steerOneIdx)
	require.Less(t, steerOneIdx, steerTwoIdx)
	require.Contains(
		t,
		secondRequest.messages[toolResultIdx].Content,
		"tool result for alpha",
	)
}

func TestRunner_EnqueueUserMessage_ClosingRunReturnsNotFound(
	t *testing.T,
) {
	rr := &runner{}
	queue := steer.NewQueue()

	_, err := rr.registerRun(
		"req-closing",
		RunStatus{},
		func() {},
		queue,
	)
	require.NoError(t, err)

	queue.Close()

	err = rr.EnqueueUserMessage(
		"req-closing",
		model.NewUserMessage("hello"),
	)
	require.ErrorIs(t, err, ErrRunNotFound)
}

func runRunnerWithTypedStructuredOutput(
	t *testing.T,
	ag agent.Agent,
	description string,
) *runnerStructuredOutputTypedPayload {
	t.Helper()
	r := NewRunner(
		"typed-structured-output-wrapper-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-wrapper",
		"session-wrapper",
		model.NewUserMessage("hello"),
		agent.WithStructuredOutputJSON(
			new(runnerStructuredOutputTypedPayload),
			true,
			description,
		),
	)
	require.NoError(t, err)
	structured := collectStructuredOutput(eventCh)
	payload, ok := structured.(*runnerStructuredOutputTypedPayload)
	require.True(t, ok, "expected typed structured output payload")
	require.Equal(t, "ok", payload.Answer)
	require.Equal(t, 7, payload.Score)
	return payload
}

func TestRunner_Run_WithRunStructuredOutputJSON_InjectsSchemaAndEmitsTypedPayload(t *testing.T) {
	modelImpl := &capturingStructuredOutputModel{
		name:    "typed-structured-output-model",
		content: `{"answer":"ok","score":7}`,
	}
	ag := llmagent.New(
		"typed-structured-output-agent",
		llmagent.WithModel(modelImpl),
	)
	r := NewRunner(
		"typed-structured-output-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)

	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithStructuredOutputJSON(
			new(runnerStructuredOutputTypedPayload),
			true,
			"Return one typed payload.",
		),
	)
	require.NoError(t, err)

	structured := collectStructuredOutput(eventCh)
	payload, ok := structured.(*runnerStructuredOutputTypedPayload)
	require.True(t, ok, "expected typed structured output payload")
	require.Equal(t, "ok", payload.Answer)
	require.Equal(t, 7, payload.Score)

	captured := modelImpl.LatestRequest()
	require.NotNil(t, captured, "expected one model request to be captured")
	require.NotNil(t, captured.structuredOutput)
	require.Equal(t, model.StructuredOutputJSONSchema, captured.structuredOutput.Type)
	require.NotNil(t, captured.structuredOutput.JSONSchema)
	require.Equal(t, "runnerStructuredOutputTypedPayload", captured.structuredOutput.JSONSchema.Name)
	require.True(t, captured.structuredOutput.JSONSchema.Strict)
	require.Equal(t, "Return one typed payload.", captured.structuredOutput.JSONSchema.Description)
	require.Equal(t, "object", captured.structuredOutput.JSONSchema.Schema["type"])
	properties, ok := captured.structuredOutput.JSONSchema.Schema["properties"].(map[string]any)
	require.True(t, ok, "expected generated schema properties")
	require.Contains(t, properties, "answer")
	require.Contains(t, properties, "score")

	systemContent := firstSystemMessageContent(captured.messages)
	require.NotEmpty(t, systemContent, "expected structured output instructions to create one system message")
	assert.Contains(t, systemContent, "IMPORTANT: Return ONLY a JSON object")
	assert.Contains(t, systemContent, `"answer"`)
	assert.Contains(t, systemContent, `"score"`)
	assert.NotContains(t, systemContent, "You MAY call tools")
}

func TestRunner_Run_WithRunStructuredOutputJSONSchema_InjectsSchemaAndEmitsUntypedPayload(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string"},
			"count":  map[string]any{"type": "integer"},
		},
		"additionalProperties": false,
	}
	modelImpl := &capturingStructuredOutputModel{
		name:    "untyped-structured-output-model",
		content: `{"status":"ok","count":3}`,
	}
	ag := llmagent.New(
		"untyped-structured-output-agent",
		llmagent.WithModel(modelImpl),
	)
	r := NewRunner(
		"untyped-structured-output-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)

	eventCh, err := r.Run(
		context.Background(),
		"user-2",
		"session-2",
		model.NewUserMessage("hello"),
		agent.WithStructuredOutputJSONSchema(
			"runtime_output",
			schema,
			true,
			"Return one payload matching the runtime schema.",
		),
	)
	require.NoError(t, err)

	structured := collectStructuredOutput(eventCh)
	payload, ok := structured.(map[string]any)
	require.True(t, ok, "expected untyped structured output payload")
	require.Equal(t, "ok", payload["status"])
	require.EqualValues(t, 3, payload["count"])

	captured := modelImpl.LatestRequest()
	require.NotNil(t, captured, "expected one model request to be captured")
	require.NotNil(t, captured.structuredOutput)
	require.Equal(t, model.StructuredOutputJSONSchema, captured.structuredOutput.Type)
	require.NotNil(t, captured.structuredOutput.JSONSchema)
	require.Equal(t, "runtime_output", captured.structuredOutput.JSONSchema.Name)
	require.True(t, captured.structuredOutput.JSONSchema.Strict)
	require.Equal(
		t,
		"Return one payload matching the runtime schema.",
		captured.structuredOutput.JSONSchema.Description,
	)
	require.Equal(t, schema, captured.structuredOutput.JSONSchema.Schema)

	systemContent := firstSystemMessageContent(captured.messages)
	require.NotEmpty(t, systemContent, "expected structured output instructions to create one system message")
	assert.Contains(t, systemContent, "IMPORTANT: Return ONLY a JSON object")
	assert.Contains(t, systemContent, `"status"`)
	assert.Contains(t, systemContent, `"count"`)
	assert.NotContains(t, systemContent, "You MAY call tools")
}

func TestRunner_Run_WithRunStructuredOutputJSON_PassesThroughChainAgent(t *testing.T) {
	const description = "Return one typed payload through chain."
	modelImpl := &capturingStructuredOutputModel{
		name:    "chain-structured-output-model",
		content: `{"answer":"ok","score":7}`,
	}
	leaf := llmagent.New(
		"chain-leaf-agent",
		llmagent.WithModel(modelImpl),
	)
	ag := chainagent.New(
		"chain-wrapper-agent",
		chainagent.WithSubAgents([]agent.Agent{leaf}),
	)

	runRunnerWithTypedStructuredOutput(t, ag, description)

	captured := modelImpl.LatestRequest()
	require.NotNil(t, captured, "expected one model request to be captured")
	require.NotNil(t, captured.structuredOutput)
	require.NotNil(t, captured.structuredOutput.JSONSchema)
	require.Equal(t, "runnerStructuredOutputTypedPayload", captured.structuredOutput.JSONSchema.Name)
	require.Equal(t, description, captured.structuredOutput.JSONSchema.Description)
}

func TestRunner_Run_WithRunStructuredOutputJSON_PassesThroughGraphAgent(t *testing.T) {
	const description = "Return one typed payload through graph."
	modelImpl := &capturingStructuredOutputModel{
		name:    "graph-structured-output-model",
		content: `{"answer":"ok","score":7}`,
	}
	leaf := llmagent.New(
		"graph-leaf-agent",
		llmagent.WithModel(modelImpl),
	)
	compiled, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddAgentNode(leaf.Info().Name).
		SetEntryPoint(leaf.Info().Name).
		SetFinishPoint(leaf.Info().Name).
		Compile()
	require.NoError(t, err)
	ag, err := graphagent.New(
		"graph-wrapper-agent",
		compiled,
		graphagent.WithSubAgents([]agent.Agent{leaf}),
	)
	require.NoError(t, err)

	runRunnerWithTypedStructuredOutput(t, ag, description)

	captured := modelImpl.LatestRequest()
	require.NotNil(t, captured, "expected one model request to be captured")
	require.NotNil(t, captured.structuredOutput)
	require.NotNil(t, captured.structuredOutput.JSONSchema)
	require.Equal(t, "runnerStructuredOutputTypedPayload", captured.structuredOutput.JSONSchema.Name)
	require.Equal(t, description, captured.structuredOutput.JSONSchema.Description)
}

func TestRunner_Run_WithRunStructuredOutputJSON_SupportsPointerSliceAndArrayFields_RealOpenAI(
	t *testing.T,
) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real API test")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	temperature := 0.0
	maxTokens := 200
	modelOptions := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(baseURL))
	}
	modelInstance := openai.New("gpt-4o-mini", modelOptions...)
	ag := llmagent.New(
		"real-complex-structured-output-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
	r := NewRunner(
		"real-complex-structured-output-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, r.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		"user-real-complex",
		"session-real-complex",
		model.NewUserMessage(
			"Return a JSON object that exactly matches the schema and uses exactly these values: "+
				`answer="ok", detail.city="shenzhen", detail.codes=[1,2], tags=["alpha","beta"], scores=[7,9].`,
		),
		agent.WithStructuredOutputJSON(
			new(realStructuredOutputComplexPayload),
			true,
			"Return one complex typed payload.",
		),
	)
	require.NoError(t, err)
	var structured any
	for evt := range eventCh {
		require.Nil(t, evt.Error, "unexpected runner event error: %+v", evt.Error)
		if evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	payload, ok := structured.(*realStructuredOutputComplexPayload)
	require.True(t, ok, "expected complex typed structured output payload, got %T", structured)
	require.Equal(t, "ok", payload.Answer)
	require.NotNil(t, payload.Detail)
	require.Equal(t, "shenzhen", payload.Detail.City)
	require.Equal(t, []int{1, 2}, payload.Detail.Codes)
	require.Equal(t, []string{"alpha", "beta"}, payload.Tags)
	require.Equal(t, [2]int{7, 9}, payload.Scores)
}

func TestRunner_Run_WithRunStructuredOutputJSON_SupportsPointerSliceAndArrayFields_NonStrict_RealOpenAI(
	t *testing.T,
) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real API test")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	temperature := 0.0
	maxTokens := 200
	modelOptions := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(baseURL))
	}
	modelInstance := openai.New("gpt-4o-mini", modelOptions...)
	ag := llmagent.New(
		"real-complex-structured-output-nonstrict-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
	r := NewRunner(
		"real-complex-structured-output-nonstrict-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, r.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		"user-real-complex-nonstrict",
		"session-real-complex-nonstrict",
		model.NewUserMessage(
			"Return a JSON object that exactly matches the schema and uses exactly these values: "+
				`answer="ok", detail.city="shenzhen", detail.codes=[1,2], tags=["alpha","beta"], scores=[7,9].`,
		),
		agent.WithStructuredOutputJSON(
			new(realStructuredOutputComplexPayload),
			false,
			"Return one complex typed payload.",
		),
	)
	require.NoError(t, err)
	var structured any
	for evt := range eventCh {
		require.Nil(t, evt.Error, "unexpected runner event error: %+v", evt.Error)
		if evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	payload, ok := structured.(*realStructuredOutputComplexPayload)
	require.True(t, ok, "expected complex typed structured output payload, got %T", structured)
	require.Equal(t, "ok", payload.Answer)
	require.NotNil(t, payload.Detail)
	require.Equal(t, "shenzhen", payload.Detail.City)
	require.Equal(t, []int{1, 2}, payload.Detail.Codes)
	require.Equal(t, []string{"alpha", "beta"}, payload.Tags)
	require.Equal(t, [2]int{7, 9}, payload.Scores)
}

func TestRunner_Run_WithRunStructuredOutputJSONSchema_LegacyNonStrictPointerSliceAndArrayFields_RealOpenAI(
	t *testing.T,
) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real API test")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	temperature := 0.0
	maxTokens := 200
	modelOptions := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(baseURL))
	}
	modelInstance := openai.New("gpt-4o-mini", modelOptions...)
	ag := llmagent.New(
		"real-legacy-complex-structured-output-nonstrict-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
	r := NewRunner(
		"real-legacy-complex-structured-output-nonstrict-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, r.Close())
	})
	legacySchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
			"detail": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
					"codes": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "integer"},
					},
				},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"scores": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
		},
		"required":             []string{"answer", "scores"},
		"additionalProperties": false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		"user-real-legacy-complex-nonstrict",
		"session-real-legacy-complex-nonstrict",
		model.NewUserMessage(
			"Return a JSON object that exactly matches the schema and uses exactly these values: "+
				`answer="ok", detail.city="shenzhen", detail.codes=[1,2], tags=["alpha","beta"], scores=[7,9].`,
		),
		agent.WithStructuredOutputJSONSchema(
			"legacy_complex_payload",
			legacySchema,
			false,
			"Return one complex typed payload.",
		),
	)
	require.NoError(t, err)
	var structured any
	for evt := range eventCh {
		require.Nil(t, evt.Error, "unexpected runner event error: %+v", evt.Error)
		if evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	require.NotNil(t, structured, "expected structured output payload")
	body, err := json.Marshal(structured)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"answer":"ok","detail":{"city":"shenzhen","codes":[1,2]},"tags":["alpha","beta"],"scores":[7,9]}`,
		string(body),
	)
}

func TestRunner_Run_WithRunStructuredOutputJSON_SupportsStringMap_RealOpenAI(
	t *testing.T,
) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real API test")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	temperature := 0.0
	maxTokens := 200
	modelOptions := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(baseURL))
	}
	modelInstance := openai.New("gpt-4o-mini", modelOptions...)
	ag := llmagent.New(
		"real-map-structured-output-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
	r := NewRunner(
		"real-map-structured-output-app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, r.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	eventCh, err := r.Run(
		ctx,
		"user-real-map",
		"session-real-map",
		model.NewUserMessage(
			"Return a JSON object that exactly matches the schema and uses exactly these values: "+
				`answer="ok", labels={"env":"test","region":"sz"}.`,
		),
		agent.WithStructuredOutputJSON(
			new(realStructuredOutputMapPayload),
			true,
			"Return one typed payload with a string map.",
		),
	)
	require.NoError(t, err)
	var structured any
	for evt := range eventCh {
		require.Nil(t, evt.Error, "unexpected runner event error: %+v", evt.Error)
		if evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	payload, ok := structured.(*realStructuredOutputMapPayload)
	require.True(t, ok, "expected typed structured output payload, got %T", structured)
	require.Equal(t, "ok", payload.Answer)
	require.Equal(t, map[string]string{"env": "test", "region": "sz"}, payload.Labels)
}

func TestRunner_SessionIntegration(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner with session service.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Hello, world!")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect all events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify we received the mock response.
	require.Len(t, events, 2)
	assert.Equal(t, "test-agent", events[0].Author)
	assert.Contains(t, events[0].Response.Choices[0].Message.Content, "Hello, world!")

	// Verify session was created and contains events.
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session contains both user message and agent response.
	// Should have: user message + agent response + runner done = 3 events.
	assert.Len(t, sess.Events, 2)

	// Verify user event.
	userEvent := sess.Events[0]
	assert.Equal(t, authorUser, userEvent.Author)
	assert.Equal(t, "test-app", userEvent.FilterKey)
	assert.Equal(t, "Hello, world!", userEvent.Response.Choices[0].Message.Content)

	// Verify agent event.
	agentEvent := sess.Events[1]
	assert.Equal(t, "test-agent", agentEvent.Author)
	assert.Contains(t, agentEvent.Response.Choices[0].Message.Content, "Hello, world!")
}

func TestRunnerRun_WarnsOnMessageWithEmptyRole(t *testing.T) {
	original := runnerlog.WarnfContext
	warnCalls := 0
	runnerlog.WarnfContext = func(ctx context.Context, format string, args ...any) {
		warnCalls++
	}
	defer func() {
		runnerlog.WarnfContext = original
	}()

	ag := &capturingRoleAgent{name: "test-agent"}
	r := NewRunner("test-app", ag)
	eventCh, err := r.Run(context.Background(), "user", "session", model.Message{Content: "hello"})
	require.NoError(t, err)

	for range eventCh {
	}

	assert.Equal(t, 1, warnCalls)
	assert.Equal(t, model.RoleUser, ag.capturedRole)
}

func TestRunner_Run_WithEventFilterKey(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner(
		"test-app",
		mockAgent,
		WithSessionService(sessionService),
	)

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Hello, world!")
	const filterKey = "test-app/role/admin"

	eventCh, err := runner.Run(
		ctx,
		userID,
		sessionID,
		message,
		agent.WithEventFilterKey(filterKey),
	)
	require.NoError(t, err)

	// Drain all events to ensure persistence is complete.
	for range eventCh {
	}

	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}
	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, sess.Events, 2)

	userEvent := sess.Events[0]
	assert.Equal(t, authorUser, userEvent.Author)
	assert.Equal(t, filterKey, userEvent.FilterKey)
}

func TestRunner_SessionIntegration_MultimodalUserMessage(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner with session service.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session-multimodal"

	message := model.Message{Role: model.RoleUser}
	message.AddImageURL("https://example.com/image.png", "auto")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Drain all events to ensure persistence is complete.
	for range eventCh {
	}

	// Verify session was created and contains events.
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session contains both user message and agent response.
	require.Len(t, sess.Events, 2)

	// Verify user event.
	userEvent := sess.Events[0]
	assert.Equal(t, authorUser, userEvent.Author)
	require.True(t, userEvent.IsUserMessage())
	require.Len(t, userEvent.Response.Choices, 1)
	assert.Equal(t, model.RoleUser, userEvent.Response.Choices[0].Message.Role)
	assert.Empty(t, userEvent.Response.Choices[0].Message.Content)
	require.Len(t, userEvent.Response.Choices[0].Message.ContentParts, 1)
	assert.Equal(t, model.ContentTypeImage, userEvent.Response.Choices[0].Message.ContentParts[0].Type)
	require.NotNil(t, userEvent.Response.Choices[0].Message.ContentParts[0].Image)
	assert.Equal(t, "https://example.com/image.png", userEvent.Response.Choices[0].Message.ContentParts[0].Image.URL)

	// Verify agent event.
	agentEvent := sess.Events[1]
	assert.Equal(t, "test-agent", agentEvent.Author)
	assert.Contains(t, agentEvent.Response.Choices[0].Message.Content, "Hello! I received your message:")
}

type testPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *testPlugin) Name() string { return p.name }

func (p *testPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

func TestRunner_WithPlugins_AppliesHooks(t *testing.T) {
	beforeCalled := false
	const tagged = "tagged"

	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				beforeCalled = true
				return nil, nil
			})
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				if e != nil {
					e.Tag = tagged
				}
				return nil, nil
			})
		},
	}

	sessionService := sessioninmemory.NewSessionService()
	ag := &mockAgent{name: "test-agent"}
	r := NewRunner(
		"test-app",
		ag,
		WithSessionService(sessionService),
		WithPlugins(p),
	)

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)

	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}

	require.True(t, beforeCalled)
	require.NotEmpty(t, events)
	for _, evt := range events {
		require.Equal(t, tagged, evt.Tag)
	}
}

func TestRunner_applyEventPlugins_ReplacesEventAndCopiesFields(t *testing.T) {
	const (
		reqID    = "req"
		invID    = "inv"
		parentID = "parent"
		branch   = "branch"
		filter   = "filter"
		tag      = "tag"
	)
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				updated := &event.Event{
					Tag:    tag,
					Author: "plugin",
				}
				return updated, nil
			})
		},
	}

	ag := &mockAgent{name: "test-agent"}
	run := NewRunner("test-app", ag, WithPlugins(p)).(*runner)

	inv := &agent.Invocation{Plugins: plugin.MustNewManager(p)}
	orig := &event.Event{
		Response:           &model.Response{Done: true},
		RequestID:          reqID,
		InvocationID:       invID,
		ParentInvocationID: parentID,
		Branch:             branch,
		FilterKey:          filter,
		Author:             "a",
	}

	out := run.applyEventPlugins(context.Background(), inv, orig)
	require.NotNil(t, out)
	require.Equal(t, tag, out.Tag)
	require.Equal(t, reqID, out.RequestID)
	require.Equal(t, invID, out.InvocationID)
	require.Equal(t, parentID, out.ParentInvocationID)
	require.Equal(t, branch, out.Branch)
	require.Equal(t, filter, out.FilterKey)
}

func TestRunner_applyEventPlugins_ErrorKeepsOriginal(t *testing.T) {
	p := &testPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				ctx context.Context,
				inv *agent.Invocation,
				e *event.Event,
			) (*event.Event, error) {
				return nil, errors.New("boom")
			})
		},
	}

	ag := &mockAgent{name: "test-agent"}
	run := NewRunner("test-app", ag, WithPlugins(p)).(*runner)

	inv := &agent.Invocation{Plugins: plugin.MustNewManager(p)}
	orig := &event.Event{Response: &model.Response{Done: true}}
	out := run.applyEventPlugins(context.Background(), inv, orig)
	require.Same(t, orig, out)
}

type closePlugin struct {
	name     string
	closed   bool
	closeErr error
}

func (p *closePlugin) Name() string { return p.name }

func (p *closePlugin) Register(r *plugin.Registry) {}

func (p *closePlugin) Close(ctx context.Context) error {
	p.closed = true
	return p.closeErr
}

func TestRunner_Close_ClosesPlugins(t *testing.T) {
	p := &closePlugin{name: "p"}
	ag := &mockAgent{name: "test-agent"}
	run := NewRunner("test-app", ag, WithPlugins(p)).(*runner)

	err := run.Close()
	require.NoError(t, err)
	require.True(t, p.closed)
}

func TestRunner_Close_PropagatesPluginCloseError(t *testing.T) {
	p := &closePlugin{name: "p", closeErr: errors.New("boom")}
	ag := &mockAgent{name: "test-agent"}
	run := NewRunner("test-app", ag, WithPlugins(p)).(*runner)

	err := run.Close()
	require.Error(t, err)
	require.True(t, p.closed)
}

func TestRunner_SessionCreateIfMissing(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "new-user"
	sessionID := "new-session"
	message := model.NewUserMessage("First message")

	// Run the agent (should create new session).
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Consume events.
	for range eventCh {
		// Just consume all events.
	}

	// Verify session was created.
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, sessionID, sess.ID)
	assert.Equal(t, userID, sess.UserID)
	assert.Equal(t, "test-app", sess.AppName)
}

func TestRunner_EmptyMessageHandling(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	emptyMessage := model.NewUserMessage("") // Empty message

	// Run the agent with empty message.
	eventCh, err := runner.Run(ctx, userID, sessionID, emptyMessage)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Consume events.
	for range eventCh {
		// Just consume all events.
	}

	// Verify session was created but only contains agent response (no user message).
	sessionKey := session.Key{
		AppName:   "test-app",
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := sessionService.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Should have no events, user message was empty and not added to session, and session service filtered event start with user.
	assert.Len(t, sess.Events, 0)
}

func TestRunner_SkipAppendingSeedUserMessage(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "seed-user"
	sessionID := "seed-session"
	seedHistory := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("prev reply"),
		model.NewUserMessage("hello"),
	}

	message := model.NewUserMessage("hello")

	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.WithMessages(seedHistory))
	require.NoError(t, err)

	for range eventCh {
		// drain channel
	}

	sess, err := sessionService.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Expect: due to EnsureEventStartWithUser filtering, only the first user
	// event from seed is kept, plus agent response and runner completion = 3
	require.Len(t, sess.Events, 2)
	// Ensure we did not append a duplicate user message beyond the seed.
	userCount := 0
	for _, e := range sess.Events {
		if e.Author == authorUser {
			userCount++
		}
	}
	require.Equal(t, 1, userCount)
}

func TestRunner_AppendsDifferentUserAfterSeed(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "seed-user2"
	sessionID := "seed-session2"
	seedHistory := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("prev reply"),
		model.NewUserMessage("hello"),
	}

	// Different latest user, should be appended in addition to seeded user.
	message := model.NewUserMessage("hello too")

	eventCh, err := runner.Run(ctx, userID, sessionID, message, agent.WithMessages(seedHistory))
	require.NoError(t, err)

	for range eventCh {
		// drain channel
	}

	sess, err := sessionService.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Expect: seeded first user retained + appended user + agent response + runner completion = 4
	require.Len(t, sess.Events, 3)

	// Verify the first two events are users with expected contents.
	if !(len(sess.Events) >= 2) {
		t.Fatalf("expected at least two events")
	}
	// Event 0: seeded user
	if sess.Events[0].Author != authorUser {
		t.Fatalf("expected first event author user, got %s", sess.Events[0].Author)
	}
	if got := sess.Events[0].Response.Choices[0].Message.Content; got != "hello" {
		t.Fatalf("expected seeded user content 'hello', got %q", got)
	}
	// Event 1: appended user
	if sess.Events[1].Author != authorUser {
		t.Fatalf("expected second event author user, got %s", sess.Events[1].Author)
	}
	if got := sess.Events[1].Response.Choices[0].Message.Content; got != "hello too" {
		t.Fatalf("expected appended user content 'hello too', got %q", got)
	}
}

func TestRunner_Run_EmptyIncomingMessagePreservesSeedHistory(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "seed-user-empty"
	sessionID := "seed-session-empty"
	seedHistory := []model.Message{
		model.NewUserMessage("seed"),
	}

	eventCh, err := runner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(""),
		agent.WithMessages(seedHistory),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	sess, err := sessionService.GetSession(
		ctx,
		session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.Events)
	require.Equal(t, "seed", sess.Events[0].Choices[0].Message.Content)
}

// TestRunner_InvocationInjection verifies that runner correctly injects invocation into context.
func TestRunner_InvocationInjection(t *testing.T) {
	// Create an in-memory session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create a simple mock agent that verifies invocation is in context.
	mockAgent := &invocationVerificationAgent{name: "test-agent"}

	// Create runner.
	runner := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Test invocation injection")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect all events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify we received the success response indicating invocation was found in context.
	require.Len(t, events, 2)

	// First event should be from the mock agent.
	agentEvent := events[0]
	assert.Equal(t, "test-agent", agentEvent.Author)
	assert.Equal(t, "invocation-verification-success", agentEvent.Response.ID)
	assert.True(t, agentEvent.Response.Done)

	// Verify the response content indicates success.
	assert.Contains(t, agentEvent.Response.Choices[0].Message.Content, "Invocation found in context with ID:")
}

func TestRunner_Run_WithAgentNameRegistry(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	defaultAgent := &mockAgent{name: "default-agent"}
	altAgent := &mockAgent{name: "alt-agent"}
	r := NewRunner("test-app", defaultAgent,
		WithSessionService(sessionService),
		WithAgent("alt", altAgent),
	)

	ctx := context.Background()
	msg := model.NewUserMessage("hello")
	ch, err := r.Run(ctx, "user", "session", msg, agent.WithAgentByName("alt"))
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 2)
	assert.Equal(t, "alt-agent", events[0].Author)
	assert.Contains(t, events[0].Response.Choices[0].Message.Content, "hello")
}

func TestRunner_Run_WithAgentFactoryByName(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	defaultAgent := &mockAgent{name: "default-agent"}

	const (
		factoryKey      = "dynamic"
		factoryNameBase = "dynamic-agent-"
	)

	calls := 0
	r := NewRunner(
		"test-app",
		defaultAgent,
		WithSessionService(sessionService),
		WithAgentFactory(factoryKey, func(
			_ context.Context,
			_ agent.RunOptions,
		) (agent.Agent, error) {
			calls++
			name := fmt.Sprintf("%s%d", factoryNameBase, calls)
			return &mockAgent{name: name}, nil
		}),
	)

	ctx := context.Background()
	msg := model.NewUserMessage("hi")

	runOnce := func(expectedName string) {
		ch, err := r.Run(
			ctx,
			"user",
			"session",
			msg,
			agent.WithAgentByName(factoryKey),
		)
		require.NoError(t, err)
		var events []*event.Event
		for e := range ch {
			events = append(events, e)
		}
		require.Len(t, events, 2)
		assert.Equal(t, expectedName, events[0].Author)
	}

	runOnce(factoryNameBase + "1")
	runOnce(factoryNameBase + "2")
}

func TestRunner_Run_WithDefaultAgentFactory(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()

	const (
		defaultKey      = "dynamic-default"
		defaultNameBase = "default-agent-"
	)

	calls := 0
	r := NewRunnerWithAgentFactory(
		"test-app",
		defaultKey,
		func(_ context.Context, _ agent.RunOptions) (agent.Agent, error) {
			calls++
			name := fmt.Sprintf("%s%d", defaultNameBase, calls)
			return &mockAgent{name: name}, nil
		},
		WithSessionService(sessionService),
	)

	ctx := context.Background()
	msg := model.NewUserMessage("hello")
	ch, err := r.Run(ctx, "user", "session", msg)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 2)
	assert.Equal(t, defaultNameBase+"1", events[0].Author)
}

func TestRunner_NewRunnerWithAgentFactory_CoverageBranches(t *testing.T) {
	const (
		appName       = "test-app"
		defaultName   = "default-factory"
		staticAgentID = "static"
	)

	factoryCalled := false
	r := NewRunnerWithAgentFactory(
		appName,
		defaultName,
		func(_ context.Context, _ agent.RunOptions) (agent.Agent, error) {
			factoryCalled = true
			return &mockAgent{name: "created"}, nil
		},
		WithAgent(staticAgentID, &mockAgent{name: "static-agent"}),
		WithPlugins(plugin.NewLogging()),
		WithRalphLoop(RalphLoopConfig{MaxIterations: 1}),
	).(*runner)

	t.Cleanup(func() { _ = r.Close() })
	assert.True(t, r.ownedSessionService)

	ag, err := r.selectAgent(context.Background(), agent.RunOptions{})
	require.NoError(t, err)
	require.True(t, factoryCalled)

	_, ok := ag.(*ralphLoopAgent)
	require.True(t, ok)
}

func TestRunner_selectAgent_FactoryError(t *testing.T) {
	const (
		appName  = "test-app"
		agentKey = "factory-error"
	)

	r := NewRunner(
		appName,
		&mockAgent{name: "default"},
		WithAgentFactory(agentKey, func(
			_ context.Context,
			_ agent.RunOptions,
		) (agent.Agent, error) {
			return nil, errors.New("boom")
		}),
	).(*runner)

	assert.Nil(t, r.wrapSelectedAgent(nil))

	_, err := r.selectAgent(context.Background(), agent.RunOptions{
		AgentByName: agentKey,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent factory")
}

func TestRunner_selectAgent_FactoryNil(t *testing.T) {
	const (
		appName  = "test-app"
		agentKey = "factory-nil"
	)

	r := NewRunner(
		appName,
		&mockAgent{name: "default"},
		WithAgentFactory(agentKey, func(
			_ context.Context,
			_ agent.RunOptions,
		) (agent.Agent, error) {
			return nil, nil
		}),
	).(*runner)

	_, err := r.selectAgent(context.Background(), agent.RunOptions{
		AgentByName: agentKey,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent factory returned nil")
}

func TestRunner_Run_WithAgentInstanceOverride(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	defaultAgent := &mockAgent{name: "default-agent"}
	override := &mockAgent{name: "override-agent"}
	r := NewRunner("test-app", defaultAgent, WithSessionService(sessionService))

	ctx := context.Background()
	msg := model.NewUserMessage("hi")
	ch, err := r.Run(ctx, "user", "session", msg, agent.WithAgent(override))
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 2)
	assert.Equal(t, "override-agent", events[0].Author)
}

func TestRunner_Run_WithAgentNameNotFound(t *testing.T) {
	r := NewRunner("test-app", &mockAgent{name: "default"}, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(context.Background(), "user", "session", model.NewUserMessage("hi"), agent.WithAgentByName("missing"))
	require.Error(t, err)
	require.Nil(t, ch)
}

// invocationVerificationAgent is a simple mock agent that verifies invocation is present in context.
type invocationVerificationAgent struct {
	name string
}

func (m *invocationVerificationAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing invocation injection",
	}
}

func (m *invocationVerificationAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *invocationVerificationAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *invocationVerificationAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 1)

	// Verify that invocation is present in context.
	ctxInvocation, ok := agent.InvocationFromContext(ctx)
	if !ok || ctxInvocation == nil {
		// Create error event if invocation is not in context.
		errorEvent := &event.Event{
			Response: &model.Response{
				ID:    "invocation-verification-error",
				Model: "test-model",
				Done:  true,
				Error: &model.ResponseError{
					Type:    "invocation_verification_error",
					Message: "Invocation not found in context",
				},
			},
			InvocationID: invocation.InvocationID,
			Author:       m.name,
			ID:           "error-event-id",
			Timestamp:    time.Now(),
		}
		eventCh <- errorEvent
		close(eventCh)
		return eventCh, nil
	}

	// Create success response event.
	responseEvent := &event.Event{
		Response: &model.Response{
			ID:    "invocation-verification-success",
			Model: "test-model",
			Done:  true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Invocation found in context with ID: " + ctxInvocation.InvocationID,
					},
				},
			},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "success-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- responseEvent
	close(eventCh)

	return eventCh, nil
}

func (m *invocationVerificationAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func TestWithMemoryService(t *testing.T) {
	t.Run("sets memory service in options", func(t *testing.T) {
		memoryService := memoryinmemory.NewMemoryService()
		opts := &Options{}

		option := WithMemoryService(memoryService)
		option(opts)

		assert.Equal(t, memoryService, opts.memoryService, "Memory service should be set in options")
	})

	t.Run("sets nil memory service", func(t *testing.T) {
		opts := &Options{}

		option := WithMemoryService(nil)
		option(opts)

		assert.Nil(t, opts.memoryService, "Memory service should be nil")
	})
}

func TestWithSessionIngestor(t *testing.T) {
	t.Run("sets ingestor in options", func(t *testing.T) {
		ingestor := &mockIngestor{}
		opts := &Options{}

		option := WithSessionIngestor(ingestor)
		option(opts)

		assert.Equal(t, ingestor, opts.ingestor, "Ingestor should be set in options")
	})

	t.Run("sets nil ingestor", func(t *testing.T) {
		opts := &Options{}

		option := WithSessionIngestor(nil)
		option(opts)

		assert.Nil(t, opts.ingestor, "Ingestor should be nil")
	})
}

func TestWithArtifactService(t *testing.T) {
	t.Run("sets artifact service in options", func(t *testing.T) {
		artifactService := artifactinmemory.NewService()
		opts := &Options{}

		option := WithArtifactService(artifactService)
		option(opts)

		assert.Equal(t, artifactService, opts.artifactService, "Artifact service should be set in options")
	})

	t.Run("sets nil artifact service", func(t *testing.T) {
		opts := &Options{}

		option := WithArtifactService(nil)
		option(opts)

		assert.Nil(t, opts.artifactService, "Artifact service should be nil")
	})
}

// TestRunner_GraphCompletionPropagation tests that graph completion events
// are properly captured and propagated to the runner completion event.
func TestRunner_GraphCompletionPropagation(t *testing.T) {
	// Create a mock agent that emits a graph completion event.
	graphAgent := &graphCompletionMockAgent{name: "graph-agent"}

	// Create runner with in-memory session service.
	sessionService := sessioninmemory.NewSessionService()
	runner := NewRunner("test-app", graphAgent, WithSessionService(sessionService))

	ctx := context.Background()
	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Execute graph")

	// Run the agent.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)
	require.NoError(t, err, "Run should not return an error")

	// Collect all events.
	var events []*event.Event
	for ev := range eventCh {
		events = append(events, ev)
	}

	// Verify we received events.
	require.NotEmpty(t, events, "Should receive events")

	// Find the runner completion event (should be the last one).
	var runnerCompletionEvent *event.Event
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Object == model.ObjectTypeRunnerCompletion {
			runnerCompletionEvent = events[i]
			break
		}
	}

	require.NotNil(t, runnerCompletionEvent, "Should have runner completion event")

	// Verify that the state delta was propagated.
	assert.NotNil(t, runnerCompletionEvent.StateDelta, "State delta should be propagated")
	assert.Equal(t, "final_value", string(runnerCompletionEvent.StateDelta["final_key"]),
		"State delta should contain the final key-value pair")

	// Verify that the final choices were propagated.
	assert.NotEmpty(t, runnerCompletionEvent.Response.Choices,
		"Final choices should be propagated")
	assert.Equal(t, "Graph execution completed",
		runnerCompletionEvent.Response.Choices[0].Message.Content,
		"Final message content should match")
}

func TestRunner_DisableGraphCompletionEvent_KeepsRunnerCompletion(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "hidden graph completion",
		}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()

	ga, err := graphagent.New("ga", compiled)
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	require.NotEmpty(t, events)

	var completion *event.Event
	for _, evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"hidden graph completion"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "hidden graph completion", completion.Response.Choices[0].Message.Content)
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
	})
	require.NoError(t, err)
	for _, evt := range sess.Events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
	}
}

func TestRunner_DisableGraphCompletionEvent_KeepsRunnerCompletionWithGraphAgentCallbacks(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "hidden graph completion",
		}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return nil, nil
	})
	ga, err := graphagent.New("ga", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	for evt := range ch {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"hidden graph completion"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "hidden graph completion", completion.Response.Choices[0].Message.Content)
}

func TestRunner_DisableGraphCompletionEvent_DropsCapturedGraphCompletionAfterCustomCallback(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "hidden graph completion",
		}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return &agent.AfterAgentResult{
			CustomResponse: &model.Response{
				Object: "after.custom",
				Done:   true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "after callback",
					},
				}},
			},
		}, nil
	})
	ga, err := graphagent.New("ga", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	var sawAfterCustom bool
	for evt := range ch {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt.Object == "after.custom" {
			sawAfterCustom = true
		}
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.True(t, sawAfterCustom)
	require.NotNil(t, completion)
	require.Empty(t, completion.StateDelta)
	require.Empty(t, completion.Response.Choices)
}

func TestRunner_DisableGraphCompletionEvent_DropsCapturedGraphCompletionAfterCallbackError(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "hidden graph completion",
		}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return nil, errors.New("after callback failed")
	})
	ga, err := graphagent.New("ga", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	var sawCallbackError bool
	for evt := range ch {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt.Object == model.ObjectTypeError &&
			evt.Error != nil &&
			evt.Error.Message == "after callback failed" {
			sawCallbackError = true
		}
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.True(t, sawCallbackError)
	require.NotNil(t, completion)
	require.Empty(t, completion.StateDelta)
	require.Empty(t, completion.Response.Choices)
}

func TestRunner_DisableGraphCompletionEvent_KeepsRunnerCompletionWithWrappedGraphAgent(t *testing.T) {
	tests := []struct {
		name  string
		build func(child agent.Agent) agent.Agent
	}{
		{
			name: "chain",
			build: func(child agent.Agent) agent.Agent {
				return chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child}))
			},
		},
		{
			name: "cycle",
			build: func(child agent.Agent) agent.Agent {
				return cycleagent.New(
					"cycle",
					cycleagent.WithSubAgents([]agent.Agent{child}),
					cycleagent.WithMaxIterations(1),
				)
			},
		},
		{
			name: "parallel",
			build: func(child agent.Agent) agent.Agent {
				return parallelagent.New("parallel", parallelagent.WithSubAgents([]agent.Agent{child}))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child := newWrappedGraphChildAgent(t)
			svc := sessioninmemory.NewSessionService()
			r := NewRunner("app", tt.build(child), WithSessionService(svc))
			ch, err := r.Run(
				context.Background(),
				"u",
				tt.name,
				model.NewUserMessage("hi"),
				agent.WithDisableGraphCompletionEvent(true),
			)
			require.NoError(t, err)

			var completion *event.Event
			var visibleResponses int
			for evt := range ch {
				require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
				if evt.Response != nil &&
					len(evt.Response.Choices) > 0 &&
					evt.Response.Choices[0].Message.Content == "child-final" {
					visibleResponses++
				}
				if evt.IsRunnerCompletion() {
					completion = evt
				}
			}

			require.Equal(t, 1, visibleResponses)
			require.NotNil(t, completion)
			require.NotNil(t, completion.StateDelta)
			require.Equal(t, `"child-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
			require.Equal(t, `"child-state"`, string(completion.StateDelta["child_state"]))
			require.Empty(t, completion.Response.Choices)
			sess, err := svc.GetSession(context.Background(), session.Key{
				AppName:   "app",
				UserID:    "u",
				SessionID: tt.name,
			})
			require.NoError(t, err)
			require.Len(t, sess.Events, 2)
			require.Equal(t, "child-final", sess.Events[1].Choices[0].Message.Content)
		})
	}
}

func TestWrappedAgents_DisableGraphCompletionEvent_AfterCallbackSeesVisibleCompletion(
	t *testing.T,
) {
	tests := []struct {
		name  string
		build func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent
	}{
		{
			name: "chain",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return chainagent.New(
					"chain",
					chainagent.WithSubAgents([]agent.Agent{child}),
					chainagent.WithAgentCallbacks(callbacks),
				)
			},
		},
		{
			name: "cycle",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return cycleagent.New(
					"cycle",
					cycleagent.WithSubAgents([]agent.Agent{child}),
					cycleagent.WithMaxIterations(1),
					cycleagent.WithAgentCallbacks(callbacks),
				)
			},
		},
		{
			name: "parallel",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return parallelagent.New(
					"parallel",
					parallelagent.WithSubAgents([]agent.Agent{child}),
					parallelagent.WithAgentCallbacks(callbacks),
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callbacks := agent.NewCallbacks()
			callbacks.RegisterAfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				if args.FullResponseEvent != nil &&
					graph.IsVisibleGraphCompletionEvent(args.FullResponseEvent) {
					return &agent.AfterAgentResult{
						CustomResponse: &model.Response{
							Object: "after.custom",
							Done:   true,
							Choices: []model.Choice{{
								Message: model.NewAssistantMessage("after callback"),
							}},
						},
					}, nil
				}
				return nil, nil
			})
			child := newWrappedGraphChildAgent(t)
			ag := tt.build(child, callbacks)
			inv := agent.NewInvocation(
				agent.WithInvocationMessage(model.NewUserMessage("hi")),
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithDisableGraphCompletionEvent(true),
				)),
			)
			ch, err := ag.Run(context.Background(), inv)
			require.NoError(t, err)

			var sawAfterCustom bool
			for evt := range ch {
				if evt.Object == "after.custom" {
					sawAfterCustom = true
				}
			}

			require.True(t, sawAfterCustom)
		})
	}
}

func TestWrappedAgents_DisableGraphCompletionEvent_GraphEmitFinalModelResponses_AfterCallbackSeesFinalText(
	t *testing.T,
) {
	tests := []struct {
		name  string
		build func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent
	}{
		{
			name: "chain",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return chainagent.New(
					"chain",
					chainagent.WithSubAgents([]agent.Agent{child}),
					chainagent.WithAgentCallbacks(callbacks),
				)
			},
		},
		{
			name: "cycle",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return cycleagent.New(
					"cycle",
					cycleagent.WithSubAgents([]agent.Agent{child}),
					cycleagent.WithMaxIterations(1),
					cycleagent.WithAgentCallbacks(callbacks),
				)
			},
		},
		{
			name: "parallel",
			build: func(child agent.Agent, callbacks *agent.Callbacks) agent.Agent {
				return parallelagent.New(
					"parallel",
					parallelagent.WithSubAgents([]agent.Agent{child}),
					parallelagent.WithAgentCallbacks(callbacks),
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callbacks := agent.NewCallbacks()
			var fullRespEvent *event.Event
			callbacks.RegisterAfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				fullRespEvent = args.FullResponseEvent
				return nil, nil
			})
			child := newWrappedGraphLLMChildAgent(t)
			ag := tt.build(child, callbacks)
			inv := agent.NewInvocation(
				agent.WithInvocationMessage(model.NewUserMessage("hi")),
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithDisableGraphCompletionEvent(true),
					agent.WithGraphEmitFinalModelResponses(true),
				)),
			)
			ch, err := ag.Run(context.Background(), inv)
			require.NoError(t, err)
			for range ch {
			}

			require.NotNil(t, fullRespEvent)
			require.True(t, graph.IsVisibleGraphCompletionEvent(fullRespEvent))
			require.Len(t, fullRespEvent.Response.Choices, 1)
			require.Equal(t, "wrapped-final", fullRespEvent.Response.Choices[0].Message.Content)
		})
	}
}

func TestRunner_DisableGraphCompletionEvent_StreamModeUpdates_KeepsFinalTextInRunnerCompletion(t *testing.T) {
	child := newWrappedGraphChildAgent(t)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner(
		"app",
		chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child})),
		WithSessionService(svc),
	)
	ch, err := r.Run(
		context.Background(),
		"u",
		"updates-mode",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
		agent.WithStreamMode(agent.StreamModeUpdates),
	)
	require.NoError(t, err)

	var completion *event.Event
	for evt := range ch {
		require.NotEqual(t, model.ObjectTypeChatCompletion, evt.Object)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"child-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "child-final", completion.Response.Choices[0].Message.Content)
	assertSessionKeepsSingleFinalAssistantEvent(
		t,
		svc,
		"updates-mode",
		"child-final",
	)
}

func TestRunner_DisableGraphCompletionEvent_StreamModeUpdates_WrappedGraphLLM_KeepsSingleFinalAssistantEvent(
	t *testing.T,
) {
	child := newWrappedGraphLLMChildAgent(t)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner(
		"app",
		chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child})),
		WithSessionService(svc),
	)
	ch, err := r.Run(
		context.Background(),
		"u",
		"updates-mode-wrapped-llm",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
		agent.WithStreamMode(agent.StreamModeUpdates),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range ch {
		require.NotEqual(t, model.ObjectTypeChatCompletion, evt.Object)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"wrapped-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "wrapped-final", completion.Response.Choices[0].Message.Content)
	assertSessionKeepsSingleFinalAssistantEvent(
		t,
		svc,
		"updates-mode-wrapped-llm",
		"wrapped-final",
	)
}

func TestRunner_DisableGraphCompletionEvent_StreamModeUpdates_WithFinalModelResponses_KeepsFinalTextInRunnerCompletion(t *testing.T) {
	child := newWrappedGraphLLMChildAgent(t)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner(
		"app",
		chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child})),
		WithSessionService(svc),
	)
	ch, err := r.Run(
		context.Background(),
		"u",
		"updates-mode-final-model",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphCompletionEvent(true),
		agent.WithGraphEmitFinalModelResponses(true),
		agent.WithStreamMode(agent.StreamModeUpdates),
	)
	require.NoError(t, err)

	var completion *event.Event
	for evt := range ch {
		require.NotEqual(t, model.ObjectTypeChatCompletion, evt.Object)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"wrapped-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "wrapped-final", completion.Response.Choices[0].Message.Content)
	assertSessionKeepsSingleFinalAssistantEvent(
		t,
		svc,
		"updates-mode-final-model",
		"wrapped-final",
	)
}

func newWrappedGraphChildAgent(t *testing.T) agent.Agent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "child-final",
			"child_state":              "child-state",
		}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	child, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	return child
}

func newWrappedGraphLLMChildAgent(t *testing.T) agent.Agent {
	t.Helper()
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&staticModel{name: "m1", content: "wrapped-final"},
		"i1",
		nil,
	)
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n1").MustCompile()
	child, err := graphagent.New("graph-child-llm", compiled)
	require.NoError(t, err)
	return child
}

func newWrappedGraphLLMEmptyIDChildAgent(t *testing.T) agent.Agent {
	t.Helper()
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&emptyIDModel{name: "m-empty-id", content: "empty-id-final"},
		"i1",
		nil,
	)
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n1").MustCompile()
	child, err := graphagent.New("graph-child-llm-empty-id", compiled)
	require.NoError(t, err)
	return child
}

func assertSessionKeepsSingleFinalAssistantEvent(
	t *testing.T,
	svc *sessioninmemory.SessionService,
	sessionID string,
	finalText string,
) {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: sessionID,
	})
	require.NoError(t, err)

	var assistantTextCount int
	for _, evt := range sess.Events {
		if evt.IsRunnerCompletion() {
			require.Empty(t, evt.Response.Choices)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Content == finalText {
				assistantTextCount++
			}
		}
	}

	require.Equal(t, 1, assistantTextCount)
}

func assertSessionPreservesRunnerCompletionText(
	t *testing.T,
	svc *sessioninmemory.SessionService,
	sessionID string,
	finalText string,
) {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: sessionID,
	})
	require.NoError(t, err)
	var assistantTextCount int
	var sawRunnerCompletionText bool
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		if evt.IsRunnerCompletion() &&
			evt.Response.Choices[0].Message.Content == finalText {
			sawRunnerCompletionText = true
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Content == finalText {
				assistantTextCount++
			}
		}
	}
	require.True(t, sawRunnerCompletionText)
	require.Equal(t, 2, assistantTextCount)
}

func TestRunner_GraphCompletion_DedupFinalChoices(t *testing.T) {
	const (
		appName       = "test-app"
		userID        = "user"
		sessionID     = "session"
		agentName     = "dedup-agent"
		finalMsg      = "final"
		stateDeltaKey = "k"
		stateDeltaVal = "v"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &dedupGraphCompletionAgent{
		name:          agentName,
		assistantText: finalMsg,
		stateKey:      stateDeltaKey,
		stateVal:      stateDeltaVal,
	}
	r := NewRunner(appName, ag, WithSessionService(sessionService))

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage(""),
		agent.WithGraphEmitFinalModelResponses(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		if e.Object == model.ObjectTypeRunnerCompletion {
			completion = e
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, stateDeltaVal,
		string(completion.StateDelta[stateDeltaKey]))
	require.Empty(t, completion.Response.Choices)
}

func TestRunner_GraphCompletion_DifferentResponseIDDoesNotDedupBySignature(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	ag := &mismatchedIDGraphCompletionAgent{
		name:          "mismatch-agent",
		assistantText: "answer",
	}
	r := NewRunner("app", ag, WithSessionService(sessionService))
	ch, err := r.Run(
		context.Background(),
		"user",
		"session-mismatch-id",
		model.NewUserMessage(""),
		agent.WithGraphEmitFinalModelResponses(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		if e.Object == model.ObjectTypeRunnerCompletion {
			completion = e
		}
	}

	require.NotNil(t, completion)
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "answer", completion.Response.Choices[0].Message.Content)
}

func TestRunner_DisableGraphCompletionEvent_KeepsTopLevelFinalChoicesAfterChildVisibleCompletion(
	t *testing.T,
) {
	const sessionID = "session-child-visible-top-final"
	sessionService := sessioninmemory.NewSessionService()
	ag := &childVisibleThenTopGraphCompletionAgent{name: "root-agent"}
	r := NewRunner("app", ag, WithSessionService(sessionService))
	ch, err := r.Run(
		context.Background(),
		"user",
		sessionID,
		model.NewUserMessage(""),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var childVisibleCount int
	var completion *event.Event
	for e := range ch {
		if graph.IsVisibleGraphCompletionEvent(e) &&
			e.Response != nil &&
			len(e.Response.Choices) > 0 &&
			e.Response.Choices[0].Message.Content == "child:hello" {
			childVisibleCount++
		}
		if e.Object == model.ObjectTypeRunnerCompletion {
			completion = e
		}
	}
	require.Equal(t, 1, childVisibleCount)
	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(
		t,
		`"top:child:hello"`,
		string(completion.StateDelta[graph.StateKeyLastResponse]),
	)
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(
		t,
		"top:child:hello",
		completion.Response.Choices[0].Message.Content,
	)
}

func TestRunner_StreamModeUpdates_WithFinalModelResponses_EmptyResponseIDPreservesRunnerCompletionText(
	t *testing.T,
) {
	child := newWrappedGraphLLMEmptyIDChildAgent(t)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", child, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"updates-mode-empty-id",
		model.NewUserMessage("hi"),
		agent.WithGraphEmitFinalModelResponses(true),
		agent.WithStreamMode(agent.StreamModeUpdates),
	)
	require.NoError(t, err)

	var completion *event.Event
	for evt := range ch {
		require.NotEqual(t, model.ObjectTypeChatCompletion, evt.Object)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "empty-id-final", completion.Response.Choices[0].Message.Content)
	assertSessionPreservesRunnerCompletionText(
		t,
		svc,
		"updates-mode-empty-id",
		"empty-id-final",
	)
}

func TestRunner_GraphEmitFinalModelResponses_EmptyResponseIDPreservesRunnerCompletionText(
	t *testing.T,
) {
	child := newWrappedGraphLLMEmptyIDChildAgent(t)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", child, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"messages-mode-empty-id",
		model.NewUserMessage("hi"),
		agent.WithGraphEmitFinalModelResponses(true),
	)
	require.NoError(t, err)

	var chatCompletionCount int
	var completion *event.Event
	for evt := range ch {
		if evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			len(evt.StateDelta) == 0 &&
			evt.Response.Choices[0].Message.Content == "empty-id-final" {
			chatCompletionCount++
		}
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.Equal(t, 1, chatCompletionCount)
	require.NotNil(t, completion)
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(t, "empty-id-final", completion.Response.Choices[0].Message.Content)
	assertSessionPreservesRunnerCompletionText(
		t,
		svc,
		"messages-mode-empty-id",
		"empty-id-final",
	)
}

func TestRunner_StreamModeMessages_GraphCompletionPersistsFinalTextInSession(t *testing.T) {
	sessionService := sessioninmemory.NewSessionService()
	ag := &graphCompletionMockAgent{name: "graph-completion-agent"}
	r := NewRunner("app", ag, WithSessionService(sessionService))
	ch, err := r.Run(
		context.Background(),
		"u",
		"messages-mode-graph-completion",
		model.NewUserMessage("hi"),
		agent.WithStreamMode(agent.StreamModeMessages),
	)
	require.NoError(t, err)

	var completion *event.Event
	for evt := range ch {
		require.NotEqual(t, graph.ObjectTypeGraphExecution, evt.Object)
		if evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.Len(t, completion.Response.Choices, 1)
	require.Equal(
		t,
		"Graph execution completed",
		completion.Response.Choices[0].Message.Content,
	)
	sess, err := sessionService.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: "messages-mode-graph-completion",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	var assistantTextCount int
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Content == "Graph execution completed" {
				assistantTextCount++
			}
		}
	}
	require.Equal(t, 1, assistantTextCount)
}

func TestRunner_StreamMode_FiltersEvents(t *testing.T) {
	const (
		appName   = "stream-mode-app"
		userID    = "user"
		sessionID = "session"
		agentName = "stream-mode-agent"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &streamModeMockAgent{name: agentName}
	r := NewRunner(appName, ag, WithSessionService(sessionService))

	tests := []struct {
		name     string
		opts     []agent.RunOption
		allowed  map[string]bool
		required map[string]bool
	}{
		{
			name: "messages",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeMessages),
			},
			allowed: map[string]bool{
				model.ObjectTypeChatCompletionChunk: true,
				model.ObjectTypeChatCompletion:      true,
				model.ObjectTypeRunnerCompletion:    true,
			},
			required: map[string]bool{
				model.ObjectTypeChatCompletionChunk: true,
				model.ObjectTypeRunnerCompletion:    true,
			},
		},
		{
			name: "updates",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeUpdates),
			},
			allowed: map[string]bool{
				graph.ObjectTypeGraphStateUpdate:   true,
				graph.ObjectTypeGraphChannelUpdate: true,
				graph.ObjectTypeGraphExecution:     true,
				model.ObjectTypeStateUpdate:        true,
				model.ObjectTypeRunnerCompletion:   true,
			},
			required: map[string]bool{
				graph.ObjectTypeGraphStateUpdate: true,
				model.ObjectTypeRunnerCompletion: true,
			},
		},
		{
			name: "checkpoints",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeCheckpoints),
			},
			allowed: map[string]bool{
				graph.ObjectTypeGraphCheckpointCommitted: true,
				model.ObjectTypeRunnerCompletion:         true,
			},
			required: map[string]bool{
				graph.ObjectTypeGraphCheckpointCommitted: true,
				model.ObjectTypeRunnerCompletion:         true,
			},
		},
		{
			name: "tasks",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeTasks),
			},
			allowed: map[string]bool{
				graph.ObjectTypeGraphNodeStart:    true,
				graph.ObjectTypeGraphNodeComplete: true,
				graph.ObjectTypeGraphPregelStep:   true,
				model.ObjectTypeRunnerCompletion:  true,
			},
			required: map[string]bool{
				graph.ObjectTypeGraphNodeStart:   true,
				model.ObjectTypeRunnerCompletion: true,
			},
		},
		{
			name: "custom",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeCustom),
			},
			allowed: map[string]bool{
				graph.ObjectTypeGraphNodeCustom:  true,
				model.ObjectTypeRunnerCompletion: true,
			},
			required: map[string]bool{
				graph.ObjectTypeGraphNodeCustom:  true,
				model.ObjectTypeRunnerCompletion: true,
			},
		},
		{
			name: "debug",
			opts: []agent.RunOption{
				agent.WithStreamMode(agent.StreamModeDebug),
			},
			allowed: map[string]bool{
				graph.ObjectTypeGraphNodeStart:           true,
				graph.ObjectTypeGraphNodeComplete:        true,
				graph.ObjectTypeGraphCheckpointCommitted: true,
				graph.ObjectTypeGraphPregelStep:          true,
				model.ObjectTypeRunnerCompletion:         true,
			},
			required: map[string]bool{
				graph.ObjectTypeGraphNodeStart:           true,
				graph.ObjectTypeGraphCheckpointCommitted: true,
				model.ObjectTypeRunnerCompletion:         true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := r.Run(
				context.Background(),
				userID,
				sessionID,
				model.NewUserMessage("hi"),
				tt.opts...,
			)
			require.NoError(t, err)

			seen := make(map[string]bool)
			for e := range ch {
				require.NotNil(t, e)
				seen[e.Object] = true
				if !tt.allowed[e.Object] {
					t.Fatalf("unexpected event object: %q", e.Object)
				}
			}

			for obj := range tt.required {
				require.True(t, seen[obj], "missing %q", obj)
			}
		})
	}
}

type streamModeMockAgent struct {
	name string
}

func (m *streamModeMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *streamModeMockAgent) Tools() []tool.Tool {
	return nil
}

func (m *streamModeMockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *streamModeMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *streamModeMockAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 8)
	go func() {
		defer close(ch)
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphNodeStart))
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphStateUpdate))
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphCheckpointCommitted))
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphNodeCustom))
		ch <- event.NewResponseEvent(invocation.InvocationID, m.name,
			&model.Response{
				Object: model.ObjectTypeChatCompletionChunk,
				Done:   false,
				Choices: []model.Choice{{
					Index: 0,
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: "hi",
					},
				}},
			})
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphPregelStep))
		ch <- event.New(invocation.InvocationID, m.name,
			event.WithObject(graph.ObjectTypeGraphNodeComplete))
	}()
	return ch, nil
}

// graphCompletionMockAgent emits a graph completion event with state delta
// and choices.
type graphCompletionMockAgent struct {
	name string
}

func (m *graphCompletionMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent that emits graph completion events",
	}
}

func (m *graphCompletionMockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *graphCompletionMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *graphCompletionMockAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 2)

	// Emit a graph completion event with state delta and choices.
	graphCompletionEvent := &event.Event{
		Response: &model.Response{
			ID:     "graph-completion",
			Object: "graph.execution",
			Done:   true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Graph execution completed",
					},
				},
			},
		},
		StateDelta: map[string][]byte{
			"final_key": []byte("final_value"),
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "graph-event-id",
		Timestamp:    time.Now(),
	}

	eventCh <- graphCompletionEvent
	close(eventCh)

	return eventCh, nil
}

func (m *graphCompletionMockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// dedupGraphCompletionAgent emits an assistant message followed by a graph
// completion event with the same assistant content, so runner completion
// should not echo the final choices.
type dedupGraphCompletionAgent struct {
	name          string
	assistantText string
	stateKey      string
	stateVal      string
}

type mismatchedIDGraphCompletionAgent struct {
	name          string
	assistantText string
}

type childVisibleThenTopGraphCompletionAgent struct {
	name string
}

func (m *dedupGraphCompletionAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for dedup final choices",
	}
}

func (m *dedupGraphCompletionAgent) SubAgents() []agent.Agent { return nil }

func (m *dedupGraphCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *dedupGraphCompletionAgent) Tools() []tool.Tool { return nil }

func (m *dedupGraphCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	const (
		assistantEventID = "assistant-event-id"
		graphEventID     = "graph-event-id"
	)

	eventCh := make(chan *event.Event, 2)

	assistantEvent := &event.Event{
		Response: &model.Response{
			ID:     assistantEventID,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: m.assistantText,
				},
			}},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           assistantEventID,
		Timestamp:    time.Now(),
	}

	graphCompletionEvent := &event.Event{
		Response: &model.Response{
			ID:     graphEventID,
			Object: graph.ObjectTypeGraphExecution,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(m.assistantText),
			}},
		},
		StateDelta: map[string][]byte{
			m.stateKey: []byte(m.stateVal),
			graph.StateKeyLastResponseID: []byte(
				"\"" + assistantEventID + "\"",
			),
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           graphEventID,
		Timestamp:    time.Now(),
	}

	eventCh <- assistantEvent
	eventCh <- graphCompletionEvent
	close(eventCh)
	return eventCh, nil
}

func (m *mismatchedIDGraphCompletionAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for final response mismatch dedup testing",
	}
}

func (m *mismatchedIDGraphCompletionAgent) SubAgents() []agent.Agent { return nil }

func (m *mismatchedIDGraphCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mismatchedIDGraphCompletionAgent) Tools() []tool.Tool { return nil }

func (m *childVisibleThenTopGraphCompletionAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for child visible completion followed by top graph completion",
	}
}

func (m *childVisibleThenTopGraphCompletionAgent) SubAgents() []agent.Agent { return nil }

func (m *childVisibleThenTopGraphCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *childVisibleThenTopGraphCompletionAgent) Tools() []tool.Tool { return nil }

func (m *mismatchedIDGraphCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 2)
	assistantEvent := &event.Event{
		Response: &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(m.assistantText),
			}},
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "assistant-event-id",
		Timestamp:    time.Now(),
	}
	graphCompletionEvent := &event.Event{
		Response: &model.Response{
			ID:     "graph-event-id",
			Object: graph.ObjectTypeGraphExecution,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(m.assistantText),
			}},
		},
		StateDelta: map[string][]byte{
			graph.StateKeyLastResponseID: []byte(`"resp-2"`),
		},
		InvocationID: invocation.InvocationID,
		Author:       m.name,
		ID:           "graph-event-id",
		Timestamp:    time.Now(),
	}
	eventCh <- assistantEvent
	eventCh <- graphCompletionEvent
	close(eventCh)
	return eventCh, nil
}

func (m *childVisibleThenTopGraphCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 2)
	childRaw := graph.NewGraphCompletionEvent(
		graph.WithCompletionEventInvocationID(invocation.InvocationID),
		graph.WithCompletionEventFinalState(graph.State{
			graph.StateKeyLastResponse:   "child:hello",
			graph.StateKeyLastResponseID: "child-visible",
		}),
	)
	childVisible, ok := graph.VisibleGraphCompletionEventForAuthor(
		childRaw,
		"graph-child",
	)
	if !ok {
		close(eventCh)
		return eventCh, nil
	}
	topRaw := graph.NewGraphCompletionEvent(
		graph.WithCompletionEventInvocationID(invocation.InvocationID),
		graph.WithCompletionEventFinalState(graph.State{
			graph.StateKeyLastResponse:   "top:child:hello",
			graph.StateKeyLastResponseID: "top-final",
		}),
	)
	eventCh <- childVisible
	eventCh <- topRaw
	close(eventCh)
	return eventCh, nil
}

// failingAgent returns an error from Run to cover error path in Runner.Run.
type failingAgent struct{ name string }

func (m *failingAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *failingAgent) SubAgents() []agent.Agent             { return nil }
func (m *failingAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *failingAgent) Tools() []tool.Tool                   { return nil }
func (m *failingAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, errors.New("run failed")
}

// completionNoticeAgent emits an event that requires completion; it pre-adds
// a notice channel so Runner can notify it. The test asserts the channel closes.
type completionNoticeAgent struct {
	name     string
	noticeCh chan any
}

func (m *completionNoticeAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *completionNoticeAgent) SubAgents() []agent.Agent             { return nil }
func (m *completionNoticeAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *completionNoticeAgent) Tools() []tool.Tool                   { return nil }
func (m *completionNoticeAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	// Prepare an event that requires completion and pre-create the notice channel.
	id := "need-complete-1"
	m.noticeCh = inv.AddNoticeChannel(ctx, agent.GetAppendEventNoticeKey(id))
	ch <- &event.Event{
		Response:           &model.Response{ID: id, Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}},
		ID:                 id,
		RequiresCompletion: true,
	}
	close(ch)
	return ch, nil
}

// panicAppendSessionService panics when AppendEvent is called to exercise
// the recover path inside processAgentEvents.
type panicAppendSessionService struct{ session.Service }

func (s *panicAppendSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, _ ...session.Option) error {
	panic("append failed")
}

// appendErrorSessionService returns error on AppendEvent to cover the error
// branch and to ensure EnqueueSummaryJob is not called afterward.
type appendErrorSessionService struct{ *mockSessionService }

func (s *appendErrorSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, _ ...session.Option) error {
	s.mockSessionService.appendEventCalls = append(s.mockSessionService.appendEventCalls, appendEventCall{sess, e, nil})
	return errors.New("append error")
}

// getSessionErrorService returns error on GetSession to cover error path in getOrCreateSession.
type getSessionErrorService struct{ *mockSessionService }

func (s *getSessionErrorService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, errors.New("get session error")
}

// closeErrorSessionService returns error on Close to test error handling.
type closeErrorSessionService struct {
	session.Service
	closeErr    error
	closeCalled int
}

func (s *closeErrorSessionService) Close() error {
	s.closeCalled++
	return s.closeErr
}

func (s *closeErrorSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		Events:    []event.Event{},
		Summaries: map[string]*session.Summary{},
	}, nil
}

func (s *closeErrorSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (s *closeErrorSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, options ...session.Option) error {
	return nil
}

func (s *closeErrorSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

// noOpAgent emits one qualifying assistant message then closes.
type noOpAgent struct{ name string }

func (m *noOpAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *noOpAgent) SubAgents() []agent.Agent             { return nil }
func (m *noOpAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *noOpAgent) Tools() []tool.Tool                   { return nil }
func (m *noOpAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("hi")}}}}
	close(ch)
	return ch, nil
}

// graphDoneAgent emits a final graph.execution event with customizable state delta and choices.
type graphDoneAgent struct {
	name        string
	delta       map[string][]byte
	withChoices bool
}

func (m *graphDoneAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *graphDoneAgent) SubAgents() []agent.Agent             { return nil }
func (m *graphDoneAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *graphDoneAgent) Tools() []tool.Tool                   { return nil }
func (m *graphDoneAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ev := &event.Event{
		Response:   &model.Response{ID: "graph-done", Object: graph.ObjectTypeGraphExecution, Done: true},
		StateDelta: m.delta,
	}
	if m.withChoices {
		ev.Response.Choices = []model.Choice{{Index: 0, Message: model.NewAssistantMessage("final")}}
	}
	ch <- ev
	close(ch)
	return ch, nil
}

type fallbackCompletionAgent struct {
	name        string
	delta       map[string][]byte
	errType     string
	errMessage  string
	successText string
}

func (m *fallbackCompletionAgent) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *fallbackCompletionAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *fallbackCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *fallbackCompletionAgent) Tools() []tool.Tool {
	return nil
}

func (m *fallbackCompletionAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 3)
	if len(m.delta) > 0 {
		ch <- event.New(
			inv.InvocationID,
			m.name,
			event.WithStateDelta(m.delta),
		)
	}
	if m.errMessage != "" {
		ch <- event.NewErrorEvent(
			inv.InvocationID,
			m.name,
			m.errType,
			m.errMessage,
		)
	}
	if m.successText != "" {
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			m.name,
			&model.Response{
				ID:   "fallback-success",
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: m.successText,
					},
				}},
			},
		)
	}
	close(ch)
	return ch, nil
}

type fallbackGraphCompletionAgent struct {
	name       string
	delta      map[string][]byte
	errType    string
	errMessage string
}

func (m *fallbackGraphCompletionAgent) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *fallbackGraphCompletionAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *fallbackGraphCompletionAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *fallbackGraphCompletionAgent) Tools() []tool.Tool {
	return nil
}

func (m *fallbackGraphCompletionAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	if len(m.delta) > 0 {
		ch <- event.New(
			inv.InvocationID,
			m.name,
			event.WithStateDelta(m.delta),
		)
	}
	if m.errMessage != "" {
		ch <- graph.NewNodeErrorEvent(
			graph.WithNodeEventInvocationID(inv.InvocationID),
			graph.WithNodeEventNodeID("lookup"),
			graph.WithNodeEventNodeType(graph.NodeTypeFunction),
			graph.WithNodeEventError(m.errMessage),
			graph.WithNodeEventResponseError(&model.ResponseError{
				Type:    m.errType,
				Message: m.errMessage,
			}),
		)
	}
	close(ch)
	return ch, nil
}

func TestNewRunner_DefaultSessionService(t *testing.T) {
	// No WithSessionService option -> should default to inmemory session service.
	r := NewRunner("app", &noOpAgent{name: "a"})
	rr := r.(*runner)
	require.NotNil(t, rr.sessionService)
}

func TestRunner_Run_AgentRunError(t *testing.T) {
	r := NewRunner("app", &failingAgent{name: "f"})
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("m"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestGetOrCreateSession_Existing(t *testing.T) {
	// Pre-create a session; getOrCreateSession should return it without creating a new one.
	svc := sessioninmemory.NewSessionService()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	_, err := svc.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), key.UserID, key.SessionID, model.NewUserMessage("hi"))
	require.NoError(t, err)
	for range ch {
	}
}

func TestGetOrCreateSession_GetError(t *testing.T) {
	// Service that fails GetSession should make Run return the error immediately.
	svc := &getSessionErrorService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("m"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestProcessAgentEvents_PanicRecovery(t *testing.T) {
	// Use mock service that panics on append to exercise recover in the goroutine.
	base := &mockSessionService{}
	svc := &panicAppendSessionService{Service: base}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))

	// Empty message to avoid initial user append; only agent event will be processed and panic.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Consume until closed; should not hang due to recover.
	for range ch {
	}
}

func TestHandleEventPersistence_AppendErrorSkipsSummarize(t *testing.T) {
	base := &mockSessionService{}
	svc := &appendErrorSessionService{mockSessionService: base}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	// Empty message avoids initial user append which would error out early.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	for range ch {
	}
	// Append failed -> EnqueueSummaryJob should not be called.
	require.Len(t, base.enqueueSummaryJobCalls, 0)
}

func TestEmitRunnerCompletion_AppendErrorStillEmits(t *testing.T) {
	base := &mockSessionService{}
	svc := &appendErrorSessionService{mockSessionService: base}
	// Emit a graph completion so emitRunnerCompletion propagates state/choices as well.
	ag := &graphDoneAgent{name: "g", delta: map[string][]byte{"k": []byte("v")}, withChoices: true}
	r := NewRunner("app", ag, WithSessionService(svc))
	// Empty message avoids initial append error; ensures we reach completion emission.
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	var last *event.Event
	for e := range ch {
		last = e
	}
	require.NotNil(t, last)
	require.True(t, last.Done)
	require.Equal(t, model.ObjectTypeRunnerCompletion, last.Object)
	// Even though append failed internally, the completion event is still emitted.
}

func TestRunner_CompletionIncludesFallbackBusinessState(t *testing.T) {
	const (
		stateKey   = "_node_error_"
		stateValue = "fatal callback"
		errType    = model.ErrorTypeFlowError
		errMessage = "execution failed"
	)

	svc := sessioninmemory.NewSessionService()
	ag := &fallbackCompletionAgent{
		name: "fallback",
		delta: map[string][]byte{
			stateKey:              []byte(stateValue),
			graph.MetadataKeyNode: []byte(`{"node_id":"n1"}`),
			graph.MetadataKeyTool: []byte(`{"tool_id":"t1"}`),
		},
		errType:    errType,
		errMessage: errMessage,
	}
	r := NewRunner("app", ag, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage(""),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		if e.IsRunnerCompletion() {
			completion = e
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.Response)
	require.Nil(t, completion.Response.Error)
	require.Equal(t, stateValue,
		string(completion.StateDelta[stateKey]))
	require.NotContains(t, completion.StateDelta, graph.MetadataKeyNode)
	require.NotContains(t, completion.StateDelta, graph.MetadataKeyTool)
}

func TestRunner_CompletionCarriesGraphTerminalError(t *testing.T) {
	const (
		stateKey   = "_node_error_"
		stateValue = "fatal callback"
		errType    = model.ErrorTypeFlowError
		errMessage = "execution failed"
	)

	svc := sessioninmemory.NewSessionService()
	ag := &fallbackGraphCompletionAgent{
		name: "graph-fallback",
		delta: map[string][]byte{
			stateKey: []byte(stateValue),
		},
		errType:    errType,
		errMessage: errMessage,
	}
	r := NewRunner("app", ag, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage(""),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		if e.IsRunnerCompletion() {
			completion = e
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.Response)
	require.NotNil(t, completion.Response.Error)
	require.Equal(t, errType, completion.Response.Error.Type)
	require.Equal(t, errMessage, completion.Response.Error.Message)
	require.Equal(t, stateValue, string(completion.StateDelta[stateKey]))
}

func TestRunner_CompletionSkipsFallbackAfterRecovery(t *testing.T) {
	const (
		stateKey   = "_node_error_"
		stateValue = "fatal callback"
		errMessage = "execution failed"
		successMsg = "recovered"
	)

	svc := sessioninmemory.NewSessionService()
	ag := &fallbackCompletionAgent{
		name:        "fallback",
		delta:       map[string][]byte{stateKey: []byte(stateValue)},
		errType:     model.ErrorTypeFlowError,
		errMessage:  errMessage,
		successText: successMsg,
	}
	r := NewRunner("app", ag, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage(""),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		if e.IsRunnerCompletion() {
			completion = e
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.Response)
	require.Nil(t, completion.Response.Error)
	require.Empty(t, completion.StateDelta)
}

func TestCaptureCompletionFallback_IgnoresPartialError(t *testing.T) {
	loop := &eventLoopContext{}
	r := &runner{}

	r.captureCompletionFallback(loop, &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Error:     &model.ResponseError{Message: "boom"},
		},
	})

	require.Nil(t, loop.finalError)
}

func TestMergeCompletionFallbackStateDelta(t *testing.T) {
	t.Run("empty src", func(t *testing.T) {
		dst := map[string][]byte{"keep": []byte("v")}

		got := mergeCompletionFallbackStateDelta(dst, nil)

		require.Equal(t, dst, got)
		require.Equal(t, "v", string(got["keep"]))
	})

	t.Run("filters metadata and copies business keys", func(t *testing.T) {
		const (
			stateKey   = "node_error"
			nilState   = "nil_state"
			stateValue = "fatal"
		)

		srcValue := []byte(stateValue)
		got := mergeCompletionFallbackStateDelta(nil, map[string][]byte{
			stateKey:              srcValue,
			nilState:              nil,
			graph.MetadataKeyNode: []byte(`{"node_id":"n1"}`),
			graph.MetadataKeyTool: []byte(`{"tool_id":"t1"}`),
		})

		require.Equal(t, stateValue, string(got[stateKey]))
		require.Contains(t, got, nilState)
		require.Nil(t, got[nilState])
		require.NotContains(t, got, graph.MetadataKeyNode)
		require.NotContains(t, got, graph.MetadataKeyTool)

		srcValue[0] = 'F'
		require.Equal(t, stateValue, string(got[stateKey]))
	})
}

func TestCloneResponseError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		require.Nil(t, cloneResponseError(nil))
	})

	t.Run("deep copy", func(t *testing.T) {
		param := "p"
		code := "c"
		in := &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "boom",
			Param:   &param,
			Code:    &code,
		}

		got := cloneResponseError(in)

		require.NotNil(t, got)
		require.NotSame(t, in, got)
		require.NotSame(t, in.Param, got.Param)
		require.NotSame(t, in.Code, got.Code)

		*in.Param = "mutated-param"
		*in.Code = "mutated-code"
		require.Equal(t, "p", *got.Param)
		require.Equal(t, "c", *got.Code)
	})
}

func TestGraphCompletionNotPersistedAsMessage(t *testing.T) {
	const (
		appName   = "app"
		userID    = "u"
		sessionID = "s"
		userMsg   = "hi"
		stateKey  = "k"
		stateVal  = "v"
	)

	svc := sessioninmemory.NewSessionService()
	ag := &graphDoneAgent{
		name:        "g",
		delta:       map[string][]byte{stateKey: []byte(stateVal)},
		withChoices: true,
	}
	r := NewRunner(appName, ag, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage(userMsg),
	)
	require.NoError(t, err)
	for range ch {
	}

	sess, err := svc.GetSession(
		context.Background(),
		session.Key{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
		},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 2)
	require.True(t, sess.Events[0].IsUserMessage())
	require.Equal(t, model.ObjectTypeRunnerCompletion,
		sess.Events[1].Object)
}

func TestRunner_GraphAgent_LegacyRunnerCompletionIncludesFinalResponse(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&staticModel{name: "m1", content: "first"},
		"i1",
		nil,
	)
	sg.AddLLMNode(
		"n2",
		&staticModel{name: "m2", content: "second"},
		"i2",
		nil,
	)
	sg.AddEdge("n1", "n2")
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n2").MustCompile()

	ga, err := graphagent.New("ga", compiled)
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)

	var last *event.Event
	for e := range ch {
		last = e
	}
	require.NotNil(t, last)
	require.True(t, last.IsRunnerCompletion())
	require.Len(t, last.Response.Choices, 1)
	require.Equal(t, model.RoleAssistant,
		last.Response.Choices[0].Message.Role)
	require.Equal(t, "second",
		last.Response.Choices[0].Message.Content)

	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
	})
	require.NoError(t, err)
	require.Len(t, sess.Events, 2)
	require.True(t, sess.Events[0].IsUserMessage())
	require.Equal(t, model.ObjectTypeRunnerCompletion,
		sess.Events[1].Object)
	require.Len(t, sess.Events[1].Choices, 1)
	require.Equal(t, model.RoleAssistant,
		sess.Events[1].Choices[0].Message.Role)
	require.Equal(t, "second",
		sess.Events[1].Choices[0].Message.Content)
}

func TestRunner_DisableGraphExecutorEvents_HidesBarrierEvents(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&staticModel{name: "m1", content: "hidden barrier"},
		"i1",
		nil,
	)
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n1").MustCompile()
	ga, err := graphagent.New("ga", compiled)
	require.NoError(t, err)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphExecutorEvents(true),
	)
	require.NoError(t, err)
	var last *event.Event
	for evt := range ch {
		require.NotEqual(t, graph.ObjectTypeGraphNodeBarrier, evt.Object)
		require.NotEqual(t, graph.ObjectTypeGraphBarrier, evt.Object)
		last = evt
	}
	require.NotNil(t, last)
	require.True(t, last.IsRunnerCompletion())
	require.Len(t, last.Response.Choices, 1)
	require.Equal(t, "hidden barrier", last.Response.Choices[0].Message.Content)
}

func TestRunner_DisableGraphExecutorEvents_PreservesGraphFailure(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("boom")
	})
	compiled := sg.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	ga, err := graphagent.New("ga", compiled)
	require.NoError(t, err)
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithDisableGraphExecutorEvents(true),
	)
	require.NoError(t, err)
	var last *event.Event
	var sawErrorEvent bool
	for evt := range ch {
		require.NotEqual(t, graph.ObjectTypeGraphPregelStep, evt.Object)
		if evt.Object == model.ObjectTypeError &&
			evt.Response != nil &&
			evt.Response.Error != nil {
			sawErrorEvent = true
			require.Contains(t, evt.Response.Error.Message, "boom")
		}
		last = evt
	}
	require.True(t, sawErrorEvent)
	require.NotNil(t, last)
	require.True(t, last.IsRunnerCompletion())
	require.NotNil(t, last.Response)
	require.Nil(t, last.Response.Error)
	require.Len(t, last.Response.Choices, 0)
}

func TestRunner_GraphAgentPersistsLLMDoneResponses(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&staticModel{name: "m1", content: "first"},
		"i1",
		nil,
	)
	sg.AddLLMNode(
		"n2",
		&staticModel{name: "m2", content: "second"},
		"i2",
		nil,
	)
	sg.AddEdge("n1", "n2")
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n2").MustCompile()

	ga, err := graphagent.New("ga", compiled)
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithGraphEmitFinalModelResponses(true),
	)
	require.NoError(t, err)

	var last *event.Event
	for e := range ch {
		last = e
	}
	require.NotNil(t, last)
	require.True(t, last.IsRunnerCompletion())
	require.Empty(t, last.Response.Choices)

	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
	})
	require.NoError(t, err)
	require.Len(t, sess.Events, 3)
	require.True(t, sess.Events[0].IsUserMessage())

	require.Equal(t, model.RoleAssistant,
		sess.Events[1].Choices[0].Message.Role)
	require.Equal(t, "first",
		sess.Events[1].Choices[0].Message.Content)

	require.Equal(t, model.RoleAssistant,
		sess.Events[2].Choices[0].Message.Role)
	require.Equal(t, "second",
		sess.Events[2].Choices[0].Message.Content)
}

func TestRunner_GraphAgentPersistsLLMDoneResponsesWithCallbacksAndHiddenCompletion(t *testing.T) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		"n1",
		&staticModel{name: "m1", content: "first"},
		"i1",
		nil,
	)
	sg.AddLLMNode(
		"n2",
		&staticModel{name: "m2", content: "second"},
		"i2",
		nil,
	)
	sg.AddEdge("n1", "n2")
	compiled := sg.SetEntryPoint("n1").SetFinishPoint("n2").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return nil, nil
	})
	ga, err := graphagent.New("ga", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ga, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hi"),
		agent.WithGraphEmitFinalModelResponses(true),
		agent.WithDisableGraphCompletionEvent(true),
	)
	require.NoError(t, err)

	var completion *event.Event
	for e := range ch {
		require.False(t, e.Done && e.Object == graph.ObjectTypeGraphExecution)
		if e.Object == model.ObjectTypeRunnerCompletion {
			completion = e
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.StateDelta)
	require.Equal(t, `"second"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
	require.Empty(t, completion.Response.Choices)

	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
	})
	require.NoError(t, err)
	require.Len(t, sess.Events, 3)
	require.True(t, sess.Events[0].IsUserMessage())
	require.Equal(t, "first", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "second", sess.Events[2].Choices[0].Message.Content)
}

func TestRunner_WrappedGraphAgentFinalModelResponses_NoDuplicateFinalText(t *testing.T) {
	tests := []struct {
		name  string
		build func(child agent.Agent) agent.Agent
	}{
		{
			name: "chain",
			build: func(child agent.Agent) agent.Agent {
				return chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child}))
			},
		},
		{
			name: "cycle",
			build: func(child agent.Agent) agent.Agent {
				return cycleagent.New(
					"cycle",
					cycleagent.WithSubAgents([]agent.Agent{child}),
					cycleagent.WithMaxIterations(1),
				)
			},
		},
		{
			name: "parallel",
			build: func(child agent.Agent) agent.Agent {
				return parallelagent.New("parallel", parallelagent.WithSubAgents([]agent.Agent{child}))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child := newWrappedGraphLLMChildAgent(t)
			svc := sessioninmemory.NewSessionService()
			r := NewRunner("app", tt.build(child), WithSessionService(svc))
			ch, err := r.Run(
				context.Background(),
				"u",
				tt.name+"-llm",
				model.NewUserMessage("hi"),
				agent.WithDisableGraphCompletionEvent(true),
				agent.WithGraphEmitFinalModelResponses(true),
			)
			require.NoError(t, err)

			var completion *event.Event
			var finalTextEvents int
			for evt := range ch {
				require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
				if evt.Response != nil &&
					len(evt.Response.Choices) > 0 &&
					evt.Response.Choices[0].Message.Content == "wrapped-final" {
					finalTextEvents++
				}
				if evt.IsRunnerCompletion() {
					completion = evt
				}
			}

			require.Equal(t, 1, finalTextEvents)
			require.NotNil(t, completion)
			require.NotNil(t, completion.StateDelta)
			require.Equal(t, `"wrapped-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
			require.Empty(t, completion.Response.Choices)

			sess, err := svc.GetSession(context.Background(), session.Key{
				AppName:   "app",
				UserID:    "u",
				SessionID: tt.name + "-llm",
			})
			require.NoError(t, err)
			require.Len(t, sess.Events, 2)
			require.Equal(t, "wrapped-final", sess.Events[1].Choices[0].Message.Content)
		})
	}
}

func TestRunner_WrappedGraphAgentFinalModelResponses_EmptyResponseID_StreamModeUpdates_KeepsFinalText(
	t *testing.T,
) {
	tests := []struct {
		name  string
		build func(child agent.Agent) agent.Agent
	}{
		{
			name: "chain",
			build: func(child agent.Agent) agent.Agent {
				return chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{child}))
			},
		},
		{
			name: "cycle",
			build: func(child agent.Agent) agent.Agent {
				return cycleagent.New(
					"cycle",
					cycleagent.WithSubAgents([]agent.Agent{child}),
					cycleagent.WithMaxIterations(1),
				)
			},
		},
		{
			name: "parallel",
			build: func(child agent.Agent) agent.Agent {
				return parallelagent.New("parallel", parallelagent.WithSubAgents([]agent.Agent{child}))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child := newWrappedGraphLLMEmptyIDChildAgent(t)
			svc := sessioninmemory.NewSessionService()
			r := NewRunner("app", tt.build(child), WithSessionService(svc))
			ch, err := r.Run(
				context.Background(),
				"u",
				tt.name+"-llm-empty-id-updates",
				model.NewUserMessage("hi"),
				agent.WithDisableGraphCompletionEvent(true),
				agent.WithGraphEmitFinalModelResponses(true),
				agent.WithStreamMode(agent.StreamModeUpdates),
			)
			require.NoError(t, err)

			var completion *event.Event
			for evt := range ch {
				require.NotEqual(t, model.ObjectTypeChatCompletion, evt.Object)
				if evt.IsRunnerCompletion() {
					completion = evt
				}
			}

			require.NotNil(t, completion)
			require.NotNil(t, completion.StateDelta)
			require.Equal(t, `"empty-id-final"`, string(completion.StateDelta[graph.StateKeyLastResponse]))
			require.Len(t, completion.Response.Choices, 1)
			require.Equal(t, "empty-id-final", completion.Response.Choices[0].Message.Content)
			assertSessionKeepsSingleFinalAssistantEvent(
				t,
				svc,
				tt.name+"-llm-empty-id-updates",
				"empty-id-final",
			)
		})
	}
}

func TestPropagateGraphCompletion_NilStateValue(t *testing.T) {
	// Call propagateGraphCompletion directly to cover the nil-value copy branch.
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)
	ev := event.NewResponseEvent("inv", "app", &model.Response{ID: "rc", Object: model.ObjectTypeRunnerCompletion, Done: true})
	delta := map[string][]byte{"nil": nil}
	rr.propagateGraphCompletion(ev, delta, nil, false)
	require.Contains(t, ev.StateDelta, "nil")
	require.Nil(t, ev.StateDelta["nil"]) // explicit nil copy branch covered
}

func TestShouldEchoFinalChoicesInCompletion_Cases(t *testing.T) {
	const (
		appName        = "app"
		agentName      = "a"
		content        = "content"
		responseID     = "response-id"
		responseIDJSON = "\"response-id\""
	)

	rr := NewRunner(appName, &noOpAgent{name: agentName}).(*runner)

	t.Run("nil loop", func(t *testing.T) {
		require.True(t, rr.shouldEchoFinalChoicesInCompletion(nil, nil, nil))
	})

	t.Run("no final choices", func(t *testing.T) {
		loop := &eventLoopContext{finalChoices: nil}
		require.False(t, rr.shouldEchoFinalChoicesInCompletion(loop, nil, nil))
	})

	t.Run("legacy always includes", func(t *testing.T) {
		invocation := agent.NewInvocation(agent.WithInvocationRunOptions(
			agent.RunOptions{},
		))
		loop := &eventLoopContext{
			invocation: invocation,
			finalChoices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
			finalStateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte(responseIDJSON),
			},
			emittedAssistantResponseIDs: map[string]struct{}{
				responseID: {},
			},
		}
		require.True(t, rr.shouldEchoFinalChoicesInCompletion(
			loop,
			loop.finalChoices,
			loop.finalStateDelta,
		))
	})

	t.Run("new mode missing final id includes", func(t *testing.T) {
		invocation := agent.NewInvocation(agent.WithInvocationRunOptions(
			agent.RunOptions{GraphEmitFinalModelResponses: true},
		))
		loop := &eventLoopContext{
			invocation: invocation,
			finalChoices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		require.True(t, rr.shouldEchoFinalChoicesInCompletion(
			loop,
			loop.finalChoices,
			loop.finalStateDelta,
		))
	})

	t.Run("new mode duplicate id excluded", func(t *testing.T) {
		invocation := agent.NewInvocation(agent.WithInvocationRunOptions(
			agent.RunOptions{GraphEmitFinalModelResponses: true},
		))
		loop := &eventLoopContext{
			invocation: invocation,
			finalChoices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
			finalStateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte(responseIDJSON),
			},
			emittedAssistantResponseIDs: map[string]struct{}{
				responseID: {},
			},
		}
		require.False(t, rr.shouldEchoFinalChoicesInCompletion(
			loop,
			loop.finalChoices,
			loop.finalStateDelta,
		))
	})

	t.Run("new mode id not seen includes", func(t *testing.T) {
		invocation := agent.NewInvocation(agent.WithInvocationRunOptions(
			agent.RunOptions{GraphEmitFinalModelResponses: true},
		))
		loop := &eventLoopContext{
			invocation: invocation,
			finalChoices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
			finalStateDelta: map[string][]byte{
				graph.StateKeyLastResponseID: []byte(responseIDJSON),
			},
		}
		require.True(t, rr.shouldEchoFinalChoicesInCompletion(
			loop,
			loop.finalChoices,
			loop.finalStateDelta,
		))
	})
}

func TestShouldClearRunnerCompletionChoicesInSession_DoesNotDedupMismatchedResponseID(
	t *testing.T,
) {
	loop := &eventLoopContext{
		persistedAssistantChoiceSignatures: map[string]struct{}{
			assistantChoiceSignature([]model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("wrapped-final"),
			}}): {},
		},
	}
	finalChoices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("wrapped-final"),
	}}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"response-from-state"`),
	}
	require.False(t, shouldClearRunnerCompletionChoicesInSession(
		loop,
		finalChoices,
		finalStateDelta,
	))
}

func TestShouldClearRunnerCompletionChoicesInSession_FallsBackToChoiceSignatureWhenResponseIDMissing(
	t *testing.T,
) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithDisableGraphCompletionEvent(true),
			)),
		),
		persistedAssistantChoiceSignatures: map[string]struct{}{
			assistantChoiceSignature([]model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("wrapped-final"),
			}}): {},
		},
	}
	finalChoices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("wrapped-final"),
	}}
	require.True(t, shouldClearRunnerCompletionChoicesInSession(
		loop,
		finalChoices,
		nil,
	))
}

func TestShouldClearRunnerCompletionChoicesInSession_DedupsByPersistedResponseIDEvenWhenGraphCompletionEventIsVisible(
	t *testing.T,
) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithGraphEmitFinalModelResponses(true),
			)),
		),
		persistedAssistantResponseIDs: map[string]struct{}{
			"response-from-state": {},
		},
	}
	finalChoices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("wrapped-final"),
	}}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"response-from-state"`),
	}
	require.True(t, shouldClearRunnerCompletionChoicesInSession(
		loop,
		finalChoices,
		finalStateDelta,
	))
}

func TestShouldClearRunnerCompletionChoicesInSession_DoesNotDedupUsingEmittedResponseIDWithoutPersistence(
	t *testing.T,
) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithGraphEmitFinalModelResponses(true),
			)),
		),
		emittedAssistantResponseIDs: map[string]struct{}{
			"response-from-state": {},
		},
	}
	finalChoices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("wrapped-final"),
	}}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"response-from-state"`),
	}
	require.False(t, shouldClearRunnerCompletionChoicesInSession(
		loop,
		finalChoices,
		finalStateDelta,
	))
}

func TestShouldClearRunnerCompletionChoicesInSession_PreservesVisibleChoicesWhenResponseIDMissing(
	t *testing.T,
) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithGraphEmitFinalModelResponses(true),
			)),
		),
		persistedAssistantChoiceSignatures: map[string]struct{}{
			assistantChoiceSignature([]model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("wrapped-final"),
			}}): {},
		},
	}
	finalChoices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("wrapped-final"),
	}}
	require.False(t, shouldClearRunnerCompletionChoicesInSession(
		loop,
		finalChoices,
		nil,
	))
}

func TestShouldMarkCompletionSnapshotOnly_ResumeRunWithoutNewAssistantContent(t *testing.T) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
				}),
			)),
		),
		baselineFinalResponseID: "resp-1",
		priorAssistantResponseIDs: map[string]struct{}{
			"resp-1": {},
		},
	}
	choices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("stale-final"),
	}}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"resp-1"`),
	}

	require.True(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
}

func TestShouldMarkCompletionSnapshotOnly_SkipsNormalRunsAndFreshAssistantRuns(t *testing.T) {
	choices := []model.Choice{{
		Index:   0,
		Message: model.NewAssistantMessage("fresh-final"),
	}}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"resp-2"`),
	}

	t.Run("normal run", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: agent.NewInvocation(),
		}
		require.False(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
	})

	t.Run("resume with assistant content produced", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: agent.NewInvocation(
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithRuntimeState(graph.State{
						graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
					}),
				)),
			),
			baselineFinalResponseID:       "resp-2",
			freshAssistantContentProduced: true,
		}
		require.False(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
	})

	t.Run("resume with only snapshot-only visible completion recorded", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: agent.NewInvocation(
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithRuntimeState(graph.State{
						graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
					}),
				)),
			),
			baselineFinalResponseID: "resp-2",
			priorAssistantResponseIDs: map[string]struct{}{
				"resp-2": {},
			},
			persistedAssistantResponseIDs: map[string]struct{}{
				"resp-2": {},
			},
		}
		require.True(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
	})

	t.Run("resume with fresh completion identity", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: agent.NewInvocation(
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithRuntimeState(graph.State{
						graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
					}),
				)),
			),
			baselineFinalResponseID: "resp-1",
		}
		require.False(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
	})

	t.Run("resume without baseline identity", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: agent.NewInvocation(
				agent.WithInvocationRunOptions(agent.NewRunOptions(
					agent.WithRuntimeState(graph.State{
						graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
					}),
				)),
			),
		}
		require.False(t, shouldMarkCompletionSnapshotOnly(loop, choices, finalStateDelta))
	})
}

func TestBaselineFinalResponseIDFromRuntimeState(t *testing.T) {
	t.Run("state key string", func(t *testing.T) {
		runtimeState := map[string]any{
			graph.StateKeyLastResponseID: "resp-1",
		}
		require.Equal(t, "resp-1", baselineFinalResponseIDFromRuntimeState(runtimeState))
	})

	t.Run("completion metadata bytes", func(t *testing.T) {
		runtimeState := map[string]any{
			graph.MetadataKeyCompletion: []byte(`{"finalResponseID":"resp-meta"}`),
		}
		require.Equal(t, "resp-meta", baselineFinalResponseIDFromRuntimeState(runtimeState))
	})

	t.Run("invalid state key falls back to metadata", func(t *testing.T) {
		runtimeState := map[string]any{
			graph.StateKeyLastResponseID: []byte("{"),
			graph.MetadataKeyCompletion:  []byte(`{"finalResponseID":"resp-meta"}`),
		}
		require.Equal(t, "resp-meta", baselineFinalResponseIDFromRuntimeState(runtimeState))
	})
}

func TestBaselineFinalResponseID(t *testing.T) {
	t.Run("session state takes precedence", func(t *testing.T) {
		sess := &session.Session{
			State: session.StateMap{
				graph.StateKeyLastResponseID: []byte(`"resp-from-session"`),
			},
		}
		runtimeState := map[string]any{
			graph.StateKeyLastResponseID: "resp-from-runtime",
		}
		require.Equal(t, "resp-from-session", baselineFinalResponseID(sess, runtimeState))
	})

	t.Run("falls back to runtime state", func(t *testing.T) {
		runtimeState := map[string]any{
			graph.StateKeyLastResponseID: "resp-from-runtime",
		}
		require.Equal(t, "resp-from-runtime", baselineFinalResponseID(nil, runtimeState))
	})
}

func TestCollectPriorAssistantResponseIDs(t *testing.T) {
	sess := &session.Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					ID: "resp-1",
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("assistant-1"),
					}},
				},
			},
			{
				Response: &model.Response{
					Object: model.ObjectTypeRunnerCompletion,
					Done:   true,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("runner-completion"),
					}},
				},
				Author: "app",
			},
			func() event.Event {
				raw := graph.NewGraphCompletionEvent(
					graph.WithCompletionEventInvocationID("inv"),
					graph.WithCompletionEventFinalState(graph.State{
						graph.StateKeyLastResponse:   "visible-final",
						graph.StateKeyLastResponseID: "visible-resp",
					}),
				)
				visible, ok := graph.VisibleGraphCompletionEventForAuthor(raw, "graph-child")
				require.True(t, ok)
				return *visible
			}(),
		},
	}

	ids := collectPriorAssistantResponseIDs(sess)
	require.Contains(t, ids, "resp-1")
	require.Contains(t, ids, "visible-resp")
	require.Len(t, ids, 2)
}

func TestCollectPriorAssistantResponseIDs_SkipsNonAssistantBranches(t *testing.T) {
	sess := &session.Session{
		Events: []event.Event{
			{},
			{
				Response: &model.Response{
					ID:        "partial-resp",
					IsPartial: true,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("partial"),
					}},
				},
			},
			{
				Response: &model.Response{
					ID: "user-resp",
					Choices: []model.Choice{{
						Message: model.NewUserMessage("user"),
					}},
				},
			},
			func() event.Event {
				evt := graph.NewGraphCompletionEvent(
					graph.WithCompletionEventInvocationID("inv-skip"),
					graph.WithCompletionEventFinalState(graph.State{
						graph.StateKeyLastResponse:   "graph-completion",
						graph.StateKeyLastResponseID: "graph-completion-id",
					}),
				)
				return *evt
			}(),
			{
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("missing-id"),
					}},
				},
			},
		},
	}

	require.Nil(t, collectPriorAssistantResponseIDs(nil))
	require.Nil(t, collectPriorAssistantResponseIDs(&session.Session{}))
	require.Nil(t, collectPriorAssistantResponseIDs(sess))
}

func TestStringValueFromRuntimeState(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   string
		wantOK bool
	}{
		{name: "plain string", input: "resp-1", want: "resp-1", wantOK: true},
		{name: "empty string", input: "", wantOK: false},
		{name: "json encoded bytes", input: []byte(`"resp-2"`), want: "resp-2", wantOK: true},
		{name: "plain bytes", input: []byte(`resp-3`), want: "resp-3", wantOK: true},
		{name: "empty bytes", input: []byte{}, wantOK: false},
		{name: "object bytes", input: []byte(`{}`), wantOK: false},
		{name: "array bytes", input: []byte(`[]`), wantOK: false},
		{name: "quoted raw bytes", input: []byte(`"x`), wantOK: false},
		{name: "unsupported type", input: 123, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := stringValueFromRuntimeState(tt.input)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCompletionMetadataFinalResponseID(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{name: "metadata struct", input: graph.CompletionMetadata{FinalResponseID: "resp-1"}, want: "resp-1"},
		{name: "metadata pointer", input: &graph.CompletionMetadata{FinalResponseID: "resp-2"}, want: "resp-2"},
		{name: "nil metadata pointer", input: (*graph.CompletionMetadata)(nil), want: ""},
		{name: "map value", input: map[string]any{"finalResponseID": "resp-3"}, want: "resp-3"},
		{name: "map missing", input: map[string]any{"other": "x"}, want: ""},
		{name: "json string", input: `{"finalResponseID":"resp-4"}`, want: "resp-4"},
		{name: "invalid string", input: `{`, want: ""},
		{name: "json bytes", input: []byte(`{"finalResponseID":"resp-5"}`), want: "resp-5"},
		{name: "invalid bytes", input: []byte(`{`), want: ""},
		{name: "unsupported type", input: 1, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, completionMetadataFinalResponseID(tt.input))
		})
	}
}

func TestIsResumeRunAndRunProducedAssistantContent(t *testing.T) {
	require.False(t, isResumeRun(nil))
	require.False(t, isResumeRun(&eventLoopContext{invocation: agent.NewInvocation()}))

	cmdLoop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: &graph.Command{ResumeMap: map[string]any{"k": "v"}},
				}),
			)),
		),
	}
	require.True(t, isResumeRun(cmdLoop))

	cmdValueLoop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.Command{ResumeMap: map[string]any{"k": "v"}},
				}),
			)),
		),
	}
	require.True(t, isResumeRun(cmdValueLoop))

	resumeLoop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.NewResumeCommand().WithResume("ok"),
				}),
			)),
		),
	}
	require.True(t, isResumeRun(resumeLoop))

	resumeValueLoop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.ResumeCommand{Resume: "ok"},
				}),
			)),
		),
	}
	require.True(t, isResumeRun(resumeValueLoop))

	require.False(t, runProducedAssistantContent(nil))
	require.False(t, runProducedAssistantContent(&eventLoopContext{}))
	require.True(t, runProducedAssistantContent(&eventLoopContext{
		freshAssistantContentProduced: true,
	}))
}

func TestMarkCompletionSnapshotOnly(t *testing.T) {
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)

	require.NotPanics(t, func() {
		rr.markCompletionSnapshotOnly(nil, nil)
	})

	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
				}),
			)),
		),
		priorAssistantResponseIDs: map[string]struct{}{"resp-1": {}},
	}

	nonGraph := event.NewResponseEvent("inv", "agent", &model.Response{
		ID: "resp-1",
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("assistant"),
		}},
	})
	rr.markCompletionSnapshotOnly(loop, nonGraph)
	require.False(t, graph.CompletionSnapshotOnlyFromStateDelta(nonGraph.StateDelta))

	graphEvt := graph.NewGraphCompletionEvent(
		graph.WithCompletionEventInvocationID("inv"),
		graph.WithCompletionEventFinalState(graph.State{
			graph.StateKeyLastResponse:   "assistant",
			graph.StateKeyLastResponseID: "resp-1",
		}),
	)
	rr.markCompletionSnapshotOnly(loop, graphEvt)
	require.True(t, graph.CompletionSnapshotOnlyFromStateDelta(graphEvt.StateDelta))
}

func TestRecordPersistedAssistantEvent_DoesNotCountSnapshotOnlyVisibleCompletionAsFreshContent(t *testing.T) {
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)

	loop := &eventLoopContext{}
	snapshotOnlyVisible, ok := graph.VisibleGraphCompletionEventForAuthor(
		graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID("inv"),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse:   "assistant",
				graph.StateKeyLastResponseID: "resp-1",
			}),
			graph.WithCompletionEventSnapshotOnly(true),
		),
		"graph-child",
	)
	require.True(t, ok)

	rr.recordPersistedAssistantEvent(loop, snapshotOnlyVisible, true)
	require.False(t, loop.freshAssistantContentProduced)
	require.Contains(t, loop.persistedAssistantResponseIDs, "resp-1")

	freshVisible, ok := graph.VisibleGraphCompletionEventForAuthor(
		graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID("inv"),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse:   "assistant",
				graph.StateKeyLastResponseID: "resp-2",
			}),
		),
		"graph-child",
	)
	require.True(t, ok)

	rr.recordPersistedAssistantEvent(loop, freshVisible, true)
	require.True(t, loop.freshAssistantContentProduced)
	require.Contains(t, loop.persistedAssistantResponseIDs, "resp-2")
}

func TestAssistantChoiceSignature_UsesAllAssistantChoices(t *testing.T) {
	require.Equal(
		t,
		`[{"role":"assistant","content":"wrapped-final"},{"role":"assistant","content":"alt"}]`,
		assistantChoiceSignature([]model.Choice{
			{
				Index:   0,
				Message: model.NewAssistantMessage("wrapped-final"),
			},
			{
				Index:   1,
				Message: model.NewAssistantMessage("alt"),
			},
		}),
	)
}

func TestRecordEmittedAssistantResponseID_Cases(t *testing.T) {
	const (
		appName        = "app"
		agentName      = "a"
		invocationID   = "inv"
		author         = "author"
		content        = "content"
		deltaContent   = "delta"
		stateEventID   = "state-event-id"
		graphEventID   = "graph-event-id"
		partialEvent   = "partial-event-id"
		invalidEvent   = "invalid-event-id"
		userEvent      = "user-event-id"
		deltaOnlyEvent = "delta-only-event-id"
	)

	rr := NewRunner(appName, &noOpAgent{name: agentName}).(*runner)
	enabledInvocation := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.RunOptions{GraphEmitFinalModelResponses: true},
	))

	t.Run("nil loop", func(t *testing.T) {
		rsp := &model.Response{
			ID:     stateEventID,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(nil, e)
	})

	t.Run("nil event", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rr.recordEmittedAssistantResponseID(loop, nil)
	})

	t.Run("nil response", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rr.recordEmittedAssistantResponseID(loop, &event.Event{})
	})

	t.Run("nil invocation", func(t *testing.T) {
		loop := &eventLoopContext{}
		rsp := &model.Response{
			ID:     stateEventID,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Nil(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("skips when flag disabled", func(t *testing.T) {
		loop := &eventLoopContext{invocation: agent.NewInvocation()}
		rsp := &model.Response{
			ID:     stateEventID,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Nil(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("skips empty response id", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rsp := &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Nil(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("records assistant message", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rsp := &model.Response{
			ID:     stateEventID,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		_, ok := loop.emittedAssistantResponseIDs[stateEventID]
		require.True(t, ok)
	})

	t.Run("skips graph completion", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: enabledInvocation,
			emittedAssistantResponseIDs: map[string]struct{}{
				stateEventID: {},
			},
		}
		rsp := &model.Response{
			ID:     graphEventID,
			Object: graph.ObjectTypeGraphExecution,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Len(t, loop.emittedAssistantResponseIDs, 1)
	})

	t.Run("skips partial response", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rsp := &model.Response{
			ID:        partialEvent,
			Object:    model.ObjectTypeChatCompletionChunk,
			Done:      false,
			IsPartial: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Empty(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("skips invalid content", func(t *testing.T) {
		loop := &eventLoopContext{invocation: enabledInvocation}
		rsp := &model.Response{
			ID:     invalidEvent,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
				},
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Empty(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("skips non assistant role", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: enabledInvocation,
		}
		rsp := &model.Response{
			ID:     userEvent,
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleUser,
					Content: content,
				},
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Empty(t, loop.emittedAssistantResponseIDs)
	})

	t.Run("skips empty message content when delta used", func(t *testing.T) {
		loop := &eventLoopContext{
			invocation: enabledInvocation,
		}
		rsp := &model.Response{
			ID:     deltaOnlyEvent,
			Object: model.ObjectTypeChatCompletionChunk,
			Done:   false,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
				},
				Delta: model.Message{
					Content: deltaContent,
				},
			}},
		}
		e := event.NewResponseEvent(invocationID, author, rsp)
		rr.recordEmittedAssistantResponseID(loop, e)
		require.Empty(t, loop.emittedAssistantResponseIDs)
	})
}

func TestProcessAgentEvents_NotifyCompletion(t *testing.T) {
	// Verify that RequiresCompletion results in NotifyCompletion closing the notice channel.
	ag := &completionNoticeAgent{name: "c"}
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("go"))
	require.NoError(t, err)
	// Drain events to allow processing.
	for range ch {
	}
	// Wait for notice channel to close; a closed channel receives immediately.
	select {
	case <-ag.noticeCh:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive completion notice in time")
	}
}

// nilEventAgent emits a nil event to exercise the skip branch.
type nilEventAgent struct{ name string }

func (m *nilEventAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *nilEventAgent) SubAgents() []agent.Agent             { return nil }
func (m *nilEventAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *nilEventAgent) Tools() []tool.Tool                   { return nil }
func (m *nilEventAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- nil
	close(ch)
	return ch, nil
}

func TestProcessAgentEvents_NilEventSkipped(t *testing.T) {
	r := NewRunner("app", &nilEventAgent{name: "n"}, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Expect only the runner completion event to arrive.
	var count int
	for range ch {
		count++
	}
	require.Equal(t, 1, count)
}

func TestRunner_Run_AppendUserEventError(t *testing.T) {
	// Non-empty message with append-error service should cause Run to return error.
	svc := &appendErrorSessionService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("hello"))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestRunner_Run_SeedAppendError(t *testing.T) {
	// Append error should be surfaced when seeding history into an empty session.
	svc := &appendErrorSessionService{mockSessionService: &mockSessionService{}}
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	seed := []model.Message{model.NewUserMessage("seed")}
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage(""), agent.WithMessages(seed))
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestRunner_Run_UserMessageRewriter_RewritesCurrentTurnMessages(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			require.Equal(t, "app", args.AppName)
			require.Equal(t, "u", args.UserID)
			require.Equal(t, "s", args.SessionID)
			require.Equal(t, "hello", args.OriginalMessage.Content)
			return []model.Message{
				model.NewUserMessage("ctx"),
				model.NewUserMessage("rewritten"),
			}, nil
		}),
	)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, model.NewUserMessage("rewritten"), ag.invocationMessage)
	sess, err := svc.GetSession(
		context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 2)
	require.Equal(t, "ctx", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "rewritten", sess.Events[1].Choices[0].Message.Content)
}

func TestRunner_Run_UserMessageRewriter_EmptyResultReturnsError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return nil, nil
		}),
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "user message rewriter returned no messages")
	require.Nil(t, ch)
}

func TestRunner_Run_UserMessageRewriter_NormalizesEmptyRolePayloadMessages(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingRoleAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return []model.Message{{Content: "rewritten"}}, nil
		}),
	)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, model.RoleUser, ag.capturedRole)
	sess, err := svc.GetSession(
		context.Background(),
		session.Key{AppName: "app", UserID: "u", SessionID: "s"},
	)
	require.NoError(t, err)
	require.Len(t, sess.Events, 1)
	require.Equal(t, model.RoleUser, sess.Events[0].Choices[0].Message.Role)
	require.Equal(t, authorUser, sess.Events[0].Author)
}

func TestRunner_Run_UserMessageRewriter_ReplacesCurrentMessageInsideSeedHistory(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	ag := &capturingInvocationMessagesAgent{name: "a"}
	r := NewRunner("app", ag, WithSessionService(svc))
	seed := []model.Message{
		model.NewUserMessage("previous"),
		model.NewUserMessage("hello"),
		model.NewAssistantMessage("after"),
	}
	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithMessages(seed),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return []model.Message{
				model.NewUserMessage("ctx"),
				model.NewUserMessage("rewritten"),
			}, nil
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
	require.Len(t, sess.Events, 4)
	require.Equal(t, "previous", sess.Events[0].Choices[0].Message.Content)
	require.Equal(t, "ctx", sess.Events[1].Choices[0].Message.Content)
	require.Equal(t, "rewritten", sess.Events[2].Choices[0].Message.Content)
	require.Equal(t, "after", sess.Events[3].Choices[0].Message.Content)
	require.Equal(t, model.NewUserMessage("rewritten"), ag.invocationMessage)
}

func TestRunner_Run_SecondRequestIncludesRewrittenTranscript(t *testing.T) {
	modelStub := &sequentialModel{
		name: "seq",
		responses: []*model.Response{
			{
				ID:   "resp-1",
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("first reply"),
				}},
			},
			{
				ID:   "resp-2",
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("second reply"),
				}},
			},
		},
	}
	ag := llmagent.New("a", llmagent.WithModel(modelStub))
	svc := sessioninmemory.NewSessionService()
	r := NewRunner("app", ag, WithSessionService(svc))

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("hello"),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return []model.Message{
				model.NewUserMessage("A"),
				model.NewUserMessage("hello"),
			}, nil
		}),
	)
	require.NoError(t, err)
	for range ch {
	}

	ch, err = r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage("again"),
		agent.WithUserMessageRewriter(func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return []model.Message{
				model.NewUserMessage("B"),
				model.NewUserMessage("again"),
			}, nil
		}),
	)
	require.NoError(t, err)
	for range ch {
	}

	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	require.Equal(t, []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("hello"),
		model.NewAssistantMessage("first reply"),
		model.NewUserMessage("B"),
		model.NewUserMessage("again"),
	}, requests[1].messages)
}

// oneEventAgent emits a single valid event; used to cover EmitEvent error path when context is cancelled.
type oneEventAgent struct{ name string }

func (m *oneEventAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *oneEventAgent) SubAgents() []agent.Agent             { return nil }
func (m *oneEventAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *oneEventAgent) Tools() []tool.Tool                   { return nil }
func (m *oneEventAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// Unbuffered channel so EmitEvent will block unless receiver is ready
	ch := make(chan *event.Event)
	go func() {
		ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("x")}}}}
		close(ch)
	}()
	return ch, nil
}

type tickCtxAgent struct {
	name     string
	interval time.Duration
}

func (m *tickCtxAgent) Info() agent.Info { return agent.Info{Name: m.name} }

func (m *tickCtxAgent) SubAgents() []agent.Agent { return nil }

func (m *tickCtxAgent) FindSubAgent(name string) agent.Agent { return nil }

func (m *tickCtxAgent) Tools() []tool.Tool { return nil }

func (m *tickCtxAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	const tickContent = "tick"
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				evt := event.NewResponseEvent(
					inv.InvocationID,
					m.name,
					&model.Response{
						Done: false,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								tickContent,
							),
						}},
					},
				)
				select {
				case <-ctx.Done():
					return
				case ch <- evt:
				}
			}
		}
	}()
	return ch, nil
}

func TestProcessAgentEvents_EmitEventContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running; EmitEvent should take ctx.Done() branch
	r := NewRunner("app", &oneEventAgent{name: "o"}, WithSessionService(sessioninmemory.NewSessionService()))
	ch, err := r.Run(ctx, "u", "s", model.NewUserMessage(""))
	require.NoError(t, err)
	// Should close without emitting any event due to emit error path returning early.
	var got int
	for range ch {
		got++
	}
	require.Equal(t, 0, got)
}

func TestRunner_Run_DetachedCancelKeepsDeadline(t *testing.T) {
	const (
		timeout  = 80 * time.Millisecond
		maxWait  = 500 * time.Millisecond
		interval = 10 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cancel()

	r := NewRunner(
		"app",
		&tickCtxAgent{name: "t", interval: interval},
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.NewUserMessage(""),
		agent.WithDetachedCancel(true),
	)
	require.NoError(t, err)

	deadline := time.After(maxWait)
	var events int
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				require.Greater(t, events, 0)
				return
			}
			events++
		case <-deadline:
			t.Fatal("run did not finish in time")
		}
	}
}

func TestRunner_Run_MaxRunDuration(t *testing.T) {
	const (
		parentTimeout = time.Second
		maxRun        = 50 * time.Millisecond
		maxWait       = 500 * time.Millisecond
		interval      = 10 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
	defer cancel()

	r := NewRunner(
		"app",
		&tickCtxAgent{name: "t", interval: interval},
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.NewUserMessage(""),
		agent.WithMaxRunDuration(maxRun),
	)
	require.NoError(t, err)

	deadline := time.After(maxWait)
	select {
	case <-deadline:
		t.Fatal("run did not finish in time")
	case <-drainChannel(ch):
	}
}

func TestRunner_ManagedRunner_CancelAndStatus(t *testing.T) {
	const (
		requestID = "req-cancel-1"
		maxWait   = 500 * time.Millisecond
		interval  = 10 * time.Millisecond
	)

	r := NewRunner(
		"app",
		&tickCtxAgent{name: "t", interval: interval},
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	mr, ok := r.(ManagedRunner)
	require.True(t, ok)

	ctx := context.Background()
	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.NewUserMessage(""),
		agent.WithRequestID(requestID),
		agent.WithDetachedCancel(true),
	)
	require.NoError(t, err)

	select {
	case <-time.After(maxWait):
		t.Fatal("did not receive first event")
	case _, ok := <-ch:
		require.True(t, ok)
	}

	status, ok := mr.RunStatus(requestID)
	require.True(t, ok)
	require.Equal(t, requestID, status.RequestID)
	require.NotEmpty(t, status.InvocationID)
	require.GreaterOrEqual(t, status.EventCount, 1)
	require.False(t, status.LastEventAt.IsZero())

	require.True(t, mr.Cancel(requestID))

	select {
	case <-time.After(maxWait):
		t.Fatal("run did not finish after cancel")
	case <-drainChannel(ch):
	}

	_, ok = mr.RunStatus(requestID)
	require.False(t, ok)
	require.False(t, mr.Cancel(requestID))
}

func TestRunner_Close_CancelsRunningRuns(t *testing.T) {
	const (
		requestID = "req-close-1"
		maxWait   = 500 * time.Millisecond
		interval  = 10 * time.Millisecond
	)

	r := NewRunner(
		"app",
		&tickCtxAgent{name: "t", interval: interval},
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	mr, ok := r.(ManagedRunner)
	require.True(t, ok)

	ch, err := r.Run(
		context.Background(),
		"u",
		"s",
		model.NewUserMessage(""),
		agent.WithRequestID(requestID),
	)
	require.NoError(t, err)

	select {
	case <-time.After(maxWait):
		t.Fatal("did not receive first event")
	case _, ok := <-ch:
		require.True(t, ok)
	}

	require.NoError(t, r.Close())

	select {
	case <-time.After(maxWait):
		t.Fatal("run did not finish after close")
	case <-drainChannel(ch):
	}

	_, ok = mr.RunStatus(requestID)
	require.False(t, ok)
}

func TestRunner_registerRun_ValidatesInput(t *testing.T) {
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)

	_, err := rr.registerRun("", RunStatus{}, func() {}, nil)
	require.Error(t, err)

	_, err = rr.registerRun("run", RunStatus{}, nil, nil)
	require.Error(t, err)
}

func TestRunner_registerRun_DuplicateRunID(t *testing.T) {
	rr := NewRunner("app", &noOpAgent{name: "a"}).(*runner)

	handle, err := rr.registerRun("run", RunStatus{}, func() {}, nil)
	require.NoError(t, err)
	require.NotNil(t, handle)

	_, err = rr.registerRun("run", RunStatus{}, func() {}, nil)
	require.Error(t, err)
}

func TestProcessAgentEvents_EmitEventErrorBranch_Direct(t *testing.T) {
	// Call processAgentEvents directly to deterministically exercise the emit error branch.
	rr := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inv := agent.NewInvocation()
	sess, _ := rr.sessionService.CreateSession(context.Background(), session.Key{AppName: "app", UserID: "u", SessionID: "s"}, session.StateMap{})

	agentCh := make(chan *event.Event)
	flushCh := make(chan *flush.FlushRequest)
	// No Attach needed because processAgentEvents will attach using this channel.
	processed := rr.processAgentEvents(ctx, sess, inv, agentCh, flushCh, nil)
	// Send one event, then close agentCh
	go func() {
		agentCh <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("x")}}}}
		close(agentCh)
	}()

	// Do not read from processed until goroutine has had a chance to hit emit; then drain.
	time.Sleep(50 * time.Millisecond)
	var n int
	for range processed {
		n++
	}
	require.Equal(t, 0, n)
}

func drainChannel(ch <-chan *event.Event) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	return done
}

func TestMergeCurrentTurnMessagesIntoSeed_ReplacesLastUserMessageWhenItMatchesOriginal(t *testing.T) {
	seed := []model.Message{
		model.NewUserMessage("first"),
		model.NewUserMessage("current"),
		model.NewAssistantMessage("after"),
	}
	currentTurn := []model.Message{
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
	}
	merged := mergeCurrentTurnMessagesIntoSeed(
		seed,
		model.NewUserMessage("current"),
		currentTurn,
	)
	require.Equal(t, []model.Message{
		model.NewUserMessage("first"),
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
		model.NewAssistantMessage("after"),
	}, merged)
}

func TestMergeCurrentTurnMessagesIntoSeed_AppendsWhenOnlyOlderMessageMatchesOriginal(t *testing.T) {
	seed := []model.Message{
		model.NewUserMessage("current"),
		model.NewAssistantMessage("after"),
		model.NewUserMessage("latest"),
	}
	currentTurn := []model.Message{
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
	}
	merged := mergeCurrentTurnMessagesIntoSeed(
		seed,
		model.NewUserMessage("current"),
		currentTurn,
	)
	require.Equal(t, []model.Message{
		model.NewUserMessage("current"),
		model.NewAssistantMessage("after"),
		model.NewUserMessage("latest"),
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
	}, merged)
}

func TestMergeCurrentTurnMessagesIntoSeed_AppendsWhenOriginalMissing(t *testing.T) {
	seed := []model.Message{
		model.NewUserMessage("first"),
		model.NewAssistantMessage("after"),
	}
	currentTurn := []model.Message{
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
	}
	merged := mergeCurrentTurnMessagesIntoSeed(
		seed,
		model.NewUserMessage("current"),
		currentTurn,
	)
	require.Equal(t, []model.Message{
		model.NewUserMessage("first"),
		model.NewAssistantMessage("after"),
		model.NewUserMessage("ctx"),
		model.NewUserMessage("rewritten"),
	}, merged)
}

func TestMergeCurrentTurnMessagesIntoSeed_PreservesSeedWhenCurrentTurnIsEmpty(t *testing.T) {
	seed := []model.Message{
		model.NewUserMessage("first"),
		model.NewUserMessage("current"),
	}
	merged := mergeCurrentTurnMessagesIntoSeed(
		seed,
		model.NewUserMessage("current"),
		nil,
	)
	require.Equal(t, seed, merged)
}

func TestFinalResponseIDFromStateDelta_Cases(t *testing.T) {
	const (
		unknownKey     = "other"
		unknownValue   = "\"x\""
		invalidJSON    = "{"
		responseID     = "resp-123"
		responseIDJSON = "\"resp-123\""
	)

	t.Run("nil delta", func(t *testing.T) {
		require.Equal(t, "", finalResponseIDFromStateDelta(nil))
	})

	t.Run("missing key", func(t *testing.T) {
		delta := map[string][]byte{
			unknownKey: []byte(unknownValue),
		}
		require.Equal(t, "", finalResponseIDFromStateDelta(delta))
	})

	t.Run("empty value", func(t *testing.T) {
		delta := map[string][]byte{
			graph.StateKeyLastResponseID: nil,
		}
		require.Equal(t, "", finalResponseIDFromStateDelta(delta))
	})

	t.Run("invalid json", func(t *testing.T) {
		delta := map[string][]byte{
			graph.StateKeyLastResponseID: []byte(invalidJSON),
		}
		require.Equal(t, "", finalResponseIDFromStateDelta(delta))
	})

	t.Run("invalid json falls back to completion metadata", func(t *testing.T) {
		delta := map[string][]byte{
			graph.StateKeyLastResponseID: []byte(invalidJSON),
			graph.MetadataKeyCompletion:  []byte(`{"finalResponseID":"resp-from-metadata"}`),
		}
		require.Equal(t, "resp-from-metadata", finalResponseIDFromStateDelta(delta))
	})

	t.Run("valid json", func(t *testing.T) {
		delta := map[string][]byte{
			graph.StateKeyLastResponseID: []byte(responseIDJSON),
		}
		require.Equal(t, responseID, finalResponseIDFromStateDelta(delta))
	})

	t.Run("completion metadata fallback", func(t *testing.T) {
		delta := map[string][]byte{
			graph.MetadataKeyCompletion: []byte(`{"finalResponseID":"resp-from-metadata"}`),
		}
		require.Equal(t, "resp-from-metadata", finalResponseIDFromStateDelta(delta))
	})
}

func TestRunner_Close_OwnedSessionService(t *testing.T) {
	// Create runner without providing session service.
	// Runner should create and own the default inmemory session service.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent)

	// Close should succeed.
	err := r.Close()
	require.NoError(t, err)

	// Close should be idempotent (safe to call multiple times).
	err = r.Close()
	require.NoError(t, err)
}

func TestRunner_Close_ProvidedSessionService(t *testing.T) {
	// Create a session service that we control.
	sessionService := sessioninmemory.NewSessionService()
	defer sessionService.Close()

	// Create runner with provided session service.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent, WithSessionService(sessionService))

	// Close the runner.
	err := r.Close()
	require.NoError(t, err)

	// The session service should still be usable because runner didn't
	// close it (it was provided by user).
	// This is a simple check - in practice, you'd verify the service is
	// still functional.
	assert.NotNil(t, sessionService)
}

func TestRunner_Close_Idempotent(t *testing.T) {
	// Test that calling Close multiple times is safe.
	mockAgent := &mockAgent{name: "test-agent"}
	r := NewRunner("test-app", mockAgent)

	// Call Close multiple times.
	for i := 0; i < 5; i++ {
		err := r.Close()
		require.NoError(t, err, "Close call %d should succeed", i+1)
	}
}

func TestRunner_Close_SessionServiceError(t *testing.T) {
	const closeErrMsg = "session service close error"

	// Create a mock session service that fails on Close.
	errorSessionService := &closeErrorSessionService{
		closeErr: errors.New(closeErrMsg),
	}

	// Create a mock agent.
	mockAgent := &mockAgent{name: "test-agent"}

	// Create runner directly and manually set it to own the error session service.
	// This simulates the case where the runner created a session service that
	// fails on Close.
	r := &runner{
		appName:             "test-app",
		defaultAgentName:    mockAgent.name,
		agents:              map[string]agent.Agent{mockAgent.name: mockAgent},
		sessionService:      errorSessionService,
		ownedSessionService: true, // Mark as owned to trigger Close in runner.Close().
	}

	// Close should return the error from session service.
	err := r.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), closeErrMsg)

	// Verify Close was called.
	assert.Equal(t, 1, errorSessionService.closeCalled)

	// Close should still be idempotent, even on error.
	// Second call should not return error because closeOnce protects it.
	err = r.Close()
	require.NoError(t, err)
	assert.Equal(t, 1, errorSessionService.closeCalled, "Close should only be called once")
}

func TestHandleFlushRequest_ProcessesEventAndClosesAck(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx := context.Background()

	agentCh := make(chan *event.Event, 1)
	agentCh <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}}

	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     agentCh,
		processedEventCh: make(chan *event.Event, 1),
	}
	req := &flush.FlushRequest{ACK: make(chan struct{})}

	err := r.handleFlushRequest(ctx, loop, req)
	require.NoError(t, err)

	select {
	case ev := <-loop.processedEventCh:
		require.NotNil(t, ev)
	default:
		require.Fail(t, "expected processed event")
	}
	_, ok := <-req.ACK
	require.False(t, ok)
}

func TestHandleFlushRequest_AgentChannelClosed(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	agentCh := make(chan *event.Event)
	close(agentCh)

	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     agentCh,
		processedEventCh: make(chan *event.Event, 1),
	}
	req := &flush.FlushRequest{ACK: make(chan struct{})}

	err := r.handleFlushRequest(context.Background(), loop, req)
	require.NoError(t, err)
	_, ok := <-req.ACK
	require.False(t, ok)
}

func TestHandleFlushRequest_ContextCancelled(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     nil,
		processedEventCh: make(chan *event.Event, 1),
	}
	req := &flush.FlushRequest{ACK: make(chan struct{})}

	err := r.handleFlushRequest(ctx, loop, req)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	_, ok := <-req.ACK
	require.False(t, ok)
}

func TestHandleFlushRequest_ProcessSingleAgentEventError(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx, cancel := context.WithCancel(context.Background())

	agentCh := make(chan *event.Event, 1)
	agentCh <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("err")}}}}
	close(agentCh)

	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     agentCh,
		processedEventCh: make(chan *event.Event),
	}
	req := &flush.FlushRequest{ACK: make(chan struct{})}

	time.AfterFunc(10*time.Millisecond, cancel)
	err := r.handleFlushRequest(ctx, loop, req)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	_, ok := <-req.ACK
	require.False(t, ok)
}

func TestRunEventLoop_FlushNilAndChannelClosed(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)

	flushCh := make(chan *flush.FlushRequest, 1)
	agentCh := make(chan *event.Event)
	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     agentCh,
		flushChan:        flushCh,
		processedEventCh: make(chan *event.Event, 1),
	}

	done := make(chan struct{})
	go func() {
		r.runEventLoop(context.Background(), loop)
		close(done)
	}()

	flushCh <- nil
	close(flushCh)
	time.Sleep(20 * time.Millisecond)
	close(agentCh)
	<-done
}

func TestRunEventLoop_HandleFlushRequestError(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "a"}, WithSessionService(sessioninmemory.NewSessionService())).(*runner)
	ctx, cancel := context.WithCancel(context.Background())

	agentCh := make(chan *event.Event, 1)
	agentCh <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("x")}}}}
	close(agentCh)

	loop := &eventLoopContext{
		sess:             session.NewSession("app", "u", "s"),
		invocation:       agent.NewInvocation(),
		agentEventCh:     agentCh,
		flushChan:        make(chan *flush.FlushRequest, 1),
		processedEventCh: make(chan *event.Event),
	}

	done := make(chan struct{})
	go func() {
		r.runEventLoop(ctx, loop)
		close(done)
	}()

	time.AfterFunc(20*time.Millisecond, cancel)
	loop.flushChan <- &flush.FlushRequest{ACK: make(chan struct{})}

	<-done
}

const runnerSurfaceSkillsOverviewHeader = "Available skills:"

type surfaceCapturedRequest struct {
	messages  []model.Message
	toolNames []string
}

type scriptedSurfaceModel struct {
	name      string
	responses []model.Message
	mu        sync.Mutex
	requests  []*surfaceCapturedRequest
}

func (m *scriptedSurfaceModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	callIndex := len(m.requests)
	m.requests = append(m.requests, cloneSurfaceCapturedRequest(req))
	response := model.NewAssistantMessage("")
	if len(m.responses) > 0 {
		if callIndex < len(m.responses) {
			response = cloneSurfaceMessage(m.responses[callIndex])
		} else {
			response = cloneSurfaceMessage(m.responses[len(m.responses)-1])
		}
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:        fmt.Sprintf("%s%s-%d", staticModelResponseIDPrefix, m.name, callIndex),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: response,
		}},
	}
	close(ch)
	return ch, nil
}

func (m *scriptedSurfaceModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *scriptedSurfaceModel) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *scriptedSurfaceModel) LatestRequest() *surfaceCapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return cloneSurfaceCapturedRequestValue(m.requests[len(m.requests)-1])
}

func (m *scriptedSurfaceModel) Requests() []*surfaceCapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*surfaceCapturedRequest, 0, len(m.requests))
	for _, req := range m.requests {
		out = append(out, cloneSurfaceCapturedRequestValue(req))
	}
	return out
}

type callCountingTool struct {
	name   string
	result string
	mu     sync.Mutex
	calls  int
}

func (t *callCountingTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: "Call counting tool.",
		InputSchema: &tool.Schema{
			Type: "object",
		},
		OutputSchema: &tool.Schema{
			Type: "string",
		},
	}
}

func (t *callCountingTool) Call(_ context.Context, _ []byte) (any, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return t.result, nil
}

func (t *callCountingTool) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesAllRootLLMSurfaces(
	t *testing.T,
) {
	staticModel := &scriptedSurfaceModel{
		name:      "root-static",
		responses: []model.Message{model.NewAssistantMessage("static root")},
	}
	patchedModel := &scriptedSurfaceModel{
		name:      "root-patched",
		responses: []model.Message{model.NewAssistantMessage("patched root")},
	}
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(staticModel),
		llmagent.WithInstruction("static instruction"),
		llmagent.WithGlobalInstruction("static global"),
		llmagent.WithTools([]tool.Tool{
			&callCountingTool{name: "old_tool", result: "old"},
		}),
	)
	snapshot := mustExportSnapshot(t, ag)
	repo := createRunnerTestSkillRepository(t)
	var patch agent.SurfacePatch
	patch.SetInstruction("patched instruction")
	patch.SetGlobalInstruction("patched global")
	patch.SetFewShot([][]model.Message{{
		model.NewUserMessage("few-shot user"),
		model.NewAssistantMessage("few-shot assistant"),
	}})
	patch.SetModel(patchedModel)
	patch.SetTools([]tool.Tool{
		&callCountingTool{name: "new_tool", result: "new"},
	})
	patch.SetSkillRepository(repo)
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-root",
		"session-root",
		model.NewUserMessage("actual user"),
		agent.WithSurfacePatchForNode(snapshot.EntryNodeID, patch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Zero(t, staticModel.RequestCount())
	require.Equal(t, 1, patchedModel.RequestCount())
	request := patchedModel.LatestRequest()
	require.NotNil(t, request)
	require.GreaterOrEqual(t, len(request.messages), 4)
	system := firstSystemMessageContent(request.messages)
	require.Contains(t, system, "patched instruction")
	require.Contains(t, system, "patched global")
	require.Contains(t, system, runnerSurfaceSkillsOverviewHeader)
	require.Contains(t, system, "echoer")
	require.NotContains(t, system, "static instruction")
	require.NotContains(t, system, "static global")
	require.Equal(t, "few-shot user", request.messages[1].Content)
	require.Equal(t, "few-shot assistant", request.messages[2].Content)
	require.Equal(t, "actual user", request.messages[3].Content)
	require.Contains(t, request.toolNames, "new_tool")
	require.Contains(t, request.toolNames, "skill_load")
	require.NotContains(t, request.toolNames, "old_tool")
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesDeepNestedWorkflowPatches(
	t *testing.T,
) {
	startModel := &scriptedSurfaceModel{
		name:      "workflow-start",
		responses: []model.Message{model.NewAssistantMessage("start")},
	}
	plannerStatic := &scriptedSurfaceModel{
		name:      "workflow-planner-static",
		responses: []model.Message{model.NewAssistantMessage("planner static")},
	}
	plannerPatched := &scriptedSurfaceModel{
		name:      "workflow-planner-patched",
		responses: []model.Message{model.NewAssistantMessage("planner patched")},
	}
	workerStatic := &scriptedSurfaceModel{
		name:      "workflow-worker-static",
		responses: []model.Message{model.NewAssistantMessage("worker static")},
	}
	workerPatched := &scriptedSurfaceModel{
		name: "workflow-worker-patched",
		responses: []model.Message{
			model.NewAssistantMessage("worker patched first"),
			model.NewAssistantMessage("worker patched second"),
		},
	}
	endModel := &scriptedSurfaceModel{
		name:      "workflow-end",
		responses: []model.Message{model.NewAssistantMessage("workflow end")},
	}
	worker := llmagent.New(
		"worker",
		llmagent.WithModel(workerStatic),
		llmagent.WithInstruction("worker static instruction"),
	)
	cycle := cycleagent.New(
		"cycle",
		cycleagent.WithMaxIterations(2),
		cycleagent.WithSubAgents([]agent.Agent{worker}),
	)
	planner := llmagent.New(
		"planner",
		llmagent.WithModel(plannerStatic),
		llmagent.WithInstruction("planner static instruction"),
	)
	fanout := parallelagent.New(
		"fanout",
		parallelagent.WithSubAgents([]agent.Agent{planner, cycle}),
	)
	workflow := chainagent.New(
		"workflow",
		chainagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"start",
				llmagent.WithModel(startModel),
				llmagent.WithInstruction("start static instruction"),
			),
			fanout,
			llmagent.New("end", llmagent.WithModel(endModel)),
		}),
	)
	snapshot := mustExportSnapshot(t, workflow)
	startNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"start",
		structure.NodeKindLLM,
	)
	plannerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"planner",
		structure.NodeKindLLM,
	)
	workerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"worker",
		structure.NodeKindLLM,
	)
	var startPatch agent.SurfacePatch
	startPatch.SetInstruction("start patched instruction")
	var plannerPatch agent.SurfacePatch
	plannerPatch.SetInstruction("planner patched instruction")
	plannerPatch.SetModel(plannerPatched)
	var workerPatch agent.SurfacePatch
	workerPatch.SetInstruction("worker patched instruction")
	workerPatch.SetModel(workerPatched)
	r := NewRunner(
		"app",
		workflow,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-workflow",
		"session-workflow",
		model.NewUserMessage("run workflow"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(startNodeID, startPatch),
		agent.WithSurfacePatchForNode(plannerNodeID, plannerPatch),
		agent.WithSurfacePatchForNode(workerNodeID, workerPatch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Equal(t, 1, startModel.RequestCount())
	require.Contains(
		t,
		firstSystemMessageContent(startModel.LatestRequest().messages),
		"start patched instruction",
	)
	require.Zero(t, plannerStatic.RequestCount())
	require.Equal(t, 1, plannerPatched.RequestCount())
	require.Contains(
		t,
		firstSystemMessageContent(plannerPatched.LatestRequest().messages),
		"planner patched instruction",
	)
	require.Zero(t, workerStatic.RequestCount())
	require.Equal(t, 2, workerPatched.RequestCount())
	for _, request := range workerPatched.Requests() {
		require.Contains(
			t,
			firstSystemMessageContent(request.messages),
			"worker patched instruction",
		)
	}
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := countTraceStepsByNodeID(completion.ExecutionTrace.Steps)
	require.Equal(t, 1, traceCounts[startNodeID])
	require.Equal(t, 1, traceCounts[plannerNodeID])
	require.Equal(t, 2, traceCounts[workerNodeID])
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesDirectChainChildPatch(
	t *testing.T,
) {
	plannerStaticRepo := createNamedRunnerTestSkillRepository(
		t,
		"planner-static-skill",
	)
	plannerPatchedRepo := createNamedRunnerTestSkillRepository(
		t,
		"planner-patched-skill",
	)
	plannerStatic := &scriptedSurfaceModel{
		name:      "chain-planner-static",
		responses: []model.Message{model.NewAssistantMessage("planner static")},
	}
	plannerPatched := &scriptedSurfaceModel{
		name:      "chain-planner-patched",
		responses: []model.Message{model.NewAssistantMessage("planner patched")},
	}
	writerStatic := &scriptedSurfaceModel{
		name:      "chain-writer-static",
		responses: []model.Message{model.NewAssistantMessage("writer static")},
	}
	workflow := chainagent.New(
		"workflow",
		chainagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"planner",
				llmagent.WithModel(plannerStatic),
				llmagent.WithInstruction("planner static instruction"),
				llmagent.WithGlobalInstruction("planner static global"),
				llmagent.WithTools([]tool.Tool{
					&callCountingTool{
						name:   "planner_old_tool",
						result: "planner old",
					},
				}),
				llmagent.WithSkills(plannerStaticRepo),
			),
			llmagent.New(
				"writer",
				llmagent.WithModel(writerStatic),
				llmagent.WithInstruction("writer static instruction"),
				llmagent.WithGlobalInstruction("writer static global"),
				llmagent.WithTools([]tool.Tool{
					&callCountingTool{
						name:   "writer_old_tool",
						result: "writer old",
					},
				}),
			),
		}),
	)
	snapshot := mustExportSnapshot(t, workflow)
	plannerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"planner",
		structure.NodeKindLLM,
	)
	writerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"writer",
		structure.NodeKindLLM,
	)
	var plannerPatch agent.SurfacePatch
	plannerPatch.SetInstruction("planner patched instruction")
	plannerPatch.SetGlobalInstruction("planner patched global")
	plannerPatch.SetFewShot([][]model.Message{{
		model.NewUserMessage("planner few-shot user"),
		model.NewAssistantMessage("planner few-shot assistant"),
	}})
	plannerPatch.SetModel(plannerPatched)
	plannerPatch.SetTools([]tool.Tool{
		&callCountingTool{
			name:   "planner_new_tool",
			result: "planner new",
		},
	})
	plannerPatch.SetSkillRepository(plannerPatchedRepo)
	r := NewRunner(
		"app",
		workflow,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-chain",
		"session-chain",
		model.NewUserMessage("run chain"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(plannerNodeID, plannerPatch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Zero(t, plannerStatic.RequestCount())
	require.Equal(t, 1, plannerPatched.RequestCount())
	plannerRequest := plannerPatched.LatestRequest()
	require.NotNil(t, plannerRequest)
	require.GreaterOrEqual(t, len(plannerRequest.messages), 4)
	plannerSystem := firstSystemMessageContent(plannerRequest.messages)
	require.Contains(
		t,
		plannerSystem,
		"planner patched instruction",
	)
	require.Contains(t, plannerSystem, "planner patched global")
	require.Contains(t, plannerSystem, runnerSurfaceSkillsOverviewHeader)
	require.Contains(t, plannerSystem, "planner-patched-skill")
	require.NotContains(t, plannerSystem, "planner static instruction")
	require.NotContains(t, plannerSystem, "planner static global")
	require.NotContains(t, plannerSystem, "planner-static-skill")
	require.Equal(t, "planner few-shot user", plannerRequest.messages[1].Content)
	require.Equal(
		t,
		"planner few-shot assistant",
		plannerRequest.messages[2].Content,
	)
	require.Equal(t, "run chain", plannerRequest.messages[3].Content)
	require.Contains(t, plannerRequest.toolNames, "planner_new_tool")
	require.Contains(t, plannerRequest.toolNames, "skill_load")
	require.NotContains(t, plannerRequest.toolNames, "planner_old_tool")
	require.Equal(t, 1, writerStatic.RequestCount())
	writerRequest := writerStatic.LatestRequest()
	require.NotNil(t, writerRequest)
	writerSystem := firstSystemMessageContent(writerRequest.messages)
	require.Contains(
		t,
		writerSystem,
		"writer static instruction",
	)
	require.Contains(t, writerSystem, "writer static global")
	require.NotContains(t, writerSystem, "planner patched instruction")
	require.NotContains(t, writerSystem, "planner patched global")
	require.NotContains(t, writerSystem, runnerSurfaceSkillsOverviewHeader)
	require.Contains(t, writerRequest.toolNames, "writer_old_tool")
	require.NotContains(t, writerRequest.toolNames, "planner_new_tool")
	require.NotContains(t, writerRequest.toolNames, "skill_load")
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := countTraceStepsByNodeID(completion.ExecutionTrace.Steps)
	require.Equal(t, 1, traceCounts[plannerNodeID])
	require.Equal(t, 1, traceCounts[writerNodeID])
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesDirectParallelBranchPatches(
	t *testing.T,
) {
	researcherStaticRepo := createNamedRunnerTestSkillRepository(
		t,
		"researcher-static-skill",
	)
	researcherPatchedRepo := createNamedRunnerTestSkillRepository(
		t,
		"researcher-patched-skill",
	)
	reviewerStaticRepo := createNamedRunnerTestSkillRepository(
		t,
		"reviewer-static-skill",
	)
	researcherStatic := &scriptedSurfaceModel{
		name:      "parallel-researcher-static",
		responses: []model.Message{model.NewAssistantMessage("researcher static")},
	}
	researcherPatched := &scriptedSurfaceModel{
		name:      "parallel-researcher-patched",
		responses: []model.Message{model.NewAssistantMessage("researcher patched")},
	}
	reviewerStatic := &scriptedSurfaceModel{
		name:      "parallel-reviewer-static",
		responses: []model.Message{model.NewAssistantMessage("reviewer static")},
	}
	reviewerPatched := &scriptedSurfaceModel{
		name:      "parallel-reviewer-patched",
		responses: []model.Message{model.NewAssistantMessage("reviewer patched")},
	}
	fanout := parallelagent.New(
		"fanout",
		parallelagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"researcher",
				llmagent.WithModel(researcherStatic),
				llmagent.WithInstruction("researcher static instruction"),
				llmagent.WithGlobalInstruction("researcher static global"),
				llmagent.WithTools([]tool.Tool{
					&callCountingTool{
						name:   "researcher_old_tool",
						result: "researcher old",
					},
				}),
				llmagent.WithSkills(researcherStaticRepo),
			),
			llmagent.New(
				"reviewer",
				llmagent.WithModel(reviewerStatic),
				llmagent.WithInstruction("reviewer static instruction"),
				llmagent.WithGlobalInstruction("reviewer static global"),
				llmagent.WithTools([]tool.Tool{
					&callCountingTool{
						name:   "reviewer_old_tool",
						result: "reviewer old",
					},
				}),
				llmagent.WithSkills(reviewerStaticRepo),
			),
		}),
	)
	snapshot := mustExportSnapshot(t, fanout)
	researcherNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"researcher",
		structure.NodeKindLLM,
	)
	reviewerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"reviewer",
		structure.NodeKindLLM,
	)
	var researcherPatch agent.SurfacePatch
	researcherPatch.SetInstruction("researcher patched instruction")
	researcherPatch.SetGlobalInstruction("researcher patched global")
	researcherPatch.SetFewShot([][]model.Message{{
		model.NewUserMessage("researcher few-shot user"),
		model.NewAssistantMessage("researcher few-shot assistant"),
	}})
	researcherPatch.SetModel(researcherPatched)
	researcherPatch.SetTools([]tool.Tool{
		&callCountingTool{
			name:   "researcher_new_tool",
			result: "researcher new",
		},
	})
	researcherPatch.SetSkillRepository(researcherPatchedRepo)
	var reviewerPatch agent.SurfacePatch
	reviewerPatch.SetInstruction("reviewer patched instruction")
	reviewerPatch.SetGlobalInstruction("reviewer patched global")
	reviewerPatch.SetFewShot([][]model.Message{{
		model.NewUserMessage("reviewer few-shot user"),
		model.NewAssistantMessage("reviewer few-shot assistant"),
	}})
	reviewerPatch.SetModel(reviewerPatched)
	reviewerPatch.SetTools([]tool.Tool{
		&callCountingTool{
			name:   "reviewer_new_tool",
			result: "reviewer new",
		},
	})
	reviewerPatch.SetSkillRepository(nil)
	r := NewRunner(
		"app",
		fanout,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-parallel",
		"session-parallel",
		model.NewUserMessage("run parallel"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(researcherNodeID, researcherPatch),
		agent.WithSurfacePatchForNode(reviewerNodeID, reviewerPatch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Zero(t, researcherStatic.RequestCount())
	require.Zero(t, reviewerStatic.RequestCount())
	require.Equal(t, 1, researcherPatched.RequestCount())
	require.Equal(t, 1, reviewerPatched.RequestCount())
	researcherRequest := researcherPatched.LatestRequest()
	require.NotNil(t, researcherRequest)
	researcherSystem := firstSystemMessageContent(researcherRequest.messages)
	require.Contains(
		t,
		researcherSystem,
		"researcher patched instruction",
	)
	require.Contains(t, researcherSystem, "researcher patched global")
	require.Contains(t, researcherSystem, runnerSurfaceSkillsOverviewHeader)
	require.Contains(t, researcherSystem, "researcher-patched-skill")
	require.NotContains(t, researcherSystem, "researcher static instruction")
	require.NotContains(t, researcherSystem, "researcher static global")
	require.NotContains(t, researcherSystem, "reviewer patched instruction")
	require.NotContains(t, researcherSystem, "reviewer patched global")
	require.Equal(t, "researcher few-shot user", researcherRequest.messages[1].Content)
	require.Equal(
		t,
		"researcher few-shot assistant",
		researcherRequest.messages[2].Content,
	)
	require.Equal(t, "run parallel", researcherRequest.messages[3].Content)
	require.Contains(t, researcherRequest.toolNames, "researcher_new_tool")
	require.Contains(t, researcherRequest.toolNames, "skill_load")
	require.NotContains(t, researcherRequest.toolNames, "researcher_old_tool")
	require.NotContains(t, researcherRequest.toolNames, "reviewer_new_tool")
	reviewerRequest := reviewerPatched.LatestRequest()
	require.NotNil(t, reviewerRequest)
	reviewerSystem := firstSystemMessageContent(reviewerRequest.messages)
	require.Contains(
		t,
		reviewerSystem,
		"reviewer patched instruction",
	)
	require.Contains(t, reviewerSystem, "reviewer patched global")
	require.NotContains(t, reviewerSystem, "reviewer static instruction")
	require.NotContains(t, reviewerSystem, "reviewer static global")
	require.NotContains(t, reviewerSystem, "reviewer-static-skill")
	require.NotContains(t, reviewerSystem, runnerSurfaceSkillsOverviewHeader)
	require.NotContains(t, reviewerSystem, "researcher patched instruction")
	require.NotContains(t, reviewerSystem, "researcher patched global")
	require.Equal(t, "reviewer few-shot user", reviewerRequest.messages[1].Content)
	require.Equal(
		t,
		"reviewer few-shot assistant",
		reviewerRequest.messages[2].Content,
	)
	require.Equal(t, "run parallel", reviewerRequest.messages[3].Content)
	require.Contains(t, reviewerRequest.toolNames, "reviewer_new_tool")
	require.NotContains(t, reviewerRequest.toolNames, "reviewer_old_tool")
	require.NotContains(t, reviewerRequest.toolNames, "researcher_new_tool")
	require.NotContains(t, reviewerRequest.toolNames, "skill_load")
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := countTraceStepsByNodeID(completion.ExecutionTrace.Steps)
	require.Equal(t, 1, traceCounts[researcherNodeID])
	require.Equal(t, 1, traceCounts[reviewerNodeID])
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesDirectCycleChildPatch(
	t *testing.T,
) {
	workerStaticRepo := createNamedRunnerTestSkillRepository(
		t,
		"cycle-static-skill",
	)
	workerPatchedRepo := createNamedRunnerTestSkillRepository(
		t,
		"cycle-patched-skill",
	)
	workerStatic := &scriptedSurfaceModel{
		name:      "cycle-worker-static",
		responses: []model.Message{model.NewAssistantMessage("worker static")},
	}
	workerPatched := &scriptedSurfaceModel{
		name: "cycle-worker-patched",
		responses: []model.Message{
			model.NewAssistantMessage("worker patched first"),
			model.NewAssistantMessage("worker patched second"),
		},
	}
	loop := cycleagent.New(
		"loop",
		cycleagent.WithMaxIterations(2),
		cycleagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"worker",
				llmagent.WithModel(workerStatic),
				llmagent.WithInstruction("worker static instruction"),
				llmagent.WithGlobalInstruction("worker static global"),
				llmagent.WithTools([]tool.Tool{
					&callCountingTool{
						name:   "cycle_old_tool",
						result: "cycle old",
					},
				}),
				llmagent.WithSkills(workerStaticRepo),
			),
		}),
	)
	snapshot := mustExportSnapshot(t, loop)
	workerNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"worker",
		structure.NodeKindLLM,
	)
	var workerPatch agent.SurfacePatch
	workerPatch.SetInstruction("worker patched instruction")
	workerPatch.SetGlobalInstruction("worker patched global")
	workerPatch.SetFewShot([][]model.Message{{
		model.NewUserMessage("cycle few-shot user"),
		model.NewAssistantMessage("cycle few-shot assistant"),
	}})
	workerPatch.SetModel(workerPatched)
	workerPatch.SetTools([]tool.Tool{
		&callCountingTool{
			name:   "cycle_new_tool",
			result: "cycle new",
		},
	})
	workerPatch.SetSkillRepository(workerPatchedRepo)
	r := NewRunner(
		"app",
		loop,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-cycle",
		"session-cycle",
		model.NewUserMessage("run cycle"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(workerNodeID, workerPatch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Zero(t, workerStatic.RequestCount())
	require.Equal(t, 2, workerPatched.RequestCount())
	for _, request := range workerPatched.Requests() {
		system := firstSystemMessageContent(request.messages)
		require.Contains(
			t,
			system,
			"worker patched instruction",
		)
		require.Contains(t, system, "worker patched global")
		require.Contains(t, system, runnerSurfaceSkillsOverviewHeader)
		require.Contains(t, system, "cycle-patched-skill")
		require.NotContains(t, system, "worker static instruction")
		require.NotContains(t, system, "worker static global")
		require.NotContains(t, system, "cycle-static-skill")
		contents := surfaceMessageContents(request.messages)
		require.Contains(t, contents, "cycle few-shot user")
		require.Contains(t, contents, "cycle few-shot assistant")
		require.Equal(t, 1, countStringValues(contents, "cycle few-shot user"))
		require.Equal(
			t,
			1,
			countStringValues(contents, "cycle few-shot assistant"),
		)
		require.Contains(t, request.toolNames, "cycle_new_tool")
		require.Contains(t, request.toolNames, "skill_load")
		require.NotContains(t, request.toolNames, "cycle_old_tool")
	}
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := countTraceStepsByNodeID(completion.ExecutionTrace.Steps)
	require.Equal(t, 2, traceCounts[workerNodeID])
}

func TestRunner_Run_WithSurfacePatchForNode_IgnoresUnknownDirectShapeNodeID(
	t *testing.T,
) {
	t.Run("chain", func(t *testing.T) {
		staticModel := &scriptedSurfaceModel{
			name:      "chain-fallback-static",
			responses: []model.Message{model.NewAssistantMessage("chain fallback")},
		}
		patchedModel := &scriptedSurfaceModel{
			name:      "chain-fallback-patched",
			responses: []model.Message{model.NewAssistantMessage("should not run")},
		}
		workflow := chainagent.New(
			"workflow",
			chainagent.WithSubAgents([]agent.Agent{
				llmagent.New(
					"planner",
					llmagent.WithModel(staticModel),
					llmagent.WithInstruction("chain fallback static instruction"),
				),
			}),
		)
		var patch agent.SurfacePatch
		patch.SetInstruction("chain fallback patched instruction")
		patch.SetModel(patchedModel)
		r := NewRunner(
			"app",
			workflow,
			WithSessionService(sessioninmemory.NewSessionService()),
		)
		eventCh, err := r.Run(
			context.Background(),
			"user-chain-fallback",
			"session-chain-fallback",
			model.NewUserMessage("chain fallback"),
			agent.WithSurfacePatchForNode("workflow/missing", patch),
		)
		require.NoError(t, err)
		completion := collectRunnerCompletionEvent(t, eventCh)
		require.NotNil(t, completion.Response)
		require.Equal(t, 1, staticModel.RequestCount())
		require.Zero(t, patchedModel.RequestCount())
		require.Contains(
			t,
			firstSystemMessageContent(staticModel.LatestRequest().messages),
			"chain fallback static instruction",
		)
	})
	t.Run("parallel", func(t *testing.T) {
		leftStatic := &scriptedSurfaceModel{
			name:      "parallel-fallback-left-static",
			responses: []model.Message{model.NewAssistantMessage("left fallback")},
		}
		rightStatic := &scriptedSurfaceModel{
			name:      "parallel-fallback-right-static",
			responses: []model.Message{model.NewAssistantMessage("right fallback")},
		}
		patchedModel := &scriptedSurfaceModel{
			name:      "parallel-fallback-patched",
			responses: []model.Message{model.NewAssistantMessage("should not run")},
		}
		fanout := parallelagent.New(
			"fanout",
			parallelagent.WithSubAgents([]agent.Agent{
				llmagent.New(
					"left",
					llmagent.WithModel(leftStatic),
					llmagent.WithInstruction("left static instruction"),
				),
				llmagent.New(
					"right",
					llmagent.WithModel(rightStatic),
					llmagent.WithInstruction("right static instruction"),
				),
			}),
		)
		var patch agent.SurfacePatch
		patch.SetInstruction("parallel fallback patched instruction")
		patch.SetModel(patchedModel)
		r := NewRunner(
			"app",
			fanout,
			WithSessionService(sessioninmemory.NewSessionService()),
		)
		eventCh, err := r.Run(
			context.Background(),
			"user-parallel-fallback",
			"session-parallel-fallback",
			model.NewUserMessage("parallel fallback"),
			agent.WithSurfacePatchForNode("fanout/missing", patch),
		)
		require.NoError(t, err)
		completion := collectRunnerCompletionEvent(t, eventCh)
		require.NotNil(t, completion.Response)
		require.Equal(t, 1, leftStatic.RequestCount())
		require.Equal(t, 1, rightStatic.RequestCount())
		require.Zero(t, patchedModel.RequestCount())
		require.Contains(
			t,
			firstSystemMessageContent(leftStatic.LatestRequest().messages),
			"left static instruction",
		)
		require.Contains(
			t,
			firstSystemMessageContent(rightStatic.LatestRequest().messages),
			"right static instruction",
		)
	})
	t.Run("cycle", func(t *testing.T) {
		staticModel := &scriptedSurfaceModel{
			name:      "cycle-fallback-static",
			responses: []model.Message{model.NewAssistantMessage("cycle fallback")},
		}
		patchedModel := &scriptedSurfaceModel{
			name:      "cycle-fallback-patched",
			responses: []model.Message{model.NewAssistantMessage("should not run")},
		}
		loop := cycleagent.New(
			"loop",
			cycleagent.WithMaxIterations(2),
			cycleagent.WithSubAgents([]agent.Agent{
				llmagent.New(
					"worker",
					llmagent.WithModel(staticModel),
					llmagent.WithInstruction("cycle fallback static instruction"),
				),
			}),
		)
		var patch agent.SurfacePatch
		patch.SetInstruction("cycle fallback patched instruction")
		patch.SetModel(patchedModel)
		r := NewRunner(
			"app",
			loop,
			WithSessionService(sessioninmemory.NewSessionService()),
		)
		eventCh, err := r.Run(
			context.Background(),
			"user-cycle-fallback",
			"session-cycle-fallback",
			model.NewUserMessage("cycle fallback"),
			agent.WithSurfacePatchForNode("loop/missing", patch),
		)
		require.NoError(t, err)
		completion := collectRunnerCompletionEvent(t, eventCh)
		require.NotNil(t, completion.Response)
		require.Equal(t, 2, staticModel.RequestCount())
		require.Zero(t, patchedModel.RequestCount())
		for _, request := range staticModel.Requests() {
			require.Contains(
				t,
				firstSystemMessageContent(request.messages),
				"cycle fallback static instruction",
			)
		}
	})
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesComplexGraphPatches(
	t *testing.T,
) {
	oldTool := &callCountingTool{name: "old_graph_tool", result: "old graph tool"}
	patchedTool := &callCountingTool{name: "patched_graph_tool", result: "patched graph tool"}
	staticGraphModel := &scriptedSurfaceModel{
		name:      "graph-static",
		responses: []model.Message{model.NewAssistantMessage("graph static")},
	}
	patchedGraphModel := &scriptedSurfaceModel{
		name: "graph-patched",
		responses: []model.Message{
			toolCallAssistantMessage("patched_graph_tool", `{}`),
			model.NewAssistantMessage("branch-a"),
		},
	}
	schema := graph.MessagesStateSchema().
		AddField("visited", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})
	builder := graph.NewStateGraph(schema)
	builder.AddNode("start", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"start"}}, nil
	})
	builder.AddNode("prepare", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"prepare"}}, nil
	})
	builder.AddLLMNode(
		"llm",
		staticGraphModel,
		"graph static instruction",
		map[string]tool.Tool{"old_graph_tool": oldTool},
	)
	builder.AddToolsNode("tools", map[string]tool.Tool{
		"old_graph_tool": oldTool,
	})
	builder.AddNode("branch_a", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_a"}}, nil
	})
	builder.AddNode("branch_b", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_b"}}, nil
	})
	builder.AddNode("join", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"join"}}, nil
	})
	builder.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"done"}}, nil
	})
	builder.SetEntryPoint("start")
	builder.AddEdge("start", "llm")
	builder.AddEdge("start", "prepare")
	builder.AddToolsConditionalEdges("llm", "tools", "branch_a")
	builder.AddEdge("tools", "llm")
	builder.AddEdge("prepare", "branch_b")
	builder.AddJoinEdge([]string{"branch_a", "branch_b"}, "join")
	builder.AddConditionalEdges("join", func(context.Context, graph.State) (string, error) {
		return "done", nil
	}, map[string]string{"done": "done"})
	builder.SetFinishPoint("done")
	compiled := builder.MustCompile()
	ag, err := graphagent.New(
		"assistant",
		compiled,
		graphagent.WithMaxConcurrency(1),
	)
	require.NoError(t, err)
	snapshot := mustExportSnapshot(t, ag)
	llmNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"llm",
		structure.NodeKindLLM,
	)
	toolsNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"tools",
		structure.NodeKindTool,
	)
	var llmPatch agent.SurfacePatch
	llmPatch.SetInstruction("graph patched instruction")
	llmPatch.SetFewShot([][]model.Message{{
		model.NewUserMessage("graph few-shot user"),
		model.NewAssistantMessage("graph few-shot assistant"),
	}})
	llmPatch.SetModel(patchedGraphModel)
	llmPatch.SetTools([]tool.Tool{patchedTool})
	var toolsPatch agent.SurfacePatch
	toolsPatch.SetTools([]tool.Tool{patchedTool})
	r := NewRunner(
		"app",
		ag,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-graph",
		"session-graph",
		model.NewUserMessage("graph input"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(llmNodeID, llmPatch),
		agent.WithSurfacePatchForNode(toolsNodeID, toolsPatch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.Zero(t, staticGraphModel.RequestCount())
	require.Equal(t, 2, patchedGraphModel.RequestCount())
	firstRequest := patchedGraphModel.Requests()[0]
	require.GreaterOrEqual(t, len(firstRequest.messages), 4)
	require.Contains(
		t,
		firstSystemMessageContent(firstRequest.messages),
		"graph patched instruction",
	)
	require.Equal(t, "graph few-shot user", firstRequest.messages[1].Content)
	require.Equal(t, "graph few-shot assistant", firstRequest.messages[2].Content)
	require.Contains(t, firstRequest.toolNames, "patched_graph_tool")
	require.NotContains(t, firstRequest.toolNames, "old_graph_tool")
	require.Equal(t, 1, patchedTool.Calls())
	require.Zero(t, oldTool.Calls())
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := countTraceStepsByNodeID(completion.ExecutionTrace.Steps)
	require.Equal(t, 2, traceCounts[llmNodeID])
	require.Equal(t, 1, traceCounts[toolsNodeID])
	require.Equal(t, 1, traceCounts["assistant/start"])
	require.Equal(t, 1, traceCounts["assistant/prepare"])
	require.Equal(t, 1, traceCounts["assistant/branch_a"])
	require.Equal(t, 1, traceCounts["assistant/branch_b"])
	require.Equal(t, 1, traceCounts["assistant/join"])
	require.Equal(t, 1, traceCounts["assistant/done"])
}

func TestRunner_Run_WithSurfacePatchForNode_AppliesGraphChildAgentPatch(
	t *testing.T,
) {
	childStatic := &scriptedSurfaceModel{
		name:      "graph-child-static",
		responses: []model.Message{model.NewAssistantMessage("child static")},
	}
	childPatched := &scriptedSurfaceModel{
		name:      "graph-child-patched",
		responses: []model.Message{model.NewAssistantMessage("child patched")},
	}
	child := llmagent.New(
		"researcher",
		llmagent.WithModel(childStatic),
		llmagent.WithInstruction("child static instruction"),
	)
	builder := graph.NewStateGraph(graph.MessagesStateSchema())
	builder.AddAgentNode("researcher")
	builder.SetEntryPoint("researcher")
	builder.SetFinishPoint("researcher")
	compiled := builder.MustCompile()
	parent, err := graphagent.New(
		"assistant",
		compiled,
		graphagent.WithSubAgents([]agent.Agent{child}),
	)
	require.NoError(t, err)
	snapshot := mustExportSnapshot(t, parent)
	childNodeID := requireNodeIDByNameAndKind(
		t,
		snapshot,
		"researcher",
		structure.NodeKindLLM,
	)
	var patch agent.SurfacePatch
	patch.SetInstruction("child patched instruction")
	patch.SetModel(childPatched)
	r := NewRunner(
		"app",
		parent,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-graph-child",
		"session-graph-child",
		model.NewUserMessage("graph child input"),
		agent.WithSurfacePatchForNode(childNodeID, patch),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Zero(t, childStatic.RequestCount())
	require.Equal(t, 1, childPatched.RequestCount())
	require.Contains(
		t,
		firstSystemMessageContent(childPatched.LatestRequest().messages),
		"child patched instruction",
	)
}

func TestRunner_GraphChildAgentNode_PersistedRunnerCompletionDoesNotReplayIntoNextTurnHistory(
	t *testing.T,
) {
	const (
		appName    = "app"
		userID     = "u"
		sessionID  = "session-graph-child-replay"
		firstReply = "child first"
	)

	childModel := &scriptedSurfaceModel{
		name: "graph-child-history",
		responses: []model.Message{
			model.NewAssistantMessage(firstReply),
			model.NewAssistantMessage("child second"),
		},
	}
	child := llmagent.New("researcher", llmagent.WithModel(childModel))

	builder := graph.NewStateGraph(graph.MessagesStateSchema())
	builder.AddAgentNode("researcher")
	builder.SetEntryPoint("researcher")
	builder.SetFinishPoint("researcher")
	parent, err := graphagent.New(
		"assistant",
		builder.MustCompile(),
		graphagent.WithSubAgents([]agent.Agent{child}),
	)
	require.NoError(t, err)

	svc := sessioninmemory.NewSessionService()
	r := NewRunner(appName, parent, WithSessionService(svc))

	firstTurn, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	firstCompletion := collectRunnerCompletionEvent(t, firstTurn)
	require.NotNil(t, firstCompletion.Response)
	require.Len(t, firstCompletion.Response.Choices, 1)
	require.Equal(t, firstReply, firstCompletion.Response.Choices[0].Message.Content)
	assertSessionKeepsSingleFinalAssistantEvent(t, svc, sessionID, firstReply)

	secondTurn, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("next"),
	)
	require.NoError(t, err)
	_ = collectRunnerCompletionEvent(t, secondTurn)

	requests := childModel.Requests()
	require.Len(t, requests, 2)
	require.Equal(
		t,
		[]string{"user:hello", "assistant:" + firstReply, "user:next"},
		surfaceRoleContentSummaries(requests[1].messages),
	)
}

func cloneSurfaceCapturedRequest(req *model.Request) *surfaceCapturedRequest {
	if req == nil {
		return nil
	}
	return &surfaceCapturedRequest{
		messages:  append([]model.Message(nil), req.Messages...),
		toolNames: surfaceToolNames(req.Tools),
	}
}

func cloneSurfaceCapturedRequestValue(
	req *surfaceCapturedRequest,
) *surfaceCapturedRequest {
	if req == nil {
		return nil
	}
	return &surfaceCapturedRequest{
		messages:  append([]model.Message(nil), req.messages...),
		toolNames: append([]string(nil), req.toolNames...),
	}
}

func cloneSurfaceMessage(message model.Message) model.Message {
	cloned := message
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	}
	if len(message.ContentParts) > 0 {
		cloned.ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	}
	return cloned
}

func surfaceToolNames(tools map[string]tool.Tool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func toolCallAssistantMessage(name string, args string) model.Message {
	return model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   name + "-call",
			Function: model.FunctionDefinitionParam{
				Name:      name,
				Arguments: []byte(args),
			},
		}},
	}
}

func mustExportSnapshot(t *testing.T, ag agent.Agent) *structure.Snapshot {
	t.Helper()
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	return snapshot
}

func requireNodeIDByNameAndKind(
	t *testing.T,
	snapshot *structure.Snapshot,
	name string,
	kind structure.NodeKind,
) string {
	t.Helper()
	var matches []string
	for _, node := range snapshot.Nodes {
		if node.Name == name && node.Kind == kind {
			matches = append(matches, node.NodeID)
		}
	}
	require.Len(t, matches, 1)
	return matches[0]
}

func countTraceStepsByNodeID(steps []atrace.Step) map[string]int {
	counts := make(map[string]int, len(steps))
	for _, step := range steps {
		counts[step.NodeID]++
	}
	return counts
}

func surfaceMessageContents(messages []model.Message) []string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Content != "" {
			contents = append(contents, message.Content)
		}
	}
	return contents
}

func surfaceRoleContentSummaries(messages []model.Message) []string {
	summaries := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Content == "" {
			continue
		}
		summaries = append(summaries, string(message.Role)+":"+message.Content)
	}
	return summaries
}

func countStringValues(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func createRunnerTestSkillRepository(t *testing.T) skill.Repository {
	return createNamedRunnerTestSkillRepository(t, "echoer")
}

func createNamedRunnerTestSkillRepository(
	t *testing.T,
	name string,
) skill.Repository {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(skillDir, "SKILL.md"),
			[]byte(
				fmt.Sprintf(
					"---\nname: %s\ndescription: runner test skill\n---\nbody\n",
					name,
				),
			),
			0o644,
		),
	)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	return repo
}

// TestRunner_WithAppName_OverridesSessionKey verifies that agent.WithAppName
// overrides the runner's default app name for session isolation.
func TestRunner_WithAppName_OverridesSessionKey(t *testing.T) {
	const (
		defaultAppName  = "default-app"
		overrideAppName = "project-alpha"
		userID          = "user-1"
		sessionID       = "session-1"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &mockAgent{name: "app-name-agent"}
	r := NewRunner(defaultAppName, ag, WithSessionService(sessionService))

	// Run with overridden app name.
	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello"),
		agent.WithAppName(overrideAppName),
	)
	require.NoError(t, err)
	for range ch {
	}

	// The session should exist under the overridden app name.
	overrideSess, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: overrideAppName, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	require.NotNil(t, overrideSess, "session should exist under overridden app name")

	// The session should NOT exist under the default app name.
	defaultSess, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: defaultAppName, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	assert.Nil(t, defaultSess, "session should not exist under default app name")
}

// TestRunner_WithAppName_FallbackToDefault verifies that when no AppName
// override is provided, the runner uses its default app name.
func TestRunner_WithAppName_FallbackToDefault(t *testing.T) {
	const (
		defaultAppName = "fallback-app"
		userID         = "user-1"
		sessionID      = "session-fallback"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &mockAgent{name: "fallback-agent"}
	r := NewRunner(defaultAppName, ag, WithSessionService(sessionService))

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	for range ch {
	}

	// The session should exist under the default app name.
	sess, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: defaultAppName, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	require.NotNil(t, sess, "session should exist under default app name")
}

// TestRunner_WithAppName_IsolatesDifferentProjects verifies that two runs
// with different AppName overrides create isolated sessions.
func TestRunner_WithAppName_IsolatesDifferentProjects(t *testing.T) {
	const (
		defaultAppName = "shared-runner"
		projectA       = "project-a"
		projectB       = "project-b"
		userID         = "user-1"
		sessionID      = "shared-session"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &mockAgent{name: "isolation-agent"}
	r := NewRunner(defaultAppName, ag, WithSessionService(sessionService))

	// Run for project A.
	chA, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello from A"),
		agent.WithAppName(projectA),
	)
	require.NoError(t, err)
	for range chA {
	}

	// Run for project B.
	chB, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello from B"),
		agent.WithAppName(projectB),
	)
	require.NoError(t, err)
	for range chB {
	}

	// Both sessions should exist under their respective app names.
	sessA, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: projectA, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	require.NotNil(t, sessA, "project A session should exist")

	sessB, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: projectB, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	require.NotNil(t, sessB, "project B session should exist")

	// Default app name should have no session.
	sessDefault, err := sessionService.GetSession(
		context.Background(),
		session.Key{AppName: defaultAppName, UserID: userID, SessionID: sessionID},
	)
	require.NoError(t, err)
	assert.Nil(t, sessDefault, "default app name should have no session")
}

// TestRunner_WithAppName_CompletionEventAuthor verifies that the runner
// completion event uses the overridden app name as its Author, not the
// runner's default app name.
func TestRunner_WithAppName_CompletionEventAuthor(t *testing.T) {
	const (
		defaultAppName  = "default-app"
		overrideAppName = "tenant-x"
		userID          = "user-1"
		sessionID       = "session-completion"
	)

	sessionService := sessioninmemory.NewSessionService()
	ag := &mockAgent{name: "completion-author-agent"}
	r := NewRunner(defaultAppName, ag, WithSessionService(sessionService))

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("hello"),
		agent.WithAppName(overrideAppName),
	)
	require.NoError(t, err)

	var completionAuthor string
	for ev := range ch {
		if ev.Response != nil && ev.Response.Object == model.ObjectTypeRunnerCompletion {
			completionAuthor = ev.Author
		}
	}

	assert.Equal(t, overrideAppName, completionAuthor,
		"runner completion event Author should use the overridden app name")
}
