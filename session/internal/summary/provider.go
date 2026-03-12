//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/session"
	psummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// HasSummarizer reports whether summary generation is configured either through
// a static summarizer or a request-scoped resolver.
func HasSummarizer(
	summarizer psummary.SessionSummarizer,
	resolver psummary.SessionSummarizerResolver,
) bool {
	return summarizer != nil || resolver != nil
}

// ResolveSessionSummarizer resolves the summarizer for the current summary
// attempt. A resolver takes precedence; returning nil falls back to the static
// summarizer when present.
func ResolveSessionSummarizer(
	ctx context.Context,
	summarizer psummary.SessionSummarizer,
	resolver psummary.SessionSummarizerResolver,
	sess *session.Session,
	filterKey string,
	force bool,
) (psummary.SessionSummarizer, error) {
	if resolver == nil {
		return summarizer, nil
	}

	resolved, err := resolver(ctx, psummary.SessionSummaryRequest{
		Session:   sess,
		FilterKey: filterKey,
		Force:     force,
	})
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		return resolved, nil
	}
	return summarizer, nil
}
