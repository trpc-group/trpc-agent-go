//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolerror

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type declaredTool struct {
	declaration *tool.Declaration
}

type stateTrackingTool struct {
	callErr         error
	callCount       atomic.Int32
	stateDeltaCalls atomic.Int32
}

func (t *stateTrackingTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "lookup",
		Description: "Looks up a query.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"query"},
			Properties: map[string]*tool.Schema{
				"query": {Type: "string"},
			},
		},
	}
}

func (t *stateTrackingTool) Call(
	_ context.Context,
	_ []byte,
) (any, error) {
	t.callCount.Add(1)
	if t.callErr != nil {
		return nil, t.callErr
	}
	return "ok", nil
}

func (t *stateTrackingTool) StateDelta(
	_ string,
	_ []byte,
	_ []byte,
) map[string][]byte {
	t.stateDeltaCalls.Add(1)
	return map[string][]byte{"lookup": []byte("persisted")}
}

type codedError struct{}

func (codedError) Error() string {
	return "downstream rejected the request"
}

func (codedError) Code() int32 {
	return 73001
}

func (t *declaredTool) Declaration() *tool.Declaration {
	return t.declaration
}

type stubAgent struct {
	name      string
	subAgents []agent.Agent
}

func (a *stubAgent) Run(
	_ context.Context,
	_ *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *stubAgent) Tools() []tool.Tool {
	return nil
}

func (a *stubAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *stubAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

func (a *stubAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent != nil && subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

func newManager(t *testing.T, opts ...Option) *pluginbase.Manager {
	t.Helper()
	manager, err := pluginbase.NewManager(New(opts...))
	require.NoError(t, err)
	return manager
}

func beforeTool(
	t *testing.T,
	manager *pluginbase.Manager,
	schema *tool.Schema,
	arguments string,
) *tool.BeforeToolResult {
	t.Helper()
	result, err := manager.ToolCallbacks().RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolCallID: "call-1",
			ToolName:   "lookup",
			Declaration: &tool.Declaration{
				Name:        "lookup",
				InputSchema: schema,
			},
			Arguments: []byte(arguments),
		},
	)
	require.NoError(t, err)
	return result
}

func requireFailure(t *testing.T, value any) Failure {
	t.Helper()
	failure, ok := value.(Failure)
	require.True(t, ok, "unexpected failure type %T", value)
	require.False(t, failure.OK)
	return failure
}

func TestNew(t *testing.T) {
	t.Parallel()
	require.Equal(t, defaultPluginName, New().Name())
	require.Equal(t, "custom", New(WithName("custom")).Name())
	require.Equal(t, defaultPluginName, New(WithName("")).Name())
}

func TestPluginNilSafety(t *testing.T) {
	t.Parallel()
	var p *toolErrorPlugin
	require.Empty(t, p.Name())
	require.NotPanics(t, func() {
		p.Register(nil)
	})
	p = New().(*toolErrorPlugin)
	require.NotPanics(t, func() {
		p.Register(nil)
	})
	result, err := p.beforeTool(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, result)
	afterResult, err := p.afterTool(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, afterResult)
	messagesResult, err := p.afterToolMessages(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, messagesResult)
}

func TestBeforeToolAcceptsValidArguments(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	result := beforeTool(t, manager, &tool.Schema{
		Type:     "object",
		Required: []string{"query"},
		Properties: map[string]*tool.Schema{
			"query": {Type: "string"},
		},
	}, `{"query":"weather"}`)
	require.Nil(t, result)
}

