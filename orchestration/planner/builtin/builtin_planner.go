package builtin

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/planner"
)

// Planner represents the built-in planner that uses model's built-in thinking features.
type Planner struct {
	// reasoningEffort limits the reasoning effort for reasoning models.
	// Supported values: "low", "medium", "high".
	// Only effective for OpenAI o-series models.
	reasoningEffort *string

	// thinkingEnabled enables thinking mode for models that support it.
	// Only effective for Claude and Gemini models via OpenAI API.
	thinkingEnabled *bool

	// thinkingTokens controls the length of thinking.
	// Must be greater than 1024 and not exceed max_tokens.
	// Only effective for Claude and Gemini models via OpenAI API.
	thinkingTokens *int
}

// Options contains configuration options for creating a Planner.
type Options struct {
	// ReasoningEffort limits the reasoning effort for reasoning models.
	// Supported values: "low", "medium", "high".
	ReasoningEffort *string

	// ThinkingEnabled enables thinking mode for Claude and Gemini models via OpenAI API.
	ThinkingEnabled *bool

	// ThinkingTokens controls the length of thinking for Claude and Gemini models via OpenAI API.
	// Must be greater than 1024 and not exceed max_tokens.
	ThinkingTokens *int
}

// New creates a new Planner with the given options.
func New(opts Options) *Planner {
	return &Planner{
		reasoningEffort: opts.ReasoningEffort,
		thinkingEnabled: opts.ThinkingEnabled,
		thinkingTokens:  opts.ThinkingTokens,
	}
}

// ApplyThinkingConfig applies the thinking config to the LLM request.
func (p *Planner) ApplyThinkingConfig(llmRequest *model.Request) {
	if p.reasoningEffort != nil {
		llmRequest.ReasoningEffort = p.reasoningEffort
	}
	if p.thinkingEnabled != nil {
		llmRequest.ThinkingEnabled = p.thinkingEnabled
	}
	if p.thinkingTokens != nil {
		llmRequest.ThinkingTokens = p.thinkingTokens
	}
}

// BuildPlanningInstruction builds the system instruction to be appended to the
// LLM request for planning. For the built-in planner, this returns empty string
// as the model handles planning internally through its thinking capabilities.
func (p *Planner) BuildPlanningInstruction(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) string {
	return ""
}

// ProcessPlanningResponse processes the LLM response for planning.
// For the built-in planner, this returns nil as the model handles the
// response processing internally.
func (p *Planner) ProcessPlanningResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) *model.Response {
	return nil
}

// Verify that Planner implements the planner.Planner interface.
var _ planner.Planner = (*Planner)(nil)
