//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package processor provides internal document processing capabilities.
// preprocessors perform 1:1 document transformations before chunking.
//
// Processing flow:
//
//	doc → preprocessors → processed doc → Chunking → chunks
//	      ^^^^^^^^^^^^                    ^^^^^^^^
//	      1:1 processing                  1:N splitting
package processor

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// PreProcessor defines the interface for document preprocessors.
// Preprocessors perform 1:1 document transformations before chunking.
type PreProcessor interface {
	// Process applies the preprocessing to a document and returns the processed document.
	// Returns nil if the input document is nil.
	Process(doc *document.Document) (*document.Document, error)

	// Name returns the name of this processor.
	Name() string
}

// ApplyPreProcessors applies a chain of preprocessors to a document.
// Returns the original document if no preprocessors are provided.
func ApplyPreProcessors(doc *document.Document, processors ...PreProcessor) (*document.Document, error) {
	if doc == nil || len(processors) == 0 {
		return doc, nil
	}

	result := doc
	var err error
	for _, p := range processors {
		if p == nil {
			continue
		}
		result, err = p.Process(result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

