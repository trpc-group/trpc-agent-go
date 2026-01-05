//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// helpers.go contains conversion utilities between Qdrant types and domain types.
package qdrant

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// qdrantNamespace is a randomly generated UUID namespace for deterministic UUID v5 generation
// from document IDs. Using a dedicated namespace prevents collisions with other systems.
var qdrantNamespace = uuid.MustParse("a3f2b8c1-7d4e-4f5a-9b6c-8e1d2f3a4b5c")

// idToUUID converts a document ID to a UUID.
// If the ID is already a valid UUID, it returns it as-is.
// Otherwise, it generates a deterministic UUID v5 from the ID.
func idToUUID(id string) string {
	if _, err := uuid.Parse(id); err == nil {
		return id
	}
	return uuid.NewSHA1(qdrantNamespace, []byte(id)).String()
}

// toFloat32Slice converts a float64 slice to float32 for Qdrant vector storage.
func toFloat32Slice(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// pointIDToStr converts a Qdrant PointId to its string representation.
func pointIDToStr(id *qdrant.PointId) string {
	if id == nil {
		return ""
	}
	switch v := id.PointIdOptions.(type) {
	case *qdrant.PointId_Uuid:
		return v.Uuid
	case *qdrant.PointId_Num:
		return strconv.FormatUint(v.Num, 10)
	default:
		return ""
	}
}

// stringsToPointIDs converts a slice of string IDs to Qdrant PointId pointers.
func stringsToPointIDs(ids []string) []*qdrant.PointId {
	result := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		result[i] = qdrant.NewID(idToUUID(id))
	}
	return result
}

// getPayloadString extracts a string value from a Qdrant payload by key.
func getPayloadString(payload map[string]*qdrant.Value, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	if sv, ok := v.Kind.(*qdrant.Value_StringValue); ok {
		return sv.StringValue
	}
	return ""
}

// getPayloadInt64 extracts an int64 value from a Qdrant payload by key.
func getPayloadInt64(payload map[string]*qdrant.Value, key string) int64 {
	if payload == nil {
		return 0
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return 0
	}
	if iv, ok := v.Kind.(*qdrant.Value_IntegerValue); ok {
		return iv.IntegerValue
	}
	if dv, ok := v.Kind.(*qdrant.Value_DoubleValue); ok {
		return int64(dv.DoubleValue)
	}
	return 0
}

// extractPayloadMetadata extracts the metadata struct from a Qdrant payload.
func extractPayloadMetadata(payload map[string]*qdrant.Value) map[string]any {
	if payload == nil {
		return nil
	}
	v, ok := payload[fieldMetadata]
	if !ok || v == nil {
		return nil
	}
	if sv, ok := v.Kind.(*qdrant.Value_StructValue); ok && sv.StructValue != nil {
		return convertStructToMap(sv.StructValue)
	}
	return nil
}

// convertStructToMap converts a Qdrant Struct to a Go map.
func convertStructToMap(s *qdrant.Struct) map[string]any {
	if s == nil || s.Fields == nil {
		return nil
	}
	result := make(map[string]any, len(s.Fields))
	for k, v := range s.Fields {
		result[k] = convertValueToAny(v)
	}
	return result
}

// convertValueToAny converts a Qdrant Value to its Go native type.
func convertValueToAny(v *qdrant.Value) any {
	if v == nil {
		return nil
	}
	switch k := v.Kind.(type) {
	case *qdrant.Value_StringValue:
		return k.StringValue
	case *qdrant.Value_IntegerValue:
		return k.IntegerValue
	case *qdrant.Value_DoubleValue:
		return k.DoubleValue
	case *qdrant.Value_BoolValue:
		return k.BoolValue
	case *qdrant.Value_StructValue:
		return convertStructToMap(k.StructValue)
	case *qdrant.Value_ListValue:
		if k.ListValue == nil {
			return nil
		}
		list := make([]any, len(k.ListValue.Values))
		for i, lv := range k.ListValue.Values {
			list[i] = convertValueToAny(lv)
		}
		return list
	case *qdrant.Value_NullValue:
		return nil
	default:
		return nil
	}
}

