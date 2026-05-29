//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/structuredoutput"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
	"trpc.group/trpc-go/trpc-agent-go/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// WaitNoticeWithoutTimeout is the timeout duration for waiting without timeout
	WaitNoticeWithoutTimeout = 0 * time.Second

	// AppendEventNoticeKeyPrefix is the prefix for append event notice keys
	AppendEventNoticeKeyPrefix = "append_event:"

	// BranchDelimiter is the delimiter for branch
	BranchDelimiter = "/"

	// EventFilterKeyDelimiter is the delimiter for event filter key
	EventFilterKeyDelimiter = "/"

	// flusherStateKey is the invocation state key used by flush.Attach.
	flusherStateKey = "__flush_session__"
	// barrierStateKey is the invocation state key used by internal barrier flag.
	barrierStateKey = "__graph_barrier__"
	// appenderStateKey is the invocation state key used by internal appender
	// attachment (see internal/state/appender).
	appenderStateKey = "__append_event__"

	// streamHubStateKey is the invocation state key used by the graph to
	// share ephemeral streams across node invocations within the same run.
	streamHubStateKey = "__graph_stream_hub__"
	// surfaceRootNodeIDStateKey stores one invocation's mounted surface root node id.
	surfaceRootNodeIDStateKey = "__trpc_agent_internal_surface_root_node_id_state__"
	// teamMemberTraceRootStateKey stores one invocation's mounted team member trace root.
	teamMemberTraceRootStateKey = "__trpc_agent_internal_team_member_trace_root_state__"

	// SyncSummaryIntraRunStateKey is set on the invocation by the
	// flow when sync intra-run summary is active.
	// Runner checks this key to skip redundant async summary
	// enqueue during the same run.
	SyncSummaryIntraRunStateKey = "__sync_summary_intra_run__"
)

// TransferInfo contains information about a pending agent transfer.
type TransferInfo struct {
	// TargetAgentName is the name of the agent to transfer control to.
	TargetAgentName string
	// Message is the message to send to the target agent.
	Message string
}

// Invocation represents the context for a flow execution.
type Invocation struct {
	// Agent is the agent that is being invoked.
	Agent Agent
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// Branch records agent execution chain information.
	// In multi-agent mode, this is useful for tracing agent execution trajectories.
	Branch string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Session is the session that is being used for the invocation.
	Session *session.Session
	// SessionService is the session service used by this invocation.
	SessionService session.Service
	// Model is the model that is being used for the invocation.
	Model model.Model
	// Message is the message that is being sent to the agent.
	Message model.Message
	// RunOptions is the options for the Run method.
	RunOptions RunOptions
	// TransferInfo contains information about a pending agent transfer.
	TransferInfo *TransferInfo

	// Plugins provides runner-scoped hooks applied to this invocation.
	Plugins PluginManager

	// StructuredOutput defines how the model should produce structured output for this invocation.
	StructuredOutput *model.StructuredOutput
	// StructuredOutputType is the Go type to unmarshal the final JSON into.
	StructuredOutputType reflect.Type

	// MemoryService is the service for managing memory.
	MemoryService memory.Service
	// ArtifactService is the service for managing artifacts.
	ArtifactService artifact.Service

	// noticeChannels is used to signal when events are written to the session.
	noticeChannels map[string]chan any
	noticeMu       *sync.Mutex

	// eventFilterKey is used to filter events for flow or agent
	eventFilterKey string

	// parent is the parent invocation, if any
	parent *Invocation
	// traceCapture stores the shared execution trace capture for one root run.
	traceCapture *tracecapture.Capture
	traceMu      sync.Mutex
	// entryPredecessorStepIDs stores the predecessor step ids passed to this invocation entry.
	entryPredecessorStepIDs []string
	// traceNodeID stores the mounted static root node id for this invocation.
	traceNodeID string

	// state stores invocation-scoped state data (lazy initialized).
	// Can be used by callbacks, middleware, or any invocation-scoped logic.
	state   map[string]any
	stateMu sync.RWMutex

	// MaxLLMCalls is an optional upper bound on the number of LLM calls
	// allowed for this invocation. When the value is:
	//   - > 0: the limit is enforced for this invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	//
	// Typical usage:
	//   - LLMAgent copies its per-agent limits into these fields in setupInvocation.
	//   - Other agent implementations may set them explicitly when constructing
	//     invocations. If left at zero, IncLLMCallCount/IncToolIteration are no-ops.
	MaxLLMCalls int

	// MaxToolIterations is an optional upper bound on how many tool-call
	// iterations are allowed for this invocation. A "tool iteration" is defined
	// as an assistant response that contains tool calls and triggers the
	// FunctionCallResponseProcessor. When the value is:
	//   - > 0: the limit is enforced for this invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	MaxToolIterations int

	// timingInfo stores timing information for the first LLM call in this invocation.
	timingInfo *model.TimingInfo

	// llmCallCount tracks how many LLM calls have been made in this invocation.
	// This is used together with MaxLLMCalls to enforce a per-invocation limit.
	// Note: counters are invocation-scoped. When child invocations are created
	// via Clone (for example, in transfer_to_agent or AgentTool), the counters
	// start from zero for each invocation.
	llmCallCount int

	// toolIterationCount tracks how many tool call iterations have been processed
	// in this invocation. This is used together with MaxToolIterations
	// to guard against unbounded tool_call -> LLM -> tool_call loops.
	toolIterationCount int
}

// DefaultWaitNoticeTimeoutErr is the default error returned when a wait notice times out.
var DefaultWaitNoticeTimeoutErr = NewWaitNoticeTimeoutError("wait notice timeout.")

// WaitNoticeTimeoutError represents an error that signals the wait notice timeout.
type WaitNoticeTimeoutError struct {
	// Message contains the stop reason
	Message string
}

// Error implements the error interface.
func (e *WaitNoticeTimeoutError) Error() string {
	return e.Message
}

// AsWaitNoticeTimeoutError checks if an error is a AsWaitNoticeTimeoutError using errors.As.
func AsWaitNoticeTimeoutError(err error) (*WaitNoticeTimeoutError, bool) {
	var waitNoticeTimeoutErr *WaitNoticeTimeoutError
	ok := errors.As(err, &waitNoticeTimeoutErr)
	return waitNoticeTimeoutErr, ok
}

// NewWaitNoticeTimeoutError creates a new AsWaitNoticeTimeoutError with the given message.
func NewWaitNoticeTimeoutError(message string) *WaitNoticeTimeoutError {
	return &WaitNoticeTimeoutError{Message: message}
}

// RunOption is a function that configures a RunOptions.
type RunOption func(*RunOptions)

// ModelSelector selects the model for one framework-managed LLM call.
// The invocation's Model is the base model for this call when the selector is
// invoked. Returning nil with nil error keeps that base model. Returning an
// error fails the current call before the request is built. A selector may be
// called concurrently by different runs and must protect any shared state it
// owns.
type ModelSelector func(ctx context.Context, inv *Invocation) (model.Model, error)

// AvailableSkillsRenderRequest contains inputs for rendering the request-scoped
// Available skills section.
type AvailableSkillsRenderRequest struct {
	// Summaries are the skills visible to the current request.
	Summaries []skill.Summary
}

// AvailableSkillsRenderer renders the request-scoped Available skills section.
type AvailableSkillsRenderer func(
	ctx context.Context,
	req AvailableSkillsRenderRequest,
) string

type runControlConfig struct {
	DisableGraphCompletionEvent bool
	DisableGraphExecutorEvents  bool
	EventChannelBufferSize      int
	PropagateChildAgentErrors   bool
}

// NewRunOptions builds a RunOptions value from RunOption functions.
func NewRunOptions(opts ...RunOption) RunOptions {
	var runOpts RunOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&runOpts)
		}
	}
	return runOpts
}

// TraceStartedCallback receives the root span context for a run.
type TraceStartedCallback func(oteltrace.SpanContext)

// WithAppName overrides the runner's default app name for this specific run.
//
// This enables a single runner to serve multiple projects or tenants
// by isolating session and memory data under different app names.
// When not set, the runner uses its constructor-provided default app name.
func WithAppName(name string) RunOption {
	return func(opts *RunOptions) {
		opts.AppName = name
	}
}

// WithRuntimeState sets the runtime state for the RunOptions.
func WithRuntimeState(state map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.RuntimeState = state
	}
}

