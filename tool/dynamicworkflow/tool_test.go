//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dynamicworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/eventstream"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewToolRequiresARegisteredAgent(t *testing.T) {
	_, err := NewTool(scriptedRuntime{}, nil)
	require.ErrorContains(t, err, "at least one agent is required")

	_, err = NewTool(nil, []agent.Agent{&testAgent{name: "reviewer"}})
	require.ErrorContains(t, err, "runtime is required")
}

func TestWorkflowToolOnlyExposesCallToolWhenConfigured(t *testing.T) {
	reviewer := &testAgent{name: "reviewer"}
	withoutCodeCallableTools, err := NewTool(scriptedRuntime{}, []agent.Agent{reviewer})
	require.NoError(t, err)
	require.NotContains(t, withoutCodeCallableTools.Declaration().Description, "call_tool")
	require.NotContains(t, withoutCodeCallableTools.Declaration().InputSchema.Properties["code"].Description, "call_tool")

	lookup := &testTool{name: "lookup", call: func(context.Context, []byte) (any, error) {
		return map[string]any{"ok": true}, nil
	}}
	withCodeCallableTools, err := NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithCodeCallableTools(lookup))
	require.NoError(t, err)
	require.Contains(t, withCodeCallableTools.Declaration().Description, "call_tool")
	require.Contains(t, withCodeCallableTools.Declaration().InputSchema.Properties["code"].Description, "call_tool")
}

func TestWorkflowAgentDefaultsToSoleTemplate(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: "approved"}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: reviewRaw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"approved","session_id":"session-1","history_key":"root/dynamic_workflow/<workflow>/agent-1","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))
	require.Equal(t, "review it", reviewer.lastMessage())
}

func TestWorkflowAgentDoesNotInheritRootRunScopedOverrides(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: "approved"}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: reviewRaw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	rootExtra := &testTool{name: "root_extra"}
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	parent.RunOptions.ModelName = "root-model"
	parent.RunOptions.ModelContextWindow = 1024
	parent.RunOptions.ModelRequestExtraFields = map[string]any{"root_only": true}
	parent.RunOptions.ModelRequestHeaders = map[string]string{"X-Root-Only": "true"}
	parent.RunOptions.Instruction = "root instruction"
	parent.RunOptions.GlobalInstruction = "root global instruction"
	parent.RunOptions.AdditionalTools = []tool.Tool{rootExtra}
	parent.RunOptions.ExternalToolNames = map[string]bool{"external": true}
	parent.RunOptions.ToolFilter = func(context.Context, tool.Tool) bool { return false }
	parent.RunOptions.ToolExecutionFilter = func(context.Context, tool.Tool) bool { return false }
	parent.RunOptions.ToolPermissionPolicy = tool.PermissionPolicyFunc(
		func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
			return tool.AllowPermission(), nil
		},
	)
	parent.RunOptions.StructuredOutput = &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
	}
	parent.StructuredOutput = parent.RunOptions.StructuredOutput
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	require.Empty(t, child.RunOptions.ModelName)
	require.Zero(t, child.RunOptions.ModelContextWindow)
	require.Nil(t, child.RunOptions.ModelRequestExtraFields)
	require.Nil(t, child.RunOptions.ModelRequestHeaders)
	require.Empty(t, child.RunOptions.Instruction)
	require.Empty(t, child.RunOptions.GlobalInstruction)
	require.Nil(t, child.RunOptions.AdditionalTools)
	require.Nil(t, child.RunOptions.ExternalToolNames)
	require.Nil(t, child.RunOptions.ToolFilter)
	require.Nil(t, child.RunOptions.ToolExecutionFilter)
	require.NotNil(t, child.RunOptions.ToolPermissionPolicy)
	require.Nil(t, child.RunOptions.StructuredOutput)
	require.Nil(t, child.StructuredOutput)
}

func TestWorkflowDynamicAgentToolSelectionUsesSanitizedChildProbe(t *testing.T) {
	templateTool := &testTool{name: "template_lookup"}
	rootExtra := &testTool{name: "root_extra"}
	reviewer := &runOptionsToolSurfaceAgent{testAgent: &testAgent{
		name:          "reviewer",
		response:      "approved",
		tools:         []tool.Tool{templateTool},
		userToolNames: map[string]bool{"template_lookup": true},
	}}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID:   "agent-1",
			Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"agent":{
					"template":"reviewer",
					"tools":["root_extra"]
				},
				"input":"review it"
			}`),
		})
		return Result{}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	parent.RunOptions.AdditionalTools = []tool.Tool{rootExtra}
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, `does not allow tool(s): root_extra`)
}

func TestWorkflowAgentRequiresTemplateWhenMultipleAreRegistered(t *testing.T) {
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{}, err
	}}, []agent.Agent{
		&testAgent{name: "reviewer"},
		&testAgent{name: "researcher"},
	})
	require.NoError(t, err)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, "agent options.template is required when multiple templates are registered")
}

func TestDecodeAgentOptionsRejectsUnknownField(t *testing.T) {
	_, err := decodeAgentOptions(json.RawMessage(`{
		"template": "reviewer",
		"unsupported_option": true
	}`))
	require.ErrorContains(t, err, `unsupported agent option "unsupported_option"`)
}

func TestWorkflowParallelAgentsUseDistinctChildrenAndForwardEvents(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := make(chan struct{})
	var startedOnce sync.Once
	var startedMu sync.Mutex
	startedCount := 0
	reviewer := &testAgent{name: "reviewer"}
	reviewer.runFn = func(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(inv.Message.Content), &input); err != nil {
			return nil, err
		}
		startedMu.Lock()
		startedCount++
		if startedCount == 2 {
			startedOnce.Do(func() { close(started) })
		}
		startedMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-started:
		}
		ch := make(chan *event.Event, 1)
		ch <- event.NewResponseEvent(inv.InvocationID, "reviewer", &model.Response{
			Done: true,
			Choices: []model.Choice{{Index: 0, Message: model.Message{
				Role: model.RoleAssistant, Content: input.ID,
			}}},
		})
		close(ch)
		return ch, nil
	}
	workflow, err := NewTool(LocalRunner{}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	var eventsMu sync.Mutex
	var persisted, forwarded []*event.Event
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		persisted = append(persisted, evt)
		return nil
	})
	eventstream.Attach(parent, func(_ context.Context, evt *event.Event) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		forwarded = append(forwarded, evt)
		return nil
	})

	workflowInput, err := json.Marshal(map[string]string{"code": `
