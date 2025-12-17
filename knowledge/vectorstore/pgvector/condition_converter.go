//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

var comparisonOperators = map[string]string{
	searchfilter.OperatorEqual:              "=",
	searchfilter.OperatorNotEqual:           "!=",
	searchfilter.OperatorGreaterThan:        ">",
	searchfilter.OperatorGreaterThanOrEqual: ">=",
	searchfilter.OperatorLessThan:           "<",
	searchfilter.OperatorLessThanOrEqual:    "<=",
}

type condConvertResult struct {
	cond string
	args []any
}

// pgVectorConverter converts a filter condition to a postgres vector query.
type pgVectorConverter struct {
	metadataFieldName string
}

// Convert converts a filter condition to a postgres vector query filter.
func (c *pgVectorConverter) Convert(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in pgVectorConverter Convert: %v\n%s", r, string(stack))
		}
	}()

	return c.convertCondition(cond)
}

// convertFieldName converts metadata.xxx fields to JSONB syntax.
func (c *pgVectorConverter) convertFieldName(field string) string {
	if strings.HasPrefix(field, source.MetadataFieldPrefix) {
		actualField := strings.TrimPrefix(field, source.MetadataFieldPrefix)
		return fmt.Sprintf("%s->>'%s'", c.metadataFieldName, actualField)
	}
	return field
}

func (c *pgVectorConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	if cond == nil {
		return nil, fmt.Errorf("nil condition")
	}

	switch cond.Operator {
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return c.buildLogicalCondition(cond)
	case searchfilter.OperatorEqual, searchfilter.OperatorNotEqual,
		searchfilter.OperatorGreaterThan, searchfilter.OperatorGreaterThanOrEqual,
		searchfilter.OperatorLessThan, searchfilter.OperatorLessThanOrEqual:
		return c.buildComparisonCondition(cond)
	case searchfilter.OperatorIn, searchfilter.OperatorNotIn:
		return c.buildInCondition(cond)
	case searchfilter.OperatorLike, searchfilter.OperatorNotLike:
		return c.buildLikeCondition(cond)
	case searchfilter.OperatorBetween:
		return c.buildBetweenCondition(cond)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

func (c *pgVectorConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}

	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() <= 0 {
		return nil, fmt.Errorf("in operator value must be a slice with at least one value: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	itemNum := value.Len()
	condResult := condConvertResult{args: make([]any, 0, itemNum)}
	args := make([]string, 0, itemNum)
	for i := 0; i < itemNum; i++ {
		condResult.args = append(condResult.args, value.Index(i).Interface())
		args = append(args, "$%d")
	}
	condResult.cond = fmt.Sprintf(`%s %s (%s)`, fieldName, strings.ToUpper(cond.Operator), strings.Join(args, ", "))
	return &condResult, nil
}

func (c *pgVectorConverter) buildLogicalCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	conds, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("invalid logical condition: value must be of type []*searchfilter.UniversalFilterCondition: %v", cond.Value)
	}
	var condResult *condConvertResult
	for _, child := range conds {
		childFilter, err := c.convertCondition(child)
		if err != nil {
			return nil, err
		}
		if childFilter == nil || childFilter.cond == "" {
			continue
		}
		if condResult == nil {
			condResult = childFilter
			continue
		}

		condResult.cond = fmt.Sprintf("(%s) %s (%s)", condResult.cond, strings.ToUpper(cond.Operator), childFilter.cond)
		condResult.args = append(condResult.args, childFilter.args...)
	}

	if condResult == nil {
		return nil, fmt.Errorf("empty logical condition")
	}

	return condResult, nil
}

func (c *pgVectorConverter) buildComparisonCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	operator, ok := comparisonOperators[cond.Operator]
	if !ok {
		return nil, fmt.Errorf("unsupported comparison operator: %s", cond.Operator)
	}

	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}

	fieldName := c.convertFieldName(cond.Field)
	return &condConvertResult{
		cond: fmt.Sprintf(`%s %s `, fieldName, operator) + "$%d",
		args: []any{cond.Value},
	}, nil
}

func (c *pgVectorConverter) buildLikeCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	if cond.Value == nil || reflect.TypeOf(cond.Value).Kind() != reflect.String {
		return nil, fmt.Errorf("like operator value must be a string: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	return &condConvertResult{
		cond: fmt.Sprintf(`%s %s `, fieldName, strings.ToUpper(cond.Operator)) + "$%d",
		args: []any{cond.Value},
	}, nil
}

func (c *pgVectorConverter) buildBetweenCondition(cond *searchfilter.UniversalFilterCondition) (*condConvertResult, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() != 2 {
		return nil, fmt.Errorf("between operator value must be a slice with two elements: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	return &condConvertResult{
		cond: fieldName + " >= $%d AND " + fieldName + " <= $%d",
		args: []any{value.Index(0).Interface(), value.Index(1).Interface()},
	}, nil
}