// WithModelRequestExtraFields merges provider-specific top-level fields into
// each model request created during this run. Request-level fields take
// precedence over model-level extra fields in adapters that support merging.
func WithModelRequestExtraFields(fields map[string]any) RunOption {
	return func(opts *RunOptions) {
		if len(fields) == 0 {
			return
		}
		if opts.ModelRequestExtraFields == nil {
			opts.ModelRequestExtraFields = make(map[string]any, len(fields))
		}
		for key, value := range fields {
			opts.ModelRequestExtraFields[key] = value
		}
	}
}

// MergeRuntimeState merges runtime state into existing RunOptions state.
//
// When a key already exists, the new value replaces the old one.
func MergeRuntimeState(state map[string]any) RunOption {
	return func(opts *RunOptions) {
		if len(state) == 0 {
			return
		}
		if opts.RuntimeState == nil {
			opts.RuntimeState = make(
				map[string]any,
				len(state),
			)
		}
		for key, value := range state {
			opts.RuntimeState[key] = value
		}
	}
}

// WithAgent sets the agent instance for this run only.
func WithAgent(a Agent) RunOption {
	return func(opts *RunOptions) {
		opts.Agent = a
	}
}

// WithAgentByName sets the agent name that should be resolved for this run.
func WithAgentByName(name string) RunOption {
	return func(opts *RunOptions) {
		opts.AgentByName = name
	}
}

// GetRuntimeStateValue retrieves a typed value from the runtime state.
//
// Returns the typed value and true if the key exists and the type matches,
// or the zero value and false otherwise.
//
// Example:
//
//	if userID, ok := GetRuntimeStateValue[string](&inv.RunOptions, "user_id"); ok {
//	    log.Printf("User ID: %s", userID)
//	}
//	if roomID, ok := GetRuntimeStateValue[int](&inv.RunOptions, "room_id"); ok {
//	    log.Printf("Room ID: %d", roomID)
//	}
func GetRuntimeStateValue[T any](opts *RunOptions, key string) (T, bool) {
	var zero T
	if opts == nil || opts.RuntimeState == nil {
		return zero, false
	}
	val, ok := opts.RuntimeState[key]
	if !ok {
		return zero, false
	}
	typedVal, ok := val.(T)
	if !ok {
		return zero, false
	}
	return typedVal, true
}

// WithKnowledgeFilter sets the metadata filter for the RunOptions.
func WithKnowledgeFilter(filter map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.KnowledgeFilter = filter
	}
}

// WithKnowledgeConditionedFilter sets the complex condition filter for the RunOptions.
func WithKnowledgeConditionedFilter(filter *searchfilter.UniversalFilterCondition) RunOption {
	return func(opts *RunOptions) {
		opts.KnowledgeConditionedFilter = filter
	}
}

// WithMessages sets the caller-supplied conversation history for this run.
// Runner uses this history to auto-seed an empty Session (once) and to
// populate `invocation.Message` via RunWithMessages for compatibility. The
// content processor itself does not read this field; it derives messages from
// Session events and may fall back to a single `invocation.Message` when the
// Session is empty.
func WithMessages(messages []model.Message) RunOption {
	return func(opts *RunOptions) {
		opts.Messages = messages
	}
}

// WithInjectedContextMessages appends per-run messages that are injected into the
// model request context but are not persisted into the session transcript.
func WithInjectedContextMessages(messages []model.Message) RunOption {
	return func(opts *RunOptions) {
		opts.InjectedContextMessages = append(opts.InjectedContextMessages, messages...)
	}
}

// UserMessageRewriteArgs contains stable metadata for one user message rewrite.
type UserMessageRewriteArgs struct {
	AppName         string
	UserID          string
	SessionID       string
	RequestID       string
	OriginalMessage model.Message
}

// UserMessageRewriter rewrites one current-turn user input into an ordered
// message sequence. The returned order is the persistence order for the turn,
// and the last message becomes invocation.Message.
type UserMessageRewriter func(
	ctx context.Context,
	args *UserMessageRewriteArgs,
) ([]model.Message, error)

// WithUserMessageRewriter rewrites the current-turn input into an ordered
// message sequence before runner persists it into the session transcript.
func WithUserMessageRewriter(rewriter UserMessageRewriter) RunOption {
	return func(opts *RunOptions) {
		opts.UserMessageRewriter = rewriter
	}
}

// WithResume enables or disables resume mode for this run.
// When enabled, flows like llmflow may inspect the existing Session history
// and resume unfinished work (for example, executing pending tool calls)
// before issuing a new model call.
func WithResume(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.Resume = enabled
	}
}

// WithPersistInterruptedAssistant controls whether a cancelled streaming run
// persists already-emitted assistant text as a final assistant message.
//
// By default this is not set, so the Runner uses its own default. The built-in
// Runner default is false to preserve the "cancel discards partial text"
// session semantics.
func WithPersistInterruptedAssistant(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.PersistInterruptedAssistant = &enabled
	}
}

// WithGraphEmitFinalModelResponses controls whether graph-based agents emit
// final (Done=true) model responses as events.
//
// When disabled (default), graph Large Language Model (LLM) nodes only emit
// streaming chunks (Done=false), which matches the pre-#901 behavior.
func WithGraphEmitFinalModelResponses(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.GraphEmitFinalModelResponses = enabled
	}
}

// WithGraphTerminalMessagesOnly limits caller-visible graph message events to
// terminal nodes only.
//
// When disabled (default), all graph Large Language Model (LLM) nodes and
// sub-agent nodes may emit caller-visible message events.
func WithGraphTerminalMessagesOnly(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.GraphTerminalMessagesOnly = enabled
	}
}

// WithStreamMode sets StreamMode selection for this run.
//
// When StreamModeMessages is present, graph-based Large Language Model (LLM)
// nodes will also emit their final (Done=true) model responses by default.
// If you need to override that behavior, call WithGraphEmitFinalModelResponses
// after WithStreamMode.
func WithStreamMode(modes ...StreamMode) RunOption {
	return func(opts *RunOptions) {
		opts.StreamModeEnabled = true
		if len(modes) == 0 {
			opts.StreamModes = nil
			return
		}
		copied := make([]StreamMode, len(modes))
		copy(copied, modes)
		opts.StreamModes = copied
		for _, mode := range copied {
			if mode == StreamModeMessages {
				opts.GraphEmitFinalModelResponses = true
				break
			}
		}
	}
}

// WithDisableGraphCompletionEvent disables emitting the final graph completion event.
func WithDisableGraphCompletionEvent(disable bool) RunOption {
	return func(opts *RunOptions) {
		cfg := getRunControlConfig(opts)
		cfg.DisableGraphCompletionEvent = disable
		setRunControlConfig(opts, cfg)
	}
}

// WithDisableGraphExecutorEvents disables emitting graph executor lifecycle events.
func WithDisableGraphExecutorEvents(disable bool) RunOption {
	return func(opts *RunOptions) {
		cfg := getRunControlConfig(opts)
		cfg.DisableGraphExecutorEvents = disable
		setRunControlConfig(opts, cfg)
	}
}

// WithEventChannelBufferSize overrides the event channel buffer size for this run
// on supported flow and agent implementations.
//
// When size <= 0, supported implementations use their configured default.
func WithEventChannelBufferSize(size int) RunOption {
	return func(opts *RunOptions) {
		cfg := getRunControlConfig(opts)
		cfg.EventChannelBufferSize = size
		setRunControlConfig(opts, cfg)
	}
}

// WithPropagateChildAgentErrors enables strict propagation for terminal child
// agent errors observed through agent-node event streams.
//
// When disabled (default), agent nodes preserve the legacy compatibility
// behavior: child error events remain observable in the stream but do not
// automatically fail the parent graph.
func WithPropagateChildAgentErrors(enabled bool) RunOption {
	return func(opts *RunOptions) {
		cfg := getRunControlConfig(opts)
		cfg.PropagateChildAgentErrors = enabled
		setRunControlConfig(opts, cfg)
	}
}

// WithDisableTracing requests supported agent and flow execution paths to skip
// creating OpenTelemetry spans for this run.
func WithDisableTracing(disable bool) RunOption {
	return func(opts *RunOptions) {
		opts.DisableTracing = disable
	}
}

// WithDisableResponseUsageTracking disables attaching usage and timing info to streaming responses.
func WithDisableResponseUsageTracking(disable bool) RunOption {
	return func(opts *RunOptions) {
		opts.DisableResponseUsageTracking = disable
	}
}

// WithDisableModelExecutionEvents disables emitting model execution events for this run.
func WithDisableModelExecutionEvents(disable bool) RunOption {
	return func(opts *RunOptions) {
		opts.DisableModelExecutionEvents = disable
	}
}