results = await parallel([
    lambda: agent({"id": "first"}),
    lambda: agent({"id": "second"}),
])
return results
`})
	require.NoError(t, err)
	value, err := workflow.Call(agent.NewInvocationContext(ctx, parent), workflowInput)
	require.NoError(t, err)
	result := value.(Result)
	var agentResults []agentResult
	require.NoError(t, json.Unmarshal(result.Value, &agentResults))
	require.Len(t, agentResults, 2)
	require.Equal(t, "first", agentResults[0].Text)
	require.Equal(t, "second", agentResults[1].Text)
	require.NotEqual(t, agentResults[0].InvocationID, agentResults[1].InvocationID)

	eventsMu.Lock()
	defer eventsMu.Unlock()
	require.Len(t, persisted, 2, "each child input is persisted")
	require.Len(t, forwarded, 2, "each child output reaches the shared event stream")
	require.NotEqual(t, forwarded[0].InvocationID, forwarded[1].InvocationID)
}

func TestWorkflowParallelStreamsInterleavedEventsAndKeepsResultOrder(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	firstPartialForwarded := make(chan struct{})
	secondFinalForwarded := make(chan struct{})
	var firstPartialOnce sync.Once
	var secondFinalOnce sync.Once
	reviewer := &testAgent{name: "reviewer"}
	reviewer.runFn = func(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(inv.Message.Content), &input); err != nil {
			return nil, err
		}
		ch := make(chan *event.Event, 2)
		go func() {
			defer close(ch)
			switch input.ID {
			case "first":
				ch <- testAgentPartialEvent(inv.InvocationID, "reviewer", "first partial")
				select {
				case <-ctx.Done():
					return
				case <-secondFinalForwarded:
				}
				ch <- testAgentFinalEvent(inv.InvocationID, "reviewer", "first final")
			case "second":
				select {
				case <-ctx.Done():
					return
				case <-firstPartialForwarded:
				}
				ch <- testAgentPartialEvent(inv.InvocationID, "reviewer", "second partial")
				ch <- testAgentFinalEvent(inv.InvocationID, "reviewer", "second final")
			default:
				ch <- testAgentFinalEvent(inv.InvocationID, "reviewer", input.ID)
			}
		}()
		return ch, nil
	}
	workflow, err := NewTool(LocalRunner{}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })
	var eventsMu sync.Mutex
	var streamed []string
	var invocationIDs []string
	eventstream.Attach(parent, func(_ context.Context, evt *event.Event) error {
		content, ok := assistantEventContent(evt)
		if !ok || content == "" {
			return nil
		}
		eventsMu.Lock()
		defer eventsMu.Unlock()
		streamed = append(streamed, content)
		invocationIDs = append(invocationIDs, evt.InvocationID)
		if content == "first partial" {
			firstPartialOnce.Do(func() { close(firstPartialForwarded) })
		}
		if content == "second final" {
			secondFinalOnce.Do(func() { close(secondFinalForwarded) })
		}
		return nil
	})

	input, err := json.Marshal(map[string]string{"code": `
results = await parallel([
    lambda: agent({"id": "first"}),
    lambda: agent({"id": "second"}),
])
return results
`})
	require.NoError(t, err)
	value, err := workflow.Call(agent.NewInvocationContext(ctx, parent), input)
	require.NoError(t, err)
	result := value.(Result)
	var agentResults []agentResult
	require.NoError(t, json.Unmarshal(result.Value, &agentResults))
	require.Len(t, agentResults, 2)
	require.Equal(t, "first final", agentResults[0].Text)
	require.Equal(t, "second final", agentResults[1].Text)

	eventsMu.Lock()
	defer eventsMu.Unlock()
	require.Equal(t, []string{
		"first partial",
		"second partial",
		"second final",
		"first final",
	}, streamed)
	require.Len(t, invocationIDs, 4)
	require.Equal(t, invocationIDs[0], invocationIDs[3])
	require.Equal(t, invocationIDs[1], invocationIDs[2])
	require.NotEqual(t, invocationIDs[0], invocationIDs[1])
}

func TestWorkflowParallelSerializesSharedInstanceID(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	var activeMu sync.Mutex
	active := 0
	maxActive := 0
	reviewer := &testAgent{name: "reviewer"}
	reviewer.runFn = func(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
		activeMu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		activeMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
		activeMu.Lock()
		active--
		activeMu.Unlock()
		ch := make(chan *event.Event, 1)
		ch <- event.NewResponseEvent(inv.InvocationID, "reviewer", &model.Response{
			Done: true,
			Choices: []model.Choice{{Index: 0, Message: model.Message{
				Role: model.RoleAssistant, Content: "done",
			}}},
		})
		close(ch)
		return ch, nil
	}
	workflow, err := NewTool(LocalRunner{}, []agent.Agent{reviewer})
	require.NoError(t, err)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })
	input, err := json.Marshal(map[string]string{"code": `
