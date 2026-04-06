//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package subtask provides the subtask tool that lets an agent delegate
// an ephemeral sub-task at runtime. The sub-agent reuses the parent
// agent's capabilities (model, tools) but runs in an isolated context
// scope, preventing intermediate reasoning and tool-call noise from
// polluting the parent's context window.
package subtask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// excludedToolNames lists the tool names that should be stripped from
// the sub-agent's tool set to prevent recursive delegation or
// incompatible semantics inside a tool call.
var excludedToolNames = map[string]struct{}{
	ToolName:                  {},
	transfer.TransferToolName: {},
}

// toolScopedAgent wraps a parent agent but presents a restricted tool
// list. All other methods delegate to the parent unchanged. This lets
// the subtask sub-agent run with the same model, callbacks, session, etc.
// while only seeing the tools we explicitly allow.
//
// Optional interfaces checked via type assertion on invocation.Agent
// (ToolFilterProvider, UserToolsProvider, agent.CodeExecutor) are
// explicitly forwarded so capabilities are not silently lost.
type toolScopedAgent struct {
	agent.Agent
	scopedTools []tool.Tool
}

func (a *toolScopedAgent) Tools() []tool.Tool                        { return a.scopedTools }
func (a *toolScopedAgent) FilterTools(_ context.Context) []tool.Tool { return a.scopedTools }
func (a *toolScopedAgent) UserTools() []tool.Tool                    { return a.scopedTools }

func (a *toolScopedAgent) CodeExecutor() codeexecutor.CodeExecutor {
	if ce, ok := a.Agent.(agent.CodeExecutor); ok {
		return ce.CodeExecutor()
	}
	return nil
}

const (
	// ToolName is the name exposed to the LLM.
	ToolName = "subtask"
)