// WithDisablePartialEventIDs disables generating IDs for partial response events.
func WithDisablePartialEventIDs(disable bool) RunOption {
	return func(opts *RunOptions) {
		opts.DisablePartialEventIDs = disable
	}
}

// WithDisablePartialEventTimestamps disables generating timestamps for partial response events.
func WithDisablePartialEventTimestamps(disable bool) RunOption {
	return func(opts *RunOptions) {
		opts.DisablePartialEventTimestamps = disable
	}
}

// WithRequestID sets the request id for the RunOptions.
func WithRequestID(requestID string) RunOption {
	return func(opts *RunOptions) {
		opts.RequestID = requestID
	}
}

// WithEventFilterKey sets the invocation event filter key for this run.
//
// This controls the FilterKey injected into emitted events and the default
// filter prefix used by ContentRequestProcessor when building LLM context.
func WithEventFilterKey(filterKey string) RunOption {
	return func(opts *RunOptions) {
		opts.EventFilterKey = filterKey
	}
}

// WithDetachedCancel enables running a job that ignores parent context
// cancellation.
//
// When enabled, Runner will remove the cancellation signal from the
// execution context while still preserving context values and enforcing
// timeouts and deadlines.
func WithDetachedCancel(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.DetachedCancel = enabled
	}
}

// WithMaxRunDuration sets the maximum duration for a single run.
//
// Runner will enforce the smaller of:
//   - the parent context deadline (if any)
//   - MaxRunDuration (if > 0)
func WithMaxRunDuration(d time.Duration) RunOption {
	return func(opts *RunOptions) {
		opts.MaxRunDuration = d
	}
}

// WithSpanAttributes sets custom span attributes for the RunOptions.
func WithSpanAttributes(attrs ...attribute.KeyValue) RunOption {
	return func(opts *RunOptions) {
		if len(attrs) == 0 {
			opts.SpanAttributes = nil
			return
		}
		opts.SpanAttributes = append([]attribute.KeyValue(nil), attrs...)
	}
}

// WithTraceStartedCallback registers a callback for the run root span.
func WithTraceStartedCallback(
	callback TraceStartedCallback,
) RunOption {
	return func(opts *RunOptions) {
		if callback == nil {
			return
		}
		opts.TraceStartedCallbacks = append(
			opts.TraceStartedCallbacks,
			callback,
		)
	}
}

// WithModel sets the model for this specific run.
// This allows temporarily switching the model for a single request without
// affecting other requests or the agent's default model configuration.
//
// Example:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithModel(customModel),
//	)
func WithModel(m model.Model) RunOption {
	return func(opts *RunOptions) {
		opts.Model = m
	}
}

// WithModelName sets the model name for this specific run.
// The agent will look up the model by name from its registered models.
// This is useful when the agent has multiple models registered via WithModels.
//
// Example:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithModelName("gpt-4"),
//	)
func WithModelName(name string) RunOption {
	return func(opts *RunOptions) {
		opts.ModelName = name
	}
}

// WithModelContextWindow sets the model context window for this specific run.
// This is useful for user-defined or private models whose names should not be
// registered in the process-wide model registry.
func WithModelContextWindow(tokens int) RunOption {
	return func(opts *RunOptions) {
		if tokens > 0 {
			opts.ModelContextWindow = tokens
		}
	}
}

// ModelContextWindowFromRunOptions returns the context window configured by
// WithModelContextWindow.
func ModelContextWindowFromRunOptions(opts *RunOptions) (int, bool) {
	if opts == nil || opts.ModelContextWindow <= 0 {
		return 0, false
	}
	return opts.ModelContextWindow, true
}

// WithModelSelector sets the model selector for this specific run.
// The selector is called before each framework-managed LLM call and takes
// precedence over any agent-level selector.
func WithModelSelector(selector ModelSelector) RunOption {
	return func(opts *RunOptions) {
		opts.ModelSelector = selector
	}
}

// WithCodeExecutor sets the code executor for this specific run.
// If set, it temporarily overrides the agent's default code executor for this
// request only.
func WithCodeExecutor(exec codeexecutor.CodeExecutor) RunOption {
	return func(opts *RunOptions) {
		opts.CodeExecutor = exec
	}
}

// WithStream enables or disables streaming for this specific run.
//
// When set, it overrides the agent's default Stream setting for this Run.
func WithStream(stream bool) RunOption {
	return func(opts *RunOptions) {
		opts.Stream = &stream
	}
}

// WithInstruction sets the instruction for this specific run.
// If set, it temporarily overrides the agent's instruction for this request
// only. This does not modify the agent instance.
func WithInstruction(instruction string) RunOption {
	return func(opts *RunOptions) {
		opts.Instruction = instruction
	}
}

// WithGlobalInstruction sets the global instruction (system prompt) for this
// specific run.
// If set, it temporarily overrides the agent's global instruction for this
// request only. This does not modify the agent instance.
func WithGlobalInstruction(instruction string) RunOption {
	return func(opts *RunOptions) {
		opts.GlobalInstruction = instruction
	}
}

// WithWorkspaceExecGuidance sets request-scoped workspace_exec guidance.
// Empty guidance leaves the built-in default guidance in use.
func WithWorkspaceExecGuidance(guidance string) RunOption {
	return func(opts *RunOptions) {
		opts.WorkspaceExecGuidance = guidance
	}
}

// WithAvailableSkillsRenderer sets a request-scoped renderer for the
// Available skills section.
//
// Returning a blank string omits the Available skills section.
func WithAvailableSkillsRenderer(renderer AvailableSkillsRenderer) RunOption {
	return func(opts *RunOptions) {
		opts.AvailableSkillsRenderer = renderer
	}
}

// WithStructuredOutputJSONSchema sets a JSON schema structured output for this run.
func WithStructuredOutputJSONSchema(name string, schema map[string]any, strict bool, description string) RunOption {
	return func(opts *RunOptions) {
		if schema == nil {
			return
		}
		opts.StructuredOutput = newStructuredOutput(
			structuredoutput.Name(name),
			schema,
			strict,
			description,
		)
	}
}

// WithStructuredOutputJSON sets a JSON schema structured output for this run.
// The schema is constructed automatically from the provided example type.
func WithStructuredOutputJSON(examplePtr any, strict bool, description string) RunOption {
	return func(opts *RunOptions) {
		name, schema, t := structuredoutput.FromType(examplePtr, strict)
		if schema == nil {
			return
		}
		opts.StructuredOutput = newStructuredOutput(name, schema, strict, description)
		opts.StructuredOutputType = t
	}
}

func newStructuredOutput(name string, schema map[string]any, strict bool, description string) *model.StructuredOutput {
	if schema == nil {
		return nil
	}
	return &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        name,
			Schema:      schema,
			Strict:      strict,
			Description: description,
		},
	}
}

