// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"testing"

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
