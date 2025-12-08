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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// esConverter converts a filter condition to an Elasticsearch query.
type esConverter struct {
	metadataFieldName string
}

// convertFieldName converts metadata.xxx fields to ES field path.
func (c *esConverter) convertFieldName(field string) string {
	if strings.HasPrefix(field, source.MetadataFieldPrefix) {
		actualField := strings.TrimPrefix(field, source.MetadataFieldPrefix)
		return fmt.Sprintf("%s.%s", c.metadataFieldName, actualField)
	}
	return field
}

// Convert converts a filter condition to an Elasticsearch query filter.
func (c *esConverter) Convert(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in esConverter Convert: %v\n%s", r, string(stack))
		}
	}()

	return c.convertCondition(cond)
}

func (c *esConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
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

func (c *esConverter) buildLogicalCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	conditions, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("invalid logical condition: value must be of type []*searchfilter.UniversalFilterCondition: %v", cond.Value)
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

	if len(queries) == 0 {
		return nil, fmt.Errorf("empty logical condition")
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
	fieldName := c.convertFieldName(cond.Field)
	return &types.Query{
		Term: map[string]types.TermQuery{
			fieldName: {
				Value: cond.Value,
			},
		},
	}, nil
}

func (c *esConverter) convertNotEqual(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	fieldName := c.convertFieldName(cond.Field)
	return &types.Query{
		Bool: &types.BoolQuery{
			MustNot: []types.Query{
				{
					Term: map[string]types.TermQuery{
						fieldName: {
							Value: cond.Value,
						},
					},
				},
			},
		},
	}, nil
}

func (c *esConverter) convertRange(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	fieldName := c.convertFieldName(cond.Field)
	return &types.Query{
		Range: map[string]types.RangeQuery{
			fieldName: map[string]any{
				cond.Operator: cond.Value,
			},
		},
	}, nil
}

func (c *esConverter) buildBetweenCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() != 2 {
		return nil, fmt.Errorf("between operator value must be a slice with two elements: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	return &types.Query{
		Range: map[string]types.RangeQuery{
			fieldName: map[string]any{
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
	value := reflect.ValueOf(cond.Value)
	if value.Kind() != reflect.Slice || value.Len() <= 0 {
		return nil, fmt.Errorf("in operator value must be a slice with at least one element: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	termsQuery := types.Query{
		Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				fieldName: cond.Value,
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

func (c *esConverter) buildLikeCondition(cond *searchfilter.UniversalFilterCondition) (types.QueryVariant, error) {
	if cond.Field == "" {
		return nil, fmt.Errorf("field is empty")
	}
	valueStr, ok := cond.Value.(string)
	if !ok {
		return nil, fmt.Errorf("like operator value must be a string: %v", cond.Value)
	}

	fieldName := c.convertFieldName(cond.Field)
	wildcardPattern := strings.ReplaceAll(valueStr, "%", "*")
	wildcardPattern = strings.ReplaceAll(wildcardPattern, "_", "?")

	wildcardQuery := types.Query{
		Wildcard: map[string]types.WildcardQuery{
			fieldName: {
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
