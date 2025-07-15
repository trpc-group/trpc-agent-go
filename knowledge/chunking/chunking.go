//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Strategy defines the interface for document chunking strategies.
type Strategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc *document.Document) ([]*document.Document, error)
}

var (
	defaultChunkSize = 1024
	defaultOverlap   = 128
)

// cleanText normalizes whitespace in text content.
func cleanText(content string) string {
	// Trim leading and trailing whitespace.
	content = strings.TrimSpace(content)

	// Normalize line breaks.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *document.Document, content string, chunkNumber int) *document.Document {
	// Create a copy of the original metadata.
	metadata := make(map[string]interface{})
	for k, v := range originalDoc.Metadata {
		metadata[k] = v
	}

	// Add chunk-specific metadata.
	metadata["chunk"] = chunkNumber
	metadata["chunk_size"] = len(content)

	// Generate chunk ID.
	var chunkID string
	if originalDoc.ID != "" {
		chunkID = originalDoc.ID + "_" + strconv.Itoa(chunkNumber)
	} else if originalDoc.Name != "" {
		chunkID = originalDoc.Name + "_" + strconv.Itoa(chunkNumber)
	} else {
		chunkID = "chunk_" + strconv.Itoa(chunkNumber)
	}

	return &document.Document{
		ID:        chunkID,
		Name:      originalDoc.Name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}
