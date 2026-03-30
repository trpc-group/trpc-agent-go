//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlitevec

import (
	"fmt"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// promotedColumns lists the columns that are stored directly in the vec0 table
// and can be compared without going through the metadata index table.
var promotedColumns = map[string]bool{
	"id":         true,
	"name":       true,
	"content":    true,
	"created_at": true,
	"updated_at": true,
}

// sqlFragment holds a SQL condition fragment with its bound parameters.
type sqlFragment struct {
	sql    string
	params []any
}

// filterBuilder converts SearchFilter and UniversalFilterCondition into SQL
// WHERE clauses.
type filterBuilder struct {
	vecTable  string
	metaTable string
}

func newFilterBuilder(vecTable, metaTable string) *filterBuilder {
	return &filterBuilder{
		vecTable:  vecTable,
		metaTable: metaTable,
	}
}

// buildFilterClauses converts a vectorstore.SearchFilter into SQL fragments.
// It returns a combined WHERE fragment and a list of bound parameters.
func (fb *filterBuilder) buildFilterClauses(
	ids []string,
	metadata map[string]any,
	cond *searchfilter.UniversalFilterCondition,
) (string, []any, error) {
	var parts []string
	var params []any

	// ID filter.
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			params = append(params, id)
		}
		parts = append(parts, fmt.Sprintf("v.id IN (%s)", strings.Join(placeholders, ",")))
	}

	// Simple metadata filter (equality match).
	for key, value := range metadata {
		frag := fb.buildEqualityFilter(key, value)
		parts = append(parts, frag.sql)
		params = append(params, frag.params...)
	}

	// Universal filter condition.
	if cond != nil {
		frag, err := fb.convertCondition(cond)
		if err != nil {
			return "", nil, err
		}
		if frag.sql != "" {
			parts = append(parts, frag.sql)
			params = append(params, frag.params...)
		}
	}

	if len(parts) == 0 {
		return "", nil, nil
	}
	return strings.Join(parts, " AND "), params, nil
}

// buildEqualityFilter generates an equality condition for a given key.
func (fb *filterBuilder) buildEqualityFilter(key string, value any) sqlFragment {
	col := fb.resolveColumn(key)
	if col != "" {
		return sqlFragment{
			sql:    fmt.Sprintf("v.%s = ?", col),
			params: []any{value},
		}
	}

	// Metadata key — use EXISTS subquery on the metadata index table.
	metaKey := stripMetadataPrefix(key)
	return fb.metadataExistsEq(metaKey, value)
}

// metadataExistsEq builds an EXISTS subquery checking a metadata key equals
// the given value using the appropriate typed column.
func (fb *filterBuilder) metadataExistsEq(key string, value any) sqlFragment {
	col, param := typedMetadataColumn(value)
	sql := fmt.Sprintf(
		`EXISTS (SELECT 1 FROM %s m WHERE m.doc_id = v.id AND m.key = ? AND m.%s = ?)`,
		fb.metaTable, col,
	)
	return sqlFragment{sql: sql, params: []any{key, param}}
}

// convertCondition recursively converts a UniversalFilterCondition into SQL.
func (fb *filterBuilder) convertCondition(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	if cond == nil {
		return sqlFragment{}, nil
	}

	switch cond.Operator {
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return fb.convertLogical(cond)
	case searchfilter.OperatorEqual, searchfilter.OperatorNotEqual,
		searchfilter.OperatorGreaterThan, searchfilter.OperatorGreaterThanOrEqual,
		searchfilter.OperatorLessThan, searchfilter.OperatorLessThanOrEqual:
		return fb.convertComparison(cond)
	case searchfilter.OperatorIn, searchfilter.OperatorNotIn:
		return fb.convertIn(cond)
	case searchfilter.OperatorLike, searchfilter.OperatorNotLike:
		return fb.convertLike(cond)
	case searchfilter.OperatorBetween:
		return fb.convertBetween(cond)
	default:
		return sqlFragment{}, fmt.Errorf("unsupported operator: %s", cond.Operator)
	}
}