// SubtaskRequest is the JSON schema the LLM fills in.
type SubtaskRequest struct {
	Request        string   `json:"request"`
	Instruction    string   `json:"instruction,omitempty"`
	Model          string   `json:"model,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	InheritContext bool     `json:"inherit_context,omitempty"`
}

// SubtaskTool lets the agent run a subtask in an isolated context scope using the
// parent agent's own capabilities. This is the "ephemeral sub-agent"
// pattern: clone the parent, isolate the context, execute, return result.
type SubtaskTool struct {
	name string
}

// SubtaskOption configures a SubtaskTool.
type SubtaskOption func(*SubtaskTool)

// WithSubtaskName overrides the default tool name.
func WithSubtaskName(name string) SubtaskOption {
	return func(t *SubtaskTool) { t.name = name }
}

// NewSubtaskTool creates a new subtask tool.
func NewSubtaskTool(opts ...SubtaskOption) *SubtaskTool {
	t := &SubtaskTool{name: ToolName}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Declaration implements tool.Tool.
func (t *SubtaskTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: t.name,
		Description: "Run a subtask in an isolated context using a sub-agent. " +
			"The sub-agent inherits your capabilities but works in a clean context window, " +
			"so its intermediate reasoning and tool calls do not pollute your main context. " +
			"Use this for complex sub-tasks where you care about the result, not the process.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"request": {
					Type:        "string",
					Description: "The subtask for the sub-agent to accomplish.",
				},
				"instruction": {
					Type:        "string",
					Description: "Optional system prompt override for the sub-agent (e.g. a specialist persona).",
				},
				"model": {
					Type:        "string",
					Description: "Optional model name override for the sub-agent.",
				},
				"tools": {
					Type:        "array",
					Description: "Optional subset of tool names the sub-agent may use. Defaults to all available tools (agent management tools are excluded).",
					Items:       &tool.Schema{Type: "string"},
				},
				"inherit_context": {
					Type: "boolean",
					Description: "If true, the sub-agent can see your conversation history " +
						"(useful when the subtask needs background context). " +
						"If false (default), the sub-agent starts with a completely clean context.",
				},
			},
			Required: []string{"request"},
		},
	}
}

// Call implements tool.CallableTool.
func (t *SubtaskTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var req SubtaskRequest
	if err := json.Unmarshal(jsonArgs, &req); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(req.Request) == "" {
		return "request is required", nil
	}

	parentInv, ok := agent.InvocationFromContext(ctx)
	if !ok || parentInv == nil {
		return "no invocation context available", nil
	}
	parentAgent := parentInv.Agent
	if parentAgent == nil {
		return "no parent agent available", nil
	}
	if parentInv.Session == nil {
		return "no session available for subtask isolation", nil
	}

	if err := flush.Invoke(ctx, parentInv); err != nil {
		return "", fmt.Errorf("flush parent session: %w", err)
	}

	childKey := "subtask-" + uuid.NewString()
	if req.InheritContext {
		if pk := parentInv.GetEventFilterKey(); pk != "" {
			childKey = pk + agent.EventFilterKeyDelimiter + childKey
		}
	}
	ro := parentInv.RunOptions
	if req.Instruction != "" {
		ro.Instruction = req.Instruction
	}
	if req.Model != "" {
		ro.ModelName = req.Model
	}

	childAgent := &toolScopedAgent{
		Agent:       parentAgent,
		scopedTools: scopeTools(parentAgent.Tools(), req.Tools),
	}

	subInv := parentInv.Clone(
		agent.WithInvocationAgent(childAgent),
		agent.WithInvocationMessage(model.NewUserMessage(req.Request)),
		agent.WithInvocationEventFilterKey(childKey),
		agent.WithInvocationRunOptions(ro),
	)

	subCtx := agent.NewInvocationContext(ctx, subInv)
	evCh, err := agent.RunWithPlugins(subCtx, subInv, childAgent)
	if err != nil {
		return "", fmt.Errorf("subtask agent run failed: %w", err)
	}

	return collectResponse(wrapCallSemantics(subCtx, subInv, evCh))
}

// wrapCallSemantics mirrors child events into the shared session and fires
// completion notices so the child's multi-turn flow can proceed.
//
// In the Call path, child events ONLY flow through this wrapper (they are
// NOT seen by the parent Runner's event loop). Therefore we ALWAYS mirror
// and notify here — there is no double-persistence risk. This matches
// AgentTool.wrapWithCallSemantics for the Call path.
//
// Note: the appender.IsAttached / shouldDeferStreamCompletion check is only
// relevant for the StreamableCall path (where events are forwarded into the
// parent Runner's channel). It must NOT be applied to the Call path.
func wrapCallSemantics(
	ctx context.Context,
	inv *agent.Invocation,
	src <-chan *event.Event,
) <-chan *event.Event {
	if inv == nil || inv.Session == nil {
		return src
	}

	ensureUserMessage(ctx, inv)

	out := make(chan *event.Event)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)
		for evt := range src {
			if evt == nil {
				continue
			}
			if shouldMirror(evt) {
				appendEvent(ctx, inv, persistable(evt))
			}
			if evt.RequiresCompletion {
				key := agent.GetAppendEventNoticeKey(evt.ID)
				if err := inv.NotifyCompletion(ctx, key); err != nil {
					log.Errorf("subtask: notify completion failed: %v", err)
				}
			}
			out <- evt
		}
	}(runCtx)
	return out
}

func ensureUserMessage(ctx context.Context, inv *agent.Invocation) {
	if inv.Message.Role != model.RoleUser || inv.Message.Content == "" {
		return
	}
	inv.Session.EventMu.RLock()
	for i := range inv.Session.Events {
		if inv.Session.Events[i].IsUserMessage() {
			inv.Session.EventMu.RUnlock()
			return
		}
	}
	inv.Session.EventMu.RUnlock()

	evt := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Done:    false,
		Choices: []model.Choice{{Index: 0, Message: inv.Message}},
	})
	agent.InjectIntoEvent(inv, evt)
	appendEvent(ctx, inv, evt)
}

func appendEvent(ctx context.Context, inv *agent.Invocation, evt *event.Event) {
	if inv == nil || inv.Session == nil || evt == nil {
		return
	}
	ok, err := appender.Invoke(ctx, inv, evt)
	if ok {
		if err != nil {
			log.Errorf("subtask: session append failed: %v", err)
			if evt.ID == "" || !sessionHasEventID(inv, evt.ID) {
				inv.Session.UpdateUserSession(evt)
			}
		}
		return
	}
	inv.Session.UpdateUserSession(evt)
}

func sessionHasEventID(inv *agent.Invocation, eventID string) bool {
	inv.Session.EventMu.RLock()
	defer inv.Session.EventMu.RUnlock()
	for i := range inv.Session.Events {
		if inv.Session.Events[i].ID == eventID {
			return true
		}
	}
	return false
}

func shouldMirror(evt *event.Event) bool {
	if len(evt.StateDelta) > 0 {
		return true
	}
	if evt.Response == nil || evt.IsPartial {
		return false
	}
	return evt.IsValidContent()
}

func persistable(evt *event.Event) *event.Event {
	if evt.Response == nil || !evt.Done || evt.Object != graph.ObjectTypeGraphExecution {
		return evt
	}
	cp := *evt
	cp.Response = evt.Response.Clone()
	cp.Response.Choices = nil
	return &cp
}

// scopeTools builds the tool list for the subtask sub-agent.
// It always strips excluded tools (subtask, transfer) to prevent
// recursion and incompatible semantics. When allowNames is non-empty,
// only tools whose names appear in that list (AND are not excluded)
// are kept.
func scopeTools(all []tool.Tool, allowNames []string) []tool.Tool {
	if len(allowNames) > 0 {
		nameSet := make(map[string]struct{}, len(allowNames))
		for _, n := range allowNames {
			nameSet[n] = struct{}{}
		}
		var out []tool.Tool
		for _, t := range all {
			name := t.Declaration().Name
			if _, blocked := excludedToolNames[name]; blocked {
				continue
			}
			if _, ok := nameSet[name]; ok {
				out = append(out, t)
			}
		}
		return out
	}

	var out []tool.Tool
	for _, t := range all {
		if _, blocked := excludedToolNames[t.Declaration().Name]; !blocked {
			out = append(out, t)
		}
	}
	return out
}

func collectResponse(evCh <-chan *event.Event) (string, error) {
	var b strings.Builder
	for ev := range evCh {
		if ev.Error != nil {
			return "", fmt.Errorf("subtask error: %s", ev.Error.Message)
		}
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			c := ev.Response.Choices[0]
			if c.Message.Role == model.RoleAssistant && c.Message.Content != "" {
				b.WriteString(c.Message.Content)
			}
		}
	}
	return b.String(), nil
}