func TestBeforeToolAcceptsLocalSchemaReferencesConcurrently(t *testing.T) {
	t.Parallel()
	p := New().(*toolErrorPlugin)
	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"filter": {Ref: "#/$defs/filter"},
		},
		Defs: map[string]*tool.Schema{
			"filter": {
				Type:     "object",
				Required: []string{"query"},
				Properties: map[string]*tool.Schema{
					"query": {Type: "string"},
				},
			},
		},
	}
	const goroutines = 32
	var wg sync.WaitGroup
	rejections := make(chan Details, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if details, invalid := p.validateArguments(
				"lookup",
				[]byte(`{"filter":{"query":"weather"}}`),
				schema,
			); invalid {
				rejections <- details
			}
		}()
	}
	wg.Wait()
	close(rejections)
	for details := range rejections {
		t.Fatalf("valid arguments were rejected: %+v", details)
	}
	details, invalid := p.validateArguments(
		"lookup",
		[]byte(`{"filter":{}}`),
		schema,
	)
	require.True(t, invalid)
	require.Equal(t, "required", details.Code)
	require.Equal(t, "/filter/query", details.Param)
}

func TestSchemaCacheEvictsOldestEntryAtCapacity(t *testing.T) {
	t.Parallel()
	cache := newSchemaCache()
	var firstKey, lastKey [sha256.Size]byte
	for i := 0; i <= defaultSchemaCacheCapacity; i++ {
		schema := &tool.Schema{
			Type:        "object",
			Description: fmt.Sprintf("schema-%d", i),
		}
		raw, err := json.Marshal(schema)
		require.NoError(t, err)
		key := sha256.Sum256(raw)
		if i == 0 {
			firstKey = key
		}
		lastKey = key
		_, err = cache.compile(schema)
		require.NoError(t, err)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.entries, defaultSchemaCacheCapacity)
	require.Len(t, cache.order, defaultSchemaCacheCapacity)
	require.NotContains(t, cache.entries, firstKey)
	require.Contains(t, cache.entries, lastKey)
}

func TestBeforeToolNormalizesOmittedArgumentsForZeroParameterTool(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	result := beforeTool(t, manager, &tool.Schema{Type: "object"}, "")
	require.Nil(t, result)
}

func TestBeforeToolAcceptsOmittedOptionalArguments(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	result := beforeTool(t, manager, &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"query": {Type: "string"},
		},
	}, " \n\t")
	require.Nil(t, result)
}

func TestBeforeToolRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		schema    *tool.Schema
		arguments string
		code      string
		param     string
	}{
		{
			name: "invalid JSON",
			schema: &tool.Schema{
				Type: "object",
			},
			arguments: "{",
			code:      "invalid_json",
		},
		{
			name: "required",
			schema: &tool.Schema{
				Type:     "object",
				Required: []string{"query"},
				Properties: map[string]*tool.Schema{
					"query": {Type: "string"},
				},
			},
			arguments: `{}`,
			code:      "required",
			param:     "/query",
		},
		{
			name: "required with omitted arguments",
			schema: &tool.Schema{
				Type:     "object",
				Required: []string{"query"},
				Properties: map[string]*tool.Schema{
					"query": {Type: "string"},
				},
			},
			arguments: "",
			code:      "required",
			param:     "/query",
		},
		{
			name: "type",
			schema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"limit": {Type: "integer"},
				},
			},
			arguments: `{"limit":"ten"}`,
			code:      "type",
			param:     "/limit",
		},
		{
			name: "enum",
			schema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"unit": {
						Type: "string",
						Enum: []any{"celsius", "fahrenheit"},
					},
				},
			},
			arguments: `{"unit":"kelvin"}`,
			code:      "enum",
			param:     "/unit",
		},
		{
			name: "pattern",
			schema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"date": {Type: "string", Pattern: `^\d{4}-\d{2}-\d{2}$`},
				},
			},
			arguments: `{"date":"tomorrow"}`,
			code:      "pattern",
			param:     "/date",
		},
		{
			name: "additional properties",
			schema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query": {Type: "string"},
				},
				AdditionalProperties: false,
			},
			arguments: `{"query":"weather","debug":true}`,
			code:      "additional_properties",
			param:     "/debug",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manager := newManager(t)
			result := beforeTool(t, manager, test.schema, test.arguments)
			require.NotNil(t, result)
			require.True(t, result.SkipStateDelta)
			failure := requireFailure(t, result.CustomResult)
			require.Equal(t, SourceModel, failure.Error.Source)
			require.Equal(t, KindInvalidArguments, failure.Error.Kind)
			require.Equal(t, test.code, failure.Error.Code)
			require.Equal(t, test.param, failure.Error.Param)
			require.True(t, failure.Error.Retryable)
			require.NotEmpty(t, failure.Error.Message)
		})
	}
}

