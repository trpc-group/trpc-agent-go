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

// UniversalFilterCondition represents a single condition for a search filter.
type UniversalFilterCondition struct {
	// Field is the metadata field to filter on.
	Field string

	// Operator is the comparison operator (e.g., "eq", "ne", "gt", "lt", "and", "or").
	Operator string

	// Value is the value to compare against.
	// If the Operator is "and" or "or", Value should be a slice of UniversalFilterCondition pointer.
	// If the Operator is "in" or "not in", Value should be a slice of any.
	// If the Operator is "between", Value should be a slice of two elements.
	// If the Operator is "like" or "not like", Value should be a string.
	Value any
}

// Converter is an interface for converting universal filter conditions to specific query formats.
type Converter[T any] interface {
	// Convert converts a universal filter condition to a specific query format.
	Convert(condition *UniversalFilterCondition) (T, error)
}
