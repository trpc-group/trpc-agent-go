//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package condition

// CaseCondition represents a structured condition configuration for a single
// builtin case. It contains multiple condition rules that are evaluated
// together using a logical operator.
type CaseCondition struct {
	// Conditions is the list of condition rules to evaluate
	Conditions []ConditionRule `json:"conditions"`

	// LogicalOperator specifies how to combine multiple conditions
	// Valid values: "and", "or"
	// Default: "and"
	LogicalOperator string `json:"logical_operator,omitempty"`
}

// ConditionRule represents a single condition rule.
// It compares a variable from state against a value using an operator.
type ConditionRule struct {
	// Variable is the path to the variable in state (e.g., "state.score", "state.category")
	Variable string `json:"variable"`

	// Operator is the comparison operator
	// Supported operators:
	//   String/Array: "contains", "not_contains", "starts_with", "ends_with",
	//                 "is", "is_not", "empty", "not_empty", "in", "not_in"
	//   Number: "==", "!=", ">", "<", ">=", "<="
	//   Null: "null", "not_null"
	Operator string `json:"operator"`

	// Value is the value to compare against
	// Can be string, number, boolean, or array
	Value interface{} `json:"value,omitempty"`
}

// Supported operators
const (
	// String/Array operators
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpStartsWith  = "starts_with"
	OpEndsWith    = "ends_with"
	OpIs          = "is"
	OpIsNot       = "is_not"
	OpEmpty       = "empty"
	OpNotEmpty    = "not_empty"
	OpIn          = "in"
	OpNotIn       = "not_in"

	// Number operators
	OpEqual              = "=="
	OpNotEqual           = "!="
	OpGreaterThan        = ">"
	OpLessThan           = "<"
	OpGreaterThanOrEqual = ">="
	OpLessThanOrEqual    = "<="

	// Null operators
	OpNull    = "null"
	OpNotNull = "not_null"
)

// Logical operators
const (
	LogicalAnd = "and"
	LogicalOr  = "or"
)
