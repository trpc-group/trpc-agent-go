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

	// OperatorLessThan is the "less than" operator.
	OperatorLessThan = "lt"

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
	Value any
}

// Converter is an interface for converting universal filter conditions to specific query formats.
type Converter interface {
	Convert(condition *UniversalFilterCondition) (any, error)
}
