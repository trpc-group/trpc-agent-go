//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package milvus

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

var comparisonOperators = map[string]string{
	searchfilter.OperatorEqual:              "==",
	searchfilter.OperatorNotEqual:           "!=",
	searchfilter.OperatorGreaterThan:        ">",
	searchfilter.OperatorGreaterThanOrEqual: ">=",
	searchfilter.OperatorLessThan:           "<",
	searchfilter.OperatorLessThanOrEqual:    "<=",
	searchfilter.OperatorLike:               "like",
	searchfilter.OperatorNotLike:            "not like",
}

// milvusFilterConverter converts searchfilter conditions to Milvus expressions
type milvusFilterConverter struct{}

type convertResult struct {
	exprStr string
	params  map[string]any
}

func (c *milvusFilterConverter) Convert(condition *searchfilter.UniversalFilterCondition) (*convertResult, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in milvusFilterConverter Convert: %v\n%s", r, string(stack))
		}
	}()

	cr := &convertResult{
		params: make(map[string]any),
	}

	err := c.convertCondition(condition, cr)
	if err != nil {
		return nil, err
	}

	// Return nil params if empty
	if len(cr.exprStr) == 0 || len(cr.params) == 0 {
		return nil, fmt.Errorf("empty condition")
	}

	return cr, nil
}

func (c *milvusFilterConverter) convertCondition(condition *searchfilter.UniversalFilterCondition, cr *convertResult) error {
	if condition == nil {
		return fmt.Errorf("milvus filter condition is nil")
	}
	switch condition.Operator {
	case searchfilter.OperatorEqual, searchfilter.OperatorNotEqual, searchfilter.OperatorGreaterThan,
		searchfilter.OperatorGreaterThanOrEqual, searchfilter.OperatorLessThan, searchfilter.OperatorLessThanOrEqual,
		searchfilter.OperatorLike, searchfilter.OperatorNotLike:
		return c.convertGeneralComparisonCondition(condition, cr)
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return c.convertLogicalCondition(condition, cr)
	case searchfilter.OperatorIn, searchfilter.OperatorNotIn:
		return c.convertInCondition(condition, cr)
	case searchfilter.OperatorBetween:
		return c.convertBetweenCondition(condition, cr)
	default:
		return fmt.Errorf("unsupported operator: %v", condition.Operator)
	}
}

func (c *milvusFilterConverter) convertGeneralComparisonCondition(condition *searchfilter.UniversalFilterCondition, cr *convertResult) error {
	if condition.Field == "" || condition.Value == nil {
		return fmt.Errorf("milvus filter condition is nil")
	}

	operator := comparisonOperators[condition.Operator]

	cr.params[condition.Field] = condition.Value
	cr.exprStr = fmt.Sprintf("%s %s {%s}", condition.Field, operator, condition.Field)
	return nil
}

func (c *milvusFilterConverter) convertLogicalCondition(condition *searchfilter.UniversalFilterCondition, cr *convertResult) error {
	if condition.Value == nil {
		return fmt.Errorf("milvus filter condition is nil")
	}

	conditions, ok := condition.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return fmt.Errorf("invalid logical condition value type")
	}

	parts := make([]string, 0, len(conditions))
	params := make(map[string]any)
	for _, cond := range conditions {
		condCR := &convertResult{
			params: make(map[string]any),
		}
		err := c.convertCondition(cond, condCR)
		if err != nil {
			return err
		}
		if condCR.exprStr != "" {
			parts = append(parts, condCR.exprStr)
		}
		for k, v := range condCR.params {
			params[k] = v
		}
	}

	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		cr.exprStr = parts[0]
		cr.params = params
		return nil
	}
	if condition.Operator == searchfilter.OperatorAnd {
		cr.exprStr = "(" + strings.Join(parts, " and ") + ")"
	} else {
		cr.exprStr = "(" + strings.Join(parts, " or ") + ")"
	}
	cr.params = params
	return nil
}

func (c *milvusFilterConverter) convertInCondition(condition *searchfilter.UniversalFilterCondition, cr *convertResult) error {
	if condition.Field == "" || condition.Value == nil {
		return fmt.Errorf("milvus filter condition is nil")
	}

	s := reflect.ValueOf(condition.Value)
	if s.Kind() != reflect.Slice || s.Len() <= 0 {
		return fmt.Errorf("in operator value must be a slice with at least one value: %v", condition.Value)
	}
	cr.params[condition.Field] = condition.Value

	if condition.Operator == searchfilter.OperatorNotIn {
		cr.exprStr = fmt.Sprintf("%s not in {%s}", condition.Field, condition.Field)
		return nil
	}

	cr.exprStr = fmt.Sprintf("%s in {%s}", condition.Field, condition.Field)
	return nil
}

func (c *milvusFilterConverter) convertBetweenCondition(condition *searchfilter.UniversalFilterCondition, cr *convertResult) error {
	if condition.Field == "" || condition.Value == nil {
		return fmt.Errorf("milvus filter condition is nil")
	}

	value := reflect.ValueOf(condition.Value)
	if value.Kind() != reflect.Slice || value.Len() != 2 {
		return fmt.Errorf("between operator value must be a slice with two elements: %v", condition.Value)
	}

	paramName1 := fmt.Sprintf("%s_%d", condition.Field, 0)
	paramName2 := fmt.Sprintf("%s_%d", condition.Field, 1)
	cr.params[paramName1] = value.Index(0).Interface()
	cr.params[paramName2] = value.Index(1).Interface()
	cr.exprStr = fmt.Sprintf("%s >= {%s} and %s <= {%s}", condition.Field, paramName1, condition.Field, paramName2)

	return nil
}

// formatValue formats a value for use in Milvus filter expressions.
// According to Milvus documentation:
// - String values must be enclosed in quotes
// - Numeric values (int, float) should not be quoted
// - Boolean values should not be quoted
// - Time values should be converted to Unix timestamp and not be quoted
func formatValue(value any) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", escapeDoubleQuotes(v))
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case float32, float64:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case time.Time:
		return fmt.Sprintf("%d", v.Unix())
	default:
		return fmt.Sprintf("\"%v\"", value)
	}
}

// escapeDoubleQuotes escapes double quotes in a string for use in Milvus expressions.
func escapeDoubleQuotes(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}
