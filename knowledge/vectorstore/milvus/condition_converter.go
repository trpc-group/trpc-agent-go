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
	"maps"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
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
type milvusFilterConverter struct {
	metadataFieldName string
}

// newMilvusFilterConverter creates a new milvusFilterConverter.
func newMilvusFilterConverter(metadataFieldName string) *milvusFilterConverter {
	return &milvusFilterConverter{
		metadataFieldName: metadataFieldName,
	}
}

type convertResult struct {
	exprStr string
	params  map[string]any
}

func (c *milvusFilterConverter) Convert(cond *searchfilter.UniversalFilterCondition) (*convertResult, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in milvusFilterConverter Convert: %v\n%s", r, string(stack))
		}
	}()

	var counter int
	return c.convertCondition(cond, &counter)
}

func (c *milvusFilterConverter) convertCondition(
	cond *searchfilter.UniversalFilterCondition,
	counter *int,
) (*convertResult, error) {
	if cond == nil {
		return nil, fmt.Errorf("milvus filter condition is nil")
	}
	switch cond.Operator {
	case searchfilter.OperatorEqual, searchfilter.OperatorNotEqual, searchfilter.OperatorGreaterThan,
		searchfilter.OperatorGreaterThanOrEqual, searchfilter.OperatorLessThan,
		searchfilter.OperatorLessThanOrEqual, searchfilter.OperatorLike, searchfilter.OperatorNotLike:
		return c.convertComparisonCondition(cond, counter)
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return c.convertLogicalCondition(cond, counter)
	case searchfilter.OperatorIn, searchfilter.OperatorNotIn:
		return c.convertInCondition(cond, counter)
	case searchfilter.OperatorBetween:
		return c.convertBetweenCondition(cond, counter)
	default:
		return nil, fmt.Errorf("unsupported operator: %v", cond.Operator)
	}
}

func (c *milvusFilterConverter) convertComparisonCondition(
	cond *searchfilter.UniversalFilterCondition,
	counter *int,
) (*convertResult, error) {
	condField := c.convertFieldName(cond.Field)
	if condField == "" || cond.Value == nil {
		return nil, fmt.Errorf("milvus filter condition is nil")
	}
	operator, ok := comparisonOperators[cond.Operator]
	if !ok {
		return nil, fmt.Errorf("unsupported comparison operator: %s", cond.Operator)
	}

	paramName := c.convertParamName(cond.Field, counter)
	return &convertResult{
		exprStr: fmt.Sprintf("%s %s {%s}", condField, operator, paramName),
		params:  map[string]any{paramName: cond.Value},
	}, nil
}

func (c *milvusFilterConverter) convertLogicalCondition(
	cond *searchfilter.UniversalFilterCondition,
	counter *int,
) (*convertResult, error) {
	if cond.Value == nil {
		return nil, fmt.Errorf("milvus filter condition is nil")
	}
	conds, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("invalid logical condition value type")
	}

	var condResult *convertResult
	for _, childCond := range conds {
		childRes, err := c.convertCondition(childCond, counter)
		if err != nil {
			return nil, err
		}
		if childRes == nil || childRes.exprStr == "" {
			continue
		}
		if condResult == nil {
			condResult = childRes
			continue
		}

		condResult.exprStr = fmt.Sprintf(
			"(%s) %s (%s)",
			condResult.exprStr,
			strings.ToLower(cond.Operator),
			childRes.exprStr,
		)
		maps.Copy(condResult.params, childRes.params)
	}

	if condResult == nil {
		return nil, fmt.Errorf("empty logical condition")
	}
	return condResult, nil
}

func (c *milvusFilterConverter) convertInCondition(
	cond *searchfilter.UniversalFilterCondition,
	counter *int,
) (*convertResult, error) {
	condField := c.convertFieldName(cond.Field)
	if condField == "" || cond.Value == nil {
		return nil, fmt.Errorf("milvus filter condition is nil")
	}

	s := reflect.ValueOf(cond.Value)
	if s.Kind() != reflect.Slice || s.Len() <= 0 {
		return nil, fmt.Errorf("in operator value must be a slice with at least one value: %v", cond.Value)
	}

	paramName := c.convertParamName(cond.Field, counter)
	return &convertResult{
		exprStr: fmt.Sprintf("%s %s {%s}", condField, strings.ToLower(cond.Operator), paramName),
		params:  map[string]any{paramName: cond.Value},
	}, nil
}

func (c *milvusFilterConverter) convertBetweenCondition(
	cond *searchfilter.UniversalFilterCondition,
	counter *int,
) (*convertResult, error) {
	condField := c.convertFieldName(cond.Field)
	if condField == "" || cond.Value == nil {
		return nil, fmt.Errorf("milvus filter condition is nil")
	}

	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() != 2 {
		return nil, fmt.Errorf("between operator value must be a slice with two elements: %v", cond.Value)
	}

	paramBase := c.convertParamName(cond.Field, counter)
	paramName1 := fmt.Sprintf("%s_%d", paramBase, 0)
	paramName2 := fmt.Sprintf("%s_%d", paramBase, 1)
	return &convertResult{
		exprStr: fmt.Sprintf("%s >= {%s} and %s <= {%s}", condField, paramName1, condField, paramName2),
		params: map[string]any{
			paramName1: value.Index(0).Interface(),
			paramName2: value.Index(1).Interface(),
		},
	}, nil
}

// convertFieldName converts metadata.xxx fields to Milvus JSON field path.
// e.g., metadata.topic -> metadata["topic"]
func (c *milvusFilterConverter) convertFieldName(field string) string {
	if actualField, ok := strings.CutPrefix(field, source.MetadataFieldPrefix); ok {
		return fmt.Sprintf("%s[\"%s\"]", c.metadataFieldName, actualField)
	}
	return field
}

// convertParamName converts field name to a valid Milvus template parameter name.
// Milvus template parameters don't support '.' character, so we replace it with '_'.
func (c *milvusFilterConverter) convertParamName(field string, counter *int) string {
	*counter++
	return fmt.Sprintf("%s_%d", strings.ReplaceAll(field, ".", "_"), *counter)
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
