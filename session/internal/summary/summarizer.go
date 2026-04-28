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
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// HasSummarizer reports whether summary generation is configured.
func HasSummarizer(summarizer summary.SessionSummarizer) bool {
	return summarizer != nil
}

// ShouldSummarize evaluates the summary gate, preferring the built-in
// context-aware summary path when available.
func ShouldSummarize(
	ctx context.Context,
	summarizer summary.SessionSummarizer,
	sess *session.Session,
) bool {
	if summarizer == nil {
		return false
	}
	if contextual, ok := summarizer.(summary.ContextAwareSummarizer); ok {
		return contextual.ShouldSummarizeWithContext(ctx, sess)
	}
	return summarizer.ShouldSummarize(sess)
}
