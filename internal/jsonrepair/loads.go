//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"encoding/json"
	"fmt"
)

// LoadResult holds a parsed JSON value and whether Repair was used.
type LoadResult struct {
	Value    any
	Repaired bool
}

// Loads parses s as JSON into a Go value using encoding/json.
// Valid JSON is unmarshaled directly without calling Repair; malformed input
// is repaired automatically. Use LoadsRepair when the caller needs to know
// whether repair was applied.
func Loads(s string) (any, error) {
	result, err := LoadsRepair(s)
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

// LoadsRepair parses s into a Go value. Valid JSON is unmarshaled directly
// without calling Repair. Malformed input is repaired first, then unmarshaled.
func LoadsRepair(s string) (LoadResult, error) {
	var value any
	if err := json.Unmarshal([]byte(s), &value); err == nil {
		return LoadResult{Value: value, Repaired: false}, nil
	}

	repaired, err := Repair([]byte(s))
	if err != nil {
		return LoadResult{}, fmt.Errorf("repair JSON: %w", err)
	}

	if err := json.Unmarshal(repaired, &value); err != nil {
		return LoadResult{}, fmt.Errorf("unmarshal repaired JSON: %w", err)
	}

	return LoadResult{Value: value, Repaired: true}, nil
}