results = await parallel([
    lambda: agent("first", {"instance_id": "shared"}),
    lambda: agent("second", {"instance_id": "shared"}),
])
return results
`})
	require.NoError(t, err)
	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), input)
	require.NoError(t, err)
	activeMu.Lock()
	defer activeMu.Unlock()
	require.Equal(t, 1, maxActive)
}

func TestWorkflowCoordinatesExplicitAgentAndTool(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: `{"decision":"approved"}`}
	lookup := &testTool{name: "lookup", call: func(_ context.Context, raw []byte) (any, error) {
		require.JSONEq(t, `{"id":"42"}`, string(raw))
		return map[string]any{"title": "Release plan"}, nil
	}}
	runtime := scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		lookupRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "tool-1", Kind: CallKindTool, Name: "lookup", Args: json.RawMessage(`{"id":"42"}`),
		})
		if err != nil {
			return Result{}, err
		}
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent, Name: "reviewer",
			Args: json.RawMessage(`{"input":{"document":` + string(lookupRaw) + `}}`),
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Value: json.RawMessage(`{"review":` + string(reviewRaw) + `}`)}, nil
	}}
	workflow, err := NewTool(runtime, []agent.Agent{reviewer}, WithCodeCallableTools(lookup))
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
		agent.WithInvocationEventFilterKey("app"),
	)
	var persisted []*event.Event
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		persisted = append(persisted, evt)
		return nil
	})

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result, ok := value.(Result)
	require.True(t, ok)
	require.JSONEq(t, `{"review":{"text":"{\"decision\":\"approved\"}","structured":{"decision":"approved"},"session_id":"session-1","history_key":"<history-key>","invocation_id":"<invocation-id>"}}`, normalizeWorkflowResult(t, result.Value))

	require.Len(t, persisted, 2, "child user input and child final response are persisted")
	require.Equal(t, model.RoleUser, persisted[0].Response.Choices[0].Message.Role)
	require.Equal(t, model.RoleAssistant, persisted[1].Response.Choices[0].Message.Role)
	require.Equal(t, `{"document":{"title":"Release plan"}}`, reviewer.lastMessage())
	require.Contains(t, persisted[1].FilterKey, "/dynamic_workflow/")
	require.Contains(t, persisted[1].FilterKey, "/reviewer")
}

func TestWorkflowCallToolHonorsPermissionBoundaries(t *testing.T) {
	reviewer := &testAgent{name: "reviewer"}

	t.Run("tool checker deny skips execution and run policy", func(t *testing.T) {
		sensitive := &permissionTestTool{
			name:     "sensitive",
			decision: tool.DenyPermission("blocked by tool"),
		}
		workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
			raw, err := handler.HandleWorkflowCall(ctx, Call{
				ID: "tool-1", Kind: CallKindTool, Name: "sensitive", Args: json.RawMessage(`{"id":"42"}`),
			})
			return Result{Value: raw}, err
		}}, []agent.Agent{reviewer}, WithCodeCallableTools(sensitive))
		require.NoError(t, err)

		policyCalled := false
		parent := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
			agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
				func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
					policyCalled = true
					return tool.AllowPermission(), nil
				},
			))),
		)

		value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
		require.NoError(t, err)
		result := value.(Result)
		require.JSONEq(t, `{"status":"denied","tool":"sensitive","reason":"blocked by tool"}`, string(result.Value))
		require.False(t, sensitive.called)
		require.False(t, policyCalled)
	})

	t.Run("run policy ask skips execution", func(t *testing.T) {
		sensitive := &permissionTestTool{name: "sensitive", decision: tool.AllowPermission()}
		workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
			raw, err := handler.HandleWorkflowCall(ctx, Call{
				ID: "tool-1", Kind: CallKindTool, Name: "sensitive", Args: json.RawMessage(`{"id":"42"}`),
			})
			return Result{Value: raw}, err
		}}, []agent.Agent{reviewer}, WithCodeCallableTools(sensitive))
		require.NoError(t, err)

		parent := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
			agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
				func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
					require.Equal(t, "sensitive", req.ToolName)
					require.Equal(t, "tool-1", req.ToolCallID)
					require.JSONEq(t, `{"id":"42"}`, string(req.Arguments))
					return tool.AskPermission("needs approval"), nil
				},
			))),
		)

		value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
		require.NoError(t, err)
		result := value.(Result)
		require.JSONEq(t, `{"status":"approval_required","tool":"sensitive","reason":"needs approval"}`, string(result.Value))
		require.False(t, sensitive.called)
	})
}

func TestWorkflowChildAgentToolsHonorParentPermissionPolicy(t *testing.T) {
	called := false
	sensitive := &testTool{name: "sensitive", call: func(context.Context, []byte) (any, error) {
		called = true
		return map[string]any{"ok": true}, nil
	}}
	modelImpl := &toolCallingThenFinalModel{toolName: "sensitive"}
	reviewer := llmagent.New(
		"reviewer",
		llmagent.WithModel(modelImpl),
		llmagent.WithTools([]tool.Tool{sensitive}),
	)
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID:   "agent-1",
			Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	policyCalled := false
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
		agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
			func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
				policyCalled = true
				require.Equal(t, "sensitive", req.ToolName)
				require.Equal(t, "call-1", req.ToolCallID)
				require.JSONEq(t, `{"id":"42"}`, string(req.Arguments))
				return tool.DenyPermission("blocked by run policy"), nil
			},
		))),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"done","session_id":"session-1","history_key":"root/dynamic_workflow/<workflow>/agent-1","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))
	require.True(t, policyCalled)
	require.False(t, called)
	require.GreaterOrEqual(t, modelImpl.callCount(), 2)
}

func TestWorkflowForwardsChildEventsWhenRunnerForwarderIsAttached(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: "done"}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent, Name: "reviewer", Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: json.RawMessage(`true`)}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	var persisted, forwarded []*event.Event
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		persisted = append(persisted, evt)
		return nil
	})
	eventstream.Attach(parent, func(_ context.Context, evt *event.Event) error {
		forwarded = append(forwarded, evt)
		return nil
	})

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	require.Len(t, persisted, 1, "the child input remains a session-only event")
	require.Len(t, forwarded, 1, "child output is forwarded through the runner path")
	require.Equal(t, "reviewer", forwarded[0].Author)
	require.NotEqual(t, parent.InvocationID, forwarded[0].InvocationID)
}

func TestWorkflowRunsDynamicAgentSpecWithSurfacePatch(t *testing.T) {
	lookup := &testTool{name: "lookup", call: func(context.Context, []byte) (any, error) {
		return map[string]any{"ok": true}, nil
	}}
	unused := &testTool{name: "unused", call: func(context.Context, []byte) (any, error) {
		return nil, nil
	}}
	reviewer := &testAgent{
		name:     "reviewer",
		response: "approved",
		tools:    []tool.Tool{lookup, unused},
		skills: &testSkillRepo{summaries: []skill.Summary{
			{Name: "risk"},
			{Name: "style"},
		}},
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"instance_id": "strict-review",
					"instruction": "Be strict.",
					"tools": ["lookup"],
					"skills": ["risk"]
				},
				"input": "review it"
			}`),
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Value: reviewRaw}, nil
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
		agent.WithInvocationEventFilterKey("app"),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"approved","session_id":"session-1","history_key":"app/dynamic_workflow/<workflow>/strict-review","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	rootNode := agent.InvocationSurfaceRootNodeID(child)
	require.NotEmpty(t, rootNode)
	patch, ok := surfacepatch.PatchForNode(child.RunOptions.CustomAgentConfigs, rootNode)
	require.True(t, ok)
	instruction, ok := patch.Instruction()
	require.True(t, ok)
	require.Equal(t, "Be strict.", instruction)
	selectedTools, ok := patch.Tools()
	require.True(t, ok)
	require.Equal(t, []string{"lookup"}, toolNames(selectedTools))
	selectedSkills, ok := patch.SkillRepository()
	require.True(t, ok)
	require.Equal(t, []string{"risk"}, skillNames(skill.SummariesForContext(context.Background(), selectedSkills)))
	require.True(t, patch.SuppressSubAgentTransfer())
}

func TestWorkflowDynamicAgentSpecInheritsTemplateCapabilitiesWhenOmitted(t *testing.T) {
	lookup := &testTool{name: "lookup"}
	search := &testTool{name: "search"}
	reviewer := &testAgent{
		name: "reviewer",
		tools: []tool.Tool{
			lookup,
			search,
			&testTool{name: "workspace_exec"},
		},
		userToolNames: map[string]bool{"lookup": true, "search": true},
		skills: &testSkillRepo{summaries: []skill.Summary{
			{Name: "risk"},
			{Name: "style"},
		}},
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "instruction": "Review the plan."},
				"input": "review it"
			}`),
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Value: reviewRaw}, nil
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	rootNode := agent.InvocationSurfaceRootNodeID(child)
	patch, ok := surfacepatch.PatchForNode(child.RunOptions.CustomAgentConfigs, rootNode)
	require.True(t, ok)
	selectedTools, ok := patch.Tools()
	require.True(t, ok)
	require.Equal(t, []string{"lookup", "search"}, toolNames(selectedTools))
	_, overridesSkills := patch.SkillRepository()
	require.False(t, overridesSkills, "omitted skills must inherit the template repository")
}

func TestWorkflowPlainAgentGetsCapabilityBoundaryPatch(t *testing.T) {
	lookup := &testTool{name: "lookup"}
	reviewer := &testAgent{
		name: "reviewer",
		tools: []tool.Tool{
			lookup,
			&testTool{name: "workspace_exec"},
		},
		userToolNames: map[string]bool{"lookup": true},
		response:      "approved",
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	rootNode := agent.InvocationSurfaceRootNodeID(child)
	require.NotEmpty(t, rootNode)
	patch, ok := surfacepatch.PatchForNode(child.RunOptions.CustomAgentConfigs, rootNode)
	require.True(t, ok)
	selectedTools, ok := patch.Tools()
	require.True(t, ok)
	require.Equal(t, []string{"lookup"}, toolNames(selectedTools))
	require.True(t, patch.SuppressSubAgentTransfer())
	_, overridesInstruction := patch.Instruction()
	require.False(t, overridesInstruction)
}

func TestWorkflowDynamicAgentSpecSetsStructuredOutput(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: `{"approved":true}`}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"structured_output": {
						"name": "quote_review",
						"description": "A strict quote review.",
						"strict": false,
						"schema": {
							"type": "object",
							"required": ["approved"],
							"properties": {"approved": {"type": "boolean"}}
						}
					}
				},
				"input": "review it"
			}`),
		})
		return Result{Value: reviewRaw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	parentRunOptions := parent.RunOptions
	agent.WithStructuredOutputJSONSchema(
		"root_output",
		map[string]any{"type": "object", "properties": map[string]any{"root": map[string]any{"type": "string"}}},
		true,
		"must not leak to child",
	)(&parentRunOptions)
	parent.RunOptions = parentRunOptions
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	require.NotNil(t, child.RunOptions.StructuredOutput)
	require.NotNil(t, child.RunOptions.StructuredOutput.JSONSchema)
	require.Equal(t, "quote_review", child.RunOptions.StructuredOutput.JSONSchema.Name)
	require.False(t, child.RunOptions.StructuredOutput.JSONSchema.Strict)
	require.Equal(t, "A strict quote review.", child.RunOptions.StructuredOutput.JSONSchema.Description)
	require.Equal(t, map[string]any{
		"type":       "object",
		"required":   []any{"approved"},
		"properties": map[string]any{"approved": map[string]any{"type": "boolean"}},
	}, child.RunOptions.StructuredOutput.JSONSchema.Schema)
}

