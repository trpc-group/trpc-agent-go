//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package condition

import (
	"fmt"
	"reflect"
	"strings"
)

// evaluateOperator evaluates a single operator against actual and expected values.
func evaluateOperator(operator string, actual, expected interface{}) (bool, error) {
	switch operator {
	// String/Array operators
	case OpContains:
		return evalContains(actual, expected)
	case OpNotContains:
		return evalNotContains(actual, expected)
	case OpStartsWith:
		return evalStartsWith(actual, expected)
	case OpEndsWith:
		return evalEndsWith(actual, expected)
	case OpIs:
		return evalIs(actual, expected)
	case OpIsNot:
		return evalIsNot(actual, expected)
	case OpEmpty:
		return evalEmpty(actual)
	case OpNotEmpty:
		return evalNotEmpty(actual)
	case OpIn:
		return evalIn(actual, expected)
	case OpNotIn:
		return evalNotIn(actual, expected)

	// Number operators
	case OpEqual:
		return evalEqual(actual, expected)
	case OpNotEqual:
		return evalNotEqual(actual, expected)
	case OpGreaterThan:
		return evalGreaterThan(actual, expected)
	case OpLessThan:
		return evalLessThan(actual, expected)
	case OpGreaterThanOrEqual:
		return evalGreaterThanOrEqual(actual, expected)
	case OpLessThanOrEqual:
		return evalLessThanOrEqual(actual, expected)

	// Null operators
	case OpNull:
		return evalNull(actual)
	case OpNotNull:
		return evalNotNull(actual)

	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

// String/Array operators

func evalContains(actual, expected interface{}) (bool, error) {
	if actual == nil {
		return false, nil
	}

	switch v := actual.(type) {
	case string:
		expectedStr, ok := expected.(string)
		if !ok {
			expectedStr = fmt.Sprintf("%v", expected)
		}
		return strings.Contains(v, expectedStr), nil
	case []interface{}:
		for _, item := range v {
			if reflect.DeepEqual(item, expected) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("contains operator requires string or array, got %T", actual)
	}
}

func evalNotContains(actual, expected interface{}) (bool, error) {
	result, err := evalContains(actual, expected)
	if err != nil {
		return false, err
	}
	return !result, nil
}

func evalStartsWith(actual, expected interface{}) (bool, error) {
	if actual == nil {
		return false, nil
	}

	actualStr, ok := actual.(string)
	if !ok {
		return false, fmt.Errorf("starts_with operator requires string, got %T", actual)
	}

	expectedStr, ok := expected.(string)
	if !ok {
		expectedStr = fmt.Sprintf("%v", expected)
	}

	return strings.HasPrefix(actualStr, expectedStr), nil
}

func evalEndsWith(actual, expected interface{}) (bool, error) {
	if actual == nil {
		return false, nil
	}

	actualStr, ok := actual.(string)
	if !ok {
		return false, fmt.Errorf("ends_with operator requires string, got %T", actual)
	}

	expectedStr, ok := expected.(string)
	if !ok {
		expectedStr = fmt.Sprintf("%v", expected)
	}

	return strings.HasSuffix(actualStr, expectedStr), nil
}

func evalIs(actual, expected interface{}) (bool, error) {
	if actual == nil {
		return false, nil
	}
	return reflect.DeepEqual(actual, expected), nil
}

func evalIsNot(actual, expected interface{}) (bool, error) {
	result, err := evalIs(actual, expected)
	if err != nil {
		return false, err
	}
	return !result, nil
}

func evalEmpty(actual interface{}) (bool, error) {
	if actual == nil {
		return true, nil
	}

	switch v := actual.(type) {
	case string:
		return v == "", nil
	case []interface{}:
		return len(v) == 0, nil
	case map[string]interface{}:
		return len(v) == 0, nil
	default:
		return false, nil
	}
}

func evalNotEmpty(actual interface{}) (bool, error) {
	result, err := evalEmpty(actual)
	if err != nil {
		return false, err
	}
	return !result, nil
}

func evalIn(actual, expected interface{}) (bool, error) {
	expectedArray, ok := expected.([]interface{})
	if !ok {
		return false, fmt.Errorf("in operator requires array as expected value, got %T", expected)
	}

	for _, item := range expectedArray {
		if reflect.DeepEqual(actual, item) {
			return true, nil
		}
	}
	return false, nil
}

func evalNotIn(actual, expected interface{}) (bool, error) {
	result, err := evalIn(actual, expected)
	if err != nil {
		return false, err
	}
	return !result, nil
}

// Number operators

func evalEqual(actual, expected interface{}) (bool, error) {
	return reflect.DeepEqual(actual, expected), nil
}

func evalNotEqual(actual, expected interface{}) (bool, error) {
	return !reflect.DeepEqual(actual, expected), nil
}

func evalGreaterThan(actual, expected interface{}) (bool, error) {
	actualNum, err := toFloat64(actual)
	if err != nil {
		return false, err
	}
	expectedNum, err := toFloat64(expected)
	if err != nil {
		return false, err
	}
	return actualNum > expectedNum, nil
}

func evalLessThan(actual, expected interface{}) (bool, error) {
	actualNum, err := toFloat64(actual)
	if err != nil {
		return false, err
	}
	expectedNum, err := toFloat64(expected)
	if err != nil {
		return false, err
	}
	return actualNum < expectedNum, nil
}

func evalGreaterThanOrEqual(actual, expected interface{}) (bool, error) {
	actualNum, err := toFloat64(actual)
	if err != nil {
		return false, err
	}
	expectedNum, err := toFloat64(expected)
	if err != nil {
		return false, err
	}
	return actualNum >= expectedNum, nil
}

func evalLessThanOrEqual(actual, expected interface{}) (bool, error) {
	actualNum, err := toFloat64(actual)
	if err != nil {
		return false, err
	}
	expectedNum, err := toFloat64(expected)
	if err != nil {
		return false, err
	}
	return actualNum <= expectedNum, nil
}

// Null operators

func evalNull(actual interface{}) (bool, error) {
	return actual == nil, nil
}

func evalNotNull(actual interface{}) (bool, error) {
	return actual != nil, nil
}

// Helper functions

// toFloat64 converts various numeric types to float64
func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int8:
		return float64(val), nil
	case int16:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case uint:
		return float64(val), nil
	case uint8:
		return float64(val), nil
	case uint16:
		return float64(val), nil
	case uint32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to number", v)
	}
}
