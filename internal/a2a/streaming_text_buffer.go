//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import "strings"

// StreamingTextBuffer keeps pending streaming text together with the response
// that owns it. Artifact snapshots can replace an earlier value without
// disturbing the order of other pending text.
type StreamingTextBuffer struct {
	chunks    []streamingTextChunk
	artifacts map[string]int
}

type streamingTextChunk struct {
	responseID string
	content    string
}

// Append adds an ordinary streaming text delta.
func (b *StreamingTextBuffer) Append(responseID, content string) {
	if b == nil || content == "" {
		return
	}
	b.chunks = append(b.chunks, streamingTextChunk{
		responseID: responseID,
		content:    content,
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
	if b == nil {
		return
	}
	if artifactID == "" {
		b.Append(responseID, content)
		return
	}
	if index, ok := b.artifacts[artifactID]; ok {
		chunk := &b.chunks[index]
		if replace {
			chunk.content = content
			if responseID != "" {
				chunk.responseID = responseID
			}
			return
		}
		chunk.content += content
		if chunk.responseID == "" {
			chunk.responseID = responseID
		}
		return
	}
	if content == "" {
		return
	}
	if b.artifacts == nil {
		b.artifacts = make(map[string]int)
	}
	b.artifacts[artifactID] = len(b.chunks)
	b.chunks = append(b.chunks, streamingTextChunk{
		responseID: responseID,
		content:    content,
	})
}

// Content returns the pending text in stream order.
func (b *StreamingTextBuffer) Content() string {
	if b == nil || len(b.chunks) == 0 {
		return ""
	}
	var content strings.Builder
	for _, chunk := range b.chunks {
		content.WriteString(chunk.content)
	}
	return content.String()
}

// Take returns and clears the pending text. Its response ID comes from the
// first non-empty chunk, with fallbackResponseID retained for legacy streams
// that do not identify their text response.
func (b *StreamingTextBuffer) Take(
	fallbackResponseID string,
) (responseID string, content string, ok bool) {
	if b == nil {
		return "", "", false
	}
	content = b.Content()
	if content != "" {
		responseID = fallbackResponseID
		for _, chunk := range b.chunks {
			if chunk.content != "" && chunk.responseID != "" {
				responseID = chunk.responseID
				break
			}
		}
	}
	b.Reset()
	return responseID, content, content != ""
}

// Reset clears all pending text and artifact ownership.
func (b *StreamingTextBuffer) Reset() {
	if b == nil {
		return
	}
	b.chunks = nil
	b.artifacts = nil
}