// WithToolFilter sets a custom tool filter function for this specific run.
// The filter function receives a context and a tool, and returns true if the tool should be included.
//
// This is useful for:
//   - Permission control: restrict tool access based on user roles or runtime conditions
//   - Cost optimization: reduce token usage by limiting tool descriptions
//   - Feature isolation: limit capabilities for specific use cases
//   - Dynamic filtering: filter tools based on runtime state, session data, etc.
//
// Example - Simple name-based filtering:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithToolFilter(tool.NewIncludeToolNamesFilter("calculator", "time_tool")),
//	)
//
// Example - Custom logic with runtime state:
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithToolFilter(func(ctx context.Context, t tool.Tool) bool {
//	        // Access invocation from context if needed
//	        inv, _ := agent.InvocationFromContext(ctx)
//	        userLevel, _ := inv.Session.Get("user_level").(string)
//
//	        // Premium users get all tools
//	        if userLevel == "premium" {
//	            return true
//	        }
//
//	        // Free users only get basic tools
//	        toolName := t.Declaration().Name
//	        return toolName == "calculator" || toolName == "time_tool"
//	    }),
//	)
//
// Note: Framework tools (knowledge_search, transfer_to_agent) are never filtered
// and will always be available regardless of the filter function.
//
// Note: This is a "soft" constraint. Tools should still implement their own
// authorization logic for security.
func WithToolFilter(filter tool.FilterFunc) RunOption {
	return func(opts *RunOptions) {
		opts.ToolFilter = filter
	}
}

// WithAdditionalTools appends tools that are visible only for this run.
//
// Additional tools are treated as user tools, so WithToolFilter can still
// hide them. If an additional tool has the same name as an already available
// tool, the already available tool wins for that run.
func WithAdditionalTools(tools []tool.Tool) RunOption {
	return func(opts *RunOptions) {
		appendRunTools(opts, tools)
	}
}

// WithExternalTools appends caller-executed tools for this run.
//
// External tools are visible to the model like additional tools, but the
// framework will not execute them. When the model calls one, the run stops
// after the assistant tool_call response. The caller should execute the tool
// externally and continue with model.NewToolMessage.
func WithExternalTools(tools []tool.Tool) RunOption {
	return func(opts *RunOptions) {
		if opts == nil {
			return
		}
		for _, tl := range tools {
			if declarationName(tl) == "" {
				continue
			}
			opts.ExternalTools = append(opts.ExternalTools, tl)
		}
	}
}

// WithToolExecutionFilter sets which tools the framework will execute.
//
// This is different from WithToolFilter:
//   - WithToolFilter controls which tools are visible to the model.
//   - WithToolExecutionFilter controls which tool calls are auto-executed
//     after the model requests them.
//
// When the filter returns false for a tool, the tool call is not executed
// and the run ends after emitting the assistant tool_call response. The
// caller can then execute the tool externally and provide a RoleTool
// message with the tool result to continue.
func WithToolExecutionFilter(filter tool.FilterFunc) RunOption {
	return func(opts *RunOptions) {
		opts.ToolExecutionFilter = filter
	}
}

// WithToolPermissionPolicy sets a per-run policy that is checked after
// before-tool callbacks finalize arguments and immediately before the
// framework executes a tool call.
//
// The policy is intentionally separate from WithToolFilter and
// WithToolExecutionFilter:
//   - WithToolFilter controls which tools are visible to the model.
//   - WithToolExecutionFilter controls whether the framework auto-executes
//     a visible tool or leaves it to the caller.
//   - WithToolPermissionPolicy executes a permission check for tools the
//     framework is about to run.
//
// When no per-run policy is configured, tools without their own checker keep
// the legacy allow behavior. When a per-run policy is configured, it is applied
// to every tool the framework is about to execute, including tools that do not
// implement tool.PermissionChecker.
func WithToolPermissionPolicy(policy tool.PermissionPolicy) RunOption {
	return func(opts *RunOptions) {
		opts.ToolPermissionPolicy = policy
	}
}

// WithToolPermissionPolicyFunc adapts fn into a per-run tool permission policy.
func WithToolPermissionPolicyFunc(fn tool.PermissionPolicyFunc) RunOption {
	return WithToolPermissionPolicy(fn)
}

func appendRunTools(opts *RunOptions, tools []tool.Tool) {
	if opts == nil || len(tools) == 0 {
		return
	}
	for _, tl := range tools {
		if declarationName(tl) == "" {
			continue
		}
		opts.AdditionalTools = append(opts.AdditionalTools, tl)
	}
}

func declarationName(tl tool.Tool) string {
	if tl == nil {
		return ""
	}
	decl := tl.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}

// WithToolCallArgumentsJSONRepairEnabled enables best-effort JSON repair for tool call arguments.
func WithToolCallArgumentsJSONRepairEnabled(enabled bool) RunOption {
	return func(opts *RunOptions) {
		e := enabled
		opts.ToolCallArgumentsJSONRepairEnabled = &e
	}
}

// WithA2ARequestOptions sets the A2A request options for the RunOptions.
// These options will be passed to A2A agent's SendMessage and StreamMessage calls.
// This allows passing dynamic HTTP headers or other request-specific options for each run.
func WithA2ARequestOptions(opts ...any) RunOption {
	return func(runOpts *RunOptions) {
		runOpts.A2ARequestOptions = append(runOpts.A2ARequestOptions, opts...)
	}
}

// WithCustomAgentConfigs sets custom agent configurations.
// This allows passing agent-specific configurations at runtime without modifying the agent implementation.
//
// Parameters:
//   - configs: A map where the key is the agent type identifier and the value is the agent-specific config.
//     It's recommended to use the agent's defined RunOptionKey constant as the key and a typed options struct as the value.
//
// Usage:
//
//	// Example: Configure a custom LLM agent using its defined key and options struct
//	import customllm "your.module/agents/customllm"
//
//	runner.Run(ctx, userID, sessionID, message,
//	    agent.WithCustomAgentConfigs(map[string]any{
//	        customllm.RunOptionKey: customllm.RunOptions{
//	            "custom-context": "context",
//	        },
//	    }),
//	)
//
//
//	// In your custom agent implementation, retrieve the config:
//	func (a *CustomLLMAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
//	    config := inv.GetCustomAgentConfig(RunOptionKey)
//	    if opts, ok := config.(RunOptions); ok {
//	        client := NewLLMClient(opts.APIKey, opts.Model, opts.Temperature)
//	        // Use the configuration...
//	    }
//	    // ...
//	}
//
// Note:
//   - This function creates a shallow copy of the configs map to prevent external modifications.
//   - The stored configuration should be treated as read-only. Do not modify it after retrieval.
func WithCustomAgentConfigs(configs map[string]any) RunOption {
	return func(opts *RunOptions) {
		if configs == nil {
			opts.CustomAgentConfigs = nil
			return
		}
		// Create a shallow copy to prevent external modifications
		copied := make(map[string]any, len(configs))
		for k, v := range configs {
			copied[k] = v
		}
		opts.CustomAgentConfigs = copied
	}
}

func getRunControlConfig(opts *RunOptions) runControlConfig {
	if opts == nil {
		return runControlConfig{}
	}
	return opts.runControlConfig
}

func setRunControlConfig(opts *RunOptions, cfg runControlConfig) {
	if opts == nil {
		return
	}
	opts.runControlConfig = cfg
}

// RunOptions is the options for the Run method.
type RunOptions struct {
	// AppName overrides the runner's default app name for this specific run.
	//
	// When set, the runner uses this value instead of its constructor-provided
	// app name for session keys, memory operations, and event filter keys.
	// This enables a single runner instance to serve multiple projects or
	// tenants, isolating their session and memory data by app name.
	//
	// If empty, the runner falls back to its default app name.
	AppName string

	// RuntimeState contains key-value pairs that will be merged into the initial state
	// for this specific run. This allows callers to pass dynamic parameters
	// (e.g., room ID, user context) without modifying the agent's base initial state.
	RuntimeState map[string]any

	// EventFilterKey overrides the invocation's event filter key used for
	// scoping session events (event.FilterKey) included in LLM context.
	//
	// Runner applies this value via WithInvocationEventFilterKey when it
	// constructs the invocation. When using Runner, the value should
	// typically start with the runner app name (e.g., "<appName>/...") so
	// sessions hooks and summaries continue to work as expected.
	EventFilterKey string

	// KnowledgeFilter contains metadata key-value pairs for the knowledge filter
	KnowledgeFilter map[string]any

	// KnowledgeConditionedFilter contains complex condition filter for the knowledge search
	KnowledgeConditionedFilter *searchfilter.UniversalFilterCondition

	// Messages allows callers to provide a full conversation history to Runner.
	// Runner will seed an empty Session with this history automatically and
	// then rely on Session events for subsequent turns. The content processor
	// ignores this field and reads only from Session events (or falls back to
	// `invocation.Message` when no events exist).
	Messages []model.Message

	// InjectedContextMessages allows callers to inject additional context messages
	// into the model request for this run. These messages are not persisted into
	// session events and therefore must be provided on every run if needed.
	InjectedContextMessages []model.Message

	// UserMessageRewriter rewrites the current-turn input into an ordered
	// message sequence before runner persists it into the session transcript.
	UserMessageRewriter UserMessageRewriter

	// Resume indicates whether this run should attempt to resume from existing
	// session context before making a new model call. When true, flows may
	// inspect the latest session events (for example, assistant messages with
	// pending tool calls) and complete unfinished work prior to issuing a new
	// LLM request.
	Resume bool

	// PersistInterruptedAssistant controls whether Runner persists already
	// emitted assistant text as a final assistant message when a streaming run
	// is cancelled before a normal final assistant response is produced.
	//
	// nil means the Runner default applies. The built-in Runner default is
	// false to preserve the legacy cancellation semantics.
	PersistInterruptedAssistant *bool

	// GraphEmitFinalModelResponses controls event emission for graph-based
	// Large Language Model (LLM) nodes.
	//
	// When false (default), graph LLM nodes only emit streaming chunks
	// (Done=false).
	//
	// When true, graph LLM nodes also emit the final model response
	// (Done=true). In that mode, callers should be prepared to receive
	// assistant messages from intermediate nodes.
	//
	// When enabled, Runner may omit echoing the final assistant message
	// in its runner-completion event to avoid duplicates.
	GraphEmitFinalModelResponses bool

	// GraphTerminalMessagesOnly limits caller-visible graph message events to
	// terminal nodes only.
	//
	// When false (default), graph message-capable nodes may all emit
	// caller-visible events.
	//
	// When true, GraphAgent keeps internal state propagation unchanged, but
	// caller-visible message events are forwarded only for terminal LLM nodes
	// and terminal sub-agent nodes.
	GraphTerminalMessagesOnly bool

	// StreamModeEnabled indicates whether the caller explicitly configured
	// StreamModes for this run.
	StreamModeEnabled bool

	// StreamModes selects which categories of events are forwarded to callers.
	//
	// When StreamModeEnabled is false, runners should not apply any stream
	// filtering and preserve the existing behavior.
	StreamModes []StreamMode

	// DisableTracing requests supported agent and flow execution paths to skip
	// creating OpenTelemetry spans for this run.
	DisableTracing bool

	// DisableResponseUsageTracking disables attaching usage and timing info to streaming responses.
	DisableResponseUsageTracking bool

	// DisableModelExecutionEvents disables emitting model execution start/complete events.
	DisableModelExecutionEvents bool

	// DisablePartialEventIDs disables generating IDs for partial response events.
	DisablePartialEventIDs bool

	// DisablePartialEventTimestamps disables generating timestamps for partial response events.
	DisablePartialEventTimestamps bool

	// ExecutionTraceEnabled enables in-process execution trace recording for this run.
	ExecutionTraceEnabled bool

	// RequestID is the request id of the request.
	RequestID string

	// DetachedCancel controls whether Runner ignores parent context
	// cancellation for this run.
	DetachedCancel bool

	// MaxRunDuration bounds the total execution time for this run.
	// When set, Runner enforces the smaller of:
	//   - the parent context deadline (if any)
	//   - MaxRunDuration
	MaxRunDuration time.Duration

	// SpanAttributes carries custom span attributes for this run.
	SpanAttributes []attribute.KeyValue

	// TraceStartedCallbacks run when the root span starts for this run.
	TraceStartedCallbacks []TraceStartedCallback

	// A2ARequestOptions contains A2A client request options that will be passed to
	// A2A agent's SendMessage and StreamMessage calls. This allows callers to pass
	// dynamic HTTP headers or other request-specific options for each run.
	//
	// Note: This field uses any type to avoid direct dependency on trpc-a2a-go/client package.
	// Users should pass client.RequestOption values (e.g., client.WithRequestHeader).
	// The a2aagent package will validate the option types at runtime.
	A2ARequestOptions []any

	// CustomAgentConfigs stores configurations for custom agents.
	// Key: agent type, Value: agent-specific config.
	CustomAgentConfigs map[string]any

	// Agent overrides the runner's default agent for this run.
	Agent Agent

	// AgentByName instructs the runner to resolve an agent by name for this run.
	AgentByName string

	// Model is the model to use for this specific run.
	// If set, it temporarily overrides the agent's default model for this request only.
	// This allows per-request model switching without affecting other concurrent requests.
	Model model.Model

	// ModelName is the name of the model to use for this specific run.
	// The agent will look up the model by name from its registered models.
	// If both Model and ModelName are set, Model takes precedence.
	ModelName string
	// ModelSelector selects the model before each framework-managed LLM call.
	ModelSelector ModelSelector

	// ModelContextWindow is the model context window for this specific run.
	// If set, it takes precedence over model instance configuration and the
	// process-wide model registry.
	ModelContextWindow int

	// ModelRequestExtraFields contains provider-specific top-level request body
	// fields for model calls made during this run.
	//
	// Adapters that support extra fields merge these with model-level extra
	// fields, with these request-level values taking precedence.
	ModelRequestExtraFields map[string]any

	// CodeExecutor is the code executor to use for this specific run.
	// If set, it temporarily overrides the agent's default code executor for
	// this request only.
	CodeExecutor codeexecutor.CodeExecutor

	// Stream overrides GenerationConfig.Stream for this run when non-nil.
	//
	// This is useful when you want to switch between streaming and
	// non-streaming responses per request without rebuilding the agent.
	Stream *bool

	// Instruction overrides the agent's instruction for this run.
	// If set, it temporarily overrides the agent's instruction for this request
	// only.
	Instruction string

	// GlobalInstruction overrides the agent's global instruction (system prompt)
	// for this run.
	// If set, it temporarily overrides the agent's global instruction for
	// this request only.
	GlobalInstruction string

	// WorkspaceExecGuidance overrides workspace_exec guidance for this run.
	// If empty, the built-in guidance is used.
	WorkspaceExecGuidance string
	// AvailableSkillsRenderer renders the Available skills section for this run.
	// If nil, the built-in renderer is used. If it returns blank text, the section
	// is omitted.
	AvailableSkillsRenderer AvailableSkillsRenderer

	// StructuredOutput defines how the model should produce structured output for this run.
	StructuredOutput *model.StructuredOutput

	// StructuredOutputType is the Go type to unmarshal the final JSON into for this run.
	StructuredOutputType reflect.Type

	// ToolFilter is a custom function to filter tools for this run.
	// If set, only tools for which the filter returns true will be available to the model.
	// If nil, all registered tools will be available (default behavior).
	//
	// The filter function receives:
	//   - ctx: The context with invocation information (use agent.InvocationFromContext)
	//   - tool: The tool being filtered
	//
	// This filtering happens at the request preparation stage, before sending to the model.
	// The model will only see the tool descriptions for tools that pass the filter.
	//
	// Note: Framework tools (knowledge_search, transfer_to_agent) are never filtered
	// and will always be included regardless of the filter function's return value.
	//
	// Example:
	//   agent.WithToolFilter(tool.NewIncludeToolNamesFilter("calculator", "time_tool"))
	//   agent.WithToolFilter(func(ctx context.Context, t tool.Tool) bool {
	//       return t.Declaration().Name == "calculator"
	//   })
	ToolFilter tool.FilterFunc

	// AdditionalTools contains tools that are visible only for this run.
	//
	// These tools are treated as user tools and are therefore affected by
	// ToolFilter. They are appended to the effective tool surface without
	// mutating the agent's registered tools.
	AdditionalTools []tool.Tool

	// ExternalTools contains caller-executed tools that are visible only for
	// this run. The framework exposes them to the model, but does not execute
	// them after the model returns a tool call.
	ExternalTools []tool.Tool

	// ExternalToolNames contains the accepted caller-executed tool names for
	// this run. LLM flows set it after the invocation tool surface rejects
	// collisions with existing tools.
	ExternalToolNames map[string]bool

	// ToolExecutionFilter controls which tools are executed by the
	// framework when the model returns tool calls.
	//
	// This is different from ToolFilter:
	//   - ToolFilter controls which tools are sent to (and callable by) the
	//     model.
	//   - ToolExecutionFilter controls which tool calls are auto-executed by
	//     the framework after the model requests them.
	//
	// When this filter is set and returns false for a tool, the tool call
	// is not executed by the agent. The run stops after emitting the
	// assistant tool_call response so the caller can execute the tool
	// externally and later provide tool results (RoleTool messages).
	ToolExecutionFilter tool.FilterFunc

	// ToolPermissionPolicy checks whether a tool call may run after the model
	// has requested it, after argument repair, and after before-tool callbacks
	// have finalized arguments.
	//
	// This policy does not change the visible tool surface. Use ToolFilter for
	// that. It also does not replace callbacks or guardrail plugins; before-tool
	// callbacks can still normalize arguments before the policy sees them. A deny
	// or ask decision skips tool execution and returns a structured permission
	// result to the model.
	ToolPermissionPolicy tool.PermissionPolicy

	// ToolCallArgumentsJSONRepairEnabled enables best-effort JSON repair for tool call arguments.
	// When nil, JSON repair is disabled by default.
	ToolCallArgumentsJSONRepairEnabled *bool

	// runControlConfig stores internal event and buffering controls.
	runControlConfig runControlConfig
}

// ShouldExecuteTool reports whether the framework should execute a tool call.
//
// External tools are always caller-executed and therefore return false. The
// ToolExecutionFilter is evaluated only for non-external tools.
func (opts RunOptions) ShouldExecuteTool(
	ctx context.Context,
	tl tool.Tool,
) bool {
	if opts.isExternalTool(tl) {
		return false
	}
	if opts.ToolExecutionFilter == nil {
		return true
	}
	return opts.ToolExecutionFilter(ctx, tl)
}

func (opts RunOptions) isExternalTool(tl tool.Tool) bool {
	name := declarationName(tl)
	if opts.ExternalToolNames != nil {
		return name != "" && opts.ExternalToolNames[name]
	}
	for _, external := range opts.ExternalTools {
		if sameRunTool(tl, external) {
			return true
		}
	}
	return false
}

func sameRunTool(a tool.Tool, b tool.Tool) bool {
	if a == nil || b == nil {
		return false
	}
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if av.Type() == bv.Type() && av.Type().Comparable() {
		return a == b
	}
	return false
}

// IsGraphCompletionEventDisabled reports whether this invocation hides terminal graph completion events.
func IsGraphCompletionEventDisabled(inv *Invocation) bool {
	if inv == nil {
		return false
	}
	return getRunControlConfig(&inv.RunOptions).DisableGraphCompletionEvent
}

// IsGraphExecutorEventsDisabled reports whether this invocation hides graph executor lifecycle events.
func IsGraphExecutorEventsDisabled(inv *Invocation) bool {
	if inv == nil {
		return false
	}
	return getRunControlConfig(&inv.RunOptions).DisableGraphExecutorEvents
}

// GetEventChannelBufferSize returns the invocation-specific event channel buffer size override.
func GetEventChannelBufferSize(inv *Invocation) int {
	if inv == nil {
		return 0
	}
	return getRunControlConfig(&inv.RunOptions).EventChannelBufferSize
}

// ShouldPropagateChildAgentErrors reports whether terminal child agent errors
// should fail the parent graph by default.
func ShouldPropagateChildAgentErrors(inv *Invocation) bool {
	if inv == nil {
		return false
	}
	return getRunControlConfig(&inv.RunOptions).PropagateChildAgentErrors
}

// NewInvocation create a new invocation
func NewInvocation(invocationOpts ...InvocationOptions) *Invocation {
	inv := &Invocation{
		InvocationID:   uuid.NewString(),
		noticeMu:       &sync.Mutex{},
		noticeChannels: make(map[string]chan any),
	}

	for _, opt := range invocationOpts {
		opt(inv)
	}

	if inv.Message.Role == "" && model.HasPayload(inv.Message) {
		log.Warnf(
			"agent.NewInvocation received a message with empty role; defaulting to user",
		)
		inv.Message.Role = model.RoleUser
	}

	if inv.Branch == "" {
		inv.Branch = inv.AgentName
	}

	if inv.eventFilterKey == "" && inv.AgentName != "" {
		inv.eventFilterKey = inv.AgentName
	}
	inv.initializeExecutionTrace()

	return inv
}

// Clone clone a new invocation
func (inv *Invocation) Clone(invocationOpts ...InvocationOptions) *Invocation {
	if inv == nil {
		return nil
	}
	newInv := &Invocation{
		InvocationID:    uuid.NewString(),
		Session:         inv.Session,
		SessionService:  inv.SessionService,
		Message:         inv.Message,
		RunOptions:      inv.RunOptions,
		MemoryService:   inv.MemoryService,
		ArtifactService: inv.ArtifactService,
		Plugins:         inv.Plugins,
		noticeMu:        inv.noticeMu,
		noticeChannels:  inv.noticeChannels,
		eventFilterKey:  inv.eventFilterKey,
		parent:          inv,
		state:           inv.cloneState(),
	}

	for _, opt := range invocationOpts {
		opt(newInv)
	}

	if newInv.Branch != "" {
		// seted by WithInvocationBranch
	} else if inv.Branch != "" && newInv.AgentName != "" {
		newInv.Branch = inv.Branch + BranchDelimiter + newInv.AgentName
	} else if newInv.AgentName != "" {
		newInv.Branch = newInv.AgentName
	} else {
		newInv.Branch = inv.Branch
	}

	if newInv.eventFilterKey == "" && newInv.AgentName != "" {
		newInv.eventFilterKey = newInv.AgentName
	}
	if newInv.RunOptions.ExecutionTraceEnabled && inv.RunOptions.ExecutionTraceEnabled {
		inv.initializeExecutionTrace()
		newInv.traceCapture = inv.executionTraceCapture()
	}
	if newInv.traceCapture == nil {
		newInv.initializeExecutionTrace()
	}
	if newInv.traceCapture != nil {
		newInv.traceCapture.RegisterInvocation(inv.InvocationID, newInv.InvocationID)
		newInv.ensureTraceCaptureMetadata()
	}

	return newInv
}

// View returns an isolated invocation view that preserves identity.
func (inv *Invocation) View(invocationOpts ...InvocationOptions) *Invocation {
	if inv == nil {
		return nil
	}
	traceCapture, traceNodeID := inv.executionTraceFields()
	view := &Invocation{
		Agent:                inv.Agent,
		AgentName:            inv.AgentName,
		InvocationID:         inv.InvocationID,
		Branch:               inv.Branch,
		EndInvocation:        inv.EndInvocation,
		Session:              inv.Session,
		SessionService:       inv.SessionService,
		Model:                inv.Model,
		Message:              inv.Message,
		RunOptions:           inv.RunOptions,
		TransferInfo:         inv.TransferInfo,
		Plugins:              inv.Plugins,
		StructuredOutput:     inv.StructuredOutput,
		StructuredOutputType: inv.StructuredOutputType,
		MemoryService:        inv.MemoryService,
		ArtifactService:      inv.ArtifactService,
		noticeChannels:       inv.noticeChannels,
		noticeMu:             inv.noticeMu,
		eventFilterKey:       inv.eventFilterKey,
		parent:               inv.parent,
		traceCapture:         traceCapture,
		entryPredecessorStepIDs: cloneStringSlice(
			inv.entryPredecessorStepIDs,
		),
		traceNodeID:        traceNodeID,
		state:              inv.cloneViewState(),
		MaxLLMCalls:        inv.MaxLLMCalls,
		MaxToolIterations:  inv.MaxToolIterations,
		timingInfo:         inv.timingInfo,
		llmCallCount:       inv.llmCallCount,
		toolIterationCount: inv.toolIterationCount,
	}
	for _, opt := range invocationOpts {
		opt(view)
	}
	return view
}

// SyncView copies execution-visible state from a view while preserving RunOptions.
func (inv *Invocation) SyncView(view *Invocation) {
	if inv == nil || view == nil || inv == view {
		return
	}
	inv.Agent = view.Agent
	inv.AgentName = view.AgentName
	inv.InvocationID = view.InvocationID
	inv.Branch = view.Branch
	inv.EndInvocation = view.EndInvocation
	inv.Session = view.Session
	inv.SessionService = view.SessionService
	inv.Model = view.Model
	inv.Message = view.Message
	inv.TransferInfo = view.TransferInfo
	inv.Plugins = view.Plugins
	inv.StructuredOutput = view.StructuredOutput
	inv.StructuredOutputType = view.StructuredOutputType
	inv.MemoryService = view.MemoryService
	inv.ArtifactService = view.ArtifactService
	inv.noticeChannels = view.noticeChannels
	inv.noticeMu = view.noticeMu
	inv.eventFilterKey = view.eventFilterKey
	inv.parent = view.parent
	traceCapture, traceNodeID := view.executionTraceFields()
	inv.traceMu.Lock()
	inv.traceCapture = traceCapture
	inv.traceNodeID = traceNodeID
	inv.traceMu.Unlock()
	inv.entryPredecessorStepIDs = cloneStringSlice(
		view.entryPredecessorStepIDs,
	)
	inv.MaxLLMCalls = view.MaxLLMCalls
	inv.MaxToolIterations = view.MaxToolIterations
	inv.timingInfo = view.timingInfo
	inv.llmCallCount = view.llmCallCount
	inv.toolIterationCount = view.toolIterationCount
	inv.stateMu.Lock()
	inv.state = view.cloneViewState()
	inv.stateMu.Unlock()
}

func (inv *Invocation) cloneState() map[string]any {
	return inv.cloneStateByFilter(isCloneStateKey, keepStateValue)
}

func (inv *Invocation) cloneViewState() map[string]any {
	return inv.cloneStateByFilter(includeAllStateKeys, cloneViewStateValue)
}

func (inv *Invocation) cloneStateByFilter(
	include func(string) bool,
	cloneValue func(string, any) any,
) map[string]any {
	if inv == nil {
		return nil
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()
	if inv.state == nil {
		return nil
	}
	copied := make(map[string]any, len(inv.state))
	for key, value := range inv.state {
		if !include(key) {
			continue
		}
		copied[key] = cloneValue(key, value)
	}
	return copied
}

func includeAllStateKeys(string) bool {
	return true
}

func keepStateValue(_ string, value any) any {
	return value
}

func cloneViewStateValue(key string, value any) any {
	if isCloneStateKey(key) {
		return value
	}
	return cloneStateValue(value)
}

func isCloneStateKey(key string) bool {
	switch key {
	case flusherStateKey,
		barrierStateKey,
		appenderStateKey,
		streamHubStateKey,
		surfaceRootNodeIDStateKey,
		teamMemberTraceRootStateKey:
		return true
	default:
		return false
	}
}

// cloneStateValue isolates common mutable custom state for invocation views.
// Known mutable types such as bytes.Buffer, strings.Builder, and big.Int are
// copied explicitly. Maps, slices, pointers, arrays, and fully exported
// structs are cloned recursively. Opaque structs with unexported fields are
// kept by reference to avoid unsafe copies of no-copy state such as locks.
func cloneStateValue(value any) any {
	if value == nil {
		return nil
	}
	cloned, ok := cloneStateReflectValue(
		reflect.ValueOf(value),
		map[reflectVisit]reflect.Value{},
	)
	if !ok {
		return value
	}
	return cloned.Interface()
}

type reflectVisit struct {
	typ      reflect.Type
	ptr      uintptr
	length   int
	capacity int
}

func cloneStateReflectValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	if value.IsValid() && value.CanInterface() {
		if cloned, ok := cloneKnownStateValue(value.Interface()); ok {
			return reflect.ValueOf(cloned), true
		}
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value, true
		}
		return cloneStateReflectValue(value.Elem(), visited)
	case reflect.Pointer:
		return cloneStatePointerValue(value, visited)
	case reflect.Map:
		return cloneStateMapValue(value, visited)
	case reflect.Slice:
		return cloneStateSliceValue(value, visited)
	case reflect.Array:
		return cloneStateArrayValue(value, visited)
	case reflect.Struct:
		return cloneStateStructValue(value, visited)
	default:
		return value, true
	}
}

func cloneKnownStateValue(value any) (any, bool) {
	switch v := value.(type) {
	case *bytes.Buffer:
		if v == nil {
			return v, true
		}
		return bytes.NewBuffer(cloneBytes(v.Bytes())), true
	case bytes.Buffer:
		return *bytes.NewBuffer(cloneBytes(v.Bytes())), true
	case *strings.Builder:
		if v == nil {
			return v, true
		}
		var cloned strings.Builder
		_, _ = cloned.WriteString(v.String())
		return &cloned, true
	case *big.Int:
		if v == nil {
			return v, true
		}
		return new(big.Int).Set(v), true
	case big.Int:
		return *new(big.Int).Set(&v), true
	default:
		return nil, false
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func cloneStatePointerValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	if value.IsNil() {
		return value, true
	}
	visit := reflectVisit{
		typ: value.Type(),
		ptr: value.Pointer(),
	}
	if cloned, ok := visited[visit]; ok {
		return cloned, true
	}
	cloned := reflect.New(value.Type().Elem())
	visited[visit] = cloned
	elem, ok := cloneStateReflectValue(value.Elem(), visited)
	if !ok {
		delete(visited, visit)
		return value, false
	}
	if elem.Type().AssignableTo(cloned.Elem().Type()) {
		cloned.Elem().Set(elem)
		return cloneStateAsType(cloned, value.Type())
	}
	if elem.Type().ConvertibleTo(cloned.Elem().Type()) {
		cloned.Elem().Set(elem.Convert(cloned.Elem().Type()))
		return cloneStateAsType(cloned, value.Type())
	}
	return value, false
}

func cloneStateAsType(
	value reflect.Value,
	typ reflect.Type,
) (reflect.Value, bool) {
	if value.Type().AssignableTo(typ) {
		return value, true
	}
	if value.Type().ConvertibleTo(typ) {
		return value.Convert(typ), true
	}
	return value, false
}

func cloneStateMapValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	if value.IsNil() {
		return value, true
	}
	visit := reflectVisit{
		typ: value.Type(),
		ptr: value.Pointer(),
	}
	if cloned, ok := visited[visit]; ok {
		return cloned, true
	}
	cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
	visited[visit] = cloned
	iter := value.MapRange()
	for iter.Next() {
		// Keep keys unchanged; cloning pointer keys changes lookup identity.
		cloned.SetMapIndex(
			iter.Key(),
			cloneStateElement(iter.Value(), value.Type().Elem(), visited),
		)
	}
	return cloned, true
}

func cloneStateElement(
	value reflect.Value,
	elemType reflect.Type,
	visited map[reflectVisit]reflect.Value,
) reflect.Value {
	cloned, ok := cloneStateReflectValue(value, visited)
	if !ok {
		return value
	}
	if cloned.Type().AssignableTo(elemType) {
		return cloned
	}
	if cloned.Type().ConvertibleTo(elemType) {
		return cloned.Convert(elemType)
	}
	return value
}

func cloneStateSliceValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	if value.IsNil() {
		return value, true
	}
	visit := reflectVisit{
		typ:      value.Type(),
		ptr:      value.Pointer(),
		length:   value.Len(),
		capacity: value.Cap(),
	}
	if cloned, ok := visited[visit]; ok {
		return cloned, true
	}
	cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	visited[visit] = cloned
	for i := 0; i < value.Len(); i++ {
		cloned.Index(i).Set(
			cloneStateElement(
				value.Index(i),
				value.Type().Elem(),
				visited,
			),
		)
	}
	return cloned, true
}

func cloneStateArrayValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	cloned := reflect.New(value.Type()).Elem()
	for i := 0; i < value.Len(); i++ {
		cloned.Index(i).Set(
			cloneStateElement(
				value.Index(i),
				value.Type().Elem(),
				visited,
			),
		)
	}
	return cloned, true
}

func cloneStateStructValue(
	value reflect.Value,
	visited map[reflectVisit]reflect.Value,
) (reflect.Value, bool) {
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).PkgPath != "" {
			// Opaque structs may carry no-copy state such as locks.
			return value, false
		}
	}
	cloned := reflect.New(value.Type()).Elem()
	for i := 0; i < value.NumField(); i++ {
		target := cloned.Field(i)
		target.Set(
			cloneStateElement(
				value.Field(i),
				target.Type(),
				visited,
			),
		)
	}
	return cloned, true
}

// GetEventFilterKey get event filter key.
func (inv *Invocation) GetEventFilterKey() string {
	if inv == nil {
		return ""
	}
	return inv.eventFilterKey
}

// GetParentInvocation get parent invocation.
func (inv *Invocation) GetParentInvocation() *Invocation {
	if inv == nil {
		return nil
	}
	return inv.parent
}

// InjectIntoEvent inject invocation information into event.
func InjectIntoEvent(inv *Invocation, e *event.Event) {
	if e == nil || inv == nil {
		return
	}

	e.RequestID = inv.RunOptions.RequestID
	if inv.parent != nil {
		e.ParentInvocationID = inv.parent.InvocationID
	}
	e.InvocationID = inv.InvocationID
	e.Branch = inv.Branch
	e.FilterKey = inv.GetEventFilterKey()
}

