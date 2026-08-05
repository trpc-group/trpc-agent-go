//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import "testing"

func TestStreamingTextBufferPreservesContentOwnership(t *testing.T) {
	var buffer StreamingTextBuffer
	buffer.Append("text-response", "hello")
	buffer.Append("text-response", " world")

	responseID, content, ok := buffer.Take("tool-response")
	if !ok || responseID != "text-response" || content != "hello world" {
		t.Fatalf(
			"Take() = (%q, %q, %v), want (text-response, hello world, true)",
			responseID,
			content,
			ok,
		)
	}
	if got := buffer.Content(); got != "" {
		t.Fatalf("Content() after Take() = %q, want empty", got)
	}
}

func TestStreamingTextBufferReplacesArtifactInPlace(t *testing.T) {
	var buffer StreamingTextBuffer
	buffer.UpdateArtifact("artifact-response", "artifact", "hello", false)
	buffer.Append("suffix-response", "!")
	buffer.UpdateArtifact("snapshot-response", "artifact", "hello world", true)

	responseID, content, ok := buffer.Take("fallback")
	if !ok || responseID != "snapshot-response" || content != "hello world!" {
		t.Fatalf(
			"Take() = (%q, %q, %v), want (snapshot-response, hello world!, true)",
			responseID,
			content,
			ok,
		)
	}
}

func TestStreamingTextBufferUsesFallbackForLegacyContent(t *testing.T) {
	var buffer StreamingTextBuffer
	buffer.Append("", "legacy")

	responseID, content, ok := buffer.Take("trigger-response")
	if !ok || responseID != "trigger-response" || content != "legacy" {
		t.Fatalf(
			"Take() = (%q, %q, %v), want (trigger-response, legacy, true)",
			responseID,
			content,
			ok,
		)
	}
}

func TestStreamingTextBufferCanClearArtifactSnapshot(t *testing.T) {
	var buffer StreamingTextBuffer
	buffer.UpdateArtifact("stale-response", "artifact", "stale", false)
	buffer.UpdateArtifact("", "artifact", "", true)
	buffer.Append("next-response", "next")

	responseID, content, ok := buffer.Take("fallback")
	if !ok || responseID != "next-response" || content != "next" {
		t.Fatalf(
			"Take() = (%q, %q, %v), want (next-response, next, true)",
			responseID,
			content,
			ok,
		)
	}
}
