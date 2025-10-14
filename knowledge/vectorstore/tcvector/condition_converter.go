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
	if cond == nil {
		return nil, nil
	}

	return c.convertCondition(cond)
}

func (c *tcVectorConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	switch cond.Operator {
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return c.buildLogicalCondition(cond)
	case searchfilter.OperatorEqual, searchfilter.OperatorNotEqual,
		searchfilter.OperatorGreaterThan, searchfilter.OperatorGreaterThanOrEqual,
		searchfilter.OperatorLessThan, searchfilter.OperatorLessThanOrEqual:
		return c.buildComparisonCondition(cond)
	case searchfilter.OperatorIn, searchfilter.OperatorNotIn:
		return c.buildInCondition(cond)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

func (c *tcVectorConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (*tcvectordb.Filter, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
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
		return nil, fmt.Errorf("invalid logical condition: %v", cond.Value)
	}
	filter := tcvectordb.NewFilter("")
	for _, child := range conds {
		childFilter, err := c.convertCondition(child)
		if err != nil {
			return nil, err
		}
		if cond.Operator == searchfilter.OperatorAnd {
			filter.And(childFilter.Cond())
		} else {
			filter.Or(childFilter.Cond())
		}
	}

	return filter, nil
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
	if reflect.TypeOf(cond.Value).Kind() == reflect.String {
		filter = fmt.Sprintf(`%s %s "%v"`, cond.Field, operator, cond.Value)
	} else {
		filter = fmt.Sprintf(`%s %s %v`, cond.Field, operator, cond.Value)
	}
	return tcvectordb.NewFilter(filter), nil
}
