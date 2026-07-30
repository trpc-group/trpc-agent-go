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

// Value carries optional typed score detail produced by an evaluator.
// A nil *Value means typed detail is unavailable. When Value is non-nil, Kind
// should identify the single corresponding field to read. Numeric and Boolean
// use pointers so zero and false are valid values.
type Value struct {
	// Kind identifies which typed value field is populated.
	Kind Kind `json:"kind,omitempty"`
	// Numeric carries a score value when Kind is KindNumeric.
	Numeric *float64 `json:"numeric,omitempty"`
	// Boolean carries a score value when Kind is KindBoolean.
	Boolean *bool `json:"boolean,omitempty"`
	// Categorical carries a label when Kind is KindCategorical.
	Categorical string `json:"categorical,omitempty"`
}