func TestValidationCodeNormalizesMultiwordKeyword(t *testing.T) {
	t.Parallel()
	require.Equal(t, "min_length", validationCode(
		&jsonschema.ValidationError{ErrorKind: &kind.MinLength{}},
	))
}

func TestBeforeToolRejectsInvalidSchemaAsConfigurationFailure(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	result := beforeTool(t, manager, &tool.Schema{
		Ref: "https://schemas.example.com/input.json",
	}, `{}`)
	require.NotNil(t, result)
	failure := requireFailure(t, result.CustomResult)
	require.Equal(t, SourceFramework, failure.Error.Source)
	require.Equal(t, KindConfiguration, failure.Error.Kind)
	require.Equal(t, "invalid_schema", failure.Error.Code)
	require.False(t, failure.Error.Retryable)
	require.Contains(t, failure.Error.Message, "external schema reference")
}

func TestBeforeToolSkipsMissingDeclarationOrSchema(t *testing.T) {
	t.Parallel()
	p := New().(*toolErrorPlugin)
	result, err := p.beforeTool(context.Background(), &tool.BeforeToolArgs{})
	require.NoError(t, err)
	require.Nil(t, result)
	result, err = p.beforeTool(context.Background(), &tool.BeforeToolArgs{
		Declaration: &tool.Declaration{},
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestAfterToolClassifiesExecutionErrors(t *testing.T) {
	t.Parallel()
	syntaxErr := json.Unmarshal([]byte("{"), &struct{}{})
	require.Error(t, syntaxErr)
	deadlineCtx, cancelDeadline := context.WithDeadline(
		context.Background(),
		time.Time{},
	)
	defer cancelDeadline()
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	causeErr := errors.New("runner stopped")
	causeCtx, cancelCause := context.WithCancelCause(context.Background())
	cancelCause(causeErr)
	tests := []struct {
		name      string
		ctx       context.Context
		err       error
		source    Source
		kind      Kind
		code      string
		param     string
		retryable bool
	}{
		{
			name:   "tool execution",
			err:    errors.New("backend unavailable"),
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name:   "typed tool execution code",
			err:    fmt.Errorf("search failed: %w", codedError{}),
			source: SourceTool,
			kind:   KindExecution,
			code:   "73001",
		},
		{
			name:   "JSON syntax error from tool",
			err:    syntaxErr,
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name: "JSON type error from tool",
			err: &json.UnmarshalTypeError{
				Value: "string",
				Type:  reflect.TypeOf(0),
				Field: "filter.limit",
			},
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name:   "wrapped EOF from tool",
			err:    fmt.Errorf("decode downstream response: %w", io.EOF),
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name:   "tool deadline with healthy context",
			err:    fmt.Errorf("call timed out: %w", context.DeadlineExceeded),
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name:   "tool cancellation with healthy context",
			err:    context.Canceled,
			source: SourceTool,
			kind:   KindExecution,
			code:   "tool_execution",
		},
		{
			name:      "invocation deadline",
			ctx:       deadlineCtx,
			err:       fmt.Errorf("call timed out: %w", context.DeadlineExceeded),
			source:    SourceFramework,
			kind:      KindExecution,
			code:      "deadline_exceeded",
			retryable: true,
		},
		{
			name:   "invocation canceled",
			ctx:    canceledCtx,
			err:    context.Canceled,
			source: SourceFramework,
			kind:   KindExecution,
			code:   "canceled",
		},
		{
			name:   "invocation matching cancellation cause",
			ctx:    causeCtx,
			err:    fmt.Errorf("call stopped: %w", causeErr),
			source: SourceFramework,
			kind:   KindExecution,
			code:   "canceled",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manager := newManager(t)
			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			result, err := manager.ToolCallbacks().RunAfterTool(
				ctx,
				&tool.AfterToolArgs{Error: test.err},
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.SkipResultFormatter)
			require.True(t, result.SkipStateDelta)
			failure := requireFailure(t, result.CustomResult)
			require.Equal(t, test.source, failure.Error.Source)
			require.Equal(t, test.kind, failure.Error.Kind)
			require.Equal(t, test.code, failure.Error.Code)
			require.Equal(t, test.param, failure.Error.Param)
			require.Equal(t, test.retryable, failure.Error.Retryable)
			require.NotEmpty(t, failure.Error.Message)
		})
	}
}

func TestAfterToolLeavesSuccessfulResultUntouched(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	original := map[string]any{"ok": true}
	result, err := manager.ToolCallbacks().RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{Result: original},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
}

func TestAfterToolResolverClassifiesBusinessFailure(t *testing.T) {
	t.Parallel()
	manager := newManager(t, WithResolver(func(
		_ context.Context,
		args *tool.AfterToolArgs,
	) (Details, bool) {
		result, ok := args.Result.(map[string]any)
		if !ok || result["status"] != "error" {
			return Details{}, false
		}
		return Details{
			Source:    SourceTool,
			Kind:      KindExecution,
			Code:      "quota_exceeded",
			Message:   "quota exceeded",
			Retryable: true,
		}, true
	}))
	result, err := manager.ToolCallbacks().RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			Result: map[string]any{"status": "error"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.SkipResultFormatter)
	require.True(t, result.SkipStateDelta)
	failure := requireFailure(t, result.CustomResult)
	require.Equal(t, "quota_exceeded", failure.Error.Code)
	require.True(t, failure.Error.Retryable)
}

func TestAfterToolResolverTakesPriorityAndNormalizesDetails(t *testing.T) {
	t.Parallel()
	manager := newManager(t, WithResolver(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (Details, bool) {
		return Details{}, true
	}))
	result, err := manager.ToolCallbacks().RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{Error: errors.New("domain failure")},
	)
	require.NoError(t, err)
	failure := requireFailure(t, result.CustomResult)
	require.Equal(t, SourceTool, failure.Error.Source)
	require.Equal(t, KindExecution, failure.Error.Kind)
	require.Equal(t, string(KindExecution), failure.Error.Code)
	require.Equal(t, "domain failure", failure.Error.Message)
}

func TestAfterToolMessagesNormalizesUnknownToolFailure(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	args := &pluginbase.AfterToolMessagesArgs{
		Request: &model.Request{Tools: map[string]tool.Tool{
			"known": &declaredTool{declaration: &tool.Declaration{Name: "known"}},
		}},
		ToolCalls: []model.ToolCall{
			{
				ID: "call-known",
				Function: model.FunctionDefinitionParam{
					Name: "known",
				},
			},
			{
				ID: "call-missing",
				Function: model.FunctionDefinitionParam{
					Name: "missing",
				},
			},
		},
		ToolResultMessages: []model.Message{
			{
				Role:     model.RoleTool,
				ToolID:   "call-known",
				ToolName: "known",
				Content:  `{"result":"ok"}`,
			},
			{
				Role:    model.RoleTool,
				ToolID:  "call-missing",
				Content: `executeToolCall: Error: tool not found: missing; did you mean "known"?`,
			},
		},
	}
	result, err := manager.AfterToolMessages(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ToolResultMessages, 2)
	require.Equal(t, args.ToolResultMessages[0], result.ToolResultMessages[0])
	missing := result.ToolResultMessages[1]
	require.Equal(t, "call-missing", missing.ToolID)
	require.Equal(t, "missing", missing.ToolName)
	var failure Failure
	require.NoError(t, json.Unmarshal([]byte(missing.Content), &failure))
	require.False(t, failure.OK)
	require.Equal(t, SourceModel, failure.Error.Source)
	require.Equal(t, KindToolNotFound, failure.Error.Kind)
	require.Equal(t, string(KindToolNotFound), failure.Error.Code)
	require.True(t, failure.Error.Retryable)
	require.Equal(
		t,
		`tool not found: missing; did you mean "known"?`,
		failure.Error.Message,
	)
}

func TestAfterToolMessagesSkipsCompatibilityMappedOrKnownResults(t *testing.T) {
	t.Parallel()
	manager := newManager(t)
	tests := []struct {
		name string
		args *pluginbase.AfterToolMessagesArgs
	}{
		{
			name: "unknown tool with successful result",
			args: &pluginbase.AfterToolMessagesArgs{
				Request: &model.Request{Tools: map[string]tool.Tool{
					"known": &declaredTool{
						declaration: &tool.Declaration{Name: "known"},
					},
				}},
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name: "dynamic",
					},
				}},
				ToolResultMessages: []model.Message{{
					Role:    model.RoleTool,
					ToolID:  "call-1",
					Content: `{"result":"ok"}`,
				}},
			},
		},
		{
			name: "unknown name with successful mapped result",
			args: &pluginbase.AfterToolMessagesArgs{
				Invocation: &agent.Invocation{Agent: &stubAgent{
					name: "parent",
					subAgents: []agent.Agent{
						&stubAgent{name: "subagent"},
					},
				}},
				Request: &model.Request{Tools: map[string]tool.Tool{
					transfer.TransferToolName: &declaredTool{
						declaration: &tool.Declaration{
							Name: transfer.TransferToolName,
						},
					},
				}},
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name: "subagent",
					},
				}},
				ToolResultMessages: []model.Message{{
					Role:    model.RoleTool,
					ToolID:  "call-1",
					Content: `{"result":"transferred"}`,
				}},
			},
		},
		{
			name: "known tool error text",
			args: &pluginbase.AfterToolMessagesArgs{
				Request: &model.Request{Tools: map[string]tool.Tool{
					"known": &declaredTool{declaration: &tool.Declaration{Name: "known"}},
				}},
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name: "known",
					},
				}},
				ToolResultMessages: []model.Message{{
					Role:    model.RoleTool,
					ToolID:  "call-1",
					Content: "Error: tool not found",
				}},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := manager.AfterToolMessages(context.Background(), test.args)
			require.NoError(t, err)
			require.Nil(t, result)
		})
	}
}