func TestWorkflowDynamicAgentStructuredOutputReachesModelAndResult(t *testing.T) {
	modelImpl := &structuredOutputCaptureModel{content: `{"approved":true,"reason":"stock confirmed"}`}
	reviewer := llmagent.New("reviewer", llmagent.WithModel(modelImpl))
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"structured_output": {
						"type": "object",
						"required": ["approved", "reason"],
						"properties": {
							"approved": {"type": "boolean"},
							"reason": {"type": "string"}
						}
					}
				},
				"input": "review it"
			}`),
		})
		return Result{Value: reviewRaw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"{\"approved\":true,\"reason\":\"stock confirmed\"}","structured":{"approved":true,"reason":"stock confirmed"},"session_id":"session-1","history_key":"root/dynamic_workflow/<workflow>/agent-1","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))

	structuredOutput := modelImpl.latestStructuredOutput()
	require.NotNil(t, structuredOutput)
	require.NotNil(t, structuredOutput.JSONSchema)
	require.Equal(t, "reviewer_output", structuredOutput.JSONSchema.Name)
	require.True(t, structuredOutput.JSONSchema.Strict)
	require.Equal(t, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"approved", "reason"},
		"properties": map[string]any{
			"approved": map[string]any{"type": "boolean"},
			"reason":   map[string]any{"type": "string"},
		},
	}, structuredOutput.JSONSchema.Schema)
}

func TestWorkflowChildDoesNotInheritParentStructuredOutput(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", response: "done"}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent, Name: "reviewer",
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: reviewRaw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	parentRunOptions := parent.RunOptions
	agent.WithStructuredOutputJSONSchema(
		"root_output",
		map[string]any{"type": "object"},
		true,
		"must not leak to child",
	)(&parentRunOptions)
	parent.RunOptions = parentRunOptions
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := reviewer.lastInvocation()
	require.NotNil(t, child)
	require.Nil(t, child.RunOptions.StructuredOutput)
	require.Nil(t, child.RunOptions.StructuredOutputType)
}

func TestWorkflowUsesChildStructuredOutputEvent(t *testing.T) {
	reviewer := &testAgent{
		name:             "reviewer",
		response:         `{"approved":true}`,
		structuredOutput: map[string]any{"approved": true, "sku": "TRAIL-40"},
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		reviewRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent, Name: "reviewer",
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		if err != nil {
			return Result{}, err
		}
		return Result{Value: reviewRaw}, nil
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"{\"approved\":true}","structured":{"approved":true,"sku":"TRAIL-40"},"session_id":"session-1","history_key":"root/dynamic_workflow/<workflow>/reviewer","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))
}

func TestWorkflowRejectsUndeclaredCapabilities(t *testing.T) {
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			Kind: CallKindTool, Name: "not_allowed", Args: json.RawMessage(`{}`),
		})
		return Result{}, err
	}}, []agent.Agent{&testAgent{name: "reviewer"}})
	require.NoError(t, err)
	parent := agent.NewInvocation(agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}))
	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, `tool "not_allowed" is not allowlisted`)
}

func TestWorkflowRejectsUnknownDynamicAgentTool(t *testing.T) {
	reviewer := &testAgent{name: "reviewer", tools: []tool.Tool{&testTool{name: "lookup"}}}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "tools": ["not_allowed"]},
				"input": "review it"
			}`),
		})
		return Result{}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)
	parent := agent.NewInvocation(agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}))

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, `agent template "reviewer" does not allow tool(s): not_allowed`)
}

func TestWorkflowRejectsInvalidDynamicStructuredOutput(t *testing.T) {
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"structured_output": {"schema": ["not", "an", "object"]}
				},
				"input": "review it"
			}`),
		})
		return Result{}, err
	}}, []agent.Agent{&testAgent{name: "reviewer"}})
	require.NoError(t, err)
	parent := agent.NewInvocation(agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}))

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, "structured_output schema must be a JSON object")
}

func TestDecodeAgentSelectorDefaultsStructuredOutput(t *testing.T) {
	spec, dynamic, err := decodeAgentSelector(json.RawMessage(`{
		"template": "reviewer",
		"structured_output": {"schema": {"type": "object"}}
	}`))
	require.NoError(t, err)
	require.True(t, dynamic)
	require.NotNil(t, spec.StructuredOutput)
	require.Equal(t, "reviewer_output", spec.StructuredOutput.Name)
	require.NotNil(t, spec.StructuredOutput.Strict)
	require.True(t, *spec.StructuredOutput.Strict)
}

func TestDynamicStructuredOutputCompletesStrictObjectSchemas(t *testing.T) {
	structuredOutput, err := dynamicStructuredOutput(&StructuredOutputSpec{
		Name: "review",
		Schema: json.RawMessage(`{
			"type":"object",
			"properties": {
				"review": {
					"type":"object",
					"properties":{"approved":{"type":"boolean"}}
				}
			}
		}`),
	})
	require.NoError(t, err)
	require.NotNil(t, structuredOutput)
	require.True(t, structuredOutput.strict)
	require.Equal(t, false, structuredOutput.schema["additionalProperties"])
	require.Equal(t, []string{"review"}, structuredOutput.schema["required"])
	properties := structuredOutput.schema["properties"].(map[string]any)
	nested := properties["review"].(map[string]any)
	require.Equal(t, false, nested["additionalProperties"])
	require.Equal(t, []string{"approved"}, nested["required"])
}

func TestWorkflowDynamicAgentToolsUseUserToolClassification(t *testing.T) {
	reviewer := &testAgent{
		name: "reviewer",
		tools: []tool.Tool{
			&testTool{name: "lookup"},
			&testTool{name: "workspace_exec"},
		},
		userToolNames: map[string]bool{"lookup": true},
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "tools": ["workspace_exec"]},
				"input": "review it"
			}`),
		})
		return Result{}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)
	parent := agent.NewInvocation(agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}))

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, `agent template "reviewer" does not allow tool(s): workspace_exec`)
}

