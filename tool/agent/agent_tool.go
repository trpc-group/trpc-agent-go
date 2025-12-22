//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent provides agent tool implementations for the agent system.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Tool wraps an agent as a tool that can be called within a larger application.
// The agent's input schema is used to define the tool's input parameters, and
// the agent's output is returned as the tool's result.
type Tool struct {
	agent             agent.Agent
	skipSummarization bool
	streamInner       bool
	historyScope      HistoryScope
	name              string
	description       string
	inputSchema       *tool.Schema
	outputSchema      *tool.Schema
}

// Option is a function that configures an AgentTool.
type Option func(*agentToolOptions)

// agentToolOptions holds the configuration options for AgentTool.
type agentToolOptions struct {
	skipSummarization bool
	streamInner       bool
	historyScope      HistoryScope
}

// WithSkipSummarization sets whether to skip summarization of the agent output.
func WithSkipSummarization(skip bool) Option {
	return func(opts *agentToolOptions) {
		opts.skipSummarization = skip
	}
}

// WithStreamInner controls whether the AgentTool should forward inner agent
// streaming events up to the parent flow. When false, the flow will treat the
// tool as callable-only (no inner streaming in the parent transcript).
func WithStreamInner(enabled bool) Option {
	return func(opts *agentToolOptions) {
		opts.streamInner = enabled
	}
}

// HistoryScope controls whether and how AgentTool inherits parent history.
//   - HistoryScopeIsolated: keep child events isolated; do not inherit parent history.
//   - HistoryScopeParentBranch: inherit parent branch history by using a hierarchical
//     filter key "parent/child-uuid" so that content processors see parent events via
//     prefix matching while keeping child events in a separate sub-branch.
type HistoryScope int

// HistoryScopeIsolated: keep child events isolated; do not inherit parent history.
// HistoryScopeParentBranch: inherit parent branch history by using a hierarchical
// filter key "parent/child-uuid" so that content processors see parent events via
// prefix matching while keeping child events in a separate sub-branch.
const (
	HistoryScopeIsolated HistoryScope = iota
	HistoryScopeParentBranch
)

// WithHistoryScope sets the history inheritance behavior for AgentTool.
func WithHistoryScope(scope HistoryScope) Option {
	return func(opts *agentToolOptions) {
		opts.historyScope = scope
	}
}

// NewTool creates a new Tool that wraps the given agent.
//
// Note: The tool name is derived from the agent's info (agent.Info().Name).
// The agent name must comply with LLM API requirements for compatibility.
// Some APIs (e.g., Kimi, DeepSeek) enforce strict naming patterns:
// - Must match pattern: ^[a-zA-Z0-9_-]+$
// - Cannot contain Chinese characters, parentheses, or special symbols
//
// Best practice: Use ^[a-zA-Z0-9_-]+ only to ensure maximum compatibility.
func NewTool(agent agent.Agent, opts ...Option) *Tool {
	// Default to allowing summarization so the parent agent can perform its
	// normal post-tool reasoning unless opt-out is requested.
	options := &agentToolOptions{skipSummarization: false, historyScope: HistoryScopeIsolated}
	for _, opt := range opts {
		opt(options)
	}
	info := agent.Info()

	// Use the agent's input schema if available, otherwise fall back to default.
	var inputSchema *tool.Schema
	if info.InputSchema != nil {
		// Convert the agent's input schema to tool.Schema format.
		inputSchema = convertMapToToolSchema(info.InputSchema)
	} else {
		// Generate default input schema for the agent tool.
		inputSchema = &tool.Schema{
			Type:        "object",
			Description: "Input for the agent tool",
			Properties: map[string]*tool.Schema{
				"request": {
					Type:        "string",
					Description: "The request to send to the agent",
				},
			},
			Required: []string{"request"},
		}
	}
	var outputSchema *tool.Schema
	if info.OutputSchema != nil {
		outputSchema = convertMapToToolSchema(info.OutputSchema)
	} else {
		outputSchema = &tool.Schema{
			Type:        "string",
			Description: "The response from the agent",
		}
	}
	return &Tool{
		agent:             agent,
		skipSummarization: options.skipSummarization,
		streamInner:       options.streamInner,
		historyScope:      options.historyScope,
		name:              info.Name,
		description:       info.Description,
		inputSchema:       inputSchema,
		outputSchema:      outputSchema,
	}
}

// Call executes the agent tool with the provided JSON arguments.
func (at *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	message := model.NewUserMessage(string(jsonArgs))

	// Prefer to reuse parent invocation + session so the child can see parent
	// history according to the configured history scope.
	if parentInv, ok := agent.InvocationFromContext(ctx); ok && parentInv != nil && parentInv.Session != nil {
		return at.callWithParentInvocation(ctx, parentInv, message)
	}

	// Fallback: isolated in-memory run when parent invocation is not available.
	return at.callWithIsolatedRunner(ctx, message)
}