func TestFailureJSONContract(t *testing.T) {
	t.Parallel()
	raw := failureJSON(Details{
		Source:    SourceModel,
		Kind:      KindInvalidArguments,
		Code:      "required",
		Message:   "query is required",
		Param:     "/query",
		Retryable: true,
	})
	require.JSONEq(t, `{
		"ok": false,
		"error": {
			"source": "model",
			"kind": "invalid_arguments",
			"code": "required",
			"message": "query is required",
			"param": "/query",
			"retryable": true
		}
	}`, raw)
}

type requiredInput struct {
	Query string `json:"query" jsonschema:"required"`
}

type invalidArgumentLoopModel struct {
	mu        sync.Mutex
	requests  [][]model.Message
	arguments []byte
	toolCalls []model.ToolCall
}

func (m *invalidArgumentLoopModel) Info() model.Info {
	return model.Info{Name: "invalid-argument-loop-model"}
}

func (m *invalidArgumentLoopModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(
		m.requests,
		append([]model.Message(nil), req.Messages...),
	)
	callIndex := len(m.requests) - 1
	m.mu.Unlock()
	response := &model.Response{
		ID:        "rsp-final",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("done"),
		}},
	}
	if callIndex == 0 {
		toolCalls := m.toolCalls
		if len(toolCalls) == 0 {
			arguments := m.arguments
			if arguments == nil {
				arguments = []byte(`{}`)
			}
			toolCalls = []model.ToolCall{{
				ID:   "call-invalid",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "lookup",
					Arguments: arguments,
				},
			}}
		}
		response = &model.Response{
			ID:        "rsp-tool",
			Done:      true,
			IsPartial: false,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: toolCalls,
				},
			}},
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *invalidArgumentLoopModel) Requests() [][]model.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make([][]model.Message, len(m.requests))
	for i := range m.requests {
		requests[i] = append([]model.Message(nil), m.requests[i]...)
	}
	return requests
}

