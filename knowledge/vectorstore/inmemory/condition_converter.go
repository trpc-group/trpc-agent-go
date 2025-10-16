//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory vector store implementation.
package inmemory

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	idField        = "id"
	nameField      = "name"
	contentField   = "content"
	createdAtField = "created_at"
	updatedAtField = "updated_at"
	metadataField  = "metadata"

	valueTypeString = "string"
	valueTypeNumber = "number"
	valueTypeBool   = "bool"
	valueTypeTime   = "time"
)

var comparisonFields = map[string]bool{
	idField:        true,
	nameField:      true,
	contentField:   true,
	metadataField:  true,
	createdAtField: true,
	updatedAtField: true,
}

type comparisonFunc func(doc *document.Document) bool

// inmemoryConverter converts a filter condition to a in-memory vector query.
type inmemoryConverter struct{}

// Convert converts a filter condition to a postgres vector query filter.
func (c *inmemoryConverter) Convert(cond *searchfilter.UniversalFilterCondition) (comparisonFunc, error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Errorf("panic in pgVectorConverter Convert: %v\n%s", r, string(stack))
		}
	}()
	if cond == nil {
		return nil, nil
	}

	return c.convertCondition(cond)
}

func (c *inmemoryConverter) convertCondition(cond *searchfilter.UniversalFilterCondition) (comparisonFunc, error) {
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

func (c *inmemoryConverter) buildLogicalCondition(cond *searchfilter.UniversalFilterCondition) (comparisonFunc, error) {
	conds, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return nil, fmt.Errorf("invalid logical condition: value must be of type []*searchfilter.UniversalFilterCondition: %v", cond.Value)
	}
	var condFunc comparisonFunc
	for _, child := range conds {
		childFunc, err := c.convertCondition(child)
		if err != nil {
			return nil, err
		}
		if childFunc == nil {
			continue
		}
		if condFunc == nil {
			condFunc = childFunc
			continue
		}

		condFunc = func(doc *document.Document) bool {
			preCheck := condFunc(doc)
			// short circuit
			if cond.Operator == searchfilter.OperatorOr || preCheck {
				return true
			} else if cond.Operator == searchfilter.OperatorAnd && !preCheck {
				return false
			}

			return childFunc(doc)
		}
	}

	return condFunc, nil
}

func (c *inmemoryConverter) buildInCondition(cond *searchfilter.UniversalFilterCondition) (comparisonFunc, error) {
	if !isValidField(cond.Field) {
		return nil, fmt.Errorf(`field name only be in ["id", "name", "content", "created_at", "updated_at", "metadata.*"]: %s`, cond.Field)
	}
	if reflect.TypeOf(cond.Value).Kind() != reflect.Slice {
		return nil, fmt.Errorf("in operator requires an array of values")
	}
	s := reflect.ValueOf(cond.Value)
	itemNum := s.Len()
	if itemNum <= 0 {
		return nil, fmt.Errorf("in operator requires an array with at least one value")
	}

	condFunc := func(doc *document.Document) bool {
		docValue, ok := fieldValue(doc, cond.Field)
		if !ok {
			log.Errorf("field %s not found in document", cond.Field)
			return false
		}

		for i := 0; i < itemNum; i++ {
			if reflect.DeepEqual(docValue, s.Index(i).Interface()) {
				return true
			}
		}
		return false
	}
	return condFunc, nil
}

func (c *inmemoryConverter) buildComparisonCondition(cond *searchfilter.UniversalFilterCondition) (comparisonFunc, error) {
	if !isValidField(cond.Field) {
		return nil, fmt.Errorf(`field name only be in ["id", "name", "content", "created_at", "updated_at", "metadata.*"]: %s`, cond.Field)
	}

	condFunc := func(doc *document.Document) bool {
		docValue, ok := fieldValue(doc, cond.Field)
		if !ok {
			log.Errorf("field %s not found in document", cond.Field)
			return false
		}

		switch valueType(cond.Value) {
		case valueTypeString:
			return compareString(docValue, cond.Value, cond.Operator)
		case valueTypeNumber:
			compareNumber(docValue, cond.Value, cond.Operator)
		case valueTypeTime:
			return compareTime(docValue, cond.Value, cond.Operator)
		case valueTypeBool:
			return compareBool(docValue, cond.Value, cond.Operator)
		default:
			log.Errorf("this value is unsupported comparison operator: %v - %v", cond.Value, cond.Operator)
		}
		return false
	}
	return condFunc, nil
}

