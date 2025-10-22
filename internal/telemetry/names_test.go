//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import "testing"

// Test span name helpers for simple formatting and empty model edge case.
func TestSpanNameHelpers(t *testing.T) {
	if got := NewChatSpanName("gpt-4.0"); got != "chat gpt-4.0" {
		t.Fatalf("NewChatSpanName got %q", got)
	}
	if got := NewChatSpanName(""); got != "chat" {
		t.Fatalf("NewChatSpanName empty got %q", got)
	}
	if got := NewExecuteToolSpanName("read_file"); got != "execute_tool read_file" {
		t.Fatalf("NewExecuteToolSpanName got %q", got)
	}
}
