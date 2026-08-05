//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// StreamingTextBuffer keeps pending streaming content together with the
// response that owns it. Artifact snapshots can replace earlier text and rich
// content parts without disturbing the order of other pending content.
type StreamingTextBuffer struct {
	chunks    []streamingTextChunk
	artifacts map[string]int
}

type streamingTextChunk struct {
	responseID string
	fragments  []string
	parts      []model.ContentPart
}

// Append adds an ordinary streaming text delta.
func (b *StreamingTextBuffer) Append(responseID, content string) {
	b.AppendContent(responseID, content, nil)
}

// AppendContent adds ordinary streaming text and rich content parts.
func (b *StreamingTextBuffer) AppendContent(
	responseID string,
	content string,
	parts []model.ContentPart,
) {
	if b == nil || content == "" && len(parts) == 0 {
		return
	}
	var fragments []string
	if content != "" {
		fragments = []string{content}
	}
	b.chunks = append(b.chunks, streamingTextChunk{
		responseID: responseID,
		fragments:  fragments,
		parts:      append([]model.ContentPart(nil), parts...),
	})
}

// UpdateArtifact adds or replaces text for one artifact. The first update
// fixes the artifact's position relative to other pending text.
func (b *StreamingTextBuffer) UpdateArtifact(
	responseID string,
	artifactID string,
	content string,
	replace bool,
) {
	b.UpdateArtifactContent(responseID, artifactID, content, nil, replace)
}

// UpdateArtifactContent adds or replaces pending content for one artifact.
func (b *StreamingTextBuffer) UpdateArtifactContent(
	responseID string,
	artifactID string,
	content string,
	parts []model.ContentPart,
	replace bool,
) {
	if b == nil {
		return
	}
	if artifactID == "" {
		b.AppendContent(responseID, content, parts)
		return
	}
	if index, ok := b.artifacts[artifactID]; ok {
		chunk := &b.chunks[index]
		if replace {
			chunk.fragments = nil
			chunk.parts = append([]model.ContentPart(nil), parts...)
			if content != "" {
				chunk.fragments = []string{content}
			}
			if responseID != "" {
				chunk.responseID = responseID
			}
			return
		}
		if content != "" {
			chunk.fragments = append(chunk.fragments, content)
		}
		chunk.parts = append(chunk.parts, parts...)
		if chunk.responseID == "" {
			chunk.responseID = responseID
		}
		return
	}
	if content == "" && len(parts) == 0 {
		return
	}
	if b.artifacts == nil {
		b.artifacts = make(map[string]int)
	}
	var fragments []string
	if content != "" {
		fragments = []string{content}
	}
	b.artifacts[artifactID] = len(b.chunks)
	b.chunks = append(b.chunks, streamingTextChunk{
		responseID: responseID,
		fragments:  fragments,
		parts:      append([]model.ContentPart(nil), parts...),
	})
}

// Content returns the pending text in stream order.
func (b *StreamingTextBuffer) Content() string {
	if b == nil || len(b.chunks) == 0 {
		return ""
	}
	var content strings.Builder
	for _, chunk := range b.chunks {
		for _, fragment := range chunk.fragments {
			content.WriteString(fragment)
		}
	}
	return content.String()
}

// ContentParts returns the pending rich content parts in stream order.
func (b *StreamingTextBuffer) ContentParts() []model.ContentPart {
	if b == nil || len(b.chunks) == 0 {
		return nil
	}
	var parts []model.ContentPart
	for _, chunk := range b.chunks {
		parts = append(parts, chunk.parts...)
	}
	return parts
}

// Take returns and clears the pending text. Its response ID comes from the
// first non-empty chunk, with fallbackResponseID retained for legacy streams
// that do not identify their text response.
func (b *StreamingTextBuffer) Take(
	fallbackResponseID string,
) (responseID string, content string, ok bool) {
	responseID, content, _, ok = b.TakeContent(fallbackResponseID)
	return responseID, content, ok
}

// TakeContent returns and clears all pending text and rich content parts.
func (b *StreamingTextBuffer) TakeContent(
	fallbackResponseID string,
) (responseID string, content string, parts []model.ContentPart, ok bool) {
	if b == nil {
		return "", "", nil, false
	}
	content = b.Content()
	parts = b.ContentParts()
	if content != "" || len(parts) > 0 {
		responseID = fallbackResponseID
		for _, chunk := range b.chunks {
			if (len(chunk.fragments) > 0 || len(chunk.parts) > 0) &&
				chunk.responseID != "" {
				responseID = chunk.responseID
				break
			}
		}
	}
	b.Reset()
	return responseID, content, parts, content != "" || len(parts) > 0
}

// Reset clears all pending text and artifact ownership.
func (b *StreamingTextBuffer) Reset() {
	if b == nil {
		return
	}
	b.chunks = nil
	b.artifacts = nil
}
