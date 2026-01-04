//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func TestIdToUUID(t *testing.T) {
	t.Parallel()
	t.Run("already valid uuid", func(t *testing.T) {
		validUUID := "550e8400-e29b-41d4-a716-446655440000"
		result := idToUUID(validUUID)
		assert.Equal(t, validUUID, result)
	})

	t.Run("non-uuid id gets converted", func(t *testing.T) {
		id := "my-document-id"
		result := idToUUID(id)
		// Should be a valid UUID format
		assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, result)
		// Should be deterministic
		assert.Equal(t, result, idToUUID(id))
	})

	t.Run("different ids produce different uuids", func(t *testing.T) {
		id1 := "document-1"
		id2 := "document-2"
		assert.NotEqual(t, idToUUID(id1), idToUUID(id2))
	})

	t.Run("handles special characters", func(t *testing.T) {
		id := "llm_20251222225235_2"
		result := idToUUID(id)
		assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, result)
	})
}

func TestToFloat32Slice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    []float64
		expected []float32
	}{
		{
			name:     "empty slice",
			input:    []float64{},
			expected: []float32{},
		},
		{
			name:     "single element",
			input:    []float64{1.5},
			expected: []float32{1.5},
		},
		{
			name:     "multiple elements",
			input:    []float64{1.0, 2.5, 3.14},
			expected: []float32{1.0, 2.5, 3.14},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toFloat32Slice(tt.input)
			assert.Equal(t, len(tt.expected), len(result))
			for i := range result {
				assert.InDelta(t, tt.expected[i], result[i], 0.001)
			}
		})
	}
}

func TestPtrHelpers(t *testing.T) {
	t.Parallel()
	t.Run("ptrBool", func(t *testing.T) {
		truePtr := qdrant.PtrOf(true)
		falsePtr := qdrant.PtrOf(false)
		assert.True(t, *truePtr)
		assert.False(t, *falsePtr)
	})

	t.Run("ptrUint32", func(t *testing.T) {
		ptr := qdrant.PtrOf(uint32(42))
		assert.Equal(t, uint32(42), *ptr)
	})

	t.Run("ptrUint64", func(t *testing.T) {
		ptr := qdrant.PtrOf(uint64(123456789))
		assert.Equal(t, uint64(123456789), *ptr)
	})
}

func TestPointIDToStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    *qdrant.PointId
		expected string
	}{
		{
			name:     "nil",
			input:    nil,
			expected: "",
		},
		{
			name:     "uuid",
			input:    qdrant.NewID("test-uuid-123"),
			expected: "test-uuid-123",
		},
		{
			name: "numeric",
			input: &qdrant.PointId{
				PointIdOptions: &qdrant.PointId_Num{Num: 42},
			},
			expected: "42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pointIDToStr(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStringsToPointIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []string
	}{
		{
			name:  "empty",
			input: []string{},
		},
		{
			name:  "single",
			input: []string{"id1"},
		},
		{
			name:  "multiple",
			input: []string{"id1", "id2", "id3"},
		},
		{
			name:  "with uuid",
			input: []string{"550e8400-e29b-41d4-a716-446655440000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringsToPointIDs(tt.input)
			assert.Equal(t, len(tt.input), len(result))
			for i, id := range tt.input {
				// The point ID should be the UUID-converted version
				assert.Equal(t, idToUUID(id), pointIDToStr(result[i]))
			}
		})
	}
}

