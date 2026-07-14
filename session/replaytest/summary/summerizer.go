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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

//模拟一个 summarizer  代替大模型

// FakeSummarizer 用确定性规则模拟大模型摘要，避免测试依赖真实 LLM。
//
// 摘要文本由 summarizer 实际收到的有序事件 ID/内容以及 filter key 决定，
// 而不是只依赖 session ID。这样当后端使用了错误的 filter-key、陈旧事件集，
// 或者强制覆盖未真正刷新摘要内容时，比较都能检出差异。
type FakeSummarizer struct{}

// ShouldSummarize always reports that summarization should run.
func (FakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	_ = sess
	return true
}

// Summarize returns a deterministic summary derived from the ordered events the
// summarizer actually received and the filter key the caller requested.
//
// The real summary pipeline builds a temporary session whose ID is
// "<baseID>:<filterKey>" and whose Events slice contains only the events that
// survived filter-key and boundary selection (with the previous summary
// prepended as a synthetic system event). Encoding those inputs makes the
// output sensitive to wrong filter keys, stale event windows, and missing
// refreshes.
func (FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	_ = ctx
	if sess == nil {
		return "", fmt.Errorf("session is nil")
	}
	filterKey := scopeFilterKeyFromSession(sess)
	var b strings.Builder
	fmt.Fprintf(&b, "replay-summary[filter=%s,n=%d]", filterKey, len(sess.Events))
	for i := range sess.Events {
		b.WriteString("|")
		b.WriteString(eventSignature(sess.Events[i]))
	}
	return b.String(), nil
}

// SetPrompt is a no-op that satisfies the summarizer interface.
func (FakeSummarizer) SetPrompt(prompt string) {
	_ = prompt
}

// SetModel is a no-op that satisfies the summarizer interface.
func (FakeSummarizer) SetModel(m model.Model) {
	_ = m
}

// Metadata returns nil metadata for the fake summarizer.
func (FakeSummarizer) Metadata() map[string]any {
	return nil
}

// scopeFilterKeyFromSession recovers the requested filter key from the
// temporary summary session. The summary pipeline sets the temp session ID to
// "<baseID>:<filterKey>"; for the full-session summary filterKey is empty and
// the ID ends with a trailing colon. This keeps the fake summarizer free of any
// dependency on internal packages.
func scopeFilterKeyFromSession(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	id := sess.ID
	idx := strings.LastIndex(id, ":")
	if idx < 0 {
		return ""
	}
	return id[idx+1:]
}

// eventSignature builds a stable, content-derived signature for one event so
// that reordering, dropping, or mutating an input event changes the summary.
func eventSignature(evt event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return fmt.Sprintf("%s:%s", evt.ID, evt.Author)
	}
	msg := evt.Response.Choices[0].Message
	return fmt.Sprintf("%s:%s:%s", evt.ID, msg.Role, msg.Content)
}
