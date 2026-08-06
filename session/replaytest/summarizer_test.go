// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestFakeSummarizerDeterministic(t *testing.T) {
	s := NewFakeSummarizer()
	sess := &session.Session{}
	sess.Events = append(sess.Events, *UserEvent("k1", "hello"))
	text1, err := s.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	text2, err := s.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if text1 != text2 {
		t.Fatalf("not deterministic: %q vs %q", text1, text2)
	}
	if text1 == "" {
		t.Fatal("empty summary")
	}
	if !s.ShouldSummarize(sess) {
		t.Fatal("should summarize")
	}
	_ = s.Metadata()
	s.SetPrompt("p {conversation_text}")
	s.SetModel(nil)
}

func TestFakeSummarizerFixedAndError(t *testing.T) {
	s := NewFakeSummarizer(WithFixedSummaryText("fixed"), WithShouldSummarize(false))
	if s.ShouldSummarize(nil) {
		t.Fatal("expected false")
	}
	text, err := s.Summarize(context.Background(), nil)
	if err != nil || text != "fixed" {
		t.Fatalf("got %q %v", text, err)
	}
	s2 := NewFakeSummarizer(WithSummarizeError(context.Canceled))
	if _, err := s2.Summarize(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestTruncateText_UTF8Boundary(t *testing.T) {
	input := strings.Repeat("多语言摘要边界🙂", 12)
	got := truncateText(input, 64)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText returned invalid UTF-8: %q", got)
	}
	if len(got) > 67 { // 64-byte budget plus "..."
		t.Fatalf("len=%d want <=67", len(got))
	}
	if got == input {
		t.Fatal("expected truncation")
	}
	if got := truncateText(input, 0); got != "" {
		t.Fatalf("zero limit got %q", got)
	}
}

func TestFakeSummarizer_UTF8Summary(t *testing.T) {
	s := NewFakeSummarizer()
	sess := &session.Session{}
	sess.Events = append(sess.Events, *UserEvent("multi", strings.Repeat("你好🙂", 40)))
	text, err := s.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(text) {
		t.Fatalf("summary is invalid UTF-8: %q", text)
	}
}