// EmitEvent inject invocation information into event and emit it to channel.
func EmitEvent(ctx context.Context, inv *Invocation, ch chan<- *event.Event,
	e *event.Event) error {
	if ch == nil || e == nil {
		return nil
	}
	attachAwaitUserReplyRoute(inv, e)
	InjectIntoEvent(inv, e)
	var agentName, requestID string
	if inv != nil {
		agentName = inv.AgentName
		requestID = inv.RunOptions.RequestID
	}
	log.Tracef(
		"[agent.EmitEvent]queue monitoring:RequestID: %s channel capacity: "+
			"%d, current length: %d, branch: %s, agent name:%s",
		requestID,
		cap(ch),
		len(ch),
		e.Branch,
		agentName,
	)
	return event.EmitEvent(ctx, ch, e)
}

// GetAppendEventNoticeKey get append event notice key.
func GetAppendEventNoticeKey(eventID string) string {
	return AppendEventNoticeKeyPrefix + eventID
}

// SetState sets a value in the invocation state.
//
// This is a general-purpose key-value store scoped to the invocation lifecycle.
// It can be used by callbacks, middleware, or any invocation-scoped logic.
//
// Recommended key naming conventions:
//   - Agent callbacks: "agent:xxx" (e.g., "agent:start_time")
//   - Model callbacks: "model:xxx" (e.g., "model:start_time")
//   - Tool callbacks: "tool:<toolName>:<toolCallID>:xxx" (e.g., "tool:calculator:call_abc123:start_time")
//   - Middleware: "middleware:xxx" (e.g., "middleware:request_id")
//   - Custom logic: "custom:xxx" (e.g., "custom:user_context")
//
// Note: Tool callbacks should include tool call ID to support concurrent calls.
//
// Example:
//
//	inv.SetState("agent:start_time", time.Now())
//	inv.SetState("model:start_time", time.Now())
//	inv.SetState("tool:calculator:call_abc123:start_time", time.Now())
//	inv.SetState("middleware:request_id", "req-123")
//	inv.SetState("custom:user_context", userCtx)
func (inv *Invocation) SetState(key string, value any) {
	if inv == nil {
		return
	}
	inv.stateMu.Lock()
	defer inv.stateMu.Unlock()

	if inv.state == nil {
		inv.state = make(map[string]any)
	}
	inv.state[key] = value
}

