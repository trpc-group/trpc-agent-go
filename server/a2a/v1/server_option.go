//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-a2a-go/v2/auth"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// serverUserIDHeader is the default header that a2a server get UserID of invocation.
var serverUserIDHeader = "X-User-ID"

// UserIDFromContext returns the user ID from the context.
func UserIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	user, ok := ctx.Value(auth.AuthUserKey).(*auth.User)
	if !ok {
		return "", false
	}
	return user.ID, true
}

// NewContextWithUserID returns a new context with the user ID.
func NewContextWithUserID(ctx context.Context, userID string) context.Context {
	if ctx == nil {
		log.WarnfContext(
			ctx,
			"NewContextWithUserID: ctx is nil, do nothing",
		)
		return ctx
	}
	return context.WithValue(ctx, auth.AuthUserKey, &auth.User{ID: userID})
}

// ProcessMessageHook is a function that wraps the message processor with additional functionality.
type ProcessMessageHook func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor

// TaskManagerBuilder returns a task manager for the given processor. Providing
// one replaces the default stateless manager with a retaining or custom
// implementation; it does not change the processor event contract.
type TaskManagerBuilder func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error)

// ResponseRewriter rewrites one outbound A2A event before it is returned or
// sent to the remote peer. Returning nil drops the event.
//
// Unary message/send results are derived from the same rewritten event stream.
type ResponseRewriter func(ctx context.Context, result protocol.StreamEvent) protocol.StreamEvent

// EventToA2APartMapper converts an agent event into additional A2A parts.
//
// Returning nil or empty parts means this mapper contributes nothing and the
// default converter continues with its normal behavior.
type EventToA2APartMapper func(ctx context.Context, event *event.Event) ([]*protocol.Part, error)

type defaultAuthProvider struct {
	userIDHeader string
}

func (d *defaultAuthProvider) Authenticate(r *http.Request) (*auth.User, error) {
	if r == nil {
		return nil, errors.New("request is nil")
	}
	userID := r.Header.Get(d.userIDHeader)
	if userID == "" {
		log.DebugfContext(
			r.Context(),
			"UserID(Header %s) not set, will be generated from "+
				"context ID. You can use WithUserIDHeader in "+
				"A2AAgent and A2AServer to specify the header "+
				"that transfers user info.",
			d.userIDHeader,
		)
	}
	return &auth.User{ID: userID}, nil
}

type options struct {
	runner                    runner.Runner
	graphEventObjectAllowlist []string
	responseRewriter          ResponseRewriter
	agentCard                 *a2a.AgentCard
	processorHook             ProcessMessageHook
	taskManagerBuilder        TaskManagerBuilder
	runOptions                []agent.RunOption
	a2aToAgentConverter       A2AMessageToAgentMessage
	eventToA2AConverter       EventToA2AMessage
	eventPartMappers          []EventToA2APartMapper
	extraOptions              []a2a.Option
	errorHandler              ErrorHandler
	debugLogging              bool
	userIDHeader              string
	adkCompatibility          bool
}

// Option is a function that configures a Server.
type Option func(*options)

// WithRunner sets the runner to use.
// The caller retains ownership of the runner and must close it if New returns
// an error or after the server stops. WithAgentCard is also required.
func WithRunner(r runner.Runner) Option {
	return func(opts *options) {
		opts.runner = r
	}
}

func normalizeMetadataKeys(keys []string) []string {
	if len(keys) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(keys))
	dedup := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := dedup[key]; ok {
			continue
		}
		dedup[key] = struct{}{}
		normalized = append(normalized, key)
	}
	return normalized
}

// WithAgentCard sets the agent card to use.
// Use NewAgentCard to construct a card from explicit metadata when needed.
func WithAgentCard(agentCard a2a.AgentCard) Option {
	return func(opts *options) {
		opts.agentCard = &agentCard
	}
}

// WithProcessMessageHook sets the process message hook to use.
// The hook can be used to wrap the message processor with additional functionality.
func WithProcessMessageHook(hook ProcessMessageHook) Option {
	return func(opts *options) {
		opts.processorHook = hook
	}
}

// WithUserIDHeader sets the HTTP header name to extract UserID from requests.
// If not set, defaults to "X-User-ID".
func WithUserIDHeader(header string) Option {
	return func(opts *options) {
		if header != "" {
			opts.userIDHeader = header
		}
	}
}

// WithExtraA2AOptions passes extra options to the underlying A2A server.
// For example, it can be combined with a2a.WithAgentCardHandler and
// NewAgentCardHandler(...) to serve a dynamically updated AgentCard.
func WithExtraA2AOptions(opts ...a2a.Option) Option {
	return func(options *options) {
		options.extraOptions = append(options.extraOptions, opts...)
	}
}