func TestPluginIntegrationInvalidArgumentsSkipToolAndReachModel(t *testing.T) {
	modelStub := &invalidArgumentLoopModel{}
	var callCount atomic.Int32
	lookup := function.NewFunctionTool(
		func(_ context.Context, input requiredInput) (string, error) {
			callCount.Add(1)
			return input.Query, nil
		},
		function.WithName("lookup"),
		function.WithDescription("Looks up a query."),
	)
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
	)
	runnerInstance := runner.NewRunner(
		"tool-error-app",
		agentInstance,
		runner.WithPlugins(New()),
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
	})
	events, err := runnerInstance.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("look this up"),
	)
	require.NoError(t, err)
	for range events {
	}
	require.Zero(t, callCount.Load())
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	var toolMessage *model.Message
	for i := range requests[1] {
		if requests[1][i].Role == model.RoleTool {
			toolMessage = &requests[1][i]
			break
		}
	}
	require.NotNil(t, toolMessage)
	require.Equal(t, "call-invalid", toolMessage.ToolID)
	var failure Failure
	require.NoError(t, json.Unmarshal([]byte(toolMessage.Content), &failure))
	require.Equal(t, SourceModel, failure.Error.Source)
	require.Equal(t, KindInvalidArguments, failure.Error.Kind)
	require.Equal(t, "required", failure.Error.Code)
	require.Equal(t, "/query", failure.Error.Param)
}