func TestWorkflowToolConfigurationValidation(t *testing.T) {
	reviewer := &testAgent{name: "reviewer"}

	custom, err := NewTool(
		scriptedRuntime{},
		[]agent.Agent{reviewer},
		WithName("run_custom_workflow"),
		WithDescription("Custom workflow description."),
	)
	require.NoError(t, err)
	decl := custom.Declaration()
	require.Equal(t, "run_custom_workflow", decl.Name)
	require.Contains(t, decl.Description, "Custom workflow description.")

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithName(" "))
	require.ErrorContains(t, err, "tool name is required")

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{nil})
	require.ErrorContains(t, err, "agent is required")

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{&testAgent{name: " "}})
	require.ErrorContains(t, err, "agent name is required")

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{
		&testAgent{name: "reviewer"},
		&testAgent{name: "reviewer"},
	})
	require.ErrorContains(t, err, `duplicate agent "reviewer"`)

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithCodeCallableTools(nil))
	require.ErrorContains(t, err, "tool declaration is required")

	_, err = NewTool(
		scriptedRuntime{},
		[]agent.Agent{reviewer},
		WithCodeCallableTools(&testTool{name: "lookup"}, &testTool{name: "lookup"}),
	)
	require.ErrorContains(t, err, `duplicate tool "lookup"`)

	_, err = NewTool(
		scriptedRuntime{},
		[]agent.Agent{reviewer},
		WithCodeCallableTools(&testTool{name: "run_workflow"}),
	)
	require.ErrorContains(t, err, `workflow tool "run_workflow" cannot call itself`)
}

func TestWorkflowCallValidationAndRuntimeError(t *testing.T) {
	workflow, err := NewTool(
		scriptedRuntime{run: func(context.Context, CallHandler) (Result, error) {
			return Result{}, errors.New("runtime failed")
		}},
		[]agent.Agent{&testAgent{name: "reviewer"}},
	)
	require.NoError(t, err)

	_, err = workflow.Call(context.Background(), []byte(`{`))
	require.ErrorContains(t, err, "decode input")

	_, err = workflow.Call(context.Background(), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, "requires a running agent invocation")

	parent := agent.NewInvocation(agent.WithInvocationAgent(&testAgent{name: "root"}))
	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, "requires a parent session")

	parent = agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}),
	)
	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, "runtime failed")
}

func TestWorkflowGatewayToolValidation(t *testing.T) {
	gateway := &workflowGateway{tools: map[string]tool.CallableTool{
		"lookup": &testTool{name: "lookup", call: func(context.Context, []byte) (any, error) {
			return map[string]any{"ok": true}, nil
		}},
		"failing": &testTool{name: "failing", call: func(context.Context, []byte) (any, error) {
			return nil, errors.New("boom")
		}},
		"unencodable": &testTool{name: "unencodable", call: func(context.Context, []byte) (any, error) {
			return func() {}, nil
		}},
	}}

	_, err := gateway.HandleWorkflowCall(context.Background(), Call{Kind: "unknown"})
	require.ErrorContains(t, err, `unsupported call kind "unknown"`)

	_, err = (*workflowGateway)(nil).HandleWorkflowCall(context.Background(), Call{})
	require.ErrorContains(t, err, "nil gateway")

	_, err = gateway.callTool(context.Background(), Call{Kind: CallKindTool, Name: "missing", Args: json.RawMessage(`{}`)})
	require.ErrorContains(t, err, `tool "missing" is not allowlisted`)

	_, err = gateway.callTool(context.Background(), Call{Kind: CallKindTool, Name: "lookup", Args: json.RawMessage(`{`)})
	require.ErrorContains(t, err, "invalid JSON arguments")

	_, err = gateway.callTool(context.Background(), Call{Kind: CallKindTool, Name: "lookup", Args: json.RawMessage(`[]`)})
	require.ErrorContains(t, err, "requires a JSON object argument")

	_, err = gateway.callTool(context.Background(), Call{Kind: CallKindTool, Name: "failing", Args: json.RawMessage(`{}`)})
	require.ErrorContains(t, err, `call tool "failing"`)

	_, err = gateway.callTool(context.Background(), Call{Kind: CallKindTool, Name: "unencodable", Args: json.RawMessage(`{}`)})
	require.ErrorContains(t, err, `encode result from tool "unencodable"`)
}

func TestWorkflowGatewayAgentErrorBranches(t *testing.T) {
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	gateway := &workflowGateway{
		parent:     parent,
		agents:     map[string]agentTemplate{},
		workflow:   "workflow-1",
		toolName:   "run_workflow",
		agentSlots: make(chan struct{}, 1),
	}
	_, err := gateway.callAgent(context.Background(), Call{
		ID: "agent-1", Kind: CallKindAgent, Name: "missing", Args: json.RawMessage(`{"input":"hello"}`),
	})
	require.ErrorContains(t, err, `agent "missing" is not registered`)

	gateway.agents["reviewer"] = agentTemplate{name: "reviewer", agent: &testAgent{
		name: "reviewer",
		runFn: func(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
			return nil, errors.New("agent start failed")
		},
	}}
	_, err = gateway.callAgent(context.Background(), Call{
		ID: "agent-1", Kind: CallKindAgent, Name: "reviewer", Args: json.RawMessage(`{"input":"hello"}`),
	})
	require.ErrorContains(t, err, `run agent "reviewer"`)

	gateway.agents["erroring"] = agentTemplate{name: "erroring", agent: &testAgent{
		name: "erroring",
		runFn: func(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
			ch := make(chan *event.Event, 1)
			ch <- event.NewResponseEvent(inv.InvocationID, "erroring", &model.Response{
				Error: &model.ResponseError{Message: "model failed"},
			})
			close(ch)
			return ch, nil
		},
	}}
	_, err = gateway.callAgent(context.Background(), Call{
		ID: "agent-2", Kind: CallKindAgent, Name: "erroring", Args: json.RawMessage(`{"input":"hello"}`),
	})
	require.ErrorContains(t, err, `collect agent "erroring"`)
}

func TestWorkflowGatewayHelperBranches(t *testing.T) {
	require.NoError(t, (*workflowGateway)(nil).acquireAgentSlot(context.Background()))
	(*workflowGateway)(nil).releaseAgentSlot()
	unlock, err := (*workflowGateway)(nil).lockChildInstance(context.Background(), "")
	require.NoError(t, err)
	unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gateway := &workflowGateway{agentSlots: make(chan struct{})}
	require.ErrorIs(t, gateway.acquireAgentSlot(ctx), context.Canceled)
	unlock, err = gateway.lockChildInstance(context.Background(), "shared")
	require.NoError(t, err)
	_, err = gateway.lockChildInstance(ctx, "shared")
	require.ErrorIs(t, err, context.Canceled)
	unlock()

	parent := agent.NewInvocation(agent.WithInvocationSession(
		&session.Session{ID: "snapshot", AppName: "app", UserID: "user"},
	))
	require.Same(t, parent, parentWithLiveSession(parent))
	live := &session.Session{ID: "live", AppName: "app", UserID: "user"}
	livesession.Attach(parent, live)
	require.Equal(t, "live", parentWithLiveSession(parent).Session.ID)
	require.Nil(t, parentWithLiveSession(nil))

	require.ErrorContains(t, gateway.appendChildUserMessage(context.Background(), nil), "child invocation is nil")
	require.NoError(t, appendSessionEvent(context.Background(), nil, nil))
	require.NoError(t, (*workflowGateway)(nil).appendSessionEvent(context.Background(), nil, nil))
	require.NoError(t, gateway.appendSessionEvent(context.Background(), nil, nil))
	require.NoError(t, appendSessionEvent(context.Background(), agent.NewInvocation(), event.New("inv", "author")))
	require.NoError(t, errorsFromResponse(nil))
	require.Equal(t, "", declarationName(nil))
	require.Equal(t, "null", marshalOrNull(func() {}))

	lookup := &testTool{name: "lookup"}
	require.Nil(t, copyToolMap(nil))
	copied := copyToolMap(map[string]tool.Tool{"lookup": lookup})
	require.Equal(t, lookup, copied["lookup"])
	copied["lookup"] = &testTool{name: "other"}
	require.Equal(t, lookup, copyToolMap(map[string]tool.Tool{"lookup": lookup})["lookup"])

	service := sessioninmemory.NewSessionService()
	sess, err := service.CreateSession(context.Background(), session.Key{
		AppName: "app", UserID: "user", SessionID: "session",
	}, session.StateMap{})
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
	)
	require.NoError(t, appendSessionEvent(context.Background(), inv, event.New(inv.InvocationID, "author")))
}

