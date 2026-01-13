//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package transform provides document transformation capabilities.
// Transformers can preprocess documents before chunking and postprocess after chunking.
//
// Processing flow:
//
//	docs → Preprocess → processed docs → Chunking → chunks → Postprocess → final chunks
//
// Example usage:
//
//	import "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
//
//	// Create transformers
//	filter := transform.NewCharFilter("\n", "\t")
//	dedup := transform.NewCharDedup(" ")
//
//	// Use with source
//	source := file.New(paths, file.WithTransformers(filter, dedup))
package transform

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Transformer defines the interface for document transformers.
// Transformers can preprocess documents before chunking and postprocess after chunking.
type Transformer interface {
	// Preprocess applies transformation to documents before chunking.
	// Returns the input documents unchanged if no transformation is needed.
	Preprocess(docs []*document.Document) ([]*document.Document, error)

	// Postprocess applies transformation to documents after chunking.
	// Returns the input documents unchanged if no transformation is needed.
	Postprocess(docs []*document.Document) ([]*document.Document, error)

	// Name returns the name of this transformer.
	Name() string
}
