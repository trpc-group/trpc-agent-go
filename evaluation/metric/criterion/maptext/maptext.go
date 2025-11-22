//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package maptext defines map-based comparison criteria.
package maptext

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// MapTextCriterion compares two string-keyed maps.
type MapTextCriterion struct {
	// TextCriterion applies string-based matching on JSON-serialized maps.
	TextCriterion *text.TextCriterion `json:"textCriterion,omitempty"`
	// Compare overrides default comparison when provided.
	Compare func(actual, expected map[string]any) error `json:"-"`
}
