//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package score defines typed evaluation score values.
package score

// Kind identifies the typed score value shape.
type Kind string

const (
	// KindNumeric represents a numeric score value.
	KindNumeric Kind = "numeric"
	// KindBoolean represents a boolean score value.
	KindBoolean Kind = "boolean"
	// KindCategorical represents a categorical score value.
	KindCategorical Kind = "categorical"
)

// Value carries the evaluator's typed score value.
type Value struct {
	// Kind identifies which typed value field is populated.
	Kind Kind `json:"kind,omitempty"`
	// Numeric carries a numeric score value.
	Numeric *float64 `json:"numeric,omitempty"`
	// Boolean carries a boolean score value.
	Boolean *bool `json:"boolean,omitempty"`
	// Categorical carries a categorical label score value.
	Categorical string `json:"categorical,omitempty"`
}
