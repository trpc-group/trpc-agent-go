package a2a

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const userIDHeader = "X-User-ID"

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

// TaskManagerBuilder returns a task manager for the given agent.
type TaskManagerBuilder func(processor taskmanager.MessageProcessor) taskmanager.TaskManager

type defautAuthProvider struct{}

func (d *defautAuthProvider) Authenticate(r *http.Request) (*auth.User, error) {
	if r == nil {
		return nil, errors.New("request is nil")
	}
	userID := r.Header.Get(userIDHeader)
	if userID == "" {
		log.Warnf("UserID not set, you will use anonymous user")
		userID = uuid.New().String()
	}
	return &auth.User{ID: userID}, nil
}

type options struct {
	sessionService     session.Service
	agent              agent.Agent
	agentCard          *a2a.AgentCard
	processorBuilder   ProcessorBuilder
	taskManagerBuilder TaskManagerBuilder
	host               string
	extraOptions       []a2a.Option
}

// Option is a function that configures a Server.
type Option func(*options)

var defaultOptions = &options{
	host: "localhost:8080",
}

// WithSessionService sets the session service to use.
func WithSessionService(service session.Service) Option {
	return func(opts *options) {
		opts.sessionService = service
	}
}

// WithAgent sets the agent to use.
func WithAgent(agent agent.Agent) Option {
	return func(opts *options) {
		opts.agent = agent
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

// WithHost sets the host to use.
func WithHost(host string) Option {
	return func(opts *options) {
		opts.host = host
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