func TestGetPayloadString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		payload  map[string]*qdrant.Value
		key      string
		expected string
	}{
		{
			name:     "nil payload",
			payload:  nil,
			key:      "key",
			expected: "",
		},
		{
			name:     "missing key",
			payload:  map[string]*qdrant.Value{},
			key:      "missing",
			expected: "",
		},
		{
			name: "nil value",
			payload: map[string]*qdrant.Value{
				"key": nil,
			},
			key:      "key",
			expected: "",
		},
		{
			name: "string value",
			payload: map[string]*qdrant.Value{
				"key": {Kind: &qdrant.Value_StringValue{StringValue: "hello"}},
			},
			key:      "key",
			expected: "hello",
		},
		{
			name: "non-string value",
			payload: map[string]*qdrant.Value{
				"key": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 42}},
			},
			key:      "key",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPayloadString(tt.payload, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPayloadInt64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		payload  map[string]*qdrant.Value
		key      string
		expected int64
	}{
		{
			name:     "nil payload",
			payload:  nil,
			key:      "key",
			expected: 0,
		},
		{
			name:     "missing key",
			payload:  map[string]*qdrant.Value{},
			key:      "missing",
			expected: 0,
		},
		{
			name: "integer value",
			payload: map[string]*qdrant.Value{
				"key": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 42}},
			},
			key:      "key",
			expected: 42,
		},
		{
			name: "double value",
			payload: map[string]*qdrant.Value{
				"key": {Kind: &qdrant.Value_DoubleValue{DoubleValue: 3.14}},
			},
			key:      "key",
			expected: 3,
		},
		{
			name: "string value",
			payload: map[string]*qdrant.Value{
				"key": {Kind: &qdrant.Value_StringValue{StringValue: "not a number"}},
			},
			key:      "key",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPayloadInt64(tt.payload, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPayloadMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		payload  map[string]*qdrant.Value
		expected map[string]any
	}{
		{
			name:     "nil payload",
			payload:  nil,
			expected: nil,
		},
		{
			name:     "missing metadata field",
			payload:  map[string]*qdrant.Value{},
			expected: nil,
		},
		{
			name: "nil metadata value",
			payload: map[string]*qdrant.Value{
				fieldMetadata: nil,
			},
			expected: nil,
		},
		{
			name: "struct metadata",
			payload: map[string]*qdrant.Value{
				fieldMetadata: {
					Kind: &qdrant.Value_StructValue{
						StructValue: &qdrant.Struct{
							Fields: map[string]*qdrant.Value{
								"category": {Kind: &qdrant.Value_StringValue{StringValue: "docs"}},
								"version":  {Kind: &qdrant.Value_IntegerValue{IntegerValue: 1}},
							},
						},
					},
				},
			},
			expected: map[string]any{
				"category": "docs",
				"version":  int64(1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPayloadMetadata(tt.payload)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertValueToAny(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    *qdrant.Value
		expected any
	}{
		{
			name:     "nil",
			value:    nil,
			expected: nil,
		},
		{
			name:     "string",
			value:    &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: "hello"}},
			expected: "hello",
		},
		{
			name:     "integer",
			value:    &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: 42}},
			expected: int64(42),
		},
		{
			name:     "double",
			value:    &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: 3.14}},
			expected: 3.14,
		},
		{
			name:     "bool true",
			value:    &qdrant.Value{Kind: &qdrant.Value_BoolValue{BoolValue: true}},
			expected: true,
		},
		{
			name:     "bool false",
			value:    &qdrant.Value{Kind: &qdrant.Value_BoolValue{BoolValue: false}},
			expected: false,
		},
		{
			name:     "null",
			value:    &qdrant.Value{Kind: &qdrant.Value_NullValue{}},
			expected: nil,
		},
		{
			name: "list",
			value: &qdrant.Value{
				Kind: &qdrant.Value_ListValue{
					ListValue: &qdrant.ListValue{
						Values: []*qdrant.Value{
							{Kind: &qdrant.Value_StringValue{StringValue: "a"}},
							{Kind: &qdrant.Value_StringValue{StringValue: "b"}},
						},
					},
				},
			},
			expected: []any{"a", "b"},
		},
		{
			name: "nil list",
			value: &qdrant.Value{
				Kind: &qdrant.Value_ListValue{ListValue: nil},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertValueToAny(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertStructToMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    *qdrant.Struct
		expected map[string]any
	}{
		{
			name:     "nil struct",
			input:    nil,
			expected: nil,
		},
		{
			name:     "nil fields",
			input:    &qdrant.Struct{Fields: nil},
			expected: nil,
		},
		{
			name: "empty fields",
			input: &qdrant.Struct{
				Fields: map[string]*qdrant.Value{},
			},
			expected: map[string]any{},
		},
		{
			name: "mixed fields",
			input: &qdrant.Struct{
				Fields: map[string]*qdrant.Value{
					"str":  {Kind: &qdrant.Value_StringValue{StringValue: "value"}},
					"num":  {Kind: &qdrant.Value_IntegerValue{IntegerValue: 123}},
					"bool": {Kind: &qdrant.Value_BoolValue{BoolValue: true}},
				},
			},
			expected: map[string]any{
				"str":  "value",
				"num":  int64(123),
				"bool": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStructToMap(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractVectorData(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		vector   *qdrant.VectorOutput
		expected []float64
	}{
		{
			name:     "nil vector",
			vector:   nil,
			expected: nil,
		},
		{
			name: "valid dense vector",
			vector: &qdrant.VectorOutput{
				Vector: &qdrant.VectorOutput_Dense{
					Dense: &qdrant.DenseVector{Data: []float32{1.0, 2.0, 3.0}},
				},
			},
			expected: []float64{1.0, 2.0, 3.0},
		},
		{
			name: "nil dense data",
			vector: &qdrant.VectorOutput{
				Vector: &qdrant.VectorOutput_Dense{
					Dense: nil,
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractVectorData(tt.vector)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.Equal(t, len(tt.expected), len(result))
				for i := range result {
					assert.InDelta(t, tt.expected[i], result[i], 0.001)
				}
			}
		})
	}
}

func TestMetadataToCondition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		filter         map[string]any
		expectNil      bool
		expectedOp     string
		expectedFields int
	}{
		{
			name:      "nil filter",
			filter:    nil,
			expectNil: true,
		},
		{
			name:      "empty filter",
			filter:    map[string]any{},
			expectNil: true,
		},
		{
			name: "single field",
			filter: map[string]any{
				"category": "docs",
			},
			expectNil:  false,
			expectedOp: searchfilter.OperatorEqual,
		},
		{
			name: "multiple fields",
			filter: map[string]any{
				"category": "docs",
				"version":  1,
			},
			expectNil:      false,
			expectedOp:     searchfilter.OperatorAnd,
			expectedFields: 2,
		},
		{
			name: "with metadata prefix",
			filter: map[string]any{
				"metadata.category": "docs",
			},
			expectNil:  false,
			expectedOp: searchfilter.OperatorEqual,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := metadataToCondition(tt.filter)
			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expectedOp, result.Operator)
				if tt.expectedFields > 0 {
					conditions := result.Value.([]*searchfilter.UniversalFilterCondition)
					assert.Equal(t, tt.expectedFields, len(conditions))
				}
			}
		})
	}
}

