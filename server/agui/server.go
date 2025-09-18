package agui

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service/sse"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// Server wires the agent, session service, and transport together.
type Server struct {
	agent          agent.Agent
	sessionService session.Service
	service        service.Service
}

// New creates a server instance ready to accept AG-UI requests.
func New(agent agent.Agent, opt ...Option) (*Server, error) {
	if agent == nil {
		return nil, errors.New("agui: agent must not be nil")
	}
	var opts options
	for _, o := range opt {
		o(&opts)
	}
	sessionService := opts.sessionService
	if sessionService == nil {
		sessionService = inmemory.NewSessionService()
	}
	service := opts.service
	if service == nil {
		runner := runner.NewRunner(
			agent.Info().Name,
			agent,
			runner.WithSessionService(sessionService),
		)
		aguiRunner := aguirunner.New(runner)
		service = sse.New(sse.WithRunner(aguiRunner))
	}
	server := &Server{
		agent:          agent,
		sessionService: sessionService,
		service:        service,
	}
	return server, nil
}

// Serve starts the service.
func (s *Server) Serve(ctx context.Context) error {
	return s.service.Serve(ctx)
}

// Close stops the service.
func (s *Server) Close(ctx context.Context) error {
	return s.service.Close(ctx)
}
