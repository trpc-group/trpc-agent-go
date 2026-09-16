//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"bytes"
	"encoding/json"
)

type requestSpec struct {
	method         string
	path           string
	body           []byte
	expectedStatus int
}

type responseField[T any] struct {
	value   T
	present bool
	null    bool
}

// UnmarshalJSON records whether a protocol field was present and explicitly null.
func (f *responseField[T]) UnmarshalJSON(data []byte) error {
	f.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.null = true
		var zero T
		f.value = zero
		return nil
	}
	return json.Unmarshal(data, &f.value)
}

type preflightChecksResponse struct {
	MaxBatchSize           int  `json:"max_batch_size"`
	SupportsBase64Encoding bool `json:"supports_base64_encoding"`
}

type identityResponse struct {
	UserID    string   `json:"user_id"`
	Tenant    string   `json:"tenant"`
	Databases []string `json:"databases"`
}

type vectorIndexConfig struct {
	Space string `json:"space"`
}

type collectionConfiguration struct {
	HNSW  *vectorIndexConfig `json:"hnsw"`
	SPANN *vectorIndexConfig `json:"spann"`
}

type collectionSchema struct {
	Defaults map[string]map[string]schemaIndexState `json:"defaults"`
	Keys     map[string]map[string]schemaIndexState `json:"keys"`
}

type schemaIndexState struct {
	Enabled *bool `json:"enabled"`
}

type collectionResponse struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	ConfigurationJSON collectionConfiguration `json:"configuration_json"`
	Metadata          map[string]any          `json:"metadata"`
	Dimension         responseField[int]      `json:"dimension"`
	Tenant            string                  `json:"tenant"`
	Database          string                  `json:"database"`
	Schema            *collectionSchema       `json:"schema"`
}

type createCollectionRequest struct {
	Name          string                        `json:"name"`
	Metadata      map[string]any                `json:"metadata,omitempty"`
	Configuration createCollectionConfiguration `json:"configuration"`
	GetOrCreate   bool                          `json:"get_or_create"`
}

type createCollectionConfiguration struct {
	// Chroma Cloud may materialize this HNSW cosine request as a SPANN cosine index.
	HNSW vectorIndexConfig `json:"hnsw"`
}

type addRecordsRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float32      `json:"embeddings"`
	Documents  []*string        `json:"documents"`
	Metadatas  []map[string]any `json:"metadatas"`
}

type updateRecordsRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float32      `json:"embeddings,omitempty"`
	Documents  []*string        `json:"documents,omitempty"`
	Metadatas  []map[string]any `json:"metadatas,omitempty"`
}

type getRecordsRequest struct {
	IDs     []string       `json:"ids,omitempty"`
	Where   map[string]any `json:"where,omitempty"`
	Limit   *int           `json:"limit,omitempty"`
	Offset  *int           `json:"offset,omitempty"`
	Include *[]string      `json:"include,omitempty"`
}

type getRecordsResponse struct {
	IDs       responseField[[]string] `json:"ids"`
	Documents *[]*string              `json:"documents"`
	Metadatas *[]map[string]any       `json:"metadatas"`
	Include   responseField[[]string] `json:"include"`
}

type queryRecordsRequest struct {
	Where           map[string]any `json:"where,omitempty"`
	QueryEmbeddings [][]float32    `json:"query_embeddings"`
	NResults        int            `json:"n_results"`
	Include         []string       `json:"include"`
}

type queryRecordsResponse struct {
	IDs       responseField[[][]string] `json:"ids"`
	Documents *[][]*string              `json:"documents"`
	Metadatas *[][]map[string]any       `json:"metadatas"`
	Distances *[][]*float32             `json:"distances"`
	Include   responseField[[]string]   `json:"include"`
}

type deleteRecordsRequest struct {
	IDs   []string       `json:"ids,omitempty"`
	Where map[string]any `json:"where,omitempty"`
	Limit *int           `json:"limit,omitempty"`
}

type deleteRecordsResponse struct {
	Deleted responseField[int] `json:"deleted"`
}

type databaseRef struct {
	tenant   string
	database string
}

type collectionRef struct {
	databaseRef
	id string
}
