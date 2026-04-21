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
	"trpc.group/trpc-go/trpc-agent-go/internal/teamtrace"
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
	agent                  agent.Agent
	skipSummarization      bool
	streamInner            bool
	structuredStreamErrors bool
	historyScope           HistoryScope
	name                   string
	description            string
	inputSchema            *tool.Schema
	outputSchema           *tool.Schema
}

// Option is a function that configures an AgentTool.
type Option func(*agentToolOptions)

// agentToolOptions holds the configuration options for AgentTool.
type agentToolOptions struct {
	skipSummarization      bool
	streamInner            bool
	structuredStreamErrors bool
	historyScope           HistoryScope
	description            *string
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

// WithStructuredStreamErrors controls whether AgentTool opts into structured
// error chunks when it is executed through the framework as a streamable tool.
func WithStructuredStreamErrors(enabled bool) Option {
	return func(opts *agentToolOptions) {
		opts.structuredStreamErrors = enabled
	}
}

// WithDescription sets the description exposed by the agent tool declaration.
func WithDescription(description string) Option {
	return func(opts *agentToolOptions) {
		copiedDescription := description
		opts.description = &copiedDescription
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
	options := &agentToolOptions{
		skipSummarization:      false,
		structuredStreamErrors: false,
		historyScope:           HistoryScopeIsolated,
	}
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
	description := info.Description
	if options.description != nil {
		description = *options.description
	}
	return &Tool{
		agent:                  agent,
		skipSummarization:      options.skipSummarization,
		streamInner:            options.streamInner,
		structuredStreamErrors: options.structuredStreamErrors,
		historyScope:           options.historyScope,
		name:                   info.Name,
		description:            description,
		inputSchema:            inputSchema,
		outputSchema:           outputSchema,
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
	subInv := parentInv.Clone(at.childInvocationOptions(parentInv, message, childKey)...)

	// Run the agent and collect response.
	subCtx := agent.NewInvocationContext(ctx, subInv)
	evCh, err := agent.RunWithPlugins(subCtx, subInv, at.agent)
	if err != nil {
		return "", fmt.Errorf("failed to run agent: %w", err)
	}
	return at.collectResponse(subInv, at.wrapWithCallSemantics(subCtx, subInv, evCh))
}

func (at *Tool) surfaceRootNodeIDForParentInvocation(
	parentInv *agent.Invocation,
) string {
	if parentInv == nil || at.agent == nil {
		return ""
	}
	rootNodeID := teamtrace.MemberTraceRootForInvocation(parentInv)
	if rootNodeID == "" {
		return ""
	}
	return teamtrace.MemberNodeID(rootNodeID, at.agent.Info().Name)
}

func (at *Tool) childInvocationOptions(
	parentInv *agent.Invocation,
	message model.Message,
	childKey string,
) []agent.InvocationOptions {
	invocationOpts := []agent.InvocationOptions{
		agent.WithInvocationAgent(at.agent),
		agent.WithInvocationMessage(message),
		agent.WithInvocationEventFilterKey(childKey),
	}
	if parentInv == nil {
		return invocationOpts
	}
	if surfaceRootNodeID := at.surfaceRootNodeIDForParentInvocation(parentInv); surfaceRootNodeID != "" {
		invocationOpts = append(
			invocationOpts,
			func(inv *agent.Invocation) {
				agent.SetInvocationSurfaceRootNodeID(inv, surfaceRootNodeID)
			},
		)
	}
	return invocationOpts
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
		var pendingVisibleCompletion *event.Event
		for evt := range src {
			if evt != nil {
				pendingVisibleCompletion = at.updatePendingVisibleCompletionForSession(
					ctx,
					inv,
					pendingVisibleCompletion,
					evt,
				)
				if shouldSuppressGraphExecutorBarrierEvent(inv, evt) {
					at.completeSuppressedBarrierEvent(
						ctx,
						inv,
						evt,
						&pendingVisibleCompletion,
					)
					continue
				}
				if shouldMirrorEventToSession(evt) {
					persistedEvent := persistableSessionEvent(evt)
					if shouldDelayVisibleCompletionSessionMirror(persistedEvent) {
						pendingVisibleCompletion = at.replacePendingVisibleCompletionForSession(
							ctx,
							inv,
							pendingVisibleCompletion,
							persistedEvent,
						)
					} else {
						at.appendEvent(ctx, inv, persistedEvent)
					}
				}
				if evt.RequiresCompletion {
					if pendingVisibleCompletion != nil {
						at.appendEvent(ctx, inv, pendingVisibleCompletion)
						pendingVisibleCompletion = nil
					}
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
		if pendingVisibleCompletion != nil {
			at.appendEvent(ctx, inv, pendingVisibleCompletion)
		}
	}(runCtx)
	return out
}

func (at *Tool) updatePendingVisibleCompletionForSession(
	ctx context.Context,
	inv *agent.Invocation,
	pending *event.Event,
	evt *event.Event,
) *event.Event {
	if pending == nil || evt == nil {
		return pending
	}
	if shouldDelayVisibleCompletionSessionMirror(evt) {
		return pending
	}
	if evt.Error != nil {
		at.appendPendingVisibleCompletionState(ctx, inv, pending)
		return nil
	}
	if content, ok := assistantMessageContent(evt); ok && content != "" {
		at.appendPendingVisibleCompletionState(ctx, inv, pending)
		return nil
	}
	return pending
}

func (at *Tool) appendPendingVisibleCompletionState(
	ctx context.Context,
	inv *agent.Invocation,
	pending *event.Event,
) {
	stateOnly := visibleCompletionStateOnlySessionEvent(pending)
	if stateOnly == nil {
		return
	}
	at.appendEvent(ctx, inv, stateOnly)
}

func (at *Tool) replacePendingVisibleCompletionForSession(
	ctx context.Context,
	inv *agent.Invocation,
	pending *event.Event,
	next *event.Event,
) *event.Event {
	if next == nil {
		return pending
	}
	if pending != nil {
		at.appendPendingVisibleCompletionState(ctx, inv, pending)
	}
	return next
}

func (at *Tool) wrapWithStreamSemantics(
	ctx context.Context,
	inv *agent.Invocation,
	src <-chan *event.Event,
) <-chan *event.Event {
	if shouldDeferStreamCompletion(ctx, inv) {
		return src
	}
	return at.wrapWithCallSemantics(ctx, inv, src)
}

func shouldDeferStreamCompletion(
	ctx context.Context,
	inv *agent.Invocation,
) bool {
	if inv == nil || inv.Session == nil {
		return false
	}
	callID, ok := ctx.Value(tool.ContextKeyToolCallID{}).(string)
	if !ok || callID == "" {
		return false
	}
	return appender.IsAttached(inv)
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

func shouldDelayVisibleCompletionSessionMirror(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	if !graph.IsVisibleGraphCompletionEvent(evt) {
		return false
	}
	_, ok := assistantMessageContent(evt)
	return ok
}

func visibleCompletionStateOnlySessionEvent(evt *event.Event) *event.Event {
	if evt == nil || len(evt.StateDelta) == 0 {
		return nil
	}
	copyEvt := *evt
	if evt.Response != nil {
		copyEvt.Response = evt.Response.Clone()
		copyEvt.Response.Choices = nil
	}
	return &copyEvt
}

func shouldSuppressGraphExecutorBarrierEvent(
	inv *agent.Invocation,
	evt *event.Event,
) bool {
	if inv == nil || evt == nil || !agent.IsGraphExecutorEventsDisabled(inv) {
		return false
	}
	return evt.Object == graph.ObjectTypeGraphNodeBarrier ||
		evt.Object == graph.ObjectTypeGraphBarrier
}

func isGraphCompletionEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	return evt.Done &&
		evt.Object == graph.ObjectTypeGraphExecution
}

func isGraphCompletionSnapshotEvent(evt *event.Event) bool {
	return isGraphCompletionEvent(evt) ||
		graph.IsVisibleGraphCompletionEvent(evt)
}

func assistantMessageContent(evt *event.Event) (string, bool) {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "", false
	}
	message := evt.Response.Choices[0].Message
	if message.Role != model.RoleAssistant || message.Content == "" {
		return "", false
	}
	return message.Content, true
}

type pendingFinalResultChunk struct {
	Result     any
	StateDelta map[string][]byte
}

func graphCompletionFinalChunk(evt *event.Event) (pendingFinalResultChunk, bool) {
	if !isGraphCompletionSnapshotEvent(evt) {
		return pendingFinalResultChunk{}, false
	}
	chunk := pendingFinalResultChunk{
		StateDelta: cloneStateDelta(evt.StateDelta),
	}
	if result, ok := assistantMessageContent(evt); ok {
		chunk.Result = result
	}
	if chunk.Result == nil && len(chunk.StateDelta) == 0 {
		return pendingFinalResultChunk{}, false
	}
	return chunk, true
}

func completionResponseIDFromStateDelta(delta map[string][]byte) string {
	if len(delta) == 0 {
		return ""
	}
	raw, ok := delta[graph.StateKeyLastResponseID]
	if !ok || len(raw) == 0 {
		return ""
	}
	var responseID string
	if err := json.Unmarshal(raw, &responseID); err != nil {
		return ""
	}
	return responseID
}

func cloneStateDelta(delta map[string][]byte) map[string][]byte {
	if len(delta) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(delta))
	for key, value := range delta {
		if value == nil {
			cloned[key] = nil
			continue
		}
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
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
	evCh, err := r.Run(
		ctx,
		"tool_user",
		"tool_session",
		message,
		at.fallbackRunnerRunOptions(ctx)...,
	)
	if err != nil {
		return "", fmt.Errorf("failed to run agent: %w", err)
	}
	parentInv, _ := agent.InvocationFromContext(ctx)
	return at.collectResponse(parentInv, evCh)
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
func (at *Tool) collectResponse(inv *agent.Invocation, evCh <-chan *event.Event) (string, error) {
	if !shouldRewriteCallableCompletion(inv) {
		return collectLegacyResponse(evCh)
	}
	var response strings.Builder
	var lastAssistantMessage string
	var sawGraphCompletionSnapshot bool
	for ev := range evCh {
		if ev.Error != nil {
			return "", fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		graphCompletionSnapshot := isGraphCompletionSnapshotEvent(ev)
		if graphCompletionSnapshot {
			sawGraphCompletionSnapshot = true
		}
		content, ok := assistantMessageContent(ev)
		if !ok {
			continue
		}
		if graphCompletionSnapshot && content == lastAssistantMessage {
			continue
		}
		if graphCompletionSnapshot &&
			!ev.IsPartial &&
			response.Len() > 0 {
			response.Reset()
			lastAssistantMessage = ""
		}
		if !graphCompletionSnapshot &&
			sawGraphCompletionSnapshot &&
			!ev.IsPartial {
			response.Reset()
			lastAssistantMessage = ""
			sawGraphCompletionSnapshot = false
		}
		response.WriteString(content)
		if !ev.IsPartial {
			lastAssistantMessage = content
		}
	}
	return response.String(), nil
}

// collectLegacyResponse preserves the pre-#1365 concatenation semantics for the
// default callable path. It applies a narrow fix for issue #1640 by skipping a
// trailing graph-completion snapshot event that re-emits the same non-partial
// assistant content that was already collected. Divergent snapshot content is
// still concatenated to keep default output bytes stable for existing callers;
// broader alignment with the snapshot-aware collector is tracked as a separate
// semantic-change request.
func collectLegacyResponse(evCh <-chan *event.Event) (string, error) {
	var response strings.Builder
	var lastNonPartialAssistantContent string
	for ev := range evCh {
		if ev.Error != nil {
			return "", fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		if ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}
		choice := ev.Response.Choices[0]
		if choice.Message.Role != model.RoleAssistant || choice.Message.Content == "" {
			continue
		}
		content := choice.Message.Content
		if !ev.IsPartial &&
			isGraphCompletionSnapshotEvent(ev) &&
			content == lastNonPartialAssistantContent {
			continue
		}
		response.WriteString(content)
		if !ev.IsPartial {
			lastNonPartialAssistantContent = content
		}
	}
	return response.String(), nil
}

func shouldRewriteCallableCompletion(inv *agent.Invocation) bool {
	return inv != nil && agent.IsGraphCompletionEventDisabled(inv)
}

// StreamableCall executes the agent tool with streaming support and returns a stream reader.
// It runs the wrapped agent and forwards its streaming text output as chunks.
// The returned chunks' Content are plain strings representing incremental text.
func (at *Tool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	stream := tool.NewStream(64)
	runCtx := at.streamableCallContext(ctx)
	go at.runStreamableCall(runCtx, jsonArgs, stream.Writer)

	return stream.Reader, nil
}

type streamCompletionState struct {
	pendingCompletionChunk     *pendingFinalResultChunk
	pendingVisibleCompletion   *event.Event
	pendingStreamVisibleEvent  *event.Event
	sawGraphCompletionSnapshot bool
	lastAssistantResponseID    string
	lastAssistantContent       string
	overrideResult             string
}

func (at *Tool) streamableCallContext(ctx context.Context) context.Context {
	toolCallID, hasToolCallID := tool.ToolCallIDFromContext(ctx)
	runCtx := agent.CloneContext(ctx)
	if !hasToolCallID || toolCallID == "" {
		return runCtx
	}
	if _, ok := tool.ToolCallIDFromContext(runCtx); ok {
		return runCtx
	}
	return context.WithValue(
		runCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)
}

func (at *Tool) runStreamableCall(
	ctx context.Context,
	jsonArgs []byte,
	writer *tool.StreamWriter,
) {
	defer writer.Close()
	parentInv, ok := agent.InvocationFromContext(ctx)
	message := model.NewUserMessage(string(jsonArgs))
	if ok && parentInv != nil && parentInv.Session != nil {
		at.streamFromParentInvocation(ctx, parentInv, message, writer)
		return
	}
	at.streamFromFallbackRunner(ctx, message, writer)
}

func (at *Tool) streamFromParentInvocation(
	ctx context.Context,
	parentInv *agent.Invocation,
	message model.Message,
	writer *tool.StreamWriter,
) {
	if err := flush.Invoke(ctx, parentInv); err != nil {
		sendStreamableCallError(
			ctx,
			writer,
			"flush parent invocation session failed: %w",
			err,
		)
		return
	}
	childKey := at.buildChildFilterKey(parentInv)
	subInv := parentInv.Clone(at.childInvocationOptions(parentInv, message, childKey)...)
	subCtx := agent.NewInvocationContext(ctx, subInv)
	evCh, err := agent.RunWithPlugins(subCtx, subInv, at.agent)
	if err != nil {
		sendStreamableCallError(ctx, writer, "agent tool run error: %w", err)
		return
	}
	at.forwardSubInvocationStream(
		subCtx,
		subInv,
		at.wrapWithStreamSemantics(subCtx, subInv, evCh),
		writer,
	)
}

func (at *Tool) forwardSubInvocationStream(
	ctx context.Context,
	subInv *agent.Invocation,
	wrapped <-chan *event.Event,
	writer *tool.StreamWriter,
) {
	managePendingVisibleCompletion := shouldDeferStreamCompletion(ctx, subInv)
	emitFinalResultChunk := tool.FinalResultChunksFromContext(ctx)
	state := streamCompletionState{}
	for ev := range wrapped {
		if shouldSuppressGraphExecutorBarrierEvent(subInv, ev) {
			at.completeSuppressedBarrierStreamEvent(ctx, subInv, ev, &state)
			continue
		}
		if agent.IsGraphCompletionEventDisabled(subInv) &&
			isGraphCompletionSnapshotEvent(ev) {
			if emitFinalResultChunk {
				at.capturePendingCompletionChunk(ev, &state)
				if managePendingVisibleCompletion {
					at.capturePendingVisibleCompletion(ctx, subInv, ev, &state)
				}
				continue
			}
			visibleEvent, ok := visibleCompletionStreamEvent(ev, subInv.AgentName)
			if !ok {
				continue
			}
			if managePendingVisibleCompletion {
				at.capturePendingVisibleCompletion(ctx, subInv, visibleEvent, &state)
			}
			state.pendingStreamVisibleEvent = visibleEvent
			state.sawGraphCompletionSnapshot = true
			continue
		}
		if managePendingVisibleCompletion {
			state.pendingVisibleCompletion = at.updatePendingVisibleCompletionForSession(
				ctx,
				subInv,
				state.pendingVisibleCompletion,
				ev,
			)
			if ev != nil &&
				ev.RequiresCompletion &&
				state.pendingVisibleCompletion != nil {
				at.flushPendingVisibleCompletionForSession(
					ctx,
					subInv,
					&state,
				)
			}
		}
		at.updateStreamCompletionState(ev, &state)
		if writer.Send(tool.StreamChunk{Content: ev}, nil) {
			return
		}
	}
	if managePendingVisibleCompletion {
		at.flushPendingVisibleCompletionForSession(ctx, subInv, &state)
	}
	if emitFinalResultChunk {
		at.emitPendingCompletionChunk(&state, writer)
		return
	}
	at.emitPendingVisibleCompletionEvent(&state, writer)
}

func (at *Tool) capturePendingVisibleCompletion(
	ctx context.Context,
	inv *agent.Invocation,
	ev *event.Event,
	state *streamCompletionState,
) {
	if state == nil {
		return
	}
	sessionEvent := visibleCompletionSessionEvent(ev, inv.AgentName)
	if sessionEvent == nil {
		return
	}
	state.pendingVisibleCompletion = at.replacePendingVisibleCompletionForSession(
		ctx,
		inv,
		state.pendingVisibleCompletion,
		sessionEvent,
	)
}

func (at *Tool) flushPendingVisibleCompletionForSession(
	ctx context.Context,
	inv *agent.Invocation,
	state *streamCompletionState,
) {
	if state == nil || state.pendingVisibleCompletion == nil {
		return
	}
	if _, ok := assistantMessageContent(state.pendingVisibleCompletion); ok {
		at.ensureUserMessageForCall(ctx, inv)
	}
	at.appendEvent(ctx, inv, state.pendingVisibleCompletion)
	state.pendingVisibleCompletion = nil
}

func (at *Tool) completeSuppressedBarrierEvent(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
	pending **event.Event,
) {
	if evt == nil || inv == nil || !evt.RequiresCompletion {
		return
	}
	if pending != nil && *pending != nil {
		at.appendEvent(ctx, inv, *pending)
		*pending = nil
	}
	completionID := agent.GetAppendEventNoticeKey(evt.ID)
	if err := inv.NotifyCompletion(ctx, completionID); err != nil {
		log.Errorf("AgentTool: notify suppressed barrier completion failed: %v", err)
	}
}

func (at *Tool) completeSuppressedBarrierStreamEvent(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
	state *streamCompletionState,
) {
	if evt == nil || inv == nil || !evt.RequiresCompletion {
		return
	}
	if state != nil && state.pendingVisibleCompletion != nil {
		at.flushPendingVisibleCompletionForSession(ctx, inv, state)
	}
	completionID := agent.GetAppendEventNoticeKey(evt.ID)
	if err := inv.NotifyCompletion(ctx, completionID); err != nil {
		log.Errorf("AgentTool: notify suppressed barrier completion failed: %v", err)
	}
}

func (at *Tool) capturePendingCompletionChunk(
	ev *event.Event,
	state *streamCompletionState,
) {
	if chunk, ok := graphCompletionFinalChunk(ev); ok {
		responseID := completionResponseIDFromStateDelta(chunk.StateDelta)
		if chunk.Result == nil &&
			state.lastAssistantContent != "" &&
			(responseID == "" || responseID == state.lastAssistantResponseID) {
			chunk.Result = state.lastAssistantContent
		}
		pendingChunk := chunk
		state.pendingCompletionChunk = &pendingChunk
		state.sawGraphCompletionSnapshot = true
	}
}

func (at *Tool) updateStreamCompletionState(
	ev *event.Event,
	state *streamCompletionState,
) {
	graphCompletionSnapshot := isGraphCompletionSnapshotEvent(ev)
	if graphCompletionSnapshot {
		state.sawGraphCompletionSnapshot = true
	}
	if ev != nil && ev.Error != nil {
		state.pendingCompletionChunk = nil
		state.pendingStreamVisibleEvent = nil
		state.sawGraphCompletionSnapshot = false
		state.overrideResult = ""
		return
	}
	content, ok := assistantMessageContent(ev)
	if !ok || graphCompletionSnapshot || ev.IsPartial {
		return
	}
	state.lastAssistantContent = content
	if ev.Response != nil {
		state.lastAssistantResponseID = ev.Response.ID
	}
	if state.sawGraphCompletionSnapshot {
		state.overrideResult = content
	}
}

func visibleCompletionSessionEvent(evt *event.Event, author string) *event.Event {
	if evt == nil {
		return nil
	}
	visible := evt
	if isGraphCompletionEvent(evt) {
		rewritten, ok := graph.VisibleGraphCompletionEventForAuthor(evt, author)
		if !ok {
			return nil
		}
		visible = rewritten
	}
	if !shouldDelayVisibleCompletionSessionMirror(visible) {
		return visibleCompletionStateOnlySessionEvent(visible)
	}
	return persistableSessionEvent(visible)
}

func visibleCompletionStreamEvent(
	evt *event.Event,
	author string,
) (*event.Event, bool) {
	if evt == nil {
		return nil, false
	}
	if isGraphCompletionEvent(evt) {
		return graph.VisibleGraphCompletionEventForAuthor(evt, author)
	}
	if graph.IsVisibleGraphCompletionEvent(evt) {
		return evt, true
	}
	return nil, false
}

func (at *Tool) emitPendingVisibleCompletionEvent(
	state *streamCompletionState,
	writer *tool.StreamWriter,
) {
	if state == nil || state.pendingStreamVisibleEvent == nil {
		return
	}
	visibleEvent := state.pendingStreamVisibleEvent
	if state.overrideResult != "" {
		stateOnly := visibleCompletionStateOnlySessionEvent(visibleEvent)
		if stateOnly == nil {
			return
		}
		visibleEvent = stateOnly
	}
	_ = writer.Send(tool.StreamChunk{Content: visibleEvent}, nil)
}

func (at *Tool) emitPendingCompletionChunk(
	state *streamCompletionState,
	writer *tool.StreamWriter,
) {
	if state.pendingCompletionChunk == nil {
		return
	}
	if state.overrideResult != "" {
		state.pendingCompletionChunk.Result = state.overrideResult
	}
	var content any = tool.FinalResultChunk{
		Result: state.pendingCompletionChunk.Result,
	}
	if len(state.pendingCompletionChunk.StateDelta) > 0 {
		content = tool.FinalResultStateChunk{
			Result:     state.pendingCompletionChunk.Result,
			StateDelta: cloneStateDelta(state.pendingCompletionChunk.StateDelta),
		}
	}
	_ = writer.Send(tool.StreamChunk{
		Content: content,
	}, nil)
}

func (at *Tool) streamFromFallbackRunner(
	ctx context.Context,
	message model.Message,
	writer *tool.StreamWriter,
) {
	r := runner.NewRunner(
		at.name,
		at.agent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	evCh, err := r.Run(
		ctx,
		"tool_user",
		"tool_session",
		message,
		at.fallbackRunnerRunOptions(ctx)...,
	)
	if err != nil {
		sendStreamableCallError(ctx, writer, "agent tool run error: %w", err)
		return
	}
	for ev := range evCh {
		if ev != nil && writer.Send(tool.StreamChunk{Content: ev}, nil) {
			return
		}
	}
}

func sendStreamableCallError(
	ctx context.Context,
	writer *tool.StreamWriter,
	format string,
	err error,
) {
	streamErr := fmt.Errorf(format, err)
	if !tool.StructuredStreamErrorsFromContext(ctx) {
		if writer.Send(tool.StreamChunk{Content: streamErr.Error()}, nil) {
			return
		}
		return
	}
	if errorEvent := streamableCallErrorEvent(ctx, streamErr); errorEvent != nil {
		_ = writer.Send(tool.StreamChunk{Content: errorEvent}, nil)
		return
	}
	_ = writer.Send(tool.StreamChunk{Content: streamErr.Error()}, nil)
}

func streamableCallErrorEvent(ctx context.Context, err error) *event.Event {
	if err == nil {
		return nil
	}
	evt := event.NewErrorEvent("", "", model.ErrorTypeFlowError, err.Error())
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		agent.InjectIntoEvent(inv, evt)
	}
	return evt
}

func (at *Tool) fallbackRunnerRunOptions(ctx context.Context) []agent.RunOption {
	parentInv, ok := agent.InvocationFromContext(ctx)
	if !ok || parentInv == nil {
		return nil
	}
	opts := make([]agent.RunOption, 0, 3)
	if agent.IsGraphCompletionEventDisabled(parentInv) {
		opts = append(opts, agent.WithDisableGraphCompletionEvent(true))
	}
	if agent.IsGraphExecutorEventsDisabled(parentInv) {
		opts = append(opts, agent.WithDisableGraphExecutorEvents(true))
	}
	if size := agent.GetEventChannelBufferSize(parentInv); size > 0 {
		opts = append(opts, agent.WithEventChannelBufferSize(size))
	}
	return opts
}

// SkipSummarization exposes whether the AgentTool prefers skipping
// outer-agent summarization after its tool.response.
func (at *Tool) SkipSummarization() bool { return at.skipSummarization }

// StructuredStreamErrors reports that AgentTool expects structured error chunks.
func (at *Tool) StructuredStreamErrors() bool {
	if at == nil {
		return false
	}
	return at.structuredStreamErrors
}

// TRPCAgentGoStructuredStreamErrorsOptIn provides an explicit framework hook
// for structured stream error semantics.
func (at *Tool) TRPCAgentGoStructuredStreamErrorsOptIn() bool {
	return at.StructuredStreamErrors()
}

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
