//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// condition_converter.go implements the searchfilter.Converter interface for Qdrant.
// It translates UniversalFilterCondition into Qdrant's native Filter protobuf structure.
package qdrant

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// qdrantFilterConverter converts UniversalFilterCondition to Qdrant Filter.
type qdrantFilterConverter struct {
	metadataFieldName string
}

func newFilterConverter() *qdrantFilterConverter {
	return &qdrantFilterConverter{
		metadataFieldName: fieldMetadata,
	}
}

// Convert converts a UniversalFilterCondition to a Qdrant Filter.
func (c *qdrantFilterConverter) Convert(cond *searchfilter.UniversalFilterCondition) (*qdrant.Filter, error) {
	if cond == nil {
		return nil, nil
	}

	switch cond.Operator {
	case searchfilter.OperatorAnd:
		return c.convertAnd(cond)
	case searchfilter.OperatorOr:
		return c.convertOr(cond)
	default:
		condition, err := c.convertCondition(cond)
		if err != nil {
			return nil, err
		}
		if condition == nil {
			return nil, nil
		}
		return &qdrant.Filter{Must: []*qdrant.Condition{condition}}, nil
	}
}

func (c *qdrantFilterConverter) convertAnd(cond *searchfilter.UniversalFilterCondition) (*qdrant.Filter, error) {
	conditions, err := c.convertLogicalConditions(cond, "and")
	if err != nil || len(conditions) == 0 {
		return nil, err
	}
	return &qdrant.Filter{Must: conditions}, nil
}

func (c *qdrantFilterConverter) convertOr(cond *searchfilter.UniversalFilterCondition) (*qdrant.Filter, error) {
	conditions, err := c.convertLogicalConditions(cond, "or")
	if err != nil || len(conditions) == 0 {
		return nil, err
	}
	return &qdrant.Filter{Should: conditions}, nil
}

func (c *qdrantFilterConverter) convertLogicalConditions(cond *searchfilter.UniversalFilterCondition, op string) ([]*qdrant.Condition, error) {
	subconds, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("%w: %s operator requires array of conditions", ErrInvalidFilter, op)
	}

	result := make([]*qdrant.Condition, 0, len(subconds))
	for _, sub := range subconds {
		if sub.Operator == searchfilter.OperatorAnd || sub.Operator == searchfilter.OperatorOr {
			subFilter, err := c.Convert(sub)
			if err != nil {
				return nil, err
			}
			if subFilter != nil {
				result = append(result, qdrant.NewFilterAsCondition(subFilter))
			}
		} else {
			converted, err := c.convertCondition(sub)
			if err != nil {
				return nil, err
			}
			if converted != nil {
				result = append(result, converted)
			}
		}
	}
	return result, nil
}

