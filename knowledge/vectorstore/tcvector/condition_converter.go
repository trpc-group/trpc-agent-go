//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcvector provides search and filter functionality for trpc-agent-go.
package tcvector

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
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

// tcVectorConverter converts a filter condition to a TC Vector query.
type tcVectorConverter struct{}

// Convert converts a filter condition to an TC Vector query filter.
func (c *tcVectorConverter) Convert(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in tcVectorConverter Convert: %v\n%s", r, string(stack))
		}
	}()

	return c.convertCondition(cond)
}

func (c *tcVectorConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
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
	case searchfilter.OperatorBetween:
		return c.buildBetweenCondition(cond)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

func (c *tcVectorConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	s := reflect.ValueOf(cond.Value)
	if s.Kind() != reflect.Slice || s.Len() <= 0 {
		return nil, fmt.Errorf("in operator value must be a slice with at least one value: %v", cond.Value)
	}

	var filter string
	if cond.Operator == searchfilter.OperatorIn {
		filter = tcvectordb.In(cond.Field, cond.Value)
	} else {
		filter = tcvectordb.NotIn(cond.Field, cond.Value)
	}
	return tcvectordb.NewFilter(filter), nil
}

func (c *tcVectorConverter) buildLogicalCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	conds, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("invalid logical condition: value must be of type []*searchfilter.UniversalFilterCondition: %v", cond.Value)
	}

	var condStr string
	for _, child := range conds {
		childFilter, err := c.convertCondition(child)
		if err != nil {
			return nil, err
		}

		childCond := childFilter.Cond()
		if condStr == "" {
			condStr = childCond
			continue
		}
		condStr = fmt.Sprintf("(%s) %s (%s)", condStr, strings.ToLower(cond.Operator), childCond)
	}

	if condStr == "" {
		return nil, fmt.Errorf("empty logical condition")
	}

	return tcvectordb.NewFilter(condStr), nil
}

func (c *tcVectorConverter) buildComparisonCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	operator, ok := comparisonOperators[cond.Operator]
	if !ok {
		return nil, fmt.Errorf("unsupported comparison operator: %s", cond.Operator)
	}

	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}

	var filter string
	if cond.Value != nil && reflect.TypeOf(cond.Value).Kind() == reflect.String {
		filter = fmt.Sprintf(`%s %s "%v"`, cond.Field, operator, cond.Value)
	} else {
		filter = fmt.Sprintf(`%s %s %v`, cond.Field, operator, cond.Value)
	}
	return tcvectordb.NewFilter(filter), nil
}

func (c *tcVectorConverter) buildBetweenCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() != 2 {
		return nil, fmt.Errorf("between operator value must be a slice with two elements: %v", cond.Value)
	}

	isString := value.Index(0).Kind() == reflect.String
	filter := tcvectordb.NewFilter("")
	if isString {
		filter.And(fmt.Sprintf(`%s >= "%v"`, cond.Field, value.Index(0).Interface()))
		filter.And(fmt.Sprintf(`%s <= "%v"`, cond.Field, value.Index(1).Interface()))
		return filter, nil
	}

	filter.And(fmt.Sprintf(`%s >= %v`, cond.Field, value.Index(0).Interface()))
	filter.And(fmt.Sprintf(`%s <= %v`, cond.Field, value.Index(1).Interface()))
	return filter, nil
}