// callWithParentInvocation executes the agent using parent invocation context.
// This allows the child agent to inherit parent history based on the configured
// history scope.
func (at *Tool) callWithParentInvocation(
	ctx context.Context,
	parentInv *agent.Invocation,
	message model.Message,
) (string, error) {
	// If the parent invocation does not have a session, fall back to isolated mode.
	if parentInv.Session == nil {
		return at.callWithIsolatedRunner(ctx, message)
	}
	// Flush all events emitted before this tool call so that the snapshot sees all events.
	if err := flush.Invoke(ctx, parentInv); err != nil {
		return "", fmt.Errorf("flush parent invocation session: %w", err)
	}
	// Build child filter key based on history scope.
	childKey := at.buildChildFilterKey(parentInv)
	// Clone parent invocation with child-specific settings.
	subInv := parentInv.Clone(
		agent.WithInvocationAgent(at.agent),
		agent.WithInvocationMessage(message),
		agent.WithInvocationEventFilterKey(childKey),
	)

	// Run the agent and collect response.
	evCh, err := at.agent.Run(agent.NewInvocationContext(ctx, subInv), subInv)
	if err != nil {
		return "", fmt.Errorf("failed to run agent: %w", err)
	}
	childCtx := agent.NewInvocationContext(ctx, subInv)
	return at.collectResponse(at.wrapWithCallSemantics(childCtx, subInv, evCh))
}

// wrapWithCompletion consumes events, notifies completion when required, and forwards to a new channel.
func (at *Tool) wrapWithCompletion(ctx context.Context, inv *agent.Invocation, src <-chan *event.Event) <-chan *event.Event {
	if inv == nil {
		return src
	}
	out := make(chan *event.Event)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)
		for evt := range src {
			if evt != nil && evt.RequiresCompletion {
				completionID := agent.GetAppendEventNoticeKey(evt.ID)
				if err := inv.NotifyCompletion(ctx, completionID); err != nil {
					log.Errorf("AgentTool: notify completion failed: %v", err)
				}
			}
			out <- evt
		}
	}(runCtx)
	return out
}

// wrapWithCallSemantics consumes events from a child agent invocation that is
// executed without a Runner. It mirrors persisted events into the shared
// Session so multi-step tool calling can work, and notifies completion when
// required.
func (at *Tool) wrapWithCallSemantics(
	ctx context.Context,
	inv *agent.Invocation,
	src <-chan *event.Event,
) <-chan *event.Event {
	if inv == nil || inv.Session == nil {
		return at.wrapWithCompletion(ctx, inv, src)
	}

	at.ensureUserMessageForCall(ctx, inv)

	out := make(chan *event.Event)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)
		for evt := range src {
			if evt != nil {
				if shouldMirrorEventToSession(evt) {
					at.appendEvent(
						ctx, inv, persistableSessionEvent(evt),
					)
				}
				if evt.RequiresCompletion {
					completionID :=
						agent.GetAppendEventNoticeKey(evt.ID)
					if err := inv.NotifyCompletion(
						ctx, completionID,
					); err != nil {
						log.Errorf(
							"AgentTool: notify completion failed: %v",
							err,
						)
					}
				}
			}
			out <- evt
		}
	}(runCtx)
	return out
}

func (at *Tool) ensureUserMessageForCall(
	ctx context.Context,
	inv *agent.Invocation,
) {
	if inv == nil || inv.Session == nil {
		return
	}
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
	at.appendEvent(ctx, inv, evt)
}

func (at *Tool) appendEvent(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
) {
	if inv == nil || inv.Session == nil || evt == nil {
		return
	}
	ok, err := appender.Invoke(ctx, inv, evt)
	if ok {
		if err != nil {
			log.Errorf(
				"AgentTool: session append failed: %v", err,
			)
			if evt.ID == "" || !sessionHasEventID(inv, evt.ID) {
				inv.Session.UpdateUserSession(evt)
			}
		}
		return
	}
	inv.Session.UpdateUserSession(evt)
}

func sessionHasEventID(inv *agent.Invocation, eventID string) bool {
	if inv == nil || inv.Session == nil || eventID == "" {
		return false
	}
	inv.Session.EventMu.RLock()
	defer inv.Session.EventMu.RUnlock()

	for i := range inv.Session.Events {
		if inv.Session.Events[i].ID == eventID {
			return true
		}
	}
	return false
}

func shouldMirrorEventToSession(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	if len(evt.StateDelta) > 0 {
		return true
	}
	if evt.Response == nil {
		return false
	}
	if evt.IsPartial {
		return false
	}
	return evt.IsValidContent()
}

func persistableSessionEvent(evt *event.Event) *event.Event {
	if !isGraphCompletionEvent(evt) {
		return evt
	}
	copyEvt := *evt
	if evt.Response != nil {
		copyEvt.Response = evt.Response.Clone()
		copyEvt.Response.Choices = nil
	}
	return &copyEvt
}

func isGraphCompletionEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	return evt.Done &&
		evt.Object == graph.ObjectTypeGraphExecution
}

