//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
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

// cleanText normalizes whitespace in text content while ensuring UTF-8 safety.
// It automatically detects encoding and converts to UTF-8 if necessary.
func cleanText(content string) string {
	// Intelligently process text based on detected encoding
	processed, encodingInfo := encoding.SmartProcessText(content)

	// Log encoding information for debugging.
	if encodingInfo.Encoding != encoding.EncodingUTF8 || !encodingInfo.IsValid {
		log.Debugf("Text encoding detected: %s (confidence: %.2f, valid: %v)",
			encodingInfo.Encoding, encodingInfo.Confidence, encodingInfo.IsValid)
	}

	// Trim leading and trailing whitespace.
	processed = strings.TrimSpace(processed)

	// Normalize line breaks.
	processed = strings.ReplaceAll(processed, "\r\n", "\n")
	processed = strings.ReplaceAll(processed, "\r", "\n")

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(processed, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *document.Document, content string, chunkNumber int) *document.Document {
	// Create a copy of the original metadata.
	metadata := make(map[string]any)
	for k, v := range originalDoc.Metadata {
		metadata[k] = v
	}

	// Add chunk-specific metadata.
	metadata[source.MetaChunkIndex] = chunkNumber
	metadata[source.MetaChunkSize] = encoding.RuneCount(content)

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
