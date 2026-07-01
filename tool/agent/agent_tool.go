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
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
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
	innerTextMode          InnerTextMode
	structuredStreamErrors bool
	historyScope           HistoryScope
	persistentHistory      *persistentHistoryOptions
	responseMode           ResponseMode
	pinModel               bool
	pinStructuredOutput    bool
	name                   string
	description            string
	inputSchema            *tool.Schema
	outputSchema           *tool.Schema

	// dynamic enables the dynamic AgentTool mode created by NewDynamicTool.
	// In this mode the tool runs a short-lived sub-agent whose
	// capability surface (tools / skills / instruction) is selected per call
	// within a code-defined safety boundary, rather than wrapping one
	// pre-defined agent.
	dynamic bool
	// dynamicCfg holds the dynamic-mode configuration. It is only consulted
	// when dynamic is true.
	dynamicCfg *dynamicOptions
}

// Option is a function that configures an AgentTool.
type Option func(*agentToolOptions)

// agentToolOptions holds the configuration options for AgentTool.
type agentToolOptions struct {
	skipSummarization      bool
	streamInner            bool
	innerTextMode          InnerTextMode
	structuredStreamErrors bool
	historyScope           HistoryScope
	persistentHistory      *persistentHistoryOptions
	responseMode           ResponseMode
	description            *string
	name                   *string
	pinModel               bool
	pinStructuredOutput    bool

	// Dynamic AgentTool options. They are only meaningful for NewDynamicTool;
	// NewTool ignores them.
	dynamic *dynamicOptions
}

// dynamicOptions holds the configuration knobs for the dynamic AgentTool mode.
type dynamicOptions struct {
	templateAgent             agent.Agent
	capabilityProvider        CapabilitySurfaceProvider
	capabilitySurfaceProvider DetailedCapabilitySurfaceProvider
	capabilitySkillProvider   CapabilitySkillsProvider
	capabilityTools           []tool.Tool
	capabilitySkills          skillRepository
	capabilityToolsSet        bool
	exposeToolSelection       bool
	exposeSkillSelection      bool
	exposeInstruction         bool
	requestDescription        *string
	instructionDescription    *string
	toolsDescription          *string
	skillsDescription         *string
	toolAliases               map[string]string
	timeout                   time.Duration
}

func defaultDynamicOptions() *dynamicOptions {
	return &dynamicOptions{
		exposeToolSelection:  true,
		exposeSkillSelection: false,
		exposeInstruction:    true,
	}
}

func (opts *agentToolOptions) ensureDynamicOptions() *dynamicOptions {
	if opts.dynamic == nil {
		opts.dynamic = defaultDynamicOptions()
	}
	return opts.dynamic
}

// PersistentHistoryKeyFunc resolves the stable child event-filter key for one
// AgentTool invocation.
//
// Returning an empty string falls back to the default stable key.
//
// Note: This is currently supported only for NewTool (wrapped fixed agent), not
// NewDynamicTool.
type PersistentHistoryKeyFunc func(
	ctx context.Context,
	parentInv *agent.Invocation,
	jsonArgs []byte,
) string

// persistentHistoryOptions holds configuration for stable child history.
//
// It is a pointer field on Tool/agentToolOptions so those structs remain
// comparable (see dynamic_tool_test.go).
type persistentHistoryOptions struct {
	enabled bool
	key     string
	keyFunc PersistentHistoryKeyFunc
}

func (opts *agentToolOptions) ensurePersistentHistoryOptions() *persistentHistoryOptions {
	if opts.persistentHistory == nil {
		opts.persistentHistory = &persistentHistoryOptions{}
	}
	return opts.persistentHistory
}

// InnerTextMode controls whether forwarded inner assistant text is visible
// in the parent flow when StreamInner is enabled.
type InnerTextMode = tool.InnerTextMode

const (
	// InnerTextModeDefault preserves the default behavior.
	InnerTextModeDefault = tool.InnerTextModeDefault

	// InnerTextModeInclude forwards inner assistant text to the parent flow.
	InnerTextModeInclude = tool.InnerTextModeInclude

	// InnerTextModeExclude suppresses forwarded inner assistant text while
	// still aggregating that text into the final tool response.
	InnerTextModeExclude = tool.InnerTextModeExclude
)