// convertLogical handles AND / OR operators.
func (fb *filterBuilder) convertLogical(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	children, ok := cond.Value.([]*searchfilter.UniversalFilterCondition)
	if !ok {
		return sqlFragment{}, fmt.Errorf("logical operator %s: value must be []*UniversalFilterCondition", cond.Operator)
	}

	var parts []string
	var params []any
	for _, child := range children {
		frag, err := fb.convertCondition(child)
		if err != nil {
			return sqlFragment{}, err
		}
		if frag.sql != "" {
			parts = append(parts, "("+frag.sql+")")
			params = append(params, frag.params...)
		}
	}

	if len(parts) == 0 {
		return sqlFragment{}, nil
	}

	op := " AND "
	if cond.Operator == searchfilter.OperatorOr {
		op = " OR "
	}
	return sqlFragment{
		sql:    "(" + strings.Join(parts, op) + ")",
		params: params,
	}, nil
}

// convertComparison handles eq, ne, gt, gte, lt, lte operators.
func (fb *filterBuilder) convertComparison(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	if cond.Field == "" {
		return sqlFragment{}, fmt.Errorf("comparison operator %s: field is required", cond.Operator)
	}

	sqlOp := comparisonSQLOp(cond.Operator)

	col := fb.resolveColumn(cond.Field)
	if col != "" {
		return sqlFragment{
			sql:    fmt.Sprintf("v.%s %s ?", col, sqlOp),
			params: []any{cond.Value},
		}, nil
	}

	// Metadata path.
	metaKey := stripMetadataPrefix(cond.Field)
	typedCol, param := typedMetadataColumn(cond.Value)
	sql := fmt.Sprintf(
		`EXISTS (SELECT 1 FROM %s m WHERE m.doc_id = v.id AND m.key = ? AND m.%s %s ?)`,
		fb.metaTable, typedCol, sqlOp,
	)
	return sqlFragment{sql: sql, params: []any{metaKey, param}}, nil
}

// convertIn handles IN / NOT IN operators.
func (fb *filterBuilder) convertIn(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	if cond.Field == "" {
		return sqlFragment{}, fmt.Errorf("in operator: field is required")
	}

	rv := reflect.ValueOf(cond.Value)
	if rv.Kind() != reflect.Slice || rv.Len() == 0 {
		return sqlFragment{}, fmt.Errorf("in operator: value must be a non-empty slice")
	}

	placeholders := make([]string, rv.Len())
	var params []any
	for i := 0; i < rv.Len(); i++ {
		placeholders[i] = "?"
		params = append(params, rv.Index(i).Interface())
	}
	inList := strings.Join(placeholders, ",")

	not := ""
	if cond.Operator == searchfilter.OperatorNotIn {
		not = "NOT "
	}

	col := fb.resolveColumn(cond.Field)
	if col != "" {
		return sqlFragment{
			sql:    fmt.Sprintf("v.%s %sIN (%s)", col, not, inList),
			params: params,
		}, nil
	}

	// Metadata path.
	metaKey := stripMetadataPrefix(cond.Field)
	typedCol, _ := typedMetadataColumn(rv.Index(0).Interface())
	allParams := []any{metaKey}
	for i := 0; i < rv.Len(); i++ {
		_, param := typedMetadataColumn(rv.Index(i).Interface())
		allParams = append(allParams, param)
	}
	existsNot := "EXISTS"
	if cond.Operator == searchfilter.OperatorNotIn {
		existsNot = "NOT EXISTS"
	}
	sql := fmt.Sprintf(
		`%s (SELECT 1 FROM %s m WHERE m.doc_id = v.id AND m.key = ? AND m.%s IN (%s))`,
		existsNot, fb.metaTable, typedCol, inList,
	)
	return sqlFragment{sql: sql, params: allParams}, nil
}

