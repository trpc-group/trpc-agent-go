//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// testSummarizer is a no-op deterministic summarizer used only by backend
// tests. It avoids importing the harness package (which would create an import
// cycle, since harness imports backends).
type testSummarizer struct{}

var _ summary.SessionSummarizer = (*testSummarizer)(nil)

func (testSummarizer) ShouldSummarize(*session.Session) bool { return true }
func (testSummarizer) Summarize(context.Context, *session.Session) (string, error) {
	return "summary", nil
}
func (testSummarizer) SetPrompt(string)         {}
func (testSummarizer) SetModel(model.Model)     {}
func (testSummarizer) Metadata() map[string]any { return nil }