// ResponseMode controls which child assistant text AgentTool returns as the tool
// result. It does not change session event mirroring or inner streaming
// behavior.
type ResponseMode int

const (
	// ResponseModeDefault preserves the legacy assistant-content
	// concatenation behavior.
	ResponseModeDefault ResponseMode = iota

	// ResponseModeFinalOnly returns only the last complete assistant message
	// emitted by the child agent. If none is emitted, the tool result is
	// an empty string.
	ResponseModeFinalOnly
)

// WithResponseMode sets how AgentTool builds the tool result from child agent
// events.
func WithResponseMode(mode ResponseMode) Option {
	return func(opts *agentToolOptions) {
		opts.responseMode = mode
	}
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

// WithInnerTextMode controls whether forwarded inner assistant text is
// visible in the parent flow when StreamInner is enabled.
func WithInnerTextMode(mode InnerTextMode) Option {
	return func(opts *agentToolOptions) {
		opts.innerTextMode = tool.NormalizeInnerTextMode(mode)
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

// WithName overrides the model-facing tool name for NewDynamicTool.
//
// It applies ONLY to NewDynamicTool, whose name defaults to
// DefaultDynamicToolName ("dynamic_agent"); use it to expose a different,
// code-defined entrypoint name (for example "explore" or "implement").
//
// It is intentionally ignored by NewTool: a wrapped agent's tool name is its
// identity (agent.Info().Name) and is also used for the child event-filter key,
// team node id, and recursion guards, so renaming only the model-facing name
// would split that identity. Rename the wrapped agent instead.
//
// The name must comply with LLM API requirements (^[a-zA-Z0-9_-]+$).
func WithName(name string) Option {
	return func(opts *agentToolOptions) {
		copiedName := name
		opts.name = &copiedName
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

// WithPersistentHistory enables stable child history for a wrapped agent tool.
//
// When enabled, the child invocation uses a stable event-filter key so that it
// can see its own past events across multiple AgentTool calls (within the same
// session). This does not change control-flow semantics (still call-return) and
// does not make runtime/executor state persistent.
//
// This option is currently supported only for NewTool (wrapped fixed agent) and
// is incompatible with HistoryScopeParentBranch. When HistoryScopeParentBranch
// is enabled, persistent history is ignored and the legacy UUID-suffixed child
// filter keys are used.
func WithPersistentHistory() Option {
	return func(opts *agentToolOptions) {
		cfg := opts.ensurePersistentHistoryOptions()
		cfg.enabled = true
		cfg.key = ""
		cfg.keyFunc = nil
	}
}

// WithPersistentHistoryKey enables stable child history using a caller-provided
// stable event-filter key.
//
// See WithPersistentHistory for semantics and limitations.
func WithPersistentHistoryKey(key string) Option {
	return func(opts *agentToolOptions) {
		cfg := opts.ensurePersistentHistoryOptions()
		cfg.enabled = true
		cfg.keyFunc = nil
		cfg.key = strings.TrimSpace(key)
	}
}

// WithPersistentHistoryKeyFunc enables stable child history using a caller
// function to compute the stable event-filter key per call.
//
// See WithPersistentHistory for semantics and limitations.
func WithPersistentHistoryKeyFunc(fn PersistentHistoryKeyFunc) Option {
	return func(opts *agentToolOptions) {
		cfg := opts.ensurePersistentHistoryOptions()
		cfg.enabled = true
		cfg.key = ""
		cfg.keyFunc = fn
	}
}

// WithPinModel pins the sub-agent's model so that it always uses its own
// configured model (set via llmagent.WithModel) regardless of the caller's
// runtime model selection propagated through RunOptions.
//
// Background: when the caller passes agent.WithModelName(...),
// agent.WithModel(...) or agent.WithModelSelector(...) at runner.Run time
// (e.g., AGUI server forwarding the user's model choice), RunOptions
// propagate to child invocations via Clone(). This causes the sub-agent's
// own model to be overridden.
//
// WithPinModel(true) clears RunOptions.ModelName, RunOptions.Model and
// RunOptions.ModelSelector for the child invocation so the sub-agent's
// own model takes effect.
//
// If no Model/ModelName/ModelSelector is set in RunOptions, the sub-agent
// naturally uses its own model regardless of this option.
func WithPinModel(enabled bool) Option {
	return func(opts *agentToolOptions) {
		opts.pinModel = enabled
	}
}

// WithPinStructuredOutput pins the sub-agent's structured-output contract so
// it always uses its own configured structured output (for example,
// llmagent.WithStructuredOutputJSON or llmagent.WithStructuredOutputJSONSchema)
// regardless of the caller's runtime structured output propagated through
// RunOptions.
//
// Background: when the caller passes agent.WithStructuredOutputJSON(...) or
// agent.WithStructuredOutputJSONSchema(...) at runner.Run time, RunOptions
// propagate to child invocations via Clone(). LLMAgent setup prefers those
// run-scoped structured-output values over the sub-agent's own configuration.
//
// WithPinStructuredOutput(true) clears RunOptions.StructuredOutput and
// RunOptions.StructuredOutputType for the child invocation so the sub-agent's
// own structured output takes effect.
func WithPinStructuredOutput(enabled bool) Option {
	return func(opts *agentToolOptions) {
		opts.pinStructuredOutput = enabled
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
	if options.name != nil {
		log.Warnf(
			"AgentTool: WithName(%q) is ignored by NewTool; "+
				"rename the wrapped agent or use NewDynamicTool",
			*options.name,
		)
	}

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
	// NewTool's name is the wrapped agent's identity; WithName is intentionally
	// dynamic-only (see WithName) so the model-facing name never diverges from
	// the child filter key, team node id, and recursion guards.
	name := info.Name

	persistent := options.persistentHistory
	if persistent != nil &&
		persistent.enabled &&
		options.historyScope == HistoryScopeParentBranch {
		log.Warnf(
			"AgentTool[%s]: persistent history is ignored when HistoryScopeParentBranch is enabled",
			name,
		)
		persistent = nil
	}
	return &Tool{
		agent:                  agent,
		skipSummarization:      options.skipSummarization,
		streamInner:            options.streamInner,
		innerTextMode:          tool.NormalizeInnerTextMode(options.innerTextMode),
		structuredStreamErrors: options.structuredStreamErrors,
		historyScope:           options.historyScope,
		persistentHistory:      persistent,
		responseMode:           normalizeResponseMode(options.responseMode),
		pinModel:               options.pinModel,
		pinStructuredOutput:    options.pinStructuredOutput,
		name:                   name,
		description:            description,
		inputSchema:            inputSchema,
		outputSchema:           outputSchema,
	}
}

// Call executes the agent tool with the provided JSON arguments.
func (at *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	// Dynamic AgentTool mode runs a short-lived sub-agent whose capability
	// surface is selected per call from the parent invocation.
	if at.dynamic {
		return at.callDynamic(ctx, jsonArgs)
	}

	message := model.NewUserMessage(string(jsonArgs))

	// Prefer to reuse parent invocation + session so the child can see parent
	// history according to the configured history scope.
	if parentInv, ok := agent.InvocationFromContext(ctx); ok && parentInv != nil {
		if parentInv.Session != nil {
			return at.callWithParentInvocation(ctx, parentInv, message, nil)
		}
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
	runtime *parentInvocationGraphRuntime,
) (string, error) {
	var runtimeState graph.State
	var parentNodeID string
	var toolCallID string
	var toolCallKey string
	hasGraphRuntime := runtime != nil
	if hasGraphRuntime {
		runtimeState = runtime.state
		parentNodeID = runtime.parentNodeID
		toolCallID = runtime.toolCallID
		toolCallKey = runtime.toolCallKey
	}
	// If the parent invocation does not have a session, fall back to isolated mode.
	if parentInv.Session == nil && !hasGraphRuntime {
		return at.callWithIsolatedRunner(ctx, message)
	}
	// Flush all events emitted before this tool call so that the snapshot sees all events.
	if parentInv.Session != nil {
		if err := flush.Invoke(ctx, parentInv); err != nil {
			return "", fmt.Errorf("flush parent invocation session: %w", err)
		}
		parentInv = parentInvocationWithLiveSession(parentInv)
	}
	// Build child filter key based on history scope.
	childKey := at.buildChildFilterKey(ctx, parentInv, []byte(message.Content))
	if hasGraphRuntime && runtime.childKey != "" {
		childKey = runtime.childKey
	}
	if runtimeState != nil {
		if _, ok := runtimeState[graph.CfgKeyCheckpointID]; ok {
			// A checkpoint resume is driven by the command in runtime state.
			message = model.Message{}
		}
	}
	subInv := parentInv.Clone(at.childInvocationOptions(ctx, parentInv, message, childKey, runtimeState)...)

	// Run the agent and collect response.
	subCtx := agent.NewInvocationContext(ctx, subInv)
	evCh, err := agent.RunWithPlugins(subCtx, subInv, at.agent)
	if err != nil {
		return "", fmt.Errorf("failed to run agent: %w", err)
	}
	capture := at.newGraphToolInterruptCapture(runtimeState, parentNodeID, toolCallID, toolCallKey, childKey, hasGraphRuntime)
	response, err := at.collectResponse(
		subInv,
		at.wrapGraphToolInterruptCapture(
			at.wrapWithCallSemantics(subCtx, subInv, evCh),
			capture,
		),
	)
	if err != nil {
		return "", err
	}
	if interruptErr := capture.finish(); interruptErr != nil {
		return "", interruptErr
	}
	return response, nil
}

// parentInvocationWithLiveSession returns a view of parentInv whose Session
// references the live session shared with the runner. When no live session
// pointer was attached or the invocation already targets the live session,
// the original parentInv is returned unchanged.
func parentInvocationWithLiveSession(
	parentInv *agent.Invocation,
) *agent.Invocation {
	if parentInv == nil || parentInv.Session == nil {
		return parentInv
	}
	liveSess, ok := livesession.Get(parentInv)
	if !ok || liveSess == nil || liveSess == parentInv.Session {
		return parentInv
	}
	view := parentInv.View()
	if view == nil {
		return parentInv
	}
	view.Session = liveSess
	return view
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
	ctx context.Context,
	parentInv *agent.Invocation,
	message model.Message,
	childKey string,
	runtimeState map[string]any,
) []agent.InvocationOptions {
	invocationOpts := []agent.InvocationOptions{
		agent.WithInvocationAgent(at.agent),
		agent.WithInvocationMessage(message),
		agent.WithInvocationEventFilterKey(childKey),
	}
	// Override the inherited ParentMetadata at the AgentTool boundary: the
	// child invocation is freshly triggered by *this* AgentTool call, so its
	// ParentMetadata must describe this call — not whatever spawned the
	// parent invocation. Invocation.Clone copies ParentMetadata by default,
	// so we must overwrite it unconditionally; otherwise a child spawned
	// without a toolCallId in ctx would inherit the parent's ParentMetadata
	// and AG-UI would correlate child events to the wrong parent edge.
	//
	// When toolCallId is unavailable in ctx (degraded path), set
	// ParentMetadata to nil rather than fabricating one or leaving the
	// inherited value. Critical for parallel AgentTool calls to the same
	// sub-agent: parentInvocationId alone cannot disambiguate parallel
	// branches; ParentMetadata.TriggerID can.
	var childParentMetadata *agent.ParentInvocationMetadata
	if toolCallID, ok := tool.ToolCallIDFromContext(ctx); ok && toolCallID != "" {
		childParentMetadata = &agent.ParentInvocationMetadata{
			TriggerType: agent.TriggerTypeToolCall,
			TriggerID:   toolCallID,
			TriggerName: at.name,
		}
	}
	invocationOpts = append(invocationOpts, agent.WithInvocationParentMetadata(childParentMetadata))
	if runtimeState != nil {
		invocationOpts = append(invocationOpts, func(inv *agent.Invocation) {
			runOptions := inv.RunOptions
			agent.WithDisableGraphExecutorEvents(false)(&runOptions)
			runOptions.RuntimeState = runtimeState
			inv.RunOptions = runOptions
			if parentInv != nil && agent.IsGraphExecutorEventsDisabled(parentInv) {
				inv.SetState(graphRuntimeSuppressSessionEventsStateKey, true)
			}
		})
	}
	if parentInv == nil {
		return invocationOpts
	}
	if at.hasPinnedRunOptions() {
		invocationOpts = append(invocationOpts, func(inv *agent.Invocation) {
			runOptions := inv.RunOptions
			at.clearPinnedRunOptions(&runOptions)
			inv.RunOptions = runOptions
		})
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

func (at *Tool) hasPinnedRunOptions() bool {
	return at.pinModel || at.pinStructuredOutput
}

func (at *Tool) clearPinnedRunOptions(runOptions *agent.RunOptions) {
	if runOptions == nil {
		return
	}
	if at.pinModel {
		runOptions.ModelName = ""
		runOptions.Model = nil
		runOptions.ModelSelector = nil
	}
	if at.pinStructuredOutput {
		runOptions.StructuredOutput = nil
		runOptions.StructuredOutputType = nil
	}
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
			if evt != nil {
				ensureInvocationEventFields(inv, evt)
				if evt.RequiresCompletion {
					completionID := agent.GetAppendEventNoticeKey(evt.ID)
					if err := inv.NotifyCompletion(ctx, completionID); err != nil {
						log.Errorf("AgentTool: notify completion failed: %v", err)
					}
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
				ensureInvocationEventFields(inv, evt)
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
				if !shouldSuppressGraphRuntimeSessionEvent(inv, evt) &&
					shouldMirrorEventToSession(evt) {
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

func ensureInvocationEventFields(inv *agent.Invocation, evt *event.Event) {
	if inv == nil || evt == nil {
		return
	}
	if evt.RequestID == "" {
		evt.RequestID = inv.RunOptions.RequestID
	}
	if evt.InvocationID == "" {
		evt.InvocationID = inv.InvocationID
	}
	if evt.ParentInvocationID == "" {
		if parent := inv.GetParentInvocation(); parent != nil {
			evt.ParentInvocationID = parent.InvocationID
		}
	}
	if evt.ParentMetadata == nil && inv.ParentMetadata != nil {
		evt.ParentMetadata = inv.ParentMetadata
	}
	if evt.Branch == "" {
		evt.Branch = inv.Branch
	}
	if evt.FilterKey == "" {
		evt.FilterKey = inv.GetEventFilterKey()
	}
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
		if inv.Session.Events[i].InvocationID == inv.InvocationID &&
			inv.Session.Events[i].IsUserMessage() {
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
func (at *Tool) buildChildFilterKey(
	ctx context.Context,
	parentInv *agent.Invocation,
	jsonArgs []byte,
) string {
	// Persistent history is supported only for isolated history scope. When
	// HistoryScopeParentBranch is enabled, NewTool clears persistentHistory at
	// construction time to preserve legacy semantics.
	if at.persistentHistory != nil &&
		at.persistentHistory.enabled &&
		at.historyScope == HistoryScopeIsolated {
		childKey := strings.TrimSpace(at.persistentHistory.key)
		if at.persistentHistory.keyFunc != nil {
			childKey = strings.TrimSpace(at.persistentHistory.keyFunc(ctx, parentInv, jsonArgs))
		}
		if childKey == "" {
			childKey = defaultPersistentHistoryKey(at.name)
		}
		return childKey
	}

	childKey := at.agent.Info().Name + "-" + uuid.NewString()
	if at.historyScope == HistoryScopeParentBranch {
		if pk := parentInv.GetEventFilterKey(); pk != "" {
			childKey = pk + agent.EventFilterKeyDelimiter + childKey
		}
	}
	return childKey
}

func defaultPersistentHistoryKey(agentName string) string {
	if agentName == "" {
		agentName = "child"
	}
	// Avoid "/" so the key does not accidentally fall under the parent's
	// prefix/subtree filters unless the caller explicitly opts into that
	// relationship via WithPersistentHistoryKey.
	return "agenttool:" + agentName + ":default"
}

// collectResponse collects and concatenates assistant messages from the event
// channel, returning the complete response text.
func (at *Tool) collectResponse(inv *agent.Invocation, evCh <-chan *event.Event) (string, error) {
	if at.responseMode == ResponseModeFinalOnly {
		return collectFinalResponse(evCh)
	}
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

// collectFinalResponse returns the last complete child assistant message. If no
// complete assistant message is emitted, it returns an empty string and nil
// error.
func collectFinalResponse(evCh <-chan *event.Event) (string, error) {
	var finalResponse string
	for ev := range evCh {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return "", fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		content, ok := assistantMessageContent(ev)
		if !ok || ev.IsPartial {
			continue
		}
		finalResponse = content
	}
	return finalResponse, nil
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

func normalizeResponseMode(mode ResponseMode) ResponseMode {
	switch mode {
	case ResponseModeDefault:
		return ResponseModeDefault
	case ResponseModeFinalOnly:
		return mode
	default:
		log.Debugf("AgentTool: unknown response mode %d, using default", mode)
		return ResponseModeDefault
	}
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
	finalOnlyResult            string
	overrideResult             string
	// resultPrefix is prepended to the model-visible result. It carries
	// dynamic sub-agent warnings so the parent model sees them in stream mode,
	// matching the Call path. Empty for non-dynamic AgentTool streaming.
	resultPrefix string
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
	if at.dynamic {
		at.streamDynamic(ctx, jsonArgs, writer)
		return
	}
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
	// See the comment in callWithParentInvocation: when AgentTool is invoked
	// from the parallel function-call path, parentInv.Session is a frozen
	// snapshot. Without restoring the live pointer the sub-agent loses
	// visibility of its own tool_call / tool_response events and loops
	// forever.
	parentInv = parentInvocationWithLiveSession(parentInv)
	childKey := at.buildChildFilterKey(ctx, parentInv, []byte(message.Content))
	subInv := parentInv.Clone(at.childInvocationOptions(ctx, parentInv, message, childKey, nil)...)
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
		"",
	)
}

func (at *Tool) forwardSubInvocationStream(
	ctx context.Context,
	subInv *agent.Invocation,
	wrapped <-chan *event.Event,
	writer *tool.StreamWriter,
	resultPrefix string,
) {
	managePendingVisibleCompletion := shouldDeferStreamCompletion(ctx, subInv)
	emitFinalResultChunk := tool.FinalResultChunksFromContext(ctx)
	state := streamCompletionState{resultPrefix: resultPrefix}
	// When the model-visible result is built from merged inner content (no
	// final-result chunk is emitted), inject the prefix as a leading content
	// chunk so it is merged into the tool response. For final-result modes the
	// prefix is folded into the emitted result instead (see the emit helpers).
	if resultPrefix != "" && !emitFinalResultChunk {
		if writer.Send(tool.StreamChunk{Content: resultPrefix}, nil) {
			return
		}
	}
	for ev := range wrapped {
		if at.handleForwardedStreamEvent(
			ctx, subInv, ev, writer, &state,
			managePendingVisibleCompletion, emitFinalResultChunk,
		) {
			return
		}
	}
	if managePendingVisibleCompletion {
		at.flushPendingVisibleCompletionForSession(ctx, subInv, &state)
	}
	if emitFinalResultChunk {
		if at.responseMode == ResponseModeFinalOnly {
			at.emitFinalOnlyResultChunk(&state, writer)
			return
		}
		at.emitPendingCompletionChunk(&state, writer)
		return
	}
	at.emitPendingVisibleCompletionEvent(&state, writer)
}

// handleForwardedStreamEvent processes a single forwarded sub-invocation event
// and reports whether forwarding should stop (the writer signalled completion,
// so the caller must return). Graph-completion snapshots are captured for
// deferred emission and never forwarded inline.
func (at *Tool) handleForwardedStreamEvent(
	ctx context.Context,
	subInv *agent.Invocation,
	ev *event.Event,
	writer *tool.StreamWriter,
	state *streamCompletionState,
	managePendingVisibleCompletion bool,
	emitFinalResultChunk bool,
) (stop bool) {
	if ev != nil {
		ensureInvocationEventFields(subInv, ev)
	}
	if shouldSuppressGraphExecutorBarrierEvent(subInv, ev) {
		at.completeSuppressedBarrierStreamEvent(ctx, subInv, ev, state)
		return false
	}
	at.updateFinalOnlyStreamResult(ev, state)
	if agent.IsGraphCompletionEventDisabled(subInv) &&
		isGraphCompletionSnapshotEvent(ev) {
		at.handleGraphCompletionSnapshot(
			ctx, subInv, ev, state,
			managePendingVisibleCompletion, emitFinalResultChunk,
		)
		return false
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
			at.flushPendingVisibleCompletionForSession(ctx, subInv, state)
		}
	}
	at.updateStreamCompletionState(ev, state)
	return writer.Send(tool.StreamChunk{Content: ev}, nil)
}

// handleGraphCompletionSnapshot captures a graph-completion snapshot for
// deferred emission: final-result modes stash the completion chunk, otherwise a
// model-visible completion event is captured/queued for end-of-stream flush.
func (at *Tool) handleGraphCompletionSnapshot(
	ctx context.Context,
	subInv *agent.Invocation,
	ev *event.Event,
	state *streamCompletionState,
	managePendingVisibleCompletion bool,
	emitFinalResultChunk bool,
) {
	if emitFinalResultChunk {
		at.capturePendingCompletionChunk(ev, state)
		if managePendingVisibleCompletion {
			at.capturePendingVisibleCompletion(ctx, subInv, ev, state)
		}
		return
	}
	visibleEvent, ok := visibleCompletionStreamEvent(ev, subInv.AgentName)
	if !ok {
		return
	}
	if managePendingVisibleCompletion {
		at.capturePendingVisibleCompletion(ctx, subInv, visibleEvent, state)
	}
	state.pendingStreamVisibleEvent = visibleEvent
	state.sawGraphCompletionSnapshot = true
}

func (at *Tool) updateFinalOnlyStreamResult(
	ev *event.Event,
	state *streamCompletionState,
) {
	if state == nil {
		return
	}
	content, ok := assistantMessageContent(ev)
	if !ok || ev.IsPartial {
		return
	}
	state.finalOnlyResult = content
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
	result := prefixStreamResult(state.resultPrefix, state.pendingCompletionChunk.Result)
	var content any = tool.FinalResultChunk{
		Result: result,
	}
	if len(state.pendingCompletionChunk.StateDelta) > 0 {
		content = tool.FinalResultStateChunk{
			Result:     result,
			StateDelta: cloneStateDelta(state.pendingCompletionChunk.StateDelta),
		}
	}
	_ = writer.Send(tool.StreamChunk{
		Content: content,
	}, nil)
}

func (at *Tool) emitFinalOnlyResultChunk(
	state *streamCompletionState,
	writer *tool.StreamWriter,
) {
	result := ""
	var stateDelta map[string][]byte
	prefix := ""
	if state != nil {
		result = state.finalOnlyResult
		prefix = state.resultPrefix
		if state.pendingCompletionChunk != nil {
			stateDelta = state.pendingCompletionChunk.StateDelta
		}
	}
	finalResult := prefixStreamResult(prefix, result)
	var content any = tool.FinalResultChunk{Result: finalResult}
	if len(stateDelta) > 0 {
		content = tool.FinalResultStateChunk{
			Result:     finalResult,
			StateDelta: cloneStateDelta(stateDelta),
		}
	}
	_ = writer.Send(tool.StreamChunk{Content: content}, nil)
}

// prefixStreamResult prepends prefix (e.g. dynamic sub-agent warnings) to a
// streamed final result. It only prefixes string/nil results so non-text tool
// results are never corrupted; non-dynamic streaming passes an empty prefix and
// is therefore unaffected.
func prefixStreamResult(prefix string, result any) any {
	if prefix == "" {
		return result
	}
	switch v := result.(type) {
	case nil:
		return prefix
	case string:
		if v == "" {
			return prefix
		}
		return prefix + "\n" + v
	default:
		return result
	}
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

// InnerTextMode exposes how forwarded inner assistant text should be handled
// when StreamInner is enabled.
func (at *Tool) InnerTextMode() InnerTextMode {
	if at == nil {
		return tool.InnerTextModeInclude
	}
	return tool.NormalizeInnerTextMode(at.innerTextMode)
}

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
