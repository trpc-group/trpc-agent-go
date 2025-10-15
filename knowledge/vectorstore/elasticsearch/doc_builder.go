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

type esDocument map[string]any

func (es esDocument) getString(key string) string {
	value, ok := es[key]
	if !ok {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return str
}

func (es esDocument) getMetadata(key string) map[string]any {
	value, ok := es[key]
	if !ok {
		return nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func (es esDocument) getEmbedding(key string) []float64 {
	value, ok := es[key]
	if !ok {
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	var floatSlice []float64
	for _, v := range values {
		val, ok := v.(float64)
		if !ok {
			return nil
		}
		floatSlice = append(floatSlice, val)
	}

	return floatSlice
}

func (es esDocument) getTime(key string) time.Time {
	value, ok := es[key]
	if !ok {
		return time.Time{}
	}
	str, ok := value.(string)
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return time.Time{}
	}
	return t
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
