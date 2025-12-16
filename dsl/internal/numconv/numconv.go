//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package numconv provides helpers for converting loosely-typed JSON-ish values
// (e.g. map[string]any decoded from JSON or produced by other layers) into
// concrete numeric Go types.
package numconv

import (
	"encoding/json"
	"fmt"
	"math"
)

func intBounds() (max int64, min int64) {
	max = int64(^uint(0) >> 1)
	min = -max - 1
	return max, min
}

// Int converts value into an int.
// Supported input types:
//   - int, int8/16/32/64 (with bounds check for int64)
//   - uint, uint8/16/32/64 (with bounds check)
//   - float32/float64 if it is an integer value (e.g. 512.0)
//   - json.Number when it is an integer value
func Int(value any, fieldName string) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("%s must be an integer (got <nil>)", fieldName)
	}

	maxInt, minInt := intBounds()

	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		if v > maxInt || v < minInt {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint:
		if uint64(v) > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		if uint64(v) > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint64:
		if v > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case float32:
		return Int(float64(v), fieldName)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
		}
		if v > float64(maxInt) || v < float64(minInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
		}
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
		}
		if n > maxInt || n < minInt {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
	}
}

// IntTrunc converts value into an int, truncating floating-point inputs.
// It accepts the same set of types as Int, plus json.Number/float values that
// are not integers (they are truncated towards zero).
func IntTrunc(value any, fieldName string) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("%s must be an integer (got <nil>)", fieldName)
	}

	maxInt, minInt := intBounds()

	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		if v > maxInt || v < minInt {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint:
		if uint64(v) > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		if uint64(v) > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case uint64:
		if v > uint64(maxInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case float32:
		return IntTrunc(float64(v), fieldName)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
		}
		if v > float64(maxInt) || v < float64(minInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			if n > maxInt || n < minInt {
				return 0, fmt.Errorf("%s is too large", fieldName)
			}
			return int(n), nil
		}
		f, err := v.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
		}
		if f > float64(maxInt) || f < float64(minInt) {
			return 0, fmt.Errorf("%s is too large", fieldName)
		}
		return int(f), nil
	default:
		return 0, fmt.Errorf("%s must be an integer (got %T)", fieldName, value)
	}
}

// Float64 converts value into a float64.
// Supported input types:
//   - float32/float64
//   - all integer types
//   - json.Number
func Float64(value any, fieldName string) (float64, error) {
	if value == nil {
		return 0, fmt.Errorf("%s must be a number (got <nil>)", fieldName)
	}

	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("%s must be a number (got %T)", fieldName, value)
		}
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("%s must be a number (got %T)", fieldName, value)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%s must be a number (got %T)", fieldName, value)
	}
}
