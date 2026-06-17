//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package debuglog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	rootplugin "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/debuglog"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type logEntry struct {
	Sequence   uint64         `json:"sequence"`
	Plugin     string         `json:"plugin"`
	Phase      string         `json:"phase"`
	ModelName  string         `json:"model_name"`
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id"`
	Error      string         `json:"error"`
	Payload    map[string]any `json:"payload"`
}

type testModel struct{}

func (m testModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	return nil, nil
}

func (m testModel) Info() model.Info {
	return model.Info{Name: "test-model"}
}

type testTool struct {
	declaration *tool.Declaration
}

func (t testTool) Declaration() *tool.Declaration {
	return t.declaration
}

type panickingTool struct{}

func (panickingTool) Declaration() *tool.Declaration {
	panic("boom")
}

func newManager(t *testing.T, p rootplugin.Plugin) *rootplugin.Manager {
	t.Helper()
	m, err := rootplugin.NewManager(p)
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

type captureLogger struct {
	mu             sync.Mutex
	debugfMessages []string
}

func captureDebugLogs(t *testing.T) *captureLogger {
	t.Helper()
	original := agentlog.ContextDefault
	logger := &captureLogger{}
	agentlog.ContextDefault = logger
	t.Cleanup(func() {
		agentlog.ContextDefault = original
	})
	return logger
}

func (l *captureLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugfMessages = nil
}

func (l *captureLogger) entries(t *testing.T) []logEntry {
	t.Helper()
	l.mu.Lock()
	messages := append([]string(nil), l.debugfMessages...)
	l.mu.Unlock()
	if len(messages) == 0 {
		return nil
	}
	entries := make([]logEntry, 0, len(messages))
	for _, message := range messages {
		var ent logEntry
		require.NoError(t, json.Unmarshal([]byte(message), &ent))
		entries = append(entries, ent)
	}
	return entries
}

func (l *captureLogger) Debug(args ...any) {}

func (l *captureLogger) Debugf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugfMessages = append(l.debugfMessages, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Info(args ...any) {}

func (l *captureLogger) Infof(format string, args ...any) {}

func (l *captureLogger) Warn(args ...any) {}

func (l *captureLogger) Warnf(format string, args ...any) {}

func (l *captureLogger) Error(args ...any) {}

func (l *captureLogger) Errorf(format string, args ...any) {}

func (l *captureLogger) Fatal(args ...any) {}

func (l *captureLogger) Fatalf(format string, args ...any) {}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	require.True(t, ok)
	return out
}

func array(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	require.True(t, ok)
	return out
}

func TestPlugin_DefaultNameAndOptions(t *testing.T) {
	p := debuglog.New()
	require.Equal(t, "debug_log", p.Name())
	p = debuglog.New(
		nil,
		debuglog.WithName("local_debug"),
	)
	require.Equal(t, "local_debug", p.Name())
}

func TestPlugin_SkipsNilCallbackArgs(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	agentCallbacks := m.AgentCallbacks()
	require.NotNil(t, agentCallbacks)
	_, err := agentCallbacks.RunBeforeAgent(context.Background(), nil)
	require.NoError(t, err)
	_, err = agentCallbacks.RunAfterAgent(context.Background(), nil)
	require.NoError(t, err)
	modelCallbacks := m.ModelCallbacks()
	require.NotNil(t, modelCallbacks)
	_, err = modelCallbacks.RunBeforeModel(context.Background(), nil)
	require.NoError(t, err)
	_, err = modelCallbacks.RunAfterModel(context.Background(), nil)
	require.NoError(t, err)
	toolCallbacks := m.ToolCallbacks()
	require.NotNil(t, toolCallbacks)
	_, err = toolCallbacks.RunBeforeTool(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, logs.entries(t))
}

func TestPlugin_LogsModelRequestJSONContractAndSupplement(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	inv := agent.NewInvocation(
		agent.WithInvocationModel(testModel{}),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-1",
			AppName: "app-1",
			UserID:  "user-1",
		}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-1",
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	index := 0
	req := &model.Request{
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type:  "function",
				ID:    "call-1",
				Index: &index,
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"hi"}`),
				},
			}},
		}},
		ExtraFields: map[string]any{
			"bad":   func() {},
			"debug": map[string]any{"enabled": true},
		},
		Headers: map[string]string{
			"Authorization": "Bearer secret",
			"X-Trace":       "trace-1",
		},
		Tools: map[string]tool.Tool{
			"search": testTool{declaration: &tool.Declaration{
				Name:        "search",
				Description: "Search documents.",
				InputSchema: &tool.Schema{
					Type: "object",
				},
			}},
		},
	}
	_, err := m.ModelCallbacks().RunBeforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	require.Equal(t, "before_model", entries[0].Phase)
	require.Equal(t, "test-model", entries[0].ModelName)
	request := object(t, entries[0].Payload["request"])
	messages := array(t, request["messages"])
	message := object(t, messages[0])
	toolCalls := array(t, message["tool_calls"])
	toolCall := object(t, toolCalls[0])
	function := object(t, toolCall["function"])
	require.Equal(t, `{"q":"hi"}`, function["arguments"])
	supplement := object(t, entries[0].Payload["request_supplement"])
	headers := object(t, supplement["headers"])
	require.Equal(t, "Bearer secret", headers["Authorization"])
	require.Equal(t, "trace-1", headers["X-Trace"])
	extraFields := object(t, supplement["extra_fields"])
	debugField := object(t, extraFields["debug"])
	require.Equal(t, true, debugField["enabled"])
	extraErrors := object(t, supplement["extra_field_errors"])
	badError := object(t, extraErrors["bad"])
	require.Equal(t, "func()", badError["type"])
	require.Contains(t, badError["error"], "unsupported type")
	tools := object(t, supplement["tools"])
	search := object(t, tools["search"])
	require.Equal(t, "search", search["name"])
}

func TestPlugin_LogsRequestToolDeclarationErrors(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	req := model.NewRequest([]model.Message{model.NewUserMessage("hello")})
	req.Tools = map[string]tool.Tool{
		"bad_declaration": testTool{declaration: &tool.Declaration{
			Name:        "bad",
			Description: "Bad declaration.",
			InputSchema: &tool.Schema{
				Default: func() {},
			},
		}},
		"nil_declaration": testTool{},
		"nil_tool":        nil,
		"panic_tool":      panickingTool{},
	}
	_, err := m.ModelCallbacks().RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	supplement := object(t, entries[0].Payload["request_supplement"])
	toolErrors := object(t, supplement["tool_errors"])
	badDeclaration := object(t, toolErrors["bad_declaration"])
	require.Equal(t, "*tool.Declaration", badDeclaration["type"])
	require.Contains(t, badDeclaration["error"], "unsupported type")
	nilDeclaration := object(t, toolErrors["nil_declaration"])
	require.Equal(t, "debuglog_test.testTool", nilDeclaration["type"])
	require.Contains(t, nilDeclaration["error"], "tool declaration is nil")
	nilTool := object(t, toolErrors["nil_tool"])
	require.Equal(t, "nil", nilTool["type"])
	require.Contains(t, nilTool["error"], "tool is nil")
	panicTool := object(t, toolErrors["panic_tool"])
	require.Equal(t, "debuglog_test.panickingTool", panicTool["type"])
	require.Contains(t, panicTool["error"], "tool declaration panic: boom")
	require.NotContains(t, supplement, "tools")
}

func TestPlugin_LogsModelResponseAndSkipsPartialByDefault(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	req := model.NewRequest([]model.Message{model.NewUserMessage("hello")})
	partial := &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{
				Role:    model.RoleAssistant,
				Content: "partial",
			},
		}},
	}
	_, err := m.ModelCallbacks().RunAfterModel(
		context.Background(),
		&model.AfterModelArgs{Request: req, Response: partial},
	)
	require.NoError(t, err)
	require.Empty(t, logs.entries(t))
	logs.reset()
	m = newManager(t, debuglog.New(
		debuglog.WithModelPartialResponseEnabled(true),
	))
	_, err = m.ModelCallbacks().RunAfterModel(
		context.Background(),
		&model.AfterModelArgs{Request: req, Response: partial},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	response := object(t, entries[0].Payload["response"])
	require.Equal(t, true, response["is_partial"])
	require.Equal(t, model.ObjectTypeChatCompletionChunk, response["object"])
}

func TestPlugin_LogsUnserializableModelResponseType(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	req := model.NewRequest([]model.Message{model.NewUserMessage("hello")})
	resp := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ExtraFields: map[string]any{
						"bad": func() {},
					},
				}},
			},
		}},
	}
	_, err := m.ModelCallbacks().RunAfterModel(
		context.Background(),
		&model.AfterModelArgs{Request: req, Response: resp},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	payload := entries[0].Payload
	require.NotContains(t, payload, "response")
	require.Equal(t, "*model.Response", payload["response_type"])
	require.Contains(t, payload["response_encode_error"], "unsupported type")
}

func TestPlugin_LogsToolArgumentsAsJSONValues(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	callbacks := m.ToolCallbacks()
	require.NotNil(t, callbacks)
	_, err := callbacks.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolCallID:  "call-1",
			ToolName:    "calculator",
			Declaration: sampleDeclaration(),
			Arguments:   []byte(`{"a":1,"b":2}`),
		},
	)
	require.NoError(t, err)
	_, err = callbacks.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolCallID: "call-2",
			ToolName:   "calculator",
			Arguments:  []byte(`{"a":`),
		},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 2)
	require.Equal(t, "call-1", entries[0].ToolCallID)
	args := object(t, entries[0].Payload["arguments"])
	require.Equal(t, float64(1), args["a"])
	require.Equal(t, float64(2), args["b"])
	require.Equal(t, float64(len(`{"a":1,"b":2}`)), entries[0].Payload["arguments_bytes"])
	require.Equal(t, "call-2", entries[1].ToolCallID)
	require.Equal(t, `{"a":`, entries[1].Payload["arguments_text"])
	require.Contains(t, entries[1].Payload["arguments_error"], "unexpected end")
	require.Equal(t, float64(len(`{"a":`)), entries[1].Payload["arguments_bytes"])
}

func TestPlugin_UsesToolCallIDFromContext(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "ctx-call-1")
	_, err := m.ToolCallbacks().RunBeforeTool(
		ctx,
		&tool.BeforeToolArgs{
			ToolName:  "calculator",
			Arguments: []byte(`{}`),
		},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	require.Equal(t, "ctx-call-1", entries[0].ToolCallID)
}

func TestPlugin_LogsUnserializableToolResultAsEncodeError(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	callbacks := m.ToolCallbacks()
	require.NotNil(t, callbacks)
	_, err := callbacks.RunAfterTool(
		context.Background(),
		&tool.AfterToolArgs{
			ToolCallID:  "call-1",
			ToolName:    "runner",
			Declaration: sampleDeclaration(),
			Arguments:   []byte(`{"ok":true}`),
			Result:      make(chan int),
			Error:       errors.New("tool failed"),
			Meta: map[string]any{
				"bad": func() {},
				"ok":  "value",
			},
		},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	payload := entries[0].Payload
	require.Equal(t, "tool failed", entries[0].Error)
	require.NotContains(t, payload, "result")
	require.Equal(t, "chan int", payload["result_type"])
	require.Contains(t, payload["result_encode_error"], "unsupported type")
	meta := object(t, payload["meta"])
	require.Equal(t, "value", meta["ok"])
	metaErrors := object(t, payload["meta_errors"])
	badError := object(t, metaErrors["bad"])
	require.Equal(t, "func()", badError["type"])
	require.Contains(t, badError["error"], "unsupported type")
}

func TestPlugin_EventLoggingDefaultAndEnabled(t *testing.T) {
	ev := event.NewResponseEvent("inv-1", "agent-1", &model.Response{
		ID:     "rsp-1",
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("done"),
		}},
	})
	ev.ID = "event-1"
	ev.RequestID = "req-1"
	ev.StructuredOutput = map[string]any{"answer": "done"}
	ev.ExecutionTrace = &trace.Trace{RootAgentName: "agent-1", RootInvocationID: "inv-1"}
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	out, err := m.OnEvent(context.Background(), nil, ev)
	require.NoError(t, err)
	require.Same(t, ev, out)
	require.Empty(t, logs.entries(t))
	logs.reset()
	m = newManager(t, debuglog.New(
		debuglog.WithEventEnabled(true),
	))
	out, err = m.OnEvent(context.Background(), nil, ev)
	require.NoError(t, err)
	require.Same(t, ev, out)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	payloadEvent := object(t, entries[0].Payload["event"])
	require.Equal(t, "event-1", payloadEvent["id"])
	require.Equal(t, "req-1", payloadEvent["requestID"])
	supplement := object(t, entries[0].Payload["event_supplement"])
	structured := object(t, supplement["structured_output"])
	require.Equal(t, "done", structured["answer"])
	executionTrace := object(t, supplement["execution_trace"])
	require.Equal(t, "agent-1", executionTrace["RootAgentName"])
}

func TestPlugin_KeepsSerializablePayloadValuesAsIs(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	longText := strings.Repeat("界", 3000)
	_, err := m.ToolCallbacks().RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolCallID: "call-1",
			ToolName:   "secure",
			Arguments:  []byte(`{"password":"secret","long":"` + longText + `"}`),
		},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 1)
	args := object(t, entries[0].Payload["arguments"])
	require.Equal(t, "secret", args["password"])
	require.Equal(t, longText, args["long"])
}

func TestPlugin_LogsAgentMetadataAndFullResponseEvent(t *testing.T) {
	logs := captureDebugLogs(t)
	m := newManager(t, debuglog.New())
	parent := agent.NewInvocation(agent.WithInvocationID("parent-1"))
	inv := parent.Clone(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationModel(testModel{}),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-1",
			AppName: "app-1",
			UserID:  "user-1",
		}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID:             "req-1",
			StreamModeEnabled:     true,
			StreamModes:           []agent.StreamMode{agent.StreamModeMessages},
			ExecutionTraceEnabled: true,
		}),
	)
	callbacks := m.AgentCallbacks()
	require.NotNil(t, callbacks)
	_, err := callbacks.RunBeforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	require.NoError(t, err)
	full := event.NewResponseEvent("inv-1", "agent-1", &model.Response{
		ID:     "rsp-1",
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("done"),
		}},
	})
	full.ID = "event-full"
	_, err = callbacks.RunAfterAgent(
		context.Background(),
		&agent.AfterAgentArgs{
			Invocation:        inv,
			FullResponseEvent: full,
		},
	)
	require.NoError(t, err)
	entries := logs.entries(t)
	require.Len(t, entries, 2)
	invocation := object(t, entries[0].Payload["invocation"])
	require.Equal(t, "inv-1", invocation["invocation_id"])
	require.Equal(t, "parent-1", invocation["parent_invocation_id"])
	require.Equal(t, "test-model", invocation["model_name"])
	sessionValue := object(t, invocation["session"])
	require.Equal(t, "sess-1", sessionValue["id"])
	runOptions := object(t, invocation["run_options"])
	require.Equal(t, true, runOptions["stream_mode_enabled"])
	require.Equal(t, "req-1", runOptions["request_id"])
	fullEvent := object(t, entries[1].Payload["full_response_event"])
	require.Equal(t, "event-full", fullEvent["id"])
}

func sampleDeclaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "calculator",
		Description: "Calculates expressions.",
		InputSchema: &tool.Schema{
			Type: "object",
		},
	}
}
