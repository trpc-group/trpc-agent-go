//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Stores groups the stable storage roles used by BuiltinKnowledge. Each field
// remains strongly typed; Stores is configuration, not a common store API.
type Stores struct {
	// Vector stores normal document chunks, embeddings, and chunk metadata.
	Vector vectorstore.VectorStore
	// Graph stores graph nodes, edges, and graph-native query capabilities.
	Graph graphstore.Store
	// Resource stores source-level text, safe metadata, and directory relationships.
	Resource resourcestore.Store
}

// WithStores configures BuiltinKnowledge storage roles. Options are applied in
// order. A later WithStores call replaces all fields, while a later
// WithVectorStore call replaces only the vector field.
func WithStores(stores Stores) Option {
	return func(dk *BuiltinKnowledge) {
		dk.vectorStore = stores.Vector
		dk.graphStore = stores.Graph
		dk.resourceStore = stores.Resource
	}
}

// Graph returns the graph-native view when the configured graph store also
// implements graphstore.Searcher. The returned view borrows the store;
// BuiltinKnowledge remains its lifecycle owner.
func (dk *BuiltinKnowledge) Graph() (GraphKnowledge, bool) {
	if dk == nil || dk.graphStore == nil {
		return nil, false
	}
	if _, ok := dk.graphStore.(graphstore.Searcher); !ok {
		return nil, false
	}
	return NewGraphKnowledge(WithGraphStore(dk.graphStore)), true
}
