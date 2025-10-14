//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// esConverter converts a filter condition to an Elasticsearch query.
type esConverter struct{}

// Convert converts a filter condition to an Elasticsearch query filter.
func (c *esConverter) Convert(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in esConverter Convert: %v\n%s", r, string(stack))
		}
	}()
	if cond == nil {
		return nil, nil
	}

	return c.convertCondition(cond)
}

func (c *esConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
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
		return c.convertWildcard(cond)
	case searchfilter.OperatorBetween:
		return c.convertRangeBetween(cond)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

func (c *esConverter) buildLogicalCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	conditions, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("bool operator requires an array of conditions")
	}

	var queries []types.Query
	for _, condition := range conditions {
		query, err := c.convertCondition(condition)
		if err != nil {
			return nil, err
		}
		if query != nil {
			queries = append(queries, *query.QueryCaster())
		}
	}

	if cond.Operator == searchfilter.OperatorAnd {
		return &types.Query{
			Bool: &types.BoolQuery{
				Must: queries,
			},
		}, nil
	}
	// OperatorOr
	return &types.Query{
		Bool: &types.BoolQuery{
			Should: queries,
		},
	}, nil
}

func (c *esConverter) buildComparisonCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}

	switch cond.Operator {
	case searchfilter.OperatorEqual:
		return c.convertEqual(cond)
	case searchfilter.OperatorNotEqual:
		return c.convertNotEqual(cond)
	case searchfilter.OperatorGreaterThan, searchfilter.OperatorGreaterThanOrEqual,
		searchfilter.OperatorLessThan, searchfilter.OperatorLessThanOrEqual:
		return c.convertRange(cond)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

func (c *esConverter) convertEqual(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	return &types.Query{
		Term: map[string]types.TermQuery{
			cond.Field: {
				Value: cond.Value,
			},
		},
	}, nil
}

func (c *esConverter) convertNotEqual(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	return &types.Query{
		Bool: &types.BoolQuery{
			MustNot: []types.Query{
				{
					Term: map[string]types.TermQuery{
						cond.Field: {
							Value: cond.Value,
						},
					},
				},
			},
		},
	}, nil
}

func (c *esConverter) convertRange(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	return &types.Query{
		Range: map[string]types.RangeQuery{
			cond.Field: map[string]any{
				cond.Operator: cond.Value,
			},
		},
	}, nil
}

func (c *esConverter) convertRangeBetween(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	if reflect.TypeOf(cond.Value).Kind() != reflect.Slice {
		return nil, fmt.Errorf("in operator requires an array of values")
	}
	value := reflect.ValueOf(cond.Value)
	if value.Len() != 2 {
		return nil, fmt.Errorf("between operator requires an array with two values")
	}

	return &types.Query{
		Range: map[string]types.RangeQuery{
			cond.Field: map[string]any{
				"gte": value.Index(0).Interface(),
				"lte": value.Index(1).Interface(),
			},
		},
	}, nil
}

func (c *esConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	if reflect.TypeOf(cond.Value).Kind() != reflect.Slice {
		return nil, fmt.Errorf("in operator requires an array of values")
	}

	termsQuery := types.Query{
		Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				cond.Field: cond.Value,
			},
		},
	}

	if cond.Operator == searchfilter.OperatorNotIn {
		return &types.Query{
			Bool: &types.BoolQuery{
				MustNot: []types.Query{termsQuery},
			},
		}, nil
	}

	return &termsQuery, nil
}

func (c *esConverter) convertWildcard(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	valueStr, ok := cond.Value.(string)
	if !ok {
		return nil, fmt.Errorf("like operator requires string value")
	}

	wildcardPattern := strings.ReplaceAll(valueStr, "%", "*")
	wildcardPattern = strings.ReplaceAll(wildcardPattern, "_", "?")

	wildcardQuery := types.Query{
		Wildcard: map[string]types.WildcardQuery{
			cond.Field: {
				Value: &wildcardPattern,
			},
		},
	}

	if cond.Operator == searchfilter.OperatorNotLike {
		return &types.Query{
			Bool: &types.BoolQuery{
				MustNot: []types.Query{wildcardQuery},
			},
		}, nil
	}

	return &wildcardQuery, nil
}
