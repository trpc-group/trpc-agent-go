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

func (es esDocument) stringField(key string) string {
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

func (es esDocument) mapField(key string) map[string]any {
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

func (es esDocument) sliceField(key string) []float64 {
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

func (es esDocument) timeField(key string) time.Time {
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
		ID:        source.stringField(vs.option.idFieldName),
		Name:      source.stringField(vs.option.nameFieldName),
		Content:   source.stringField(vs.option.contentFieldName),
		Metadata:  source.mapField(vs.option.metadataFieldName),
		CreatedAt: source.timeField(vs.option.createdAtFieldName),
		UpdatedAt: source.timeField(vs.option.updatedAtFieldName),
	}
	return doc, source.sliceField(vs.option.embeddingFieldName), nil
}