func TestPluginIntegrationExecutionErrorReachesModel(t *testing.T) {
	modelStub := &invalidArgumentLoopModel{
		arguments: []byte(`{"query":"weather"}`),
	}
	var callCount atomic.Int32
	var formatterCalls atomic.Int32
	lookup := function.NewFunctionTool(
		func(_ context.Context, _ requiredInput) (string, error) {
			callCount.Add(1)
			return "", codedError{}
		},
		function.WithName("lookup"),
		function.WithDescription("Looks up a query."),
		function.WithResultFormatter(resultformat.FormatterFunc[string](func(
			_ context.Context,
			result string,
		) (string, error) {
			formatterCalls.Add(1)
			return "formatted:" + result, nil
		})),
	)
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
	)
	runnerInstance := runner.NewRunner(
		"tool-error-execution-app",
		agentInstance,
		runner.WithPlugins(New()),
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
	})
	events, err := runnerInstance.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("look this up"),
	)
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, int32(1), callCount.Load())
	require.Zero(t, formatterCalls.Load())
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	var failure Failure
	for _, message := range requests[1] {
		if message.Role != model.RoleTool {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(message.Content), &failure))
		break
	}
	require.Equal(t, SourceTool, failure.Error.Source)
	require.Equal(t, KindExecution, failure.Error.Kind)
	require.Equal(t, "73001", failure.Error.Code)
	require.Contains(t, failure.Error.Message, "downstream rejected the request")
}

