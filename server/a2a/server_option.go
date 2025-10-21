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

	"github.com/google/uuid"
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
		log.Warnf("NewContextWithUserID: ctx is nil, do nothing")
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
		log.Warnf("UserID(Header %s) not set, you will use anonymous user, "+
			"you can use WithUserIDHeader in A2AAgent and A2AServer to specified the header that transfer user info.",
			d.userIDHeader)
		userID = uuid.New().String()
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

// WithHost sets the host to use.
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