// convertLike handles LIKE / NOT LIKE operators.
func (fb *filterBuilder) convertLike(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	if cond.Field == "" {
		return sqlFragment{}, fmt.Errorf("like operator: field is required")
	}

	not := ""
	if cond.Operator == searchfilter.OperatorNotLike {
		not = "NOT "
	}

	pattern := fmt.Sprintf("%%%v%%", cond.Value)

	col := fb.resolveColumn(cond.Field)
	if col != "" {
		return sqlFragment{
			sql:    fmt.Sprintf("v.%s %sLIKE ?", col, not),
			params: []any{pattern},
		}, nil
	}

	metaKey := stripMetadataPrefix(cond.Field)
	existsNot := "EXISTS"
	if cond.Operator == searchfilter.OperatorNotLike {
		existsNot = "NOT EXISTS"
	}
	sql := fmt.Sprintf(
		`%s (SELECT 1 FROM %s m WHERE m.doc_id = v.id AND m.key = ? AND m.value_text LIKE ?)`,
		existsNot, fb.metaTable,
	)
	return sqlFragment{sql: sql, params: []any{metaKey, pattern}}, nil
}

// convertBetween handles BETWEEN operators.
func (fb *filterBuilder) convertBetween(cond *searchfilter.UniversalFilterCondition) (sqlFragment, error) {
	if cond.Field == "" {
		return sqlFragment{}, fmt.Errorf("between operator: field is required")
	}

	rv := reflect.ValueOf(cond.Value)
	if rv.Kind() != reflect.Slice || rv.Len() != 2 {
		return sqlFragment{}, fmt.Errorf("between operator: value must be a slice of length 2")
	}
	low := rv.Index(0).Interface()
	high := rv.Index(1).Interface()

	col := fb.resolveColumn(cond.Field)
	if col != "" {
		return sqlFragment{
			sql:    fmt.Sprintf("v.%s BETWEEN ? AND ?", col),
			params: []any{low, high},
		}, nil
	}

	metaKey := stripMetadataPrefix(cond.Field)
	sql := fmt.Sprintf(
		`EXISTS (SELECT 1 FROM %s m WHERE m.doc_id = v.id AND m.key = ? AND m.value_num BETWEEN ? AND ?)`,
		fb.metaTable,
	)
	return sqlFragment{sql: sql, params: []any{metaKey, low, high}}, nil
}

// resolveColumn checks if the field maps to a promoted vec0 column.
// Returns the column name or empty string if metadata.
func (fb *filterBuilder) resolveColumn(field string) string {
	// An explicit metadata.* path must always target metadata storage,
	// even if the key name collides with a promoted vec0 column.
	if strings.HasPrefix(field, source.MetadataFieldPrefix) {
		return ""
	}

	// Strip "metadata." prefix — if the remaining key is a promoted column, use it.
	raw := stripMetadataPrefix(field)
	if promotedColumns[raw] {
		return raw
	}
	// If the field has no prefix and is a promoted column, use it directly.
	if promotedColumns[field] {
		return field
	}
	return ""
}

// stripMetadataPrefix removes the "metadata." prefix if present.
func stripMetadataPrefix(field string) string {
	return strings.TrimPrefix(field, source.MetadataFieldPrefix)
}

// typedMetadataColumn returns the appropriate column name and parameter
// for a metadata value based on its Go type.
func typedMetadataColumn(value any) (string, any) {
	switch v := value.(type) {
	case bool:
		boolInt := int64(0)
		if v {
			boolInt = 1
		}
		return "value_bool", boolInt
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return "value_num", v
	case string:
		return "value_text", v
	default:
		return "value_text", fmt.Sprintf("%v", v)
	}
}

// comparisonSQLOp maps a searchfilter operator to a SQL comparison operator.
func comparisonSQLOp(op string) string {
	switch op {
	case searchfilter.OperatorEqual:
		return "="
	case searchfilter.OperatorNotEqual:
		return "!="
	case searchfilter.OperatorGreaterThan:
		return ">"
	case searchfilter.OperatorGreaterThanOrEqual:
		return ">="
	case searchfilter.OperatorLessThan:
		return "<"
	case searchfilter.OperatorLessThanOrEqual:
		return "<="
	default:
		return "="
	}
}
