//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sessionrestore carries internal hints for storage-backed session
// restore. It is intentionally internal so public session service contracts do
// not need a new option for runner-specific restore behavior.
package sessionrestore

import "context"

type contextKey struct{}

// Hint contains optional restore hints understood by storage backends.
type Hint struct {
	// SummaryFilterKey identifies the summary scope used to trim raw history.
	SummaryFilterKey string
}

// WithSummaryCutoff returns a context that asks storage backends to use the
// matching summary boundary as a lower bound for raw event restore.
func WithSummaryCutoff(ctx context.Context, filterKey string) context.Context {
	return context.WithValue(ctx, contextKey{}, Hint{
		SummaryFilterKey: filterKey,
	})
}

// FromContext extracts restore hints from ctx.
func FromContext(ctx context.Context) (Hint, bool) {
	if ctx == nil {
		return Hint{}, false
	}
	hint, ok := ctx.Value(contextKey{}).(Hint)
	return hint, ok
}
