package agent

import "trpc.group/trpc-go/trpc-agent-go/core/model"

// Invocation represents the context for a flow execution.
type Invocation struct {
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Model is the model that is being used for the invocation.
	Model model.Model
}