func TestSanitizeMetadata(t *testing.T) {
	t.Parallel()
	t.Run("nil metadata", func(t *testing.T) {
		result := sanitizeMetadata(nil)
		assert.Nil(t, result)
	})

	t.Run("empty metadata", func(t *testing.T) {
		result := sanitizeMetadata(map[string]any{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("converts time.Time to unix timestamp", func(t *testing.T) {
		ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		metadata := map[string]any{
			"created": ts,
			"name":    "test",
		}

		result := sanitizeMetadata(metadata)

		assert.Equal(t, ts.Unix(), result["created"])
		assert.Equal(t, "test", result["name"])
	})

	t.Run("converts *time.Time to unix timestamp", func(t *testing.T) {
		ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		metadata := map[string]any{
			"created": &ts,
		}

		result := sanitizeMetadata(metadata)

		assert.Equal(t, ts.Unix(), result["created"])
	})

	t.Run("handles nil *time.Time", func(t *testing.T) {
		var nilTime *time.Time
		metadata := map[string]any{
			"created": nilTime,
		}

		result := sanitizeMetadata(metadata)

		assert.Nil(t, result["created"])
	})

	t.Run("recursively sanitizes nested maps", func(t *testing.T) {
		ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		metadata := map[string]any{
			"nested": map[string]any{
				"timestamp": ts,
				"value":     42,
			},
		}

		result := sanitizeMetadata(metadata)

		nested := result["nested"].(map[string]any)
		assert.Equal(t, ts.Unix(), nested["timestamp"])
		assert.Equal(t, 42, nested["value"])
	})

	t.Run("sanitizes arrays with time.Time", func(t *testing.T) {
		ts1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		metadata := map[string]any{
			"timestamps": []any{ts1, ts2},
		}

		result := sanitizeMetadata(metadata)

		timestamps := result["timestamps"].([]any)
		assert.Equal(t, ts1.Unix(), timestamps[0])
		assert.Equal(t, ts2.Unix(), timestamps[1])
	})

	t.Run("preserves other types", func(t *testing.T) {
		metadata := map[string]any{
			"string":  "hello",
			"int":     42,
			"float":   3.14,
			"bool":    true,
			"strings": []any{"a", "b", "c"},
		}

		result := sanitizeMetadata(metadata)

		assert.Equal(t, "hello", result["string"])
		assert.Equal(t, 42, result["int"])
		assert.Equal(t, 3.14, result["float"])
		assert.Equal(t, true, result["bool"])
		assert.Equal(t, []any{"a", "b", "c"}, result["strings"])
	})
}

func TestToPoint(t *testing.T) {
	t.Parallel()
	t.Run("basic document", func(t *testing.T) {
		doc := &document.Document{
			ID:       "test-id",
			Name:     "test-name",
			Content:  "test-content",
			Metadata: map[string]any{"key": "value"},
		}
		emb := []float64{1.0, 2.0, 3.0}

		point := toPoint(doc, emb)

		require.NotNil(t, point)
		// Point ID is the UUID-converted version
		assert.Equal(t, idToUUID("test-id"), pointIDToStr(point.Id))
		// Original ID is stored in payload
		assert.Equal(t, "test-id", getPayloadString(point.Payload, fieldID))
		assert.NotNil(t, point.Vectors)
		assert.NotNil(t, point.Payload)
	})

	t.Run("document with timestamps", func(t *testing.T) {
		createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		updatedAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

		doc := &document.Document{
			ID:        "test-id",
			Name:      "test-name",
			Content:   "test-content",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		emb := []float64{1.0, 2.0}

		point := toPoint(doc, emb)

		require.NotNil(t, point)
		assert.Equal(t, createdAt.Unix(), getPayloadInt64(point.Payload, fieldCreatedAt))
		assert.Equal(t, updatedAt.Unix(), getPayloadInt64(point.Payload, fieldUpdatedAt))
	})

	t.Run("empty embedding", func(t *testing.T) {
		doc := &document.Document{
			ID:      "test-id",
			Name:    "test-name",
			Content: "test-content",
		}

		point := toPoint(doc, []float64{})

		require.NotNil(t, point)
		assert.NotNil(t, point.Vectors)
	})

	t.Run("metadata with time.Time values", func(t *testing.T) {
		ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		doc := &document.Document{
			ID:      "test-id",
			Name:    "test-name",
			Content: "test-content",
			Metadata: map[string]any{
				"modified_at": ts,
				"category":    "docs",
			},
		}
		emb := []float64{1.0, 2.0}

		// This should not panic - time.Time is converted to Unix timestamp
		point := toPoint(doc, emb)

		require.NotNil(t, point)
		assert.NotNil(t, point.Payload)
	})
}

func TestPayloadToDocument(t *testing.T) {
	t.Parallel()
	t.Run("complete payload", func(t *testing.T) {
		createdAt := int64(1704067200)
		updatedAt := int64(1717200000)

		payload := map[string]*qdrant.Value{
			fieldID:        {Kind: &qdrant.Value_StringValue{StringValue: "original-id"}},
			fieldName:      {Kind: &qdrant.Value_StringValue{StringValue: "test-name"}},
			fieldContent:   {Kind: &qdrant.Value_StringValue{StringValue: "test-content"}},
			fieldCreatedAt: {Kind: &qdrant.Value_IntegerValue{IntegerValue: createdAt}},
			fieldUpdatedAt: {Kind: &qdrant.Value_IntegerValue{IntegerValue: updatedAt}},
			fieldMetadata: {
				Kind: &qdrant.Value_StructValue{
					StructValue: &qdrant.Struct{
						Fields: map[string]*qdrant.Value{
							"category": {Kind: &qdrant.Value_StringValue{StringValue: "docs"}},
						},
					},
				},
			},
		}

		doc := payloadToDocument(qdrant.NewID("point-id"), payload)

		require.NotNil(t, doc)
		assert.Equal(t, "original-id", doc.ID) // Uses original ID from payload
		assert.Equal(t, "test-name", doc.Name)
		assert.Equal(t, "test-content", doc.Content)
		assert.Equal(t, time.Unix(createdAt, 0), doc.CreatedAt)
		assert.Equal(t, time.Unix(updatedAt, 0), doc.UpdatedAt)
		assert.Equal(t, "docs", doc.Metadata["category"])
	})

	t.Run("minimal payload uses point ID", func(t *testing.T) {
		payload := map[string]*qdrant.Value{}

		doc := payloadToDocument(qdrant.NewID("point-id"), payload)

		require.NotNil(t, doc)
		assert.Equal(t, "point-id", doc.ID) // Falls back to point ID
		assert.Empty(t, doc.Name)
		assert.Empty(t, doc.Content)
	})
}

func TestToSearchResult(t *testing.T) {
	t.Parallel()
	t.Run("empty results", func(t *testing.T) {
		results := []*qdrant.ScoredPoint{}

		searchResult := toSearchResult(results)

		require.NotNil(t, searchResult)
		assert.Empty(t, searchResult.Results)
	})

	t.Run("single result", func(t *testing.T) {
		results := []*qdrant.ScoredPoint{
			{
				Id: qdrant.NewID("id-1"),
				Payload: map[string]*qdrant.Value{
					fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: "name-1"}},
					fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: "content-1"}},
				},
				Score: 0.95,
			},
		}

		searchResult := toSearchResult(results)

		require.NotNil(t, searchResult)
		require.Len(t, searchResult.Results, 1)
		assert.Equal(t, "id-1", searchResult.Results[0].Document.ID)
		assert.Equal(t, "name-1", searchResult.Results[0].Document.Name)
		assert.InDelta(t, 0.95, searchResult.Results[0].Score, 0.001)
	})

	t.Run("multiple results", func(t *testing.T) {
		results := []*qdrant.ScoredPoint{
			{
				Id:      qdrant.NewID("id-1"),
				Payload: map[string]*qdrant.Value{},
				Score:   0.95,
			},
			{
				Id:      qdrant.NewID("id-2"),
				Payload: map[string]*qdrant.Value{},
				Score:   0.85,
			},
			{
				Id:      qdrant.NewID("id-3"),
				Payload: map[string]*qdrant.Value{},
				Score:   0.75,
			},
		}

		searchResult := toSearchResult(results)

		require.NotNil(t, searchResult)
		require.Len(t, searchResult.Results, 3)
		assert.Equal(t, "id-1", searchResult.Results[0].Document.ID)
		assert.Equal(t, "id-2", searchResult.Results[1].Document.ID)
		assert.Equal(t, "id-3", searchResult.Results[2].Document.ID)
		assert.InDelta(t, 0.95, searchResult.Results[0].Score, 0.001)
		assert.InDelta(t, 0.85, searchResult.Results[1].Score, 0.001)
		assert.InDelta(t, 0.75, searchResult.Results[2].Score, 0.001)
	})
}

func TestPointIDToStr_UnknownType(t *testing.T) {
	t.Parallel()
	// Test with an empty PointId (no type set)
	pt := &qdrant.PointId{}
	result := pointIDToStr(pt)
	assert.Equal(t, "", result)
}

func TestExtractPayloadMetadata_NonStruct(t *testing.T) {
	t.Parallel()
	// Test when metadata field exists but is not a struct
	payload := map[string]*qdrant.Value{
		fieldMetadata: {Kind: &qdrant.Value_StringValue{StringValue: "not a struct"}},
	}

	result := extractPayloadMetadata(payload)
	assert.Nil(t, result)
}

func TestConvertValueToAny_NestedStruct(t *testing.T) {
	t.Parallel()
	value := &qdrant.Value{
		Kind: &qdrant.Value_StructValue{
			StructValue: &qdrant.Struct{
				Fields: map[string]*qdrant.Value{
					"nested": {Kind: &qdrant.Value_StringValue{StringValue: "value"}},
				},
			},
		},
	}

	result := convertValueToAny(value)
	require.NotNil(t, result)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value", m["nested"])
}

func TestPayloadToDocument_WithOriginalID(t *testing.T) {
	t.Parallel()
	// Test that original_id from payload is used instead of point ID
	payload := map[string]*qdrant.Value{
		fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: "original-doc-id"}},
		fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: "doc-name"}},
		fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: "doc-content"}},
	}

	doc := payloadToDocument(qdrant.NewID("uuid-point-id"), payload)

	assert.Equal(t, "original-doc-id", doc.ID)
	assert.Equal(t, "doc-name", doc.Name)
}

