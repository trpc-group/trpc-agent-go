//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

// BackendProfile declares the observable capabilities of a replay backend.
type BackendProfile struct {
	// Name identifies the backend in reports and comparison output.
	Name string
	// SupportsTrack reports whether the backend implements session.TrackService.
	SupportsTrack bool
	// SupportsWindow reports whether the backend supports event window reads.
	SupportsWindow bool
	// SupportsSearch reports whether the backend supports session event search.
	SupportsSearch bool
	// SupportsAppState reports whether app-scoped state is persisted.
	SupportsAppState bool
	// SupportsUserState reports whether user-scoped state is persisted.
	SupportsUserState bool
	// SupportsSessionState reports whether session-scoped state is persisted.
	SupportsSessionState bool
	// SupportsSoftDelete reports whether deletes preserve soft-delete metadata.
	SupportsSoftDelete bool
	// SupportsAsyncSummary reports whether async summary enqueue is available.
	SupportsAsyncSummary bool
	// RetrievalProfile describes memory search semantics for the backend.
	RetrievalProfile RetrievalProfile
}

// RetrievalProfile describes memory retrieval semantics for a backend.
type RetrievalProfile struct {
	// Algorithm names the retrieval algorithm, such as bm25 or cosine_vector.
	Algorithm string
	// Tokenizer names the text tokenizer used by lexical retrieval.
	Tokenizer string
	// EmbeddingModel names the embedding model used by vector retrieval.
	EmbeddingModel string
	// Dimension records the embedding dimension for vector retrieval.
	Dimension int
	// DistanceMetric names the vector distance metric, such as cosine or l2.
	DistanceMetric string
	// HybridEnabled reports whether hybrid retrieval fusion is enabled.
	HybridEnabled bool
}

// InMemoryProfile returns the built-in profile for in-memory replay backends.
func InMemoryProfile() BackendProfile {
	return BackendProfile{
		Name:                 "inmemory",
		SupportsTrack:        true,
		SupportsWindow:       true,
		SupportsSearch:       false,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsSoftDelete:   false,
		SupportsAsyncSummary: true,
		RetrievalProfile: RetrievalProfile{
			Algorithm: "bm25",
			Tokenizer: "gse_cjk",
		},
	}
}

// SQLiteProfile returns the built-in profile for SQLite replay backends.
func SQLiteProfile() BackendProfile {
	return BackendProfile{
		Name:                 "sqlite",
		SupportsTrack:        true,
		SupportsWindow:       true,
		SupportsSearch:       false,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsSoftDelete:   true,
		SupportsAsyncSummary: true,
		RetrievalProfile: RetrievalProfile{
			Algorithm: "bm25",
			Tokenizer: "gse_cjk",
		},
	}
}

// RedisProfile returns the built-in profile for Redis replay backends.
func RedisProfile() BackendProfile {
	return BackendProfile{
		Name:                 "redis",
		SupportsTrack:        true,
		SupportsWindow:       true,
		SupportsSearch:       false,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsSoftDelete:   false,
		SupportsAsyncSummary: true,
		RetrievalProfile: RetrievalProfile{
			Algorithm: "bm25",
			Tokenizer: "gse_cjk",
		},
	}
}
