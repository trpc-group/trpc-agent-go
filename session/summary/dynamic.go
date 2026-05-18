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
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const metadataDynamicSummarizerType = "dynamic"

var errNoDynamicSummarizerResolved = errors.New("no dynamic summarizer resolved")

// NewDynamicSummarizer creates a summarizer that resolves the actual
// summarizer from the current request context and session at summary time.
//
// It is useful when the session service should be long-lived while the summary
// model, prompt, or checks need to vary per request. Returning nil from resolve
// skips automatic summary checks. Resolver errors also make the automatic
// summary gate return false, while Summarize propagates resolver errors.
// Calling Summarize directly, or forcing a summary without a resolved
// summarizer, returns an error.
func NewDynamicSummarizer(
	resolve func(context.Context, *session.Session) (SessionSummarizer, error),
) SessionSummarizer {
	if resolve == nil {
		return nil
	}
	return &dynamicSummarizer{resolve: resolve}
}

type dynamicSummarizer struct {
	resolve func(context.Context, *session.Session) (SessionSummarizer, error)
}

var _ ContextAwareSummarizer = (*dynamicSummarizer)(nil)

func (d *dynamicSummarizer) resolveSummarizer(
	ctx context.Context,
	sess *session.Session,
) (SessionSummarizer, error) {
	if d == nil || d.resolve == nil {
		return nil, nil
	}
	return d.resolve(ctx, sess)
}

// ShouldSummarize checks if the session should be summarized.
func (d *dynamicSummarizer) ShouldSummarize(sess *session.Session) bool {
	return d.ShouldSummarizeWithContext(context.Background(), sess)
}

// ShouldSummarizeWithContext resolves the current summarizer and evaluates its
// summary gate with the provided context.
func (d *dynamicSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	resolved, err := d.resolveSummarizer(ctx, sess)
	if err != nil || resolved == nil {
		return false
	}
	if contextual, ok := resolved.(ContextAwareSummarizer); ok {
		return contextual.ShouldSummarizeWithContext(ctx, sess)
	}
	return resolved.ShouldSummarize(sess)
}

// Summarize resolves the current summarizer and delegates summary generation
// to it.
func (d *dynamicSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	resolved, err := d.resolveSummarizer(ctx, sess)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		return "", errNoDynamicSummarizerResolved
	}
	return resolved.Summarize(ctx, sess)
}

// SetPrompt is a no-op for dynamic summarizers. Configure request-scoped
// prompts in the resolver instead.
func (d *dynamicSummarizer) SetPrompt(string) {}

// SetModel is a no-op for dynamic summarizers. Configure request-scoped models
// in the resolver instead.
func (d *dynamicSummarizer) SetModel(model.Model) {}

// Metadata returns metadata about the dynamic summarizer.
func (d *dynamicSummarizer) Metadata() map[string]any {
	return map[string]any{
		"type": metadataDynamicSummarizerType,
	}
}