// metadataToCondition converts a metadata filter map to a UniversalFilterCondition.
func metadataToCondition(filter map[string]any) *searchfilter.UniversalFilterCondition {
	if len(filter) == 0 {
		return nil
	}

	// Extract and sort keys for deterministic ordering
	keys := make([]string, 0, len(filter))
	for key := range filter {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	conditions := make([]*searchfilter.UniversalFilterCondition, 0, len(filter))
	for _, key := range keys {
		value := filter[key]
		fieldKey := key
		if !strings.HasPrefix(fieldKey, source.MetadataFieldPrefix) {
			fieldKey = source.MetadataFieldPrefix + key
		}
		conditions = append(conditions, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorEqual,
			Field:    fieldKey,
			Value:    value,
		})
	}

	if len(conditions) == 1 {
		return conditions[0]
	}

	return &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    conditions,
	}
}

// sanitizeMetadata recursively converts time.Time values to Unix timestamps
// since Qdrant's NewValueMap doesn't support time.Time.
func sanitizeMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = sanitizeValue(v)
	}
	return result
}

// sanitizeValue converts a single value, handling time.Time and nested structures.
func sanitizeValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case time.Time:
		return val.Unix()
	case *time.Time:
		if val == nil {
			return nil
		}
		return val.Unix()
	case map[string]any:
		return sanitizeMetadata(val)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = sanitizeValue(item)
		}
		return result
	default:
		return v
	}
}

// toPoint converts a Document and embedding to a Qdrant PointStruct for storage.
// Note: For BM25 mode, the caller should override point.Vectors with named vectors.
func toPoint(doc *document.Document, emb []float64) *qdrant.PointStruct {
	now := time.Now().Unix()
	createdAt := now
	updatedAt := now
	if !doc.CreatedAt.IsZero() {
		createdAt = doc.CreatedAt.Unix()
	}
	if !doc.UpdatedAt.IsZero() {
		updatedAt = doc.UpdatedAt.Unix()
	}

	return &qdrant.PointStruct{
		Id:      qdrant.NewID(idToUUID(doc.ID)),
		Vectors: qdrant.NewVectors(toFloat32Slice(emb)...),
		Payload: qdrant.NewValueMap(map[string]any{
			fieldID:        doc.ID, // Store original ID in payload
			fieldName:      doc.Name,
			fieldContent:   doc.Content,
			fieldMetadata:  sanitizeMetadata(doc.Metadata),
			fieldCreatedAt: createdAt,
			fieldUpdatedAt: updatedAt,
		}),
	}
}

// payloadToDocument extracts a Document from a Qdrant point ID and payload.
func payloadToDocument(id *qdrant.PointId, payload map[string]*qdrant.Value) *document.Document {
	// Try to get original ID from payload, fallback to point ID
	docID := getPayloadString(payload, fieldID)
	if docID == "" {
		docID = pointIDToStr(id)
	}
	return &document.Document{
		ID:        docID,
		Name:      getPayloadString(payload, fieldName),
		Content:   getPayloadString(payload, fieldContent),
		Metadata:  extractPayloadMetadata(payload),
		CreatedAt: time.Unix(getPayloadInt64(payload, fieldCreatedAt), 0),
		UpdatedAt: time.Unix(getPayloadInt64(payload, fieldUpdatedAt), 0),
	}
}

// toSearchResult converts Qdrant ScoredPoints to a SearchResult.
func toSearchResult(results []*qdrant.ScoredPoint) *vectorstore.SearchResult {
	searchResult := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(results)),
	}
	for _, pt := range results {
		searchResult.Results = append(searchResult.Results, &vectorstore.ScoredDocument{
			Document: payloadToDocument(pt.Id, pt.Payload),
			Score:    float64(pt.Score),
		})
	}
	return searchResult
}

// toFilterSearchResult converts Qdrant RetrievedPoints to a SearchResult.
// Used for filter-only searches where there is no similarity score.
func toFilterSearchResult(points []*qdrant.RetrievedPoint) *vectorstore.SearchResult {
	searchResult := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(points)),
	}
	for _, pt := range points {
		searchResult.Results = append(searchResult.Results, &vectorstore.ScoredDocument{
			Document: payloadToDocument(pt.Id, pt.Payload),
			Score:    0, // No similarity score for filter-only search
		})
	}
	return searchResult
}

// extractVectorData extracts float64 data from a VectorOutput.
func extractVectorData(v *qdrant.VectorOutput) []float64 {
	if v == nil {
		return nil
	}
	dense := v.GetDenseVector()
	if dense == nil {
		return nil
	}
	f64 := make([]float64, len(dense.GetData()))
	for i, val := range dense.GetData() {
		f64[i] = float64(val)
	}
	return f64
}
