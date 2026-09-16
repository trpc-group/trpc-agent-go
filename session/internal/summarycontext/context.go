//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summarycontext carries framework-internal summary call metadata.
package summarycontext

import "context"

type previousSummaryKey struct{}

// WithPreviousSummary attaches the previous rolling summary to the current
// summary call.
func WithPreviousSummary(ctx context.Context, previousSummary string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, previousSummaryKey{}, previousSummary)
}

// PreviousSummary returns the previous rolling summary attached to the current
// summary call, if any. The returned text can be empty on the first summary
// pass.
func PreviousSummary(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	previousSummary, ok := ctx.Value(previousSummaryKey{}).(string)
	return previousSummary, ok
}