// WithTaskManagerBuilder replaces the default stateless manager. The built-in
// processor emits the same request-local task lifecycle in either case; use a
// memory, Redis, or custom manager when that state must survive the request.
// Retention enables the task control plane; suspending a task for continuation
// still requires a processor or converter that emits input-required or
// auth-required status.
func WithTaskManagerBuilder(builder TaskManagerBuilder) Option {
	return func(opts *options) {
		opts.taskManagerBuilder = builder
	}
}

// WithRunOptions appends additional run options for every agent invocation.
// These options are applied before the A2A message metadata is merged into RuntimeState.
// If both WithRunOptions and A2A message metadata set the same RuntimeState key,
// the A2A metadata value takes precedence (last-write-wins).
func WithRunOptions(runOpts ...agent.RunOption) Option {
	return func(opts *options) {
		opts.runOptions = append(opts.runOptions, runOpts...)
	}
}

// WithA2AToAgentConverter sets the A2A message to agent message converter to use.
func WithA2AToAgentConverter(converter A2AMessageToAgentMessage) Option {
	return func(opts *options) {
		opts.a2aToAgentConverter = converter
	}
}

// Converter-related options.
//
// The options in this section control how A2A requests/responses are converted.
// Unless otherwise noted, options marked as "default event converter only"
// affect only the built-in EventToA2AMessage implementation created by
// buildProcessor.

// WithEventToA2AConverter sets the event to A2A message converter to use.
//
// Providing a custom converter bypasses the built-in event conversion behavior.
// The default-event-converter options below do not rewrite custom converter
// output unless their comments explicitly say they also affect server-generated
// metadata.
func WithEventToA2AConverter(converter EventToA2AMessage) Option {
	return func(opts *options) {
		opts.eventToA2AConverter = converter
	}
}

// WithGraphEventObjectAllowlist configures which graph object types
// (`evt.Response.Object`) are forwarded through A2A.
//
// Default event converter only.
// Matching applies only when object type starts with `graph.`.
//   - default (option not set): only graph.execution is forwarded.
//   - exact rule: "graph.node.start"
//   - prefix rule: "graph.node.*" or "graph.node*" (trailing '*' means prefix match)
//   - suffix rule: "*step" or "*.step" (leading '*' means suffix match)
//   - wildcard rule: "*" (allow all graph.* object types)
func WithGraphEventObjectAllowlist(objectTypes ...string) Option {
	return func(opts *options) {
		opts.graphEventObjectAllowlist = normalizeMetadataKeys(objectTypes)
	}
}

// WithResponseRewriter rewrites outbound A2A events before they are emitted.
//
// The rewriter is applied to every event the processor emits, including
// TaskArtifactUpdateEvent, TaskStatusUpdateEvent, submitted/completed lifecycle
// events, and structured task errors. Messages returned by ErrorHandler are
// also covered.
//
// Because message/send derives its result from these same events, this also
// covers unary responses. The rewriter sees each event before task aggregation
// and send, with the request context passed through for request-scoped logging.
//
// Returning nil drops the outbound event.
func WithResponseRewriter(rewriter ResponseRewriter) Option {
	return func(opts *options) {
		opts.responseRewriter = rewriter
	}
}

// WithADKCompatibility enables ADK compatibility mode.
//
// This option affects the default event converter and server-generated task
// metadata/status updates. It does not rewrite metadata produced by a custom
// EventToA2AConverter.
//
// When enabled, metadata keys in A2A messages will use the "adk_" prefix
// (e.g., "adk_app_name", "adk_user_id", "adk_session_id") to be compatible
// with ADK (Agent Development Kit) Python implementation.
func WithADKCompatibility(enabled bool) Option {
	return func(opts *options) {
		opts.adkCompatibility = enabled
	}
}

// WithEventToA2APartMapper registers a lightweight event-to-part mapper on the
// default event converter.
//
// Built-in tool-call and code-execution handling still takes precedence. For
// regular text events, mapper-generated parts are appended after reasoning and
// content TextParts so natural-language output is preserved.
//
// The mapper is ignored when WithEventToA2AConverter is used to replace the
// converter entirely.
func WithEventToA2APartMapper(mapper EventToA2APartMapper) Option {
	return func(opts *options) {
		if mapper == nil {
			return
		}
		opts.eventPartMappers = append(opts.eventPartMappers, mapper)
	}
}

// WithDebugLogging sets the debug logging to use.
func WithDebugLogging(debug bool) Option {
	return func(opts *options) {
		opts.debugLogging = debug
	}
}

// WithErrorHandler sets a custom error handler.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(opts *options) {
		opts.errorHandler = handler
	}
}

// ErrorHandler converts errors to user-friendly messages
type ErrorHandler func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error)

// DefaultErrorHandler provides intelligent error handling based on error type
func defaultErrorHandler(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
	outputMsg := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{
			protocol.NewTextPart("An error occurred while processing your request."),
		},
	)
	return &outputMsg, nil
}

// singleMsgSubscriber and its constructor were removed: the event-stream
// MessageProcessor contract returns a channel directly, so setup-time replies
// are emitted on a short-lived channel (see messageProcessor.replyError).
