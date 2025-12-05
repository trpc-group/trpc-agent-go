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
	"fmt"
	"net/http"

	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

// ProcessorBuilder returns a message processor for the given agent.
type ProcessorBuilder func(agent agent.Agent, sessionService session.Service) taskmanager.MessageProcessor

// ProcessMessageHook is a function that wraps the message processor with additional functionality.
type ProcessMessageHook func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor

// TaskManagerBuilder returns a task manager for the given agent.
type TaskManagerBuilder func(processor taskmanager.MessageProcessor) taskmanager.TaskManager

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
	sessionService      session.Service
	agent               agent.Agent
	enableStreaming     bool
	agentCard           *a2a.AgentCard
	processorBuilder    ProcessorBuilder
	processorHook       ProcessMessageHook
	taskManagerBuilder  TaskManagerBuilder
	a2aToAgentConverter A2AMessageToAgentMessage
	eventToA2AConverter EventToA2AMessage
	host                string
	extraOptions        []a2a.Option
	errorHandler        ErrorHandler
	debugLogging        bool
	userIDHeader        string
	adkCompatibility    bool
}

// Option is a function that configures a Server.
type Option func(*options)

// WithSessionService sets the session service to use.
func WithSessionService(service session.Service) Option {
	return func(opts *options) {
		opts.sessionService = service
	}
}

// WithAgent sets the agent to use.
func WithAgent(agent agent.Agent, enableStreaming bool) Option {
	return func(opts *options) {
		opts.agent = agent
		opts.enableStreaming = enableStreaming
	}
}

// WithAgentCard sets the agent card to use.
func WithAgentCard(agentCard a2a.AgentCard) Option {
	return func(opts *options) {
		opts.agentCard = &agentCard
	}
}

// WithProcessorBuilder sets the processor builder to use.
func WithProcessorBuilder(builder ProcessorBuilder) Option {
	return func(opts *options) {
		opts.processorBuilder = builder
	}
}

// WithProcessMessageHook sets the process message hook to use.
// The hook can be used to wrap the message processor with additional functionality.
func WithProcessMessageHook(hook ProcessMessageHook) Option {
	return func(opts *options) {
		opts.processorHook = hook
	}
}

// WithHost sets the host address for the A2A server's agent card URL.
// The host will be normalized to a complete URL and used by other agents to discover and communicate with this agent.
//
// Supported formats:
//   - "localhost:8080" → "http://localhost:8080"
//   - "example.com" → "http://example.com"
//   - "http://example.com/api/v1" → "http://example.com/api/v1" (used as-is)
//   - "https://example.com" → "https://example.com" (used as-is)
//   - "grpc://service:9090" → "grpc://service:9090" (custom schemes supported)
//
// If the URL contains a path (e.g., "http://example.com/api/v1"), the path will be
// automatically extracted and set as the base path for routing requests.
//
// Example:
//
//	server, _ := a2a.New(
//	    a2a.WithAgent(myAgent),
//	    a2a.WithHost("localhost:8080"),  // URL: "http://localhost:8080", basePath: ""
//	    // or
//	    a2a.WithHost("http://example.com/api/v1"),  // URL: "http://example.com/api/v1", basePath: "/api/v1"
//	)
func WithHost(host string) Option {
	return func(opts *options) {
		opts.host = host
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

// WithExtraA2AOptions sets the extra options to use.
func WithExtraA2AOptions(opts ...a2a.Option) Option {
	return func(options *options) {
		options.extraOptions = append(options.extraOptions, opts...)
	}
}

// WithTaskManagerBuilder sets the task manager builder to use.
func WithTaskManagerBuilder(builder TaskManagerBuilder) Option {
	return func(opts *options) {
		opts.taskManagerBuilder = builder
	}
}

// WithA2AToAgentConverter sets the A2A message to agent message converter to use.
func WithA2AToAgentConverter(converter A2AMessageToAgentMessage) Option {
	return func(opts *options) {
		opts.a2aToAgentConverter = converter
	}
}

// WithEventToA2AConverter sets the event to A2A message converter to use.
func WithEventToA2AConverter(converter EventToA2AMessage) Option {
	return func(opts *options) {
		opts.eventToA2AConverter = converter
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

// WithADKCompatibility enables ADK compatibility mode.
// When enabled, metadata keys in A2A messages will use the "adk_" prefix
// (e.g., "adk_app_name", "adk_user_id", "adk_session_id") to be compatible
// with ADK (Agent Development Kit) Python implementation.
// This allows trpc-agent-go servers to interoperate with ADK clients.
func WithADKCompatibility(enabled bool) Option {
	return func(opts *options) {
		opts.adkCompatibility = enabled
	}
}

// ErrorHandler converts errors to user-friendly messages
type ErrorHandler func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error)

// DefaultErrorHandler provides intelligent error handling based on error type
func defaultErrorHandler(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
	outputMsg := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]protocol.Part{
			protocol.NewTextPart("An error occurred while processing your request."),
		},
	)
	return &outputMsg, nil
}

type singleMsgSubscriber struct {
	ch chan protocol.StreamingMessageEvent
}

func newSingleMsgSubscriber(msg *protocol.Message) *singleMsgSubscriber {
	ch := make(chan protocol.StreamingMessageEvent, 1)
	ch <- protocol.StreamingMessageEvent{
		Result: msg,
	}
	close(ch)
	return &singleMsgSubscriber{
		ch: ch,
	}
}

func (e *singleMsgSubscriber) Send(event protocol.StreamingMessageEvent) error {
	return fmt.Errorf("send msg is not allowed for singleMsgSubscriber")
}

// Channel returns the channel of the task subscriber
func (e *singleMsgSubscriber) Channel() <-chan protocol.StreamingMessageEvent {
	return e.ch
}

// Closed returns true if the task subscriber is closed
func (e *singleMsgSubscriber) Closed() bool {
	return true
}

// Close close the task subscriber
func (e *singleMsgSubscriber) Close() {
}