func TestWorkflowCollectChildResultErrorBranches(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	ch := make(chan *event.Event, 1)
	ch <- event.New(inv.InvocationID, "reviewer", event.WithStructuredOutputPayload(func() {}))
	close(ch)
	_, err := (&workflowGateway{}).collectChildResult(context.Background(), inv, ch)
	require.ErrorContains(t, err, "encode structured output")

	ch = make(chan *event.Event, 1)
	evt := event.NewResponseEvent(inv.InvocationID, "reviewer", &model.Response{
		Done: true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{
			Role: model.RoleAssistant, Content: "done",
		}}},
	})
	ch <- evt
	close(ch)
	eventstream.Attach(inv, func(context.Context, *event.Event) error {
		return errors.New("forward failed")
	})
	_, err = (&workflowGateway{}).collectChildResult(context.Background(), inv, ch)
	require.ErrorContains(t, err, "forward failed")
}

func TestWorkflowCollectChildResultNotifiesForwardedEventCompletion(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	evt := event.NewResponseEvent(inv.InvocationID, "reviewer", &model.Response{
		Done: true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{
			Role: model.RoleAssistant, Content: "done",
		}}},
	})
	evt.RequiresCompletion = true
	ch := make(chan *event.Event, 1)
	ch <- evt
	close(ch)
	forwarded := false
	eventstream.Attach(inv, func(context.Context, *event.Event) error {
		forwarded = true
		return nil
	})

	_, err := (&workflowGateway{}).collectChildResult(context.Background(), inv, ch)
	require.NoError(t, err)
	require.True(t, forwarded)
	require.NoError(t, inv.AddNoticeChannelAndWait(
		context.Background(),
		agent.GetAppendEventNoticeKey(evt.ID),
		100*time.Millisecond,
	))
}

func TestAgentSpecParsingAndSelectionBranches(t *testing.T) {
	_, err := parseAgentCall(Call{Args: json.RawMessage(`{`)})
	require.ErrorContains(t, err, "decode input for agent call")

	_, err = parseAgentCall(Call{Args: json.RawMessage(`{"agent":"reviewer"}`)})
	require.ErrorContains(t, err, "agent call requires JSON input")

	req, err := parseAgentCall(Call{
		ID: "call/1", Args: json.RawMessage(`{"agent":{"template":"reviewer","instance_id":"team/a"},"input":{"task":"x"}}`),
	})
	require.NoError(t, err)
	require.Equal(t, "reviewer", req.templateName)
	require.Equal(t, "team_a", req.instanceID)

	_, _, err = decodeAgentSelector(json.RawMessage(`""`))
	require.ErrorContains(t, err, "agent name is required")

	_, err = decodeAgentOptions(json.RawMessage(`[]`))
	require.ErrorContains(t, err, "agent options must be a mapping or template name")
	_, err = decodeAgentOptions(json.RawMessage(`{`))
	require.ErrorContains(t, err, "agent options must be valid JSON")
	_, err = decodeAgentOptions(json.RawMessage(`{
		"template": "reviewer",
		"structured_output": "{"
	}`))
	require.ErrorContains(t, err, "structured_output must be a JSON object")

	optSpec, err := decodeAgentOptions(json.RawMessage(`" reviewer "`))
	require.NoError(t, err)
	require.Equal(t, "reviewer", optSpec.Template)

	optSpec, err = decodeAgentOptions(json.RawMessage(`{
		"template": "reviewer",
		"schema": {"type": "object", "properties": {"ok": {"type": "boolean"}}}
	}`))
	require.NoError(t, err)
	require.NotNil(t, optSpec.StructuredOutput)
	require.NoError(t, normalizeAgentSpec(&optSpec))
	require.Equal(t, "reviewer_output", optSpec.StructuredOutput.Name)

	optSpec, err = decodeAgentOptions(json.RawMessage(`{
		"template": "reviewer",
		"schema": {"type": "object"},
		"structured_output": {"schema": {"type": "object"}, "name": "explicit"}
	}`))
	require.NoError(t, err)
	require.Equal(t, "explicit", optSpec.StructuredOutput.Name)

	_, err = canonicalStructuredOutput(json.RawMessage(`[]`))
	require.ErrorContains(t, err, "structured_output must be a JSON object")
	_, err = canonicalStructuredOutput(json.RawMessage(`{`))
	require.ErrorContains(t, err, "structured_output must be valid JSON")

	wrapped, err := canonicalStructuredOutput(json.RawMessage(`{"schema":{"type":"object"}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"schema":{"type":"object"}}`, string(wrapped))

	require.ErrorContains(t, normalizeAgentSpec(nil), "agent options are required")
	require.ErrorContains(t, normalizeAgentSpec(&AgentSpec{}), "agent spec template is required")

	spec := AgentSpec{Template: " reviewer ", Tools: []string{" lookup ", "", "lookup"}, Skills: []string{}}
	require.NoError(t, normalizeAgentSpec(&spec))
	require.Equal(t, "reviewer", spec.Template)
	require.Equal(t, []string{"lookup"}, spec.Tools)
	require.Empty(t, spec.Skills)

	gateway := &workflowGateway{parent: parentForAgentSpecTest(), toolName: "run_workflow"}
	tmpl := agentTemplate{
		name:  "reviewer",
		agent: &testAgent{name: "reviewer", tools: []tool.Tool{&testTool{name: "lookup"}}},
		tools: map[string]tool.Tool{"lookup": &testTool{name: "lookup"}},
	}
	selectedTools, err := gateway.selectAgentTools(context.Background(), tmpl, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"lookup"}, toolNames(selectedTools))
	selectedTools, err = gateway.selectAgentTools(context.Background(), tmpl, []string{})
	require.NoError(t, err)
	require.Nil(t, selectedTools)

	plain := &plainTestAgent{name: "plain", tools: []tool.Tool{&testTool{name: "plain_lookup"}}}
	plainTools, userToolNames := templateToolSurface(context.Background(), plain)
	require.Equal(t, []string{"plain_lookup"}, toolNames(plainTools))
	require.Nil(t, userToolNames)
	tmpl = agentTemplate{
		name:  "plain",
		agent: plain,
		tools: map[string]tool.Tool{"plain_lookup": &testTool{name: "plain_lookup"}},
	}
	require.Equal(t, []string{"plain_lookup"}, sortedNames(gateway.selectableAgentTools(context.Background(), tmpl)))

	tmpl = agentTemplate{
		name:  "reviewer",
		agent: &testAgent{name: "reviewer", tools: []tool.Tool{&testTool{name: "lookup"}}},
		tools: map[string]tool.Tool{"lookup": &testTool{name: "lookup"}},
	}
	emptyRepo, err := gateway.selectAgentSkills(context.Background(), tmpl, []string{})
	require.NoError(t, err)
	require.Nil(t, emptyRepo)

	_, err = gateway.selectAgentSkills(context.Background(), tmpl, []string{"risk"})
	require.ErrorContains(t, err, `agent template "reviewer" does not expose skills`)

	repo := &testSkillRepo{summaries: []skill.Summary{{Name: "risk"}, {Name: "style"}}}
	tmpl.agent = &testAgent{name: "reviewer", skills: repo}
	inheritedRepo, err := gateway.selectAgentSkills(context.Background(), tmpl, nil)
	require.NoError(t, err)
	require.Same(t, repo, inheritedRepo)
	disabledRepo, err := gateway.selectAgentSkills(context.Background(), tmpl, []string{})
	require.NoError(t, err)
	require.Empty(t, skill.SummariesForContext(context.Background(), disabledRepo))
	filteredRepo, err := gateway.selectAgentSkills(context.Background(), tmpl, []string{"risk"})
	require.NoError(t, err)
	require.Equal(t, []string{"risk"}, skillNames(skill.SummariesForContext(context.Background(), filteredRepo)))
	_, err = gateway.selectAgentSkills(context.Background(), tmpl, []string{"missing"})
	require.ErrorContains(t, err, `does not allow skill(s): missing`)

	nilRepoAgent := &testAgent{name: "reviewer"}
	tmpl.agent = nilRepoAgent
	selectedRepo, err := gateway.selectAgentSkills(context.Background(), tmpl, nil)
	require.NoError(t, err)
	require.Nil(t, selectedRepo)
	_, err = gateway.selectAgentSkills(context.Background(), tmpl, []string{"risk"})
	require.ErrorContains(t, err, `does not expose skills`)
}

