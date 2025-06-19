// Package runner provides the core runner functionality.
package runner

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

// Runner runs agents.
type Runner struct {
	appName        string
	agent          agent.Agent
	sessionService session.Service
}

// Options is the options for the Runner.
type Options struct {
	sessionService session.Service
}

// New creates a new Runner.
func New(
	appName string,
	agent agent.Agent,
	opts Options,
) *Runner {
	return &Runner{
		appName:        appName,
		agent:          agent,
		sessionService: opts.sessionService,
	}
}

// Run runs the agent.
func (r *Runner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts agent.RunOptions,
) (<-chan *event.Event, error) {
	invocation := &agent.Invocation{
		Agent:         r.agent,
		InvocationID:  "invocation-" + uuid.New().String(),
		EndInvocation: false,
		Message:       message,
		RunOptions:    opts,
	}
	return r.agent.Run(ctx, invocation)
}
