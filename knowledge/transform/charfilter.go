//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package transform

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// CharFilter removes specific characters or strings from document content.
// This is useful for preprocessing documents before chunking.
type CharFilter struct {
	replacer *strings.Replacer
}

// NewCharFilter creates a CharFilter that removes the specified characters or strings.
//
// Example:
//
//	filter := transform.NewCharFilter("\n", "\t", "\r")
func NewCharFilter(charsToRemove ...string) *CharFilter {
	args := make([]string, 0, len(charsToRemove)*2)
	for _, char := range charsToRemove {
		if char == "" {
			continue
		}
		args = append(args, char, "")
	}
	return &CharFilter{
		replacer: strings.NewReplacer(args...),
	}
}

// Preprocess applies the character filter to documents before chunking.
func (cf *CharFilter) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return cf.transform(docs)
}

// Postprocess returns documents unchanged (no-op for CharFilter).
func (cf *CharFilter) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

// transform applies the character filter transformation to documents.
func (cf *CharFilter) transform(docs []*document.Document) ([]*document.Document, error) {
	if len(docs) == 0 {
		return docs, nil
	}

	result := make([]*document.Document, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		cleaned := cf.cleanContent(doc.Content)
		result = append(result, cf.createProcessedDoc(doc, cleaned))
	}
	return result, nil
}

// cleanContent applies all character filters to the content.
func (cf *CharFilter) cleanContent(content string) string {
	return cf.replacer.Replace(content)
}

// createProcessedDoc creates a new document with processed content.
func (cf *CharFilter) createProcessedDoc(original *document.Document, content string) *document.Document {
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

// Name returns the name of this transformer.
func (cf *CharFilter) Name() string {
	return "CharFilter"
}
