//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import "context"

type summaryAwareRestoreFilterKeyContextKey struct{}

// ContextWithSummaryAwareRestoreFilterKey attaches a summary filter key for
// storage backends that can skip already summarized raw events during restore.
func ContextWithSummaryAwareRestoreFilterKey(ctx context.Context, filterKey string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if filterKey == "" {
		return ctx
	}
	return context.WithValue(ctx, summaryAwareRestoreFilterKeyContextKey{}, filterKey)
}

// SummaryAwareRestoreFilterKeyFromContext returns the summary filter key
// attached by ContextWithSummaryAwareRestoreFilterKey, if any.
func SummaryAwareRestoreFilterKeyFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	filterKey, ok := ctx.Value(summaryAwareRestoreFilterKeyContextKey{}).(string)
	return filterKey, ok && filterKey != ""
}