func (c *qdrantFilterConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (*qdrant.Condition, error) {
	if cond == nil || cond.Value == nil {
		return nil, nil
	}

	field := c.resolveField(cond.Field)

	switch cond.Operator {
	case searchfilter.OperatorEqual:
		return c.newMatchCondition(field, cond.Value), nil

	case searchfilter.OperatorNotEqual:
		return qdrant.NewFilterAsCondition(&qdrant.Filter{
			MustNot: []*qdrant.Condition{
				c.newMatchCondition(field, cond.Value),
			},
		}), nil

	case searchfilter.OperatorGreaterThan:
		return c.newRangeCondition(field, nil, toFloat64Ptr(cond.Value), nil, nil), nil

	case searchfilter.OperatorGreaterThanOrEqual:
		return c.newRangeCondition(field, toFloat64Ptr(cond.Value), nil, nil, nil), nil

	case searchfilter.OperatorLessThan:
		return c.newRangeCondition(field, nil, nil, nil, toFloat64Ptr(cond.Value)), nil

	case searchfilter.OperatorLessThanOrEqual:
		return c.newRangeCondition(field, nil, nil, toFloat64Ptr(cond.Value), nil), nil

	case searchfilter.OperatorIn:
		return c.convertInCondition(field, cond.Value)

	case searchfilter.OperatorNotIn:
		inCond, err := c.convertInCondition(field, cond.Value)
		if err != nil {
			return nil, err
		}
		if inCond == nil {
			return nil, nil
		}
		return qdrant.NewFilterAsCondition(&qdrant.Filter{
			MustNot: []*qdrant.Condition{inCond},
		}), nil

	case searchfilter.OperatorLike:
		return qdrant.NewMatchText(field, fmt.Sprintf("%v", cond.Value)), nil

	case searchfilter.OperatorNotLike:
		return qdrant.NewFilterAsCondition(&qdrant.Filter{
			MustNot: []*qdrant.Condition{
				qdrant.NewMatchText(field, fmt.Sprintf("%v", cond.Value)),
			},
		}), nil

	case searchfilter.OperatorBetween:
		values, ok := cond.Value.([]any)
		if !ok || len(values) != 2 {
			return nil, fmt.Errorf("%w: between operator requires array of 2 values", ErrInvalidFilter)
		}
		return c.newRangeCondition(field, toFloat64Ptr(values[0]), nil, toFloat64Ptr(values[1]), nil), nil

	default:
		return nil, fmt.Errorf("%w: unsupported operator: %s", ErrInvalidFilter, cond.Operator)
	}
}

func (c *qdrantFilterConverter) convertInCondition(field string, value any) (*qdrant.Condition, error) {
	switch v := value.(type) {
	case []string:
		if len(v) == 0 {
			return nil, nil
		}

		return qdrant.NewMatchKeywords(field, v...), nil
	case []int:
		if len(v) == 0 {
			return nil, nil
		}
		ints := make([]int64, len(v))
		for i, val := range v {
			ints[i] = int64(val)
		}
		return c.newIntegersCondition(field, ints), nil

	case []int64:
		if len(v) == 0 {
			return nil, nil
		}
		return c.newIntegersCondition(field, v), nil

	case []any:
		return c.convertInConditionFromAnySlice(field, v)

	default:
		// Use reflection as fallback for other slice types
		return c.convertInConditionReflect(field, value)
	}
}

func (c *qdrantFilterConverter) newIntegersCondition(field string, ints []int64) *qdrant.Condition {
	return qdrant.NewMatchInts(field, ints...)
}

func (c *qdrantFilterConverter) convertInConditionFromAnySlice(field string, values []any) (*qdrant.Condition, error) {
	if len(values) == 0 {
		return nil, nil
	}

	// Check first element to determine type
	switch values[0].(type) {
	case string:
		strs := make([]string, len(values))
		for i, val := range values {
			if s, ok := val.(string); ok {
				strs[i] = s
			}
		}
		return qdrant.NewMatchKeywords(field, strs...), nil

	case int, int32, int64:
		ints := make([]int64, len(values))
		for i, val := range values {
			switch v := val.(type) {
			case int:
				ints[i] = int64(v)
			case int32:
				ints[i] = int64(v)
			case int64:
				ints[i] = v
			}
		}
		return c.newIntegersCondition(field, ints), nil

	default:
		// Fallback to OR filter for mixed/other types
		conditions := make([]*qdrant.Condition, len(values))
		for i, val := range values {
			conditions[i] = c.newMatchCondition(field, val)
		}
		return qdrant.NewFilterAsCondition(&qdrant.Filter{Should: conditions}), nil
	}
}

func (c *qdrantFilterConverter) convertInConditionReflect(field string, value any) (*qdrant.Condition, error) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice {
		return nil, fmt.Errorf("%w: in operator requires array value", ErrInvalidFilter)
	}

	if v.Len() == 0 {
		return nil, nil
	}

	// Convert to []any and use the typed handler
	values := make([]any, v.Len())
	for i := 0; i < v.Len(); i++ {
		values[i] = v.Index(i).Interface()
	}
	return c.convertInConditionFromAnySlice(field, values)
}

func (c *qdrantFilterConverter) newMatchCondition(field string, value any) *qdrant.Condition {
	switch v := value.(type) {
	case string:
		return qdrant.NewMatchKeyword(field, v)
	case int:
		return qdrant.NewMatchInt(field, int64(v))
	case int32:
		return qdrant.NewMatchInt(field, int64(v))
	case int64:
		return qdrant.NewMatchInt(field, v)
	case bool:
		return qdrant.NewMatchBool(field, v)
	case float32:
		f := float64(v)
		return c.newRangeCondition(field, &f, nil, &f, nil)
	case float64:
		return c.newRangeCondition(field, &v, nil, &v, nil)
	default:
		return qdrant.NewMatchText(field, fmt.Sprintf("%v", v))
	}
}

func (c *qdrantFilterConverter) newRangeCondition(field string, gte, gt, lte, lt *float64) *qdrant.Condition {
	return qdrant.NewRange(field, &qdrant.Range{
		Gte: gte,
		Gt:  gt,
		Lte: lte,
		Lt:  lt,
	})
}

func (c *qdrantFilterConverter) resolveField(field string) string {
	if actualField, ok := strings.CutPrefix(field, source.MetadataFieldPrefix); ok {
		return c.metadataFieldName + "." + actualField
	}
	return field
}

func toFloat64Ptr(value any) *float64 {
	if value == nil {
		return nil
	}

	var f float64
	switch v := value.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	case int:
		f = float64(v)
	case int32:
		f = float64(v)
	case int64:
		f = float64(v)
	case uint:
		f = float64(v)
	case uint32:
		f = float64(v)
	case uint64:
		f = float64(v)
	default:
		return nil
	}
	return &f
}
