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
	Name                 string
	SupportsTrack        bool
	SupportsWindow       bool
	SupportsSearch       bool
	SupportsAppState     bool
	SupportsUserState    bool
	SupportsSessionState bool
	SupportsSoftDelete   bool
	SupportsAsyncSummary bool
	RetrievalProfile     RetrievalProfile
}

// RetrievalProfile describes memory retrieval semantics for a backend.
type RetrievalProfile struct {
	Algorithm      string
	Tokenizer      string
	EmbeddingModel string
	Dimension      int
	DistanceMetric string
	HybridEnabled  bool
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
			Algorithm: "keyword",
			Tokenizer: "simple",
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
			Algorithm: "keyword",
			Tokenizer: "simple",
		},
	}
}
