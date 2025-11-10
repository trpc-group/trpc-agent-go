//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package searchfilter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniversalFilterCondition_JSON(t *testing.T) {
	t.Run("simple equality filter", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Field:    "category",
			Operator: OperatorEqual,
			Value:    "documentation",
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, original.Field, decoded.Field)
		assert.Equal(t, original.Operator, decoded.Operator)
		assert.Equal(t, original.Value, decoded.Value)
	})

	t.Run("AND filter with conditions", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Operator: OperatorAnd,
			Value: []*UniversalFilterCondition{
				{Field: "category", Operator: OperatorEqual, Value: "doc"},
				{Field: "topic", Operator: OperatorEqual, Value: "programming"},
			},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, OperatorAnd, decoded.Operator)
		conditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, conditions, 2)
		assert.Equal(t, "category", conditions[0].Field)
		assert.Equal(t, "doc", conditions[0].Value)
		assert.Equal(t, "topic", conditions[1].Field)
		assert.Equal(t, "programming", conditions[1].Value)
	})

	t.Run("OR filter with conditions", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Operator: OperatorOr,
			Value: []*UniversalFilterCondition{
				{Field: "type", Operator: OperatorEqual, Value: "golang"},
				{Field: "type", Operator: OperatorEqual, Value: "llm"},
			},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, OperatorOr, decoded.Operator)
		conditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, conditions, 2)
		assert.Equal(t, "type", conditions[0].Field)
		assert.Equal(t, "golang", conditions[0].Value)
		assert.Equal(t, "type", conditions[1].Field)
		assert.Equal(t, "llm", conditions[1].Value)
	})

	t.Run("nested filter", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Operator: OperatorAnd,
			Value: []*UniversalFilterCondition{
				{Field: "category", Operator: OperatorEqual, Value: "doc"},
				{
					Operator: OperatorOr,
					Value: []*UniversalFilterCondition{
						{Field: "topic", Operator: OperatorEqual, Value: "programming"},
						{Field: "topic", Operator: OperatorEqual, Value: "ml"},
					},
				},
			},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, OperatorAnd, decoded.Operator)
		conditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, conditions, 2)

		// First condition
		assert.Equal(t, "category", conditions[0].Field)
		assert.Equal(t, "doc", conditions[0].Value)

		// Second condition (nested OR)
		assert.Equal(t, OperatorOr, conditions[1].Operator)
		orConditions := conditions[1].Value.([]*UniversalFilterCondition)
		require.Len(t, orConditions, 2)
		assert.Equal(t, "topic", orConditions[0].Field)
		assert.Equal(t, "programming", orConditions[0].Value)
		assert.Equal(t, "topic", orConditions[1].Field)
		assert.Equal(t, "ml", orConditions[1].Value)
	})

	t.Run("IN operator", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Field:    "type",
			Operator: OperatorIn,
			Value:    []any{"golang", "llm", "wiki"},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "type", decoded.Field)
		assert.Equal(t, OperatorIn, decoded.Operator)

		// JSON unmarshaling converts []any to []interface{}
		values := decoded.Value.([]any)
		require.Len(t, values, 3)
		assert.Equal(t, "golang", values[0])
		assert.Equal(t, "llm", values[1])
		assert.Equal(t, "wiki", values[2])
	})

	t.Run("BETWEEN operator", func(t *testing.T) {
		original := &UniversalFilterCondition{
			Field:    "score",
			Operator: OperatorBetween,
			Value:    []any{0.5, 0.9},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "score", decoded.Field)
		assert.Equal(t, OperatorBetween, decoded.Operator)

		values := decoded.Value.([]any)
		require.Len(t, values, 2)
		assert.Equal(t, 0.5, values[0])
		assert.Equal(t, 0.9, values[1])
	})

	t.Run("operator normalization", func(t *testing.T) {
		jsonStr := `{"field": "category", "operator": "EQ", "value": "doc"}`

		var decoded UniversalFilterCondition
		err := json.Unmarshal([]byte(jsonStr), &decoded)
		require.NoError(t, err)

		assert.Equal(t, "eq", decoded.Operator) // Should be normalized to lowercase
	})

	t.Run("unmarshal from raw JSON", func(t *testing.T) {
		jsonStr := `{
			"operator": "and",
			"value": [
				{"field": "category", "operator": "eq", "value": "doc"},
				{
					"operator": "or",
					"value": [
						{"field": "topic", "operator": "eq", "value": "programming"},
						{"field": "topic", "operator": "eq", "value": "ml"}
					]
				}
			]
		}`

		var decoded UniversalFilterCondition
		err := json.Unmarshal([]byte(jsonStr), &decoded)
		require.NoError(t, err)

		assert.Equal(t, OperatorAnd, decoded.Operator)
		conditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, conditions, 2)
		assert.Equal(t, "category", conditions[0].Field)
		assert.Equal(t, OperatorOr, conditions[1].Operator)
	})

	t.Run("complex nested filter - simulating LLM output", func(t *testing.T) {
		// Simulate what an LLM might generate for:
		// "Find documents where (category=doc AND level=advanced) OR (category=tutorial AND level=beginner)"
		jsonStr := `{
			"operator": "or",
			"value": [
				{
					"operator": "and",
					"value": [
						{"field": "category", "operator": "eq", "value": "doc"},
						{"field": "level", "operator": "eq", "value": "advanced"}
					]
				},
				{
					"operator": "and",
					"value": [
						{"field": "category", "operator": "eq", "value": "tutorial"},
						{"field": "level", "operator": "eq", "value": "beginner"}
					]
				}
			]
		}`

		var decoded UniversalFilterCondition
		err := json.Unmarshal([]byte(jsonStr), &decoded)
		require.NoError(t, err)

		// Verify top-level OR
		assert.Equal(t, OperatorOr, decoded.Operator)
		orConditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, orConditions, 2)

		// Verify first AND branch
		assert.Equal(t, OperatorAnd, orConditions[0].Operator)
		firstAnd := orConditions[0].Value.([]*UniversalFilterCondition)
		require.Len(t, firstAnd, 2)
		assert.Equal(t, "category", firstAnd[0].Field)
		assert.Equal(t, "doc", firstAnd[0].Value)
		assert.Equal(t, "level", firstAnd[1].Field)
		assert.Equal(t, "advanced", firstAnd[1].Value)

		// Verify second AND branch
		assert.Equal(t, OperatorAnd, orConditions[1].Operator)
		secondAnd := orConditions[1].Value.([]*UniversalFilterCondition)
		require.Len(t, secondAnd, 2)
		assert.Equal(t, "category", secondAnd[0].Field)
		assert.Equal(t, "tutorial", secondAnd[0].Value)
		assert.Equal(t, "level", secondAnd[1].Field)
		assert.Equal(t, "beginner", secondAnd[1].Value)
	})

	t.Run("round-trip complex nested filter", func(t *testing.T) {
		// Create a complex filter programmatically
		original := &UniversalFilterCondition{
			Operator: OperatorOr,
			Value: []*UniversalFilterCondition{
				{
					Operator: OperatorAnd,
					Value: []*UniversalFilterCondition{
						{Field: "category", Operator: OperatorEqual, Value: "doc"},
						{Field: "level", Operator: OperatorEqual, Value: "advanced"},
					},
				},
				{
					Operator: OperatorAnd,
					Value: []*UniversalFilterCondition{
						{Field: "category", Operator: OperatorEqual, Value: "tutorial"},
						{Field: "level", Operator: OperatorEqual, Value: "beginner"},
					},
				},
			},
		}

		// Marshal to JSON
		data, err := json.Marshal(original)
		require.NoError(t, err)

		// Unmarshal back
		var decoded UniversalFilterCondition
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		// Verify structure is preserved
		assert.Equal(t, OperatorOr, decoded.Operator)
		orConditions := decoded.Value.([]*UniversalFilterCondition)
		require.Len(t, orConditions, 2)

		// Verify first branch
		firstAnd := orConditions[0].Value.([]*UniversalFilterCondition)
		assert.Equal(t, "category", firstAnd[0].Field)
		assert.Equal(t, "doc", firstAnd[0].Value)

		// Verify second branch
		secondAnd := orConditions[1].Value.([]*UniversalFilterCondition)
		assert.Equal(t, "category", secondAnd[0].Field)
		assert.Equal(t, "tutorial", secondAnd[0].Value)
	})
}
