//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestFakeSummarizer(t *testing.T) {
	summarizer := FakeSummarizer{}
	if !summarizer.ShouldSummarize(&session.Session{ID: "s1"}) {
		t.Fatal("应始终触发摘要")
	}
	text, err := summarizer.Summarize(context.Background(), &session.Session{ID: "s1"})
	if err != nil || text != "replay-summary:s1" {
		t.Fatalf("摘要内容错误: %q, %v", text, err)
	}
	if _, err := summarizer.Summarize(context.Background(), nil); err == nil {
		t.Fatal("nil session 应返回错误")
	}
	summarizer.SetPrompt("prompt")
	summarizer.SetModel(nil)
	if summarizer.Metadata() != nil {
		t.Fatal("metadata 应为 nil")
	}
}
