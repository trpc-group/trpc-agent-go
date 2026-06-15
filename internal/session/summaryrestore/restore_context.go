//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summaryrestore carries internal restore hints between runners and
// summary-aware session backends.
package summaryrestore

import "context"

type filterKeyContextKey struct{}

// ContextWithFilterKey attaches a summary filter key for storage backends that
// can skip already summarized raw events during restore.
func ContextWithFilterKey(ctx context.Context, filterKey string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if filterKey == "" {
		return ctx
	}
	return context.WithValue(ctx, filterKeyContextKey{}, filterKey)
}

// FilterKeyFromContext returns the summary filter key attached by
// ContextWithFilterKey, if any.
func FilterKeyFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	filterKey, ok := ctx.Value(filterKeyContextKey{}).(string)
	return filterKey, ok && filterKey != ""
}
