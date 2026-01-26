//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type searcher interface {
	Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) (context.Context, []string, error)
}

// contextKeyToolSearchUsage is the context key type for tool search usage.
type contextKeyToolSearchUsage struct{}

// ToolSearchUsageFromContext retrieves tool search usage from context.
// Returns the usage and true if found, nil and false otherwise.
func ToolSearchUsageFromContext(ctx context.Context) (*model.Usage, bool) {
	usage, ok := ctx.Value(contextKeyToolSearchUsage{}).(*model.Usage)
	return usage, ok
}

// SetToolSearchUsage sets tool search usage in context.
func SetToolSearchUsage(ctx context.Context, usage *model.Usage) context.Context {
	return context.WithValue(ctx, contextKeyToolSearchUsage{}, usage)
}
