package evaluation

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Runner defines an interface for running agents during evaluation.
type Runner interface {
	// Run executes an agent with the given input and returns the response.
	Run(ctx context.Context, input string) (message.Message, error)

	// Name returns the name of the runner.
	Name() string
}

// AgentRunner implements the Runner interface for agent evaluation.
type AgentRunner struct {
	agent  agent.Agent
	name   string
	logger *slog.Logger
}

// NewAgentRunner creates a new agent runner.
func NewAgentRunner(name string, agent agent.Agent, logger *slog.Logger) *AgentRunner {
	if logger == nil {
		logger = slog.Default()
	}

	if name == "" {
		name = fmt.Sprintf("runner-%s", agent.Name())
	}

	return &AgentRunner{
		agent:  agent,
		name:   name,
		logger: logger,
	}
}

// Run executes the agent with the given input and returns the response.
func (r *AgentRunner) Run(ctx context.Context, input string) (message.Message, error) {
	r.logger.Debug("Running agent", "runner", r.name, "agent", r.agent.Name(), "input", input)

	// Create a message from the input string
	msg := message.NewUserMessage(input)

	// Process the message with the agent
	response, err := r.agent.Run(ctx, msg)
	if err != nil {
		r.logger.Error("Agent run failed",
			"runner", r.name,
			"agent", r.agent.Name(),
			"error", err.Error(),
		)
		return message.Message{}, fmt.Errorf("agent run failed: %w", err)
	}

	r.logger.Debug("Agent run completed successfully",
		"runner", r.name,
		"agent", r.agent.Name(),
	)

	return *response, nil
}

// Name returns the name of the runner.
func (r *AgentRunner) Name() string {
	return r.name
}
