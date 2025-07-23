package a2a

import (
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ProcessorBuilder returns a message processor for the given agent.
type ProcessorBuilder func(path string, agent agent.Agent, sessionService session.Service) taskmanager.MessageProcessor

type options struct {
	sessionService   session.Service
	agents           map[string]agent.Agent
	agentCards       map[string]a2a.AgentCard
	processorBuilder ProcessorBuilder
	host             string
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

// WithAgents sets the agents to use.
// Key of map will be used to set the path of a2a server.
// For example, if the key is "agent1", the path will be "/a2a/agent1/".
func WithAgents(agents map[string]agent.Agent) Option {
	return func(opts *options) {
		opts.agents = agents
	}
}

// WithAgentCard sets the agent cards to use, the key should be the same as the path of a2a server.
func WithAgentCard(agentCards map[string]a2a.AgentCard) Option {
	return func(opts *options) {
		opts.agentCards = agentCards
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
