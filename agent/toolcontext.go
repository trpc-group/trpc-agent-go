package agent

import (
	"context"
)

// ToolContext is a context for tool calls.
type ToolContext struct {
	// CallbackContext is the context for callback.
	*CallbackContext
}

// NewToolContext creates a new ToolContext from the given context.
func NewToolContext(ctx context.Context) (*ToolContext, error) {
	cbCtx, err := NewCallbackContext(ctx)
	if err != nil {
		return nil, err
	}
	return &ToolContext{CallbackContext: cbCtx}, nil
}
