//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package searchfilter provides search and filter functionality for trpc-agent-go.
package searchfilter

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// OperatorAnd is the "and" operator.
	OperatorAnd = "and"

	// OperatorOr is the "or" operator.
	OperatorOr = "or"

	// OperatorEqual is the "equal" operator.
	OperatorEqual = "eq"

	// OperatorNotEqual is the "not equal" operator.
	OperatorNotEqual = "ne"

	// OperatorGreaterThan is the "greater than" operator.
	OperatorGreaterThan = "gt"

	// OperatorGreaterThanOrEqual is the "greater than or equal" operator.
	OperatorGreaterThanOrEqual = "gte"

	// OperatorLessThan is the "less than" operator.
	OperatorLessThan = "lt"

	// OperatorLessThanOrEqual is the "less than or equal" operator.
	OperatorLessThanOrEqual = "lte"

	// OperatorIn is the "in" operator.
	OperatorIn = "in"

	// OperatorNotIn is the "not in" operator.
	OperatorNotIn = "not in"

	// OperatorLike is the "contains" operator.
	OperatorLike = "like"

	// OperatorNotLike is the "not contains" operator.
	OperatorNotLike = "not like"

	// OperatorBetween is the "between" operator.
	OperatorBetween = "between"
)

// Converter is an interface for converting universal filter conditions to specific query formats.
type Converter[T any] interface {
	// Convert converts a universal filter condition to a specific query format.
	Convert(condition *UniversalFilterCondition) (T, error)
}

// UniversalFilterCondition represents a single condition for a search filter.
type UniversalFilterCondition struct {
	// Field is the metadata field to filter on.
	// Required for comparison operators, not used for logical operators (and/or).
	Field string `json:"field,omitempty" jsonschema:"description=The metadata field to filter on (required for comparison operators)"`

	// Operator is the comparison or logical operator.
	// Comparison operators: eq, ne, gt, gte, lt, lte, in, not in, like, not like, between
	// Logical operators: and, or
	Operator string `json:"operator" jsonschema:"description=The operator to use,enum=eq,enum=ne,enum=gt,enum=gte,enum=lt,enum=lte,enum=in,enum=not in,enum=like,enum=not like,enum=between,enum=and,enum=or"`

	// Value is the value to compare against or sub-conditions for logical operators.
	// For comparison operators: single value, array for "in"/"not in"/"between"
	// For logical operators (and/or): array of UniversalFilterCondition objects
	Value any `json:"value,omitempty" jsonschema:"description=The value to compare against (for comparison operators) or array of sub-conditions (for logical operators and/or)"`
}

// UnmarshalJSON implements custom JSON unmarshaling for UniversalFilterCondition.
// This handles the recursive structure where Value can be []*UniversalFilterCondition for logical operators.
func (c *UniversalFilterCondition) UnmarshalJSON(data []byte) error {
	// Use an auxiliary struct to avoid infinite recursion
	type Alias struct {
		Field    string `json:"field,omitempty"`
		Operator string `json:"operator"`
		Value    any    `json:"value,omitempty"`
	}

	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	c.Field = aux.Field
	c.Operator = strings.ToLower(aux.Operator)

	// Handle logical operators (and/or) - Value should be []*UniversalFilterCondition
	if c.Operator == OperatorAnd || c.Operator == OperatorOr {
		// Value can be an array of conditions
		valueSlice, ok := aux.Value.([]any)
		if !ok {
			return fmt.Errorf("logical operator %s requires an array of conditions", c.Operator)
		}

		conditions := make([]*UniversalFilterCondition, 0, len(valueSlice))
		for i, v := range valueSlice {
			// Re-marshal and unmarshal to convert map[string]any to UniversalFilterCondition
			condBytes, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("failed to marshal condition at index %d: %w", i, err)
			}

			var cond UniversalFilterCondition
			if err := json.Unmarshal(condBytes, &cond); err != nil {
				return fmt.Errorf("failed to unmarshal condition at index %d: %w", i, err)
			}
			conditions = append(conditions, &cond)
		}
		c.Value = conditions
	} else {
		// For comparison operators, keep the value as-is
		c.Value = aux.Value
	}

	return nil
}

// MarshalJSON implements custom JSON marshaling for UniversalFilterCondition.
func (c *UniversalFilterCondition) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Field    string `json:"field,omitempty"`
		Operator string `json:"operator"`
		Value    any    `json:"value,omitempty"`
	}

	return json.Marshal(&Alias{
		Field:    c.Field,
		Operator: c.Operator,
		Value:    c.Value,
	})
}