// GetState retrieves a value from the invocation state.
//
// Returns the value and true if the key exists, or nil and false otherwise.
//
// Example:
//
//	if startTime, ok := inv.GetState("agent:start_time"); ok {
//	    duration := time.Since(startTime.(time.Time))
//	}
//	if startTime, ok := inv.GetState("tool:calculator:call_abc123:start_time"); ok {
//	    duration := time.Since(startTime.(time.Time))
//	}
func (inv *Invocation) GetState(key string) (any, bool) {
	if inv == nil {
		return nil, false
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()

	if inv.state == nil {
		return nil, false
	}
	value, ok := inv.state[key]
	return value, ok
}

// GetStateValue retrieves a typed value from the invocation state.
//
// Returns the typed value and true if the key exists and the type matches,
// or the zero value and false otherwise.
//
// Example:
//
//	if startTime, ok := GetStateValue[time.Time](inv, "agent:start_time"); ok {
//	    duration := time.Since(startTime)
//	}
//	if requestID, ok := GetStateValue[string](inv, "middleware:request_id"); ok {
//	    log.Printf("Request ID: %s", requestID)
//	}
func GetStateValue[T any](inv *Invocation, key string) (T, bool) {
	var zero T
	if inv == nil {
		return zero, false
	}
	inv.stateMu.RLock()
	defer inv.stateMu.RUnlock()

	return util.GetMapValue[string, T](inv.state, key)
}

// GetOrCreateTimingInfo gets or creates timing info for this invocation.
// Only the first LLM call will create and populate timing info; subsequent calls reuse it.
// This ensures timing metrics only reflect the first LLM call in scenarios with multiple calls (e.g., tool calls).
func (inv *Invocation) GetOrCreateTimingInfo() *model.TimingInfo {
	if inv == nil {
		return nil
	}
	if inv.timingInfo == nil {
		inv.timingInfo = &model.TimingInfo{}
	}
	return inv.timingInfo
}

// IncLLMCallCount increments the LLM call counter for this invocation and
// enforces the optional MaxLLMCalls limit. When the limit is not set or
// non-positive, no restriction is applied. When the limit is exceeded, a
// StopError is returned so callers can terminate the flow early.
func (inv *Invocation) IncLLMCallCount() error {
	if inv == nil {
		return nil
	}
	limit := inv.MaxLLMCalls
	if limit <= 0 {
		// No limit configured, preserve existing behavior.
		return nil
	}
	inv.llmCallCount++
	if inv.llmCallCount > limit {
		return NewStopError(
			fmt.Sprintf("max LLM calls (%d) exceeded", limit),
		)
	}
	return nil
}

// IncToolIteration increments the tool iteration counter and reports whether
// the MaxToolIterations limit has been exceeded. A "tool iteration" is
// defined as an assistant response that contains tool calls and triggers the
// FunctionCallResponseProcessor. When the limit is not set or non-positive,
// this method always returns false, preserving existing behavior.
func (inv *Invocation) IncToolIteration() bool {
	if inv == nil {
		return false
	}
	limit := inv.MaxToolIterations
	if limit <= 0 {
		// No limit configured, preserve existing behavior.
		return false
	}
	inv.toolIterationCount++
	return inv.toolIterationCount > limit
}

// DeleteState removes a value from the invocation state.
//
// Example:
//
//	inv.DeleteState("agent:start_time")
//	inv.DeleteState("tool:calculator:call_abc123:start_time")
func (inv *Invocation) DeleteState(key string) {
	if inv == nil {
		return
	}
	inv.stateMu.Lock()
	defer inv.stateMu.Unlock()

	if inv.state != nil {
		delete(inv.state, key)
	}
}

// AddNoticeChannelAndWait add notice channel and wait it complete
func (inv *Invocation) AddNoticeChannelAndWait(ctx context.Context, key string, timeout time.Duration) error {
	ch := inv.AddNoticeChannel(ctx, key)
	if ch == nil {
		return fmt.Errorf("notice channel create failed for %s", key)
	}
	if timeout == WaitNoticeWithoutTimeout {
		// no timeout, maybe wait for ever
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	select {
	case <-ch:
	case <-time.After(timeout):
		log.InfofContext(
			ctx,
			"[AddNoticeChannelAndWait]: Wait for notification message "+
				"timeout. key: %s, timeout: %d(s)",
			key,
			int64(timeout/time.Second),
		)
		return NewWaitNoticeTimeoutError(fmt.Sprintf("Timeout waiting for completion of event %s", key))
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// AddNoticeChannel add a new notice channel
func (inv *Invocation) AddNoticeChannel(ctx context.Context, key string) chan any {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return nil
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	if ch, ok := inv.noticeChannels[key]; ok {
		return ch
	}

	ch := make(chan any)
	if inv.noticeChannels == nil {
		inv.noticeChannels = make(map[string]chan any)
	}
	inv.noticeChannels[key] = ch

	return ch
}

// NotifyCompletion notify completion signal to waiting task
func (inv *Invocation) NotifyCompletion(ctx context.Context, key string) error {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return fmt.Errorf(
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation key:%s",
			key,
		)
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	ch, ok := inv.noticeChannels[key]
	// channel not found, create a new one and close it.
	// May involve notification followed by waiting.
	if !ok {
		ch = make(chan any)
		if inv.noticeChannels == nil {
			inv.noticeChannels = make(map[string]chan any)
		}
		inv.noticeChannels[key] = ch
		close(ch)
		return nil
	}

	// channel found, close it if it's not closed
	select {
	case _, isOpen := <-ch:
		if isOpen {
			close(ch)
		}
	default:
		close(ch)
	}

	return nil
}

// CleanupNotice cleanup all notice channel
// The 'Invocation' instance created via the NewInvocation method ​​should be disposed​​
// upon completion to prevent resource leaks.
func (inv *Invocation) CleanupNotice(ctx context.Context) {
	if inv == nil || inv.noticeMu == nil {
		log.ErrorContext(
			ctx,
			"noticeMu is uninitialized, please use agent.NewInvocation or "+
				"Clone method to create Invocation",
		)
		return
	}
	inv.noticeMu.Lock()
	defer inv.noticeMu.Unlock()

	for _, ch := range inv.noticeChannels {
		select {
		case _, isOpen := <-ch:
			if isOpen {
				close(ch)
			}
		default:
			close(ch)
		}
	}
	inv.noticeChannels = nil
}

// GetCustomAgentConfig retrieves configuration for a specific custom agent type.
//
// Parameters:
//   - agentKey: The agent type identifier (typically the agent's RunOptionKey constant)
//
// Returns:
//   - The configuration value if found, nil otherwise
//
// Usage:
//
//	func (a *CustomLLMAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
//	    config := inv.GetCustomAgentConfig(RunOptionKey)
//	    if opts, ok := config.(RunOptions); ok {
//	        client := NewLLMClient(opts.APIKey, opts.Model)
//	        // ...
//	    }
//	}
//
// Note: The returned config should be treated as read-only. Do not modify it.
func (inv *Invocation) GetCustomAgentConfig(agentKey string) any {
	if inv == nil || inv.RunOptions.CustomAgentConfigs == nil {
		return nil
	}
	return inv.RunOptions.CustomAgentConfigs[agentKey]
}
