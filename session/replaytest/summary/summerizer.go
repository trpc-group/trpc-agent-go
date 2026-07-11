//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summary provides deterministic summarizer stubs for replay tests.
package summary

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

//模拟一个 summarizer  代替大模型

// FakeSummarizer 用确定性规则模拟大模型摘要，避免测试依赖真实 LLM。
type FakeSummarizer struct{}

// ShouldSummarize always reports that summarization should run.
func (FakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return true
}

// Summarize returns a deterministic summary string for the given session.
func (FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", fmt.Errorf("session is nil")
	}
	return fmt.Sprintf("replay-summary:%s", sess.ID), nil
}

// SetPrompt is a no-op that satisfies the summarizer interface.
func (FakeSummarizer) SetPrompt(prompt string) {}

// SetModel is a no-op that satisfies the summarizer interface.
func (FakeSummarizer) SetModel(m model.Model) {}

// Metadata returns nil metadata for the fake summarizer.
func (FakeSummarizer) Metadata() map[string]any {
	return nil
}
