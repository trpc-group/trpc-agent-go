//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// internalEmbeddingTextKey stores document.EmbeddingText inside the metadata
// column. It is stripped from the metadata map on every read path, so callers
// never see it.
const internalEmbeddingTextKey = "__clickhouse_embedding_text"

// Document mapping between trpc-agent-go document.Document and the ClickHouse row.
//
//	trpc-agent-go Document          ClickHouse column
//	─────────────────────           ───────────────────
//	ID                              id        String
//	Name                            name      String
//	Content                         content   String
//	Metadata (map[string]any)       metadata  String (JSON encoded)
//	CreatedAt (time.Time)           created_at DateTime64(6)
//	UpdatedAt (time.Time)           updated_at DateTime64(6)
//	embedding ([]float64)           embedding Array(Float64)
//	filterFields                    <name>    typed column
//
// Filter fields declared through WithFilterFields are materialized as dedicated
// typed columns in addition to being present in the JSON-encoded metadata.

// row is the internal in-memory representation of a ClickHouse row.
type row struct {
	id        string
	name      string
	content   string
	embedding []float64
	metadata  map[string]any
	createdAt time.Time
	updatedAt time.Time
}

// docToRow converts a trpc-agent-go document and embedding into an internal row.
//
// now is a caller-supplied timestamp used for created_at and updated_at to keep
// tests deterministic.
func (vs *VectorStore) docToRow(doc *document.Document, embedding []float64, now time.Time) (*row, error) {
	if doc == nil {
		return nil, errDocumentRequired
	}
	if doc.ID == "" {
		return nil, errDocumentIDRequired
	}
	return &row{
		id:        doc.ID,
		name:      doc.Name,
		content:   doc.Content,
		embedding: embedding,
		metadata:  withEmbeddingText(doc.Metadata, doc.EmbeddingText),
		createdAt: now,
		updatedAt: now,
	}, nil
}

// withEmbeddingText returns a copy of metadata carrying embeddingText under an
// internal key, so the field survives a round trip through the metadata column.
// The caller's map is never mutated.
func withEmbeddingText(metadata map[string]any, embeddingText string) map[string]any {
	if embeddingText == "" {
		return metadata
	}
	stored := make(map[string]any, len(metadata)+1)
	for k, v := range metadata {
		stored[k] = v
	}
	stored[internalEmbeddingTextKey] = embeddingText
	return stored
}

// rowToDoc converts an internal row back into a trpc-agent-go document.
func (vs *VectorStore) rowToDoc(r *row) (*document.Document, []float64, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("clickhouse: row is nil")
	}
	metadata, embeddingText := splitEmbeddingText(r.metadata)
	doc := &document.Document{
		ID:            r.id,
		Name:          r.name,
		Content:       r.content,
		Metadata:      metadata,
		EmbeddingText: embeddingText,
		CreatedAt:     r.createdAt,
		UpdatedAt:     r.updatedAt,
	}
	return doc, r.embedding, nil
}

// splitEmbeddingText extracts the internally stored embedding text and returns
// the metadata without that key, so callers never observe it.
func splitEmbeddingText(metadata map[string]any) (map[string]any, string) {
	raw, ok := metadata[internalEmbeddingTextKey]
	if !ok {
		return metadata, ""
	}
	text, _ := raw.(string)
	out := make(map[string]any, len(metadata)-1)
	for k, v := range metadata {
		if k == internalEmbeddingTextKey {
			continue
		}
		out[k] = v
	}
	return out, text
}

// marshalMetadata JSON-encodes the document metadata map into a string suitable
// for the metadata String column. A nil or empty map encodes to "{}".
func marshalMetadata(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalMetadata JSON-decodes a metadata String column value. An empty string
// yields an empty map.
func unmarshalMetadata(s string) (map[string]any, error) {
	if s == "" {
		return map[string]any{}, nil
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// filterFieldValues extracts the declared filter-field values from metadata and
// converts them to the typed column values expected by the ClickHouse driver.
// Missing values map to the column type's zero value.
func (vs *VectorStore) filterFieldValues(metadata map[string]any) ([]any, error) {
	if len(vs.option.filterFields) == 0 {
		return nil, nil
	}
	values := make([]any, len(vs.option.filterFields))
	for i, spec := range vs.option.filterFields {
		v, err := convertFilterFieldValue(spec.Type, metadata[spec.Name])
		if err != nil {
			return nil, fmt.Errorf("clickhouse: filter field %q: %w", spec.Name, err)
		}
		values[i] = v
	}
	return values, nil
}

// convertFilterFieldValue converts a metadata value to the typed value expected
// by the ClickHouse column for the given FilterFieldType.
func convertFilterFieldValue(t FilterFieldType, v any) (any, error) {
	switch t {
	case FilterFieldString:
		if v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("expected string, got %T", v)
		}
		return s, nil
	case FilterFieldInt64:
		if v == nil {
			return int64(0), nil
		}
		return toInt64(v)
	case FilterFieldFloat64:
		if v == nil {
			return float64(0), nil
		}
		return toFloat64(v)
	default:
		return nil, fmt.Errorf("unknown FilterFieldType %d", t)
	}
}

// toInt64 converts a supported numeric value to int64.
func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		if uint64(x) > math.MaxInt64 {
			return 0, fmt.Errorf("uint value %d overflows int64", x)
		}
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return 0, fmt.Errorf("uint64 value %d overflows int64", x)
		}
		return int64(x), nil
	case float32:
		return floatToInt64(float64(x))
	case float64:
		return floatToInt64(x)
	case json.Number:
		return x.Int64()
	default:
		return 0, fmt.Errorf("expected integer, got %T", v)
	}
}

// floatToInt64 validates a floating-point value before converting it to int64.
func floatToInt64(f float64) (int64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("non-finite float %v cannot be int64", f)
	}
	if f != math.Trunc(f) {
		return 0, fmt.Errorf("float %v is not an integer", f)
	}
	// math.MaxInt64 is not representable in float64 and rounds up to 2^63, so
	// comparing against it would let 2^63 pass and make int64(f) overflow.
	// Compare against 2^63 exclusively instead. math.MinInt64 (-2^63) is exact.
	if f < math.MinInt64 || f >= math.Exp2(63) {
		return 0, fmt.Errorf("float %v out of int64 range", f)
	}
	return int64(f), nil
}

// toFloat64 converts a supported numeric value to float64.
func toFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case int:
		return float64(x), nil
	case int8:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint8:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case json.Number:
		return x.Float64()
	default:
		return 0, fmt.Errorf("expected float64, got %T", v)
	}
}
