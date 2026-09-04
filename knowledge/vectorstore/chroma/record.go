//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

// docToRecord converts a Document and embedding into a Chroma RecordBatch.
func (vs *VectorStore) docToRecord(doc *document.Document, embedding []float64, now int64) (storage.RecordBatch, error) {
	if doc == nil {
		return storage.RecordBatch{}, errDocumentRequired
	}
	if doc.ID == "" {
		return storage.RecordBatch{}, errDocumentIDRequired
	}
	md := map[string]any{}
	if doc.Name != "" {
		md[metaName] = doc.Name
	}
	created := now
	if !doc.CreatedAt.IsZero() {
		created = doc.CreatedAt.Unix()
	}
	md[metaCreatedAt] = created
	md[metaUpdatedAt] = now

	nested := map[string]any{}
	for k, v := range doc.Metadata {
		if isReservedKey(k) || vs.opts.sparseSearch && k == vs.opts.sparseSearchKey {
			return storage.RecordBatch{}, fmt.Errorf(
				"chroma: metadata key %q conflicts with a reserved document field",
				k,
			)
		}
		if _, ok := v.([]byte); ok {
			return storage.RecordBatch{}, fmt.Errorf("chroma: metadata value for %q must not be []byte", k)
		}
		if isScalar(v) {
			md[k] = v
			continue
		}
		nested[k] = v
	}
	if len(nested) > 0 {
		raw, err := json.Marshal(nested)
		if err != nil {
			return storage.RecordBatch{}, fmt.Errorf("chroma: marshal nested metadata: %w", err)
		}
		md[metaJSON] = string(raw)
	}

	rec := storage.RecordBatch{
		IDs:       []string{doc.ID},
		Documents: []string{doc.Content},
		Metadatas: []map[string]any{md},
	}
	if len(embedding) > 0 {
		rec.Embeddings = [][]float32{toFloat32(embedding)}
	}
	return rec, nil
}

// recordToDoc converts the i-th record in a GetResult back into a Document and embedding.
func (vs *VectorStore) recordToDoc(res *storage.GetResult, i int) (*document.Document, []float64, error) {
	if res == nil || i < 0 || i >= len(res.IDs) {
		return nil, nil, fmt.Errorf("%w: %d", errRecordIndexOutOfRange, i)
	}
	doc := &document.Document{ID: res.IDs[i]}
	if i < len(res.Documents) {
		doc.Content = res.Documents[i]
	}
	if i < len(res.Metadatas) && res.Metadatas[i] != nil {
		if err := applyRecordMetadata(doc, res.Metadatas[i]); err != nil {
			return nil, nil, err
		}
	}
	var emb []float64
	if i < len(res.Embeddings) {
		emb = toFloat64(res.Embeddings[i])
	}
	return doc, emb, nil
}

// applyRecordMetadata populates Document fields from a Chroma metadata map.
func applyRecordMetadata(doc *document.Document, src map[string]any) error {
	if name, ok := asString(src[metaName]); ok {
		doc.Name = name
	}
	if ts, ok := asUnix(src[metaCreatedAt]); ok {
		doc.CreatedAt = time.Unix(ts, 0)
	}
	if ts, ok := asUnix(src[metaUpdatedAt]); ok {
		doc.UpdatedAt = time.Unix(ts, 0)
	}
	md := map[string]any{}
	if err := mergeNestedMetadata(md, src[metaJSON]); err != nil {
		return err
	}
	for k, v := range src {
		if isReservedKey(k) || v == nil || isSparseVectorValue(v) {
			continue
		}
		md[k] = v
	}
	if len(md) > 0 {
		doc.Metadata = md
	}
	return nil
}

// mergeNestedMetadata unmarshals the _json metadata key and merges its entries into md.
func mergeNestedMetadata(md map[string]any, rawJSON any) error {
	if rawJSON == nil {
		return nil
	}
	raw, ok := asString(rawJSON)
	if !ok {
		return fmt.Errorf("chroma: nested metadata %q must be a JSON string", metaJSON)
	}
	if raw == "" {
		return nil
	}
	var nested map[string]any
	if err := json.Unmarshal([]byte(raw), &nested); err != nil {
		return fmt.Errorf("chroma: decode nested metadata: %w", err)
	}
	for k, v := range nested {
		md[k] = v
	}
	return nil
}

// isReservedKey reports whether k is an internal metadata key.
func isReservedKey(k string) bool {
	for _, key := range reservedMetadataKeys {
		if k == key {
			return true
		}
	}
	return false
}

// isScalar reports whether v is a Chroma-native scalar or a homogeneous scalar slice.
func isScalar(v any) bool {
	switch v.(type) {
	case nil:
		return false
	case string, bool, json.Number,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array || rv.Len() == 0 {
		return false
	}
	var kind reflect.Kind
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		for item.Kind() == reflect.Interface {
			if item.IsNil() {
				return false
			}
			item = item.Elem()
		}
		switch item.Kind() {
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
		default:
			return false
		}
		if i == 0 {
			kind = scalarKind(item.Kind())
		} else if scalarKind(item.Kind()) != kind {
			return false
		}
	}
	return true
}

// asString extracts a string from v, returning false for non-string types.
func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	default:
		return "", false
	}
}

// asUnix extracts a Unix timestamp from v, accepting int, int64, float64,
// json.Number, and numeric strings.
func asUnix(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// toFloat32 narrows a float64 slice to float32 for Chroma wire format.
func toFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// toFloat64 widens a float32 slice from Chroma wire format to float64.
func toFloat64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

// validateEmbedding checks dimension, finiteness, and non-zero norm.
func validateEmbedding(embedding []float64, dim int, required bool) error {
	if len(embedding) == 0 && !required {
		return nil
	}
	if len(embedding) != dim {
		return fmt.Errorf("%w: want=%d got=%d", errVectorDimMismatch, dim, len(embedding))
	}
	var wireNorm float64
	for i, v := range embedding {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > math.MaxFloat32 {
			return fmt.Errorf("chroma: embedding value %d is not a finite float32", i)
		}
		wireValue := float32(v)
		wireNorm += float64(wireValue) * float64(wireValue)
	}
	if wireNorm == 0 {
		return fmt.Errorf("chroma: cosine embedding must not be a zero vector")
	}
	return nil
}
