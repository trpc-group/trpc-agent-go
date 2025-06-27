// Package callbacks provides callbacks for the agent, model, and tool.
package callbacks

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// BeforeAgentCallback is called before the agent runs.
// Returns (customResponse, skip, error).
// - customResponse: if not nil, this response will be returned to user and agent execution will be skipped.
// - skip: if true, agent execution will be skipped.
// - error: if not nil, agent execution will be stopped with this error.
type BeforeAgentCallback func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error)

// AfterAgentCallback is called after the agent runs.
// Returns (customResponse, override, error).
// - customResponse: if not nil and override is true, this response will be used instead of the actual agent response.
// - override: if true, the customResponse will be used.
// - error: if not nil, this error will be returned.
type AfterAgentCallback func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error)

// BeforeModelCallback is called before the model is invoked. It can mutate the request.
// Returns (customResponse, skip, error).
// - customResponse: if not nil, this response will be returned to user and model call will be skipped.
// - skip: if true, model call will be skipped.
// - error: if not nil, model call will be stopped with this error.
type BeforeModelCallback func(ctx context.Context, req *model.Request) (*model.Response, bool, error)

// AfterModelCallback is called after the model is invoked.
// Returns (customResponse, override, error).
// - customResponse: if not nil and override is true, this response will be used instead of the actual model response.
// - override: if true, the customResponse will be used.
// - error: if not nil, this error will be returned.
type AfterModelCallback func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error)

// BeforeToolCallback is called before the tool is invoked. It can mutate the args.
// Returns (customResult, skip, newArgs, error).
// - customResult: if not nil, this result will be used and tool call will be skipped.
// - skip: if true, tool call will be skipped.
// - newArgs: modified arguments to pass to the tool (if not skipped).
// - error: if not nil, tool call will be stopped with this error.
type BeforeToolCallback func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error)

// AfterToolCallback is called after the tool is invoked.
// Returns (customResult, override, error).
// - customResult: if not nil and override is true, this result will be used instead of the actual tool result.
// - override: if true, the customResult will be used.
// - error: if not nil, this error will be returned.
type AfterToolCallback func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error)

// CallbackRegistry holds all registered callbacks for an agent instance.
type CallbackRegistry struct {
	BeforeAgent []BeforeAgentCallback
	AfterAgent  []AfterAgentCallback
	BeforeModel []BeforeModelCallback
	AfterModel  []AfterModelCallback
	BeforeTool  []BeforeToolCallback
	AfterTool   []AfterToolCallback
}

// NewCallbackRegistry creates a new CallbackRegistry.
func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{}
}

// AddBeforeAgent adds a before agent callback.
func (r *CallbackRegistry) AddBeforeAgent(cb BeforeAgentCallback) {
	r.BeforeAgent = append(r.BeforeAgent, cb)
}

// AddAfterAgent adds an after agent callback.
func (r *CallbackRegistry) AddAfterAgent(cb AfterAgentCallback) {
	r.AfterAgent = append(r.AfterAgent, cb)
}

// AddBeforeModel adds a before model callback.
func (r *CallbackRegistry) AddBeforeModel(cb BeforeModelCallback) {
	r.BeforeModel = append(r.BeforeModel, cb)
}

// AddAfterModel adds an after model callback.
func (r *CallbackRegistry) AddAfterModel(cb AfterModelCallback) {
	r.AfterModel = append(r.AfterModel, cb)
}

// AddBeforeTool adds a before tool callback.
func (r *CallbackRegistry) AddBeforeTool(cb BeforeToolCallback) {
	r.BeforeTool = append(r.BeforeTool, cb)
}

// AddAfterTool adds an after tool callback.
func (r *CallbackRegistry) AddAfterTool(cb AfterToolCallback) {
	r.AfterTool = append(r.AfterTool, cb)
}

// RunBeforeAgent runs all before agent callbacks in order.
// Returns (customResponse, skip, error).
// If any callback returns a custom response or skip=true, stop and return.
func (r *CallbackRegistry) RunBeforeAgent(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
	for _, cb := range r.BeforeAgent {
		customResponse, skip, err := cb(ctx, invocation)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil || skip {
			return customResponse, skip, nil
		}
	}
	return nil, false, nil
}

// RunAfterAgent runs all after agent callbacks in order.
// Returns (customResponse, override, error).
// If any callback returns a custom response with override=true, stop and return.
func (r *CallbackRegistry) RunAfterAgent(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
	for _, cb := range r.AfterAgent {
		customResponse, override, err := cb(ctx, invocation, runErr)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil && override {
			return customResponse, true, nil
		}
	}
	return nil, false, nil
}

// RunBeforeModel runs all before model callbacks in order.
// Returns (customResponse, skip, error).
// If any callback returns a custom response or skip=true, stop and return.
func (r *CallbackRegistry) RunBeforeModel(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
	for _, cb := range r.BeforeModel {
		customResponse, skip, err := cb(ctx, req)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil || skip {
			return customResponse, skip, nil
		}
	}
	return nil, false, nil
}

// RunAfterModel runs all after model callbacks in order.
// Returns (customResponse, override, error).
// If any callback returns a custom response with override=true, stop and return.
func (r *CallbackRegistry) RunAfterModel(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
	for _, cb := range r.AfterModel {
		customResponse, override, err := cb(ctx, resp, modelErr)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil && override {
			return customResponse, true, nil
		}
	}
	return nil, false, nil
}

// RunBeforeTool runs all before tool callbacks in order.
// Returns (customResult, skip, newArgs, error).
// If any callback returns a custom result or skip=true, stop and return.
func (r *CallbackRegistry) RunBeforeTool(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
	newArgs := args
	for _, cb := range r.BeforeTool {
		customResult, skip, modifiedArgs, err := cb(ctx, t, newArgs)
		if err != nil {
			return nil, false, nil, err
		}
		if customResult != nil || skip {
			return customResult, skip, modifiedArgs, nil
		}
		newArgs = modifiedArgs
	}
	return nil, false, newArgs, nil
}

// RunAfterTool runs all after tool callbacks in order.
// Returns (customResult, override, error).
// If any callback returns a custom result with override=true, stop and return.
func (r *CallbackRegistry) RunAfterTool(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
	for _, cb := range r.AfterTool {
		customResult, override, err := cb(ctx, t, args, result, toolErr)
		if err != nil {
			return nil, false, err
		}
		if customResult != nil && override {
			return customResult, true, nil
		}
	}
	return nil, false, nil
}