func TestPluginIntegrationFailuresSkipToolStateDelta(t *testing.T) {
	tests := []struct {
		name                string
		arguments           []byte
		callErr             error
		wantToolCalls       int32
		wantStateDeltaCalls int32
	}{
		{
			name:          "validation failure",
			arguments:     []byte(`{}`),
			wantToolCalls: 0,
		},
		{
			name:          "execution failure",
			arguments:     []byte(`{"query":"weather"}`),
			callErr:       errors.New("backend unavailable"),
			wantToolCalls: 1,
		},
		{
			name:                "successful result control",
			arguments:           []byte(`{"query":"weather"}`),
			wantToolCalls:       1,
			wantStateDeltaCalls: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			modelStub := &invalidArgumentLoopModel{arguments: test.arguments}
			lookup := &stateTrackingTool{callErr: test.callErr}
			agentInstance := llmagent.New(
				"assistant",
				llmagent.WithModel(modelStub),
				llmagent.WithTools([]tool.Tool{lookup}),
			)
			runnerInstance := runner.NewRunner(
				"tool-error-state-delta-app",
				agentInstance,
				runner.WithPlugins(New()),
			)
			t.Cleanup(func() {
				require.NoError(t, runnerInstance.Close())
			})
			events, err := runnerInstance.Run(
				context.Background(),
				"user-1",
				"session-"+test.name,
				model.NewUserMessage("look this up"),
			)
			require.NoError(t, err)
			for range events {
			}
			require.Equal(t, test.wantToolCalls, lookup.callCount.Load())
			require.Equal(
				t,
				test.wantStateDeltaCalls,
				lookup.stateDeltaCalls.Load(),
			)
		})
	}
}

func TestPluginIntegrationParallelUnknownToolReachesModel(t *testing.T) {
	modelStub := &invalidArgumentLoopModel{
		toolCalls: []model.ToolCall{
			{
				ID:   "call-known",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "known",
					Arguments: []byte(`{}`),
				},
			},
			{
				ID:   "call-missing",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "missing",
					Arguments: []byte(`{}`),
				},
			},
		},
	}
	known := function.NewFunctionTool(
		func(context.Context, struct{}) (string, error) {
			return "ok", nil
		},
		function.WithName("known"),
		function.WithDescription("Returns a successful result."),
	)
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{known}),
		llmagent.WithEnableParallelTools(true),
	)
	runnerInstance := runner.NewRunner(
		"tool-error-parallel-app",
		agentInstance,
		runner.WithPlugins(New()),
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
	})
	events, err := runnerInstance.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("run both tools"),
	)
	require.NoError(t, err)
	for range events {
	}
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	results := make(map[string]model.Message)
	for _, message := range requests[1] {
		if message.Role == model.RoleTool {
			results[message.ToolID] = message
		}
	}
	require.Len(t, results, 2)
	require.JSONEq(t, `"ok"`, results["call-known"].Content)
	missing := results["call-missing"]
	require.Equal(t, "missing", missing.ToolName)
	var failure Failure
	require.NoError(t, json.Unmarshal([]byte(missing.Content), &failure))
	require.Equal(t, SourceModel, failure.Error.Source)
	require.Equal(t, KindToolNotFound, failure.Error.Kind)
	require.Equal(t, string(KindToolNotFound), failure.Error.Code)
}

func TestPluginIntegrationValidatesArgumentsAfterJSONRepair(t *testing.T) {
	modelStub := &invalidArgumentLoopModel{
		arguments: []byte(`{"query":"weather"`),
	}
	var callCount atomic.Int32
	lookup := function.NewFunctionTool(
		func(_ context.Context, input requiredInput) (string, error) {
			callCount.Add(1)
			return input.Query, nil
		},
		function.WithName("lookup"),
		function.WithDescription("Looks up a query."),
	)
	agentInstance := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{lookup}),
	)
	runnerInstance := runner.NewRunner(
		"tool-error-repair-app",
		agentInstance,
		runner.WithPlugins(New()),
	)
	t.Cleanup(func() {
		require.NoError(t, runnerInstance.Close())
	})
	events, err := runnerInstance.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("look this up"),
		agent.WithToolCallArgumentsJSONRepairEnabled(true),
	)
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, int32(1), callCount.Load())
	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	for _, message := range requests[1] {
		if message.Role == model.RoleTool {
			require.JSONEq(t, `"weather"`, message.Content)
			return
		}
	}
	t.Fatal("tool result message not found")
}
