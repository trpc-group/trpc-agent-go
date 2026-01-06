//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package charfilter provides character filtering preprocessor for documents.
package charfilter

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// CharFilter removes or replaces specific characters from document content.
// This is useful for preprocessing documents before chunking.
type CharFilter struct {
	// charsToRemove contains characters to be completely removed.
	charsToRemove []string
	// replacements maps characters to their replacements.
	replacements map[string]string
}

// New creates a CharFilter that removes the specified characters.
//
// Example:
//
//	filter := New("\n", "\t", "\r")
func New(charsToRemove ...string) *CharFilter {
	return &CharFilter{
		charsToRemove: charsToRemove,
		replacements:  make(map[string]string),
	}
}

// NewWithReplacements creates a CharFilter with both removals and replacements.
func NewWithReplacements(charsToRemove []string, replacements map[string]string) *CharFilter {
	r := make(map[string]string)
	for k, v := range replacements {
		r[k] = v
	}
	return &CharFilter{
		charsToRemove: charsToRemove,
		replacements:  r,
	}
}

// Process applies the character filter to a document.
func (cf *CharFilter) Process(doc *document.Document) (*document.Document, error) {
	if doc == nil {
		return nil, nil
	}

	cleaned := cf.cleanContent(doc.Content)
	return cf.createProcessedDoc(doc, cleaned), nil
}

// cleanContent applies all character filters to the content.
func (cf *CharFilter) cleanContent(content string) string {
	result := content

	// Apply removals first
	for _, char := range cf.charsToRemove {
		result = strings.ReplaceAll(result, char, "")
	}

	// Apply replacements
	for old, new := range cf.replacements {
		result = strings.ReplaceAll(result, old, new)
	}

	return result
}

// createProcessedDoc creates a new document with processed content.
func (cf *CharFilter) createProcessedDoc(original *document.Document, content string) *document.Document {
	// Copy metadata
	metadata := make(map[string]any)
	for k, v := range original.Metadata {
		metadata[k] = v
	}

	return &document.Document{
		ID:        original.ID,
		Name:      original.Name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: original.CreatedAt,
		UpdatedAt: time.Now().UTC(),
	}
}

// Name returns the name of this processor.
func (cf *CharFilter) Name() string {
	return "CharFilter"
}

// Apply applies character filtering to a document.
// Returns the original document if no characters to filter.
func Apply(doc *document.Document, charsToRemove []string) (*document.Document, error) {
	if len(charsToRemove) == 0 || doc == nil {
		return doc, nil
	}

	filter := New(charsToRemove...)
	return filter.Process(doc)
}