func TestWorkflowCapabilityHelpIncludesSchemas(t *testing.T) {
	lookup := &schemaTestTool{
		name: "lookup",
		input: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
			"query": {Type: "string"},
		}},
		output: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
			"found": {Type: "boolean"},
		}},
	}
	reviewer := &schemaTestAgent{
		name:        "reviewer",
		description: "reviews a plan",
		tools:       []tool.Tool{lookup},
		inputSchema: map[string]any{"type": "string"},
		outputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"approved": map[string]any{"type": "boolean"}},
		},
	}
	registered, err := registerAgentTemplates([]agent.Agent{reviewer})
	require.NoError(t, err)

	help := buildCapabilityHelp(registered, map[string]tool.CallableTool{"lookup": lookup})
	require.Contains(t, help, `template "reviewer": reviews a plan`)
	require.Contains(t, help, "Dynamic narrowing tools: lookup")
	require.Contains(t, help, `Input JSON Schema: {"type":"string"}`)
	require.Contains(t, help, `Default structured output JSON Schema:`)
	require.Contains(t, help, `call_tool("lookup", **json_arguments): lookup tool`)
	require.Contains(t, help, `"query"`)
	require.Contains(t, help, `Output JSON Schema:`)
	require.Contains(t, help, `"found"`)
}

