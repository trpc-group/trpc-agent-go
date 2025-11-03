//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "context"

// ContextKeyToolCallID is the context key type for tool call ID.
// It's exported so that the framework can inject tool call ID into context.
type ContextKeyToolCallID struct{}

// ToolCallIDFromContext retrieves tool call ID from context.
// Returns the tool call ID and true if found, empty string and false
// otherwise.
func ToolCallIDFromContext(ctx context.Context) (string, bool) {
	toolCallID, ok := ctx.Value(ContextKeyToolCallID{}).(string)
	return toolCallID, ok
}
