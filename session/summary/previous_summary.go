//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import "context"

type previousSummaryContextKey struct{}

// ContextWithPreviousSummary attaches the previous rolling summary to the
// current summary call. Session services use this to keep the previous summary
// distinct from newly uncovered conversation events when the prompt contains
// {previous_summary}.
func ContextWithPreviousSummary(ctx context.Context, previousSummary string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, previousSummaryContextKey{}, previousSummary)
}

// PreviousSummaryFromContext returns the previous rolling summary attached to
// the current summary call, if any. The returned text can be empty on the first
// summary pass.
func PreviousSummaryFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	previousSummary, ok := ctx.Value(previousSummaryContextKey{}).(string)
	return previousSummary, ok
}
