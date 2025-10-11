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
	"strings"

	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// esConverter converts a filter condition to an Elasticsearch query.
type esConverter struct{}

// Convert converts a filter condition to an Elasticsearch query filter.
func (c *esConverter) Convert(filter *searchfilter.UniversalFilterCondition) (any, error) {
	if filter == nil {
		return nil, nil
	}

	return nil, nil
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
	default:
		return nil, fmt.Errorf("unsupported operation: %s", cond.Operator)
	}
}

// buildLogicalCondition 构建逻辑条件查询（用于复合查询）
func (c *esConverter) buildLogicalCondition(filter *searchfilter.UniversalFilterCondition) (*types.Query, error) {
	// 假设Value是一个UniversalFilterCondition的切片
	conditions, ok := filter.Value.([]*searchfilter.UniversalFilterCondition)
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
			queries = append(queries, *query.(*types.Query))
		}
	}

	if filter.Operator == searchfilter.OperatorAnd {
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

func (c *esConverter) convertEqual(filter *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	return &types.Query{
		Term: map[string]types.TermQuery{
			filter.Field: {
				Value: filter.Value,
			},
		},
	}, nil
}

func (c *esConverter) convertNotEqual(filter *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	return &types.Query{
		Bool: &types.BoolQuery{
			MustNot: []types.Query{
				{
					Term: map[string]types.TermQuery{
						filter.Field: {
							Value: filter.Value,
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
	values, ok := cond.Value.([]any)
	if !ok || len(values) != 2 {
		return nil, fmt.Errorf("between operator requires an array with two values")
	}

	return &types.Query{
		Range: map[string]types.RangeQuery{
			cond.Field: map[string]any{
				"gte": &values[0],
				"lte": &values[1],
			},
		},
	}, nil
}

func (c *esConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	values, ok := cond.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("in operator requires an array of values")
	}

	termsQuery := types.Query{
		Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				cond.Field: values,
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

// convertWildcard 转换like和not like操作符（通配符查询）
func (c *esConverter) convertWildcard(cond *searchfilter.UniversalFilterCondition) (*types.Query, error) {
	valueStr, ok := cond.Value.(string)
	if !ok {
		return nil, fmt.Errorf("like operator requires string value")
	}

	// 将like模式转换为通配符模式
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