func valueType(value any) string {
	vKind := reflect.ValueOf(value).Kind()
	switch vKind {
	case reflect.String:
		return valueTypeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return valueTypeNumber
	case reflect.Bool:
		return valueTypeBool
	default:
		if _, ok := value.(time.Time); ok {
			return valueTypeTime
		}
	}
	return ""
}

func compareString(docValue any, condValue any, operator string) bool {
	docStr, ok1 := docValue.(string)
	condStr, ok2 := condValue.(string)
	if !ok1 || !ok2 {
		log.Errorf("string comparison requires string values: %v, %v", docValue, condValue)
		return false
	}

	switch operator {
	case searchfilter.OperatorEqual:
		return docStr == condStr
	case searchfilter.OperatorNotEqual:
		return docStr != condStr
	case searchfilter.OperatorGreaterThan:
		return docStr > condStr
	case searchfilter.OperatorGreaterThanOrEqual:
		return docStr >= condStr
	case searchfilter.OperatorLessThan:
		return docStr < condStr
	case searchfilter.OperatorLessThanOrEqual:
		return docStr <= condStr
	default:
		log.Errorf("this string comparison operator is unsupported: %s", operator)
		return false
	}
}

func compareBool(docValue any, condValue any, operator string) bool {
	docBool, ok1 := docValue.(bool)
	condBool, ok2 := condValue.(bool)
	if !ok1 || !ok2 {
		log.Errorf("bool comparison requires bool values: %v, %v", docValue, condValue)
		return false
	}

	switch operator {
	case searchfilter.OperatorEqual:
		return docBool == condBool
	case searchfilter.OperatorNotEqual:
		return docBool != condBool
	default:
		log.Errorf("this bool comparison operator is unsupported: %s", operator)
		return false
	}
}

func compareTime(docValue any, condValue any, operator string) bool {
	docTime, ok1 := docValue.(time.Time)
	condTime, ok2 := condValue.(time.Time)
	if !ok1 || !ok2 {
		log.Errorf("time comparison requires time.Time values: %v, %v", docValue, condValue)
		return false
	}

	switch operator {
	case searchfilter.OperatorGreaterThan:
		return docTime.After(condTime)
	case searchfilter.OperatorGreaterThanOrEqual:
		return docTime.After(condTime) || docTime.Equal(condTime)
	case searchfilter.OperatorLessThan:
		return docTime.Before(condTime)
	case searchfilter.OperatorLessThanOrEqual:
		return docTime.Before(condTime) || docTime.Equal(condTime)
	default:
		log.Errorf("this time comparison operator is unsupported: %s", operator)
		return false
	}
}

func compareNumber(docValue any, condValue any, operator string) bool {
	docNum, ok1 := toFloat64(docValue)
	condNum, ok2 := toFloat64(condValue)
	if !ok1 || !ok2 {
		log.Errorf("number comparison requires numeric values: %v, %v", docValue, condValue)
		return false
	}

	switch operator {
	case searchfilter.OperatorEqual:
		return docNum == condNum
	case searchfilter.OperatorNotEqual:
		return docNum != condNum
	case searchfilter.OperatorGreaterThan:
		return docNum > condNum
	case searchfilter.OperatorGreaterThanOrEqual:
		return docNum >= condNum
	case searchfilter.OperatorLessThan:
		return docNum < condNum
	case searchfilter.OperatorLessThanOrEqual:
		return docNum <= condNum
	default:
		log.Errorf("this number comparison operator is unsupported: %s", operator)
		return false
	}
}

func toFloat64(value any) (float64, bool) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	default:
		return 0, false
	}
}

func isValidField(field string) bool {
	if _, ok := comparisonFields[field]; ok {
		return true
	}

	// metadata fields are prefixed with "metadata."
	if strings.HasPrefix(field, "metadata.") {
		return true
	}
	return false
}

func fieldValue(doc *document.Document, field string) (any, bool) {
	if doc == nil || field == "" {
		return nil, false
	}

	switch field {
	case idField:
		return doc.ID, true
	case nameField:
		return doc.Name, true
	case contentField:
		return doc.Content, true
	case createdAtField:
		return doc.CreatedAt, true
	case updatedAtField:
		return doc.UpdatedAt, true
	default:
		if strings.HasPrefix(field, "metadata.") {
			if doc.Metadata == nil {
				return nil, false
			}
			metaKey := strings.TrimPrefix(field, "metadata.")
			val, ok := doc.Metadata[metaKey]
			return val, ok
		}
	}
	return nil, false
}
