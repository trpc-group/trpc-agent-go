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
	"regexp"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// CharDedup collapses consecutive repeated characters/strings into a single occurrence.
// For example, "\t\t\t\t" becomes "\t", "   " becomes " ".
type CharDedup struct {
	patterns     []*regexp.Regexp
	replacements []string
}

// NewCharDedup creates a CharDedup that collapses consecutive occurrences of the specified strings.
//
// Example:
//
//	dedup := transform.NewCharDedup("\t", " ", "\n")
//	// Input:  "hello\t\t\tworld   foo\n\n\nbar"
//	// Output: "hello\tworld foo\nbar"
func NewCharDedup(charsToDedup ...string) *CharDedup {
	patterns := make([]*regexp.Regexp, 0, len(charsToDedup))
	replacements := make([]string, 0, len(charsToDedup))

	for _, char := range charsToDedup {
		if char == "" {
			continue
		}
		// Escape special regex characters and create pattern for 2+ consecutive occurrences
		escaped := regexp.QuoteMeta(char)
		pattern := regexp.MustCompile("(" + escaped + "){2,}")
		patterns = append(patterns, pattern)
		replacements = append(replacements, char)
	}

	return &CharDedup{
		patterns:     patterns,
		replacements: replacements,
	}
}

// Preprocess applies the character deduplication to documents before chunking.
func (cd *CharDedup) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return cd.transform(docs)
}

// Postprocess returns documents unchanged (no-op for CharDedup).
func (cd *CharDedup) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

// transform applies the character deduplication transformation to documents.
func (cd *CharDedup) transform(docs []*document.Document) ([]*document.Document, error) {
	if len(docs) == 0 {
		return docs, nil
	}

	result := make([]*document.Document, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		deduped := cd.dedupContent(doc.Content)
		result = append(result, cd.createProcessedDoc(doc, deduped))
	}
	return result, nil
}

// dedupContent applies all deduplication patterns to the content.
func (cd *CharDedup) dedupContent(content string) string {
	result := content

	for i, pattern := range cd.patterns {
		result = pattern.ReplaceAllLiteralString(result, cd.replacements[i])
	}

	return result
}

// createProcessedDoc creates a new document with processed content.
func (cd *CharDedup) createProcessedDoc(original *document.Document, content string) *document.Document {
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
func (cd *CharDedup) Name() string {
	return "CharDedup"
}