func TestSanitizeValue_Nil(t *testing.T) {
	t.Parallel()
	result := sanitizeValue(nil)
	assert.Nil(t, result)
}

func TestSanitizeValue_Time(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	result := sanitizeValue(ts)
	assert.Equal(t, ts.Unix(), result)
}

func TestSanitizeValue_TimePointerNil(t *testing.T) {
	t.Parallel()
	var ts *time.Time
	result := sanitizeValue(ts)
	assert.Nil(t, result)
}

func TestSanitizeValue_TimePointer(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	result := sanitizeValue(&ts)
	assert.Equal(t, ts.Unix(), result)
}

func TestSanitizeValue_NestedMap(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	input := map[string]any{
		"timestamp": ts,
		"value":     42,
	}
	result := sanitizeValue(input)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ts.Unix(), m["timestamp"])
	assert.Equal(t, 42, m["value"])
}

func TestSanitizeValue_Array(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	input := []any{ts, "string", 42}
	result := sanitizeValue(input)
	arr, ok := result.([]any)
	require.True(t, ok)
	assert.Equal(t, ts.Unix(), arr[0])
	assert.Equal(t, "string", arr[1])
	assert.Equal(t, 42, arr[2])
}

func TestSanitizeValue_OtherType(t *testing.T) {
	t.Parallel()
	// Other types should be returned as-is
	result := sanitizeValue(42)
	assert.Equal(t, 42, result)

	result = sanitizeValue("hello")
	assert.Equal(t, "hello", result)

	result = sanitizeValue(true)
	assert.Equal(t, true, result)
}

func TestConvertValueToAny_NilKind(t *testing.T) {
	t.Parallel()
	// Value with nil Kind
	value := &qdrant.Value{Kind: nil}
	result := convertValueToAny(value)
	assert.Nil(t, result)
}

func TestExtractVectorData_NonDenseType(t *testing.T) {
	t.Parallel()
	// Test with empty vector output (no data, no dense vector)
	vector := &qdrant.VectorOutput{}
	result := extractVectorData(vector)
	assert.Nil(t, result)
}

func TestPayloadToDocument_NoOriginalID(t *testing.T) {
	t.Parallel()
	// When no original ID is present, use point ID
	payload := map[string]*qdrant.Value{
		fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: "doc-name"}},
		fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: "doc-content"}},
	}

	doc := payloadToDocument(qdrant.NewID("uuid-point-id"), payload)

	assert.Equal(t, "uuid-point-id", doc.ID)
}