func TestStrictSchemaNormalizationBranches(t *testing.T) {
	output, err := dynamicStructuredOutput(&StructuredOutputSpec{
		Name:   "union",
		Strict: ptrBool(true),
		Schema: json.RawMessage(`{
			"type":["object","null"],
			"properties":{"item":{"anyOf":[{"type":"object","properties":{"id":{"type":"string"}}}]}},
			"$defs":{"nested":{"type":"object","properties":{"ok":{"type":"boolean"}}}},
			"items":{"type":"object","properties":{"value":{"type":"number"}}}
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, false, output.schema["additionalProperties"])
	require.Equal(t, []string{"item"}, output.schema["required"])
	defs := output.schema["$defs"].(map[string]any)
	require.Equal(t, false, defs["nested"].(map[string]any)["additionalProperties"])
	items := output.schema["items"].(map[string]any)
	require.Equal(t, false, items["additionalProperties"])
	normalizeStrictObjectSchemas(nil)
	require.False(t, schemaDeclaresObject(map[string]any{"type": []any{"string", "null"}}))

	_, err = dynamicStructuredOutput(&StructuredOutputSpec{Name: "bad", Schema: json.RawMessage(`[]`)})
	require.ErrorContains(t, err, "structured_output schema must be a JSON object")

	err = normalizeStructuredOutputSpec("reviewer", &StructuredOutputSpec{
		Schema: json.RawMessage(`{`),
	})
	require.ErrorContains(t, err, "must be valid JSON")
	err = normalizeStructuredOutputSpec("reviewer", &StructuredOutputSpec{
		Schema: json.RawMessage(`{"type":"object"}` + strings.Repeat(" ", maxStructuredOutputSchemaBytes)),
	})
	require.ErrorContains(t, err, "exceeds")
}

type scriptedRuntime struct {
	run func(context.Context, CallHandler) (Result, error)
}

func (r scriptedRuntime) ExecuteWorkflow(ctx context.Context, _ Request, handler CallHandler) (Result, error) {
	if r.run == nil {
		return Result{Value: json.RawMessage(`null`)}, nil
	}
	return r.run(ctx, handler)
}

type testAgent struct {
	name             string
	response         string
	tools            []tool.Tool
	userToolNames    map[string]bool
	skills           skill.Repository
	structuredOutput any
	runFn            func(context.Context, *agent.Invocation) (<-chan *event.Event, error)
	mu               sync.Mutex
	messages         []string
	invs             []*agent.Invocation
}

func (a *testAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.messages = append(a.messages, inv.Message.Content)
	a.invs = append(a.invs, inv)
	runFn := a.runFn
	a.mu.Unlock()
	if runFn != nil {
		return runFn(ctx, inv)
	}
	ch := make(chan *event.Event, 2)
	ch <- event.NewResponseEvent(inv.InvocationID, a.name, &model.Response{
		Done: true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{
			Role: model.RoleAssistant, Content: a.response,
		}}},
	})
	if a.structuredOutput != nil {
		ch <- event.New(
			inv.InvocationID,
			a.name,
			event.WithStructuredOutputPayload(a.structuredOutput),
		)
	}
	close(ch)
	return ch, nil
}

func (a *testAgent) Tools() []tool.Tool { return a.tools }
func (a *testAgent) InvocationToolSurface(
	context.Context,
	*agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	return a.tools, a.userToolNames
}
func (a *testAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: a.name + " agent"}
}
func (a *testAgent) SubAgents() []agent.Agent        { return nil }
func (a *testAgent) FindSubAgent(string) agent.Agent { return nil }
func (a *testAgent) InvocationSkillRepository(
	context.Context,
	*agent.Invocation,
) skill.Repository {
	return a.skills
}
func (a *testAgent) lastMessage() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.messages) == 0 {
		return ""
	}
	return a.messages[len(a.messages)-1]
}
func (a *testAgent) lastInvocation() *agent.Invocation {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.invs) == 0 {
		return nil
	}
	return a.invs[len(a.invs)-1]
}

type plainTestAgent struct {
	name  string
	tools []tool.Tool
}

func (a *plainTestAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *plainTestAgent) Tools() []tool.Tool { return a.tools }

func (a *plainTestAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: a.name + " agent"}
}

func (a *plainTestAgent) SubAgents() []agent.Agent { return nil }

func (a *plainTestAgent) FindSubAgent(string) agent.Agent { return nil }

type runOptionsToolSurfaceAgent struct {
	*testAgent
}

func (a *runOptionsToolSurfaceAgent) InvocationToolSurface(
	_ context.Context,
	inv *agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	tools := append([]tool.Tool(nil), a.tools...)
	userToolNames := make(map[string]bool, len(a.userToolNames)+len(inv.RunOptions.AdditionalTools))
	for name, allowed := range a.userToolNames {
		userToolNames[name] = allowed
	}
	for _, tl := range inv.RunOptions.AdditionalTools {
		tools = append(tools, tl)
		if name := declarationName(tl); name != "" {
			userToolNames[name] = true
		}
	}
	return tools, userToolNames
}

func testAgentPartialEvent(invocationID, author, content string) *event.Event {
	return event.NewResponseEvent(invocationID, author, &model.Response{
		IsPartial: true,
		Choices: []model.Choice{{Index: 0, Delta: model.Message{
			Role: model.RoleAssistant, Content: content,
		}}},
	})
}

func testAgentFinalEvent(invocationID, author, content string) *event.Event {
	return event.NewResponseEvent(invocationID, author, &model.Response{
		Done: true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{
			Role: model.RoleAssistant, Content: content,
		}}},
	})
}

type structuredOutputCaptureModel struct {
	content string

	mu               sync.Mutex
	structuredOutput *model.StructuredOutput
}

func (m *structuredOutputCaptureModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.structuredOutput = cloneStructuredOutput(request)
	m.mu.Unlock()

	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		ID:   "structured-output-capture",
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(responses)
	return responses, nil
}

func (m *structuredOutputCaptureModel) Info() model.Info {
	return model.Info{Name: "structured-output-capture"}
}

func (m *structuredOutputCaptureModel) latestStructuredOutput() *model.StructuredOutput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneStructuredOutput(&model.Request{StructuredOutput: m.structuredOutput})
}

type toolCallingThenFinalModel struct {
	toolName string

	mu    sync.Mutex
	calls int
}

func (m *toolCallingThenFinalModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	callNumber := m.calls
	m.mu.Unlock()

	responses := make(chan *model.Response, 1)
	if callNumber == 1 {
		finishReason := "tool_calls"
		responses <- &model.Response{
			ID:   "tool-calling-model",
			Done: true,
			Choices: []model.Choice{{
				Index:        0,
				FinishReason: &finishReason,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call-1",
						Function: model.FunctionDefinitionParam{
							Name:      m.toolName,
							Arguments: []byte(`{"id":"42"}`),
						},
					}},
				},
			}},
		}
	} else {
		responses <- &model.Response{
			ID:   "tool-calling-model",
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("done"),
			}},
		}
	}
	close(responses)
	return responses, nil
}

func (m *toolCallingThenFinalModel) Info() model.Info {
	return model.Info{Name: "tool-calling-model"}
}

func (m *toolCallingThenFinalModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func cloneStructuredOutput(request *model.Request) *model.StructuredOutput {
	if request == nil || request.StructuredOutput == nil {
		return nil
	}
	cloned := *request.StructuredOutput
	if request.StructuredOutput.JSONSchema != nil {
		jsonSchema := *request.StructuredOutput.JSONSchema
		cloned.JSONSchema = &jsonSchema
	}
	return &cloned
}

type testTool struct {
	name string
	call func(context.Context, []byte) (any, error)
}

func (t *testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, Description: t.name + " tool"}
}

func (t *testTool) Call(ctx context.Context, raw []byte) (any, error) {
	if t.call == nil {
		return nil, fmt.Errorf("missing test tool callback")
	}
	return t.call(ctx, raw)
}

type schemaTestTool struct {
	name   string
	input  *tool.Schema
	output *tool.Schema
}

func (t *schemaTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         t.name,
		Description:  t.name + " tool",
		InputSchema:  t.input,
		OutputSchema: t.output,
	}
}

func (t *schemaTestTool) Call(context.Context, []byte) (any, error) {
	return map[string]any{"ok": true}, nil
}

type schemaTestAgent struct {
	name         string
	description  string
	tools        []tool.Tool
	inputSchema  map[string]any
	outputSchema map[string]any
}

func (a *schemaTestAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *schemaTestAgent) Tools() []tool.Tool { return a.tools }

func (a *schemaTestAgent) InvocationToolSurface(
	context.Context,
	*agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	userToolNames := make(map[string]bool, len(a.tools))
	for _, tl := range a.tools {
		if name := declarationName(tl); name != "" {
			userToolNames[name] = true
		}
	}
	return a.tools, userToolNames
}

func (a *schemaTestAgent) Info() agent.Info {
	return agent.Info{
		Name:         a.name,
		Description:  a.description,
		InputSchema:  a.inputSchema,
		OutputSchema: a.outputSchema,
	}
}

func (a *schemaTestAgent) SubAgents() []agent.Agent { return nil }

func (a *schemaTestAgent) FindSubAgent(string) agent.Agent { return nil }

type permissionTestTool struct {
	name     string
	decision tool.PermissionDecision
	err      error
	called   bool
}

func (t *permissionTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, Description: t.name + " tool"}
}

func (t *permissionTestTool) Call(context.Context, []byte) (any, error) {
	t.called = true
	return map[string]any{"ok": true}, nil
}

func (t *permissionTestTool) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return t.decision, t.err
}

func normalizeWorkflowResult(t *testing.T, raw []byte) string {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(raw, &value))
	review := value["review"].(map[string]any)
	review["history_key"] = "<history-key>"
	review["invocation_id"] = "<invocation-id>"
	normalized, err := json.Marshal(value)
	require.NoError(t, err)
	return string(normalized)
}

func normalizeSingleAgentResult(t *testing.T, raw []byte) string {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(raw, &value))
	value["history_key"] = normalizeWorkflowHistoryKey(value["history_key"].(string))
	value["invocation_id"] = "<invocation-id>"
	normalized, err := json.Marshal(value)
	require.NoError(t, err)
	return string(normalized)
}

func normalizeWorkflowHistoryKey(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		if part == "dynamic_workflow" && i+1 < len(parts) {
			parts[i+1] = "<workflow>"
		}
	}
	return strings.Join(parts, "/")
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, declarationName(t))
	}
	return names
}

func skillNames(summaries []skill.Summary) []string {
	names := make([]string, 0, len(summaries))
	for _, s := range summaries {
		names = append(names, s.Name)
	}
	return names
}

func parentForAgentSpecTest() *agent.Invocation {
	return agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "s", AppName: "a", UserID: "u"}),
	)
}

func ptrBool(v bool) *bool { return &v }

type testSkillRepo struct {
	summaries []skill.Summary
}

func (r *testSkillRepo) Summaries() []skill.Summary {
	return append([]skill.Summary(nil), r.summaries...)
}

func (r *testSkillRepo) Get(name string) (*skill.Skill, error) {
	for _, summary := range r.summaries {
		if summary.Name == name {
			return &skill.Skill{Summary: summary}, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

func (r *testSkillRepo) Path(name string) (string, error) {
	for _, summary := range r.summaries {
		if summary.Name == name {
			return "/tmp/" + name, nil
		}
	}
	return "", fmt.Errorf("skill %q not found", name)
}
