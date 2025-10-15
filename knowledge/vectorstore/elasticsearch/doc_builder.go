//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

type esDocument map[string]json.RawMessage

func (es esDocument) getString(key string) string {
	value, ok := es[key]
	if !ok {
		return ""
	}
	var str string
	err := json.Unmarshal(value, &str)
	if err != nil {
		return ""
	}
	return str
}

func (es esDocument) getMetadata(key string) map[string]any {
	value, ok := es[key]
	if !ok {
		return nil
	}
	var m map[string]any
	err := json.Unmarshal(value, &m)
	if err != nil {
		return nil
	}
	return m
}

func (es esDocument) getEmbedding(key string) []float64 {
	value, ok := es[key]
	if !ok {
		return nil
	}
	var embedding []float64
	err := json.Unmarshal(value, &embedding)
	if err != nil {
		return nil
	}
	return embedding
}

func (es esDocument) getTime(key string) time.Time {
	value, ok := es[key]
	if !ok {
		return time.Time{}
	}
	var date time.Time
	err := json.Unmarshal(value, &date)
	if err != nil {
		return time.Time{}
	}
	return date
}

func (vs *VectorStore) docBuilder(hitSource json.RawMessage) (*document.Document, []float64, error) {
	if vs.option.docBuilder != nil {
		return vs.option.docBuilder(hitSource)
	}
	// Parse the _source field using our unified esDocument struct.
	var source esDocument
	if err := json.Unmarshal(hitSource, &source); err != nil {
		return nil, nil, err
	}
	// Create document.
	doc := &document.Document{
		ID:        source.getString(vs.option.idFieldName),
		Name:      source.getString(vs.option.nameFieldName),
		Content:   source.getString(vs.option.contentFieldName),
		Metadata:  source.getMetadata(vs.option.metadataFieldName),
		CreatedAt: source.getTime(vs.option.createdAtFieldName),
		UpdatedAt: source.getTime(vs.option.updatedAtFieldName),
	}
	return doc, source.getEmbedding(vs.option.embeddingFieldName), nil
}
