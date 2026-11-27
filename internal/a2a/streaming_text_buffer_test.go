//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

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

func TestStreamingTextBufferReplacesArtifactContentParts(t *testing.T) {
	var buffer StreamingTextBuffer
	stale := model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "stale"}}
	replacement := model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "replacement"}}
	buffer.UpdateArtifactContent("stale-response", "artifact", "stale", []model.ContentPart{stale}, false)
	buffer.UpdateArtifactContent(
		"replacement-response",
		"artifact",
		"replacement",
		[]model.ContentPart{replacement},
		true,
	)

	responseID, content, parts, ok := buffer.TakeContent("fallback")
	if !ok || responseID != "replacement-response" || content != "replacement" {
		t.Fatalf("TakeContent() = (%q, %q, %v), want replacement snapshot", responseID, content, ok)
	}
	if len(parts) != 1 || parts[0].File == nil || parts[0].File.Name != "replacement" {
		t.Fatalf("replacement parts = %#v", parts)
	}
}

func TestStreamingTextBufferKeepsAppendFragments(t *testing.T) {
	var buffer StreamingTextBuffer
	buffer.UpdateArtifact("response", "artifact", "one", false)
	buffer.UpdateArtifact("response", "artifact", "two", false)
	buffer.UpdateArtifact("response", "artifact", "three", false)

	if len(buffer.chunks) != 1 || len(buffer.chunks[0].fragments) != 3 {
		t.Fatalf("buffer chunks = %#v, want three append fragments", buffer.chunks)
	}
	if got := buffer.Content(); got != "onetwothree" {
		t.Fatalf("Content() = %q, want onetwothree", got)
	}
}
