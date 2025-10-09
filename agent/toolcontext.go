//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