// callWithIsolatedRunner executes the agent in an isolated environment using
// an in-memory session service. This is used as a fallback when no parent
// invocation context is available.
func (at *Tool) callWithIsolatedRunner(
	ctx context.Context,
	message model.Message,
) (string, error) {
	r := runner.NewRunner(
		at.name,
		at.agent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	evCh, err := r.Run(ctx, "tool_user", "tool_session", message)
	if err != nil {
		return "", fmt.Errorf("failed to run agent: %w", err)
	}
	return at.collectResponse(evCh)
}

// buildChildFilterKey constructs a child filter key based on the history scope
// configuration. For HistoryScopeParentBranch, it creates a hierarchical key
// that allows the child to inherit parent history.
func (at *Tool) buildChildFilterKey(parentInv *agent.Invocation) string {
	childKey := at.agent.Info().Name + "-" + uuid.NewString()
	if at.historyScope == HistoryScopeParentBranch {
		if pk := parentInv.GetEventFilterKey(); pk != "" {
			childKey = pk + agent.EventFilterKeyDelimiter + childKey
		}
	}
	return childKey
}

// collectResponse collects and concatenates assistant messages from the event
// channel, returning the complete response text.
func (at *Tool) collectResponse(evCh <-chan *event.Event) (string, error) {
	var response strings.Builder
	for ev := range evCh {
		if ev.Error != nil {
			return "", fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]
			if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
				response.WriteString(choice.Message.Content)
			}
		}
	}
	return response.String(), nil
}

// StreamableCall executes the agent tool with streaming support and returns a stream reader.
// It runs the wrapped agent and forwards its streaming text output as chunks.
// The returned chunks' Content are plain strings representing incremental text.
func (at *Tool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	stream := tool.NewStream(64)

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer stream.Writer.Close()

		// Try to reuse parent invocation for consistent invocationId/session
		parentInv, ok := agent.InvocationFromContext(ctx)
		message := model.NewUserMessage(string(jsonArgs))

		if ok && parentInv != nil && parentInv.Session != nil {
			if err := flush.Invoke(ctx, parentInv); err != nil {
				err := fmt.Errorf("flush parent invocation session failed: %w", err)
				stream.Writer.Send(tool.StreamChunk{Content: err.Error()}, err)
				return
			}
			childKey := at.buildChildFilterKey(parentInv)
			subInv := parentInv.Clone(
				agent.WithInvocationAgent(at.agent),
				agent.WithInvocationMessage(message),
				// Reset event filter key to the sub-agent name so that content
				// processors fetch session messages belonging to the sub-agent,
				// not the parent agent. Use unique FilterKey to prevent cross-invocation event pollution.
				agent.WithInvocationEventFilterKey(childKey),
			)

			subCtx := agent.NewInvocationContext(ctx, subInv)
			evCh, err := at.agent.Run(subCtx, subInv)
			if err != nil {
				_ = stream.Writer.Send(tool.StreamChunk{Content: fmt.Sprintf("agent tool run error: %v", err)}, nil)
				return
			}
			wrapped := at.wrapWithCompletion(subCtx, subInv, evCh)

			for ev := range wrapped {
				if stream.Writer.Send(tool.StreamChunk{Content: ev}, nil) {
					return
				}
			}
			return
		}

		// Fallback: run with ad-hoc runner
		r := runner.NewRunner(
			at.name,
			at.agent,
			runner.WithSessionService(inmemory.NewSessionService()),
		)
		evCh, err := r.Run(ctx, "tool_user", "tool_session", message)
		if err != nil {
			_ = stream.Writer.Send(tool.StreamChunk{Content: fmt.Sprintf("agent tool run error: %v", err)}, nil)
			return
		}
		for ev := range evCh {
			if ev != nil {
				if stream.Writer.Send(tool.StreamChunk{Content: ev}, nil) {
					return
				}
			}
		}
	}(runCtx)

	return stream.Reader, nil
}

// SkipSummarization exposes whether the AgentTool prefers skipping
// outer-agent summarization after its tool.response.
func (at *Tool) SkipSummarization() bool { return at.skipSummarization }

// StreamInner exposes whether this AgentTool prefers the flow to treat it as
// streamable (forwarding inner deltas) versus callable-only.
func (at *Tool) StreamInner() bool { return at.streamInner }

// Declaration returns the tool's declaration information.
//
// Note: The tool name must comply with LLM API requirements.
// Some APIs (e.g., Kimi, DeepSeek) enforce strict naming patterns:
// - Must match pattern: ^[a-zA-Z0-9_-]+$
// - Cannot contain Chinese characters, parentheses, or special symbols
//
// Best practice: Use ^[a-zA-Z0-9_-]+ only to ensure maximum compatibility.
func (at *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         at.name,
		Description:  at.description,
		InputSchema:  at.inputSchema,
		OutputSchema: at.outputSchema,
	}
}

// convertMapToToolSchema converts a map[string]any schema to tool.Schema format.
// This function handles the conversion from the agent's input schema format to the tool schema format.
func convertMapToToolSchema(schema map[string]any) *tool.Schema {
	if schema == nil {
		return nil
	}
	bs, err := json.Marshal(schema)
	if err != nil {
		log.Errorf("json marshal schema error: %+v", err)
		return nil
	}
	result := &tool.Schema{}
	if err := json.Unmarshal(bs, result); err != nil {
		log.Errorf("json unmarshal schema error: %+v", err)
		return nil
	}
	return result
}
