//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Errors returned while converting generic filters to ClickHouse SQL predicates.
var (
	// ErrUnsupportedOperator indicates that ClickHouse SQL does not support the requested operator.
	ErrUnsupportedOperator = fmt.Errorf("clickhouse: filter operator is not supported by ClickHouse SQL")
	// ErrEmptyValueArray indicates that an IN or NOT IN value is empty.
	ErrEmptyValueArray = fmt.Errorf("clickhouse: in/not in value must be a non-empty array")
	// ErrFieldNotAllowed indicates that a field is not registered as a filterable field.
	ErrFieldNotAllowed = fmt.Errorf("clickhouse: filter field is not in the allowed list")
	// ErrFieldNameInvalid indicates that a field name is not a valid identifier.
	ErrFieldNameInvalid = fmt.Errorf("clickhouse: filter field name is not a valid identifier")
)

// safeFilterIdentifier restricts field names to a conservative ASCII identifier subset.
var safeFilterIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateFieldName checks the field name syntax and, when provided, its allowlist membership.
func validateFieldName(field string, allowedFields map[string]struct{}) error {
	if field == "" {
		return fmt.Errorf("clickhouse: filter field is required")
	}
	if !safeFilterIdentifier.MatchString(field) {
		return fmt.Errorf("%w: %q", ErrFieldNameInvalid, field)
	}
	if allowedFields != nil {
		if _, ok := allowedFields[field]; !ok {
			return fmt.Errorf("%w: %q", ErrFieldNotAllowed, field)
		}
	}
	return nil
}

// buildFilterExpr converts a UniversalFilterCondition into a ClickHouse SQL
// predicate. An empty string represents an empty filter.
func buildFilterExpr(cond *searchfilter.UniversalFilterCondition, allowedFields map[string]struct{}) (string, error) {
	if cond == nil {
		return "", nil
	}
	switch strings.ToLower(cond.Operator) {
	case searchfilter.OperatorEqual:
		return formatBinary(cond.Field, "=", cond.Value, allowedFields)
	case searchfilter.OperatorNotEqual:
		return formatBinary(cond.Field, "!=", cond.Value, allowedFields)
	case searchfilter.OperatorGreaterThan:
		return formatBinary(cond.Field, ">", cond.Value, allowedFields)
	case searchfilter.OperatorGreaterThanOrEqual:
		return formatBinary(cond.Field, ">=", cond.Value, allowedFields)
	case searchfilter.OperatorLessThan:
		return formatBinary(cond.Field, "<", cond.Value, allowedFields)
	case searchfilter.OperatorLessThanOrEqual:
		return formatBinary(cond.Field, "<=", cond.Value, allowedFields)
	case searchfilter.OperatorIn:
		return formatIn(cond.Field, "IN", cond.Value, allowedFields)
	case searchfilter.OperatorNotIn:
		return formatIn(cond.Field, "NOT IN", cond.Value, allowedFields)
	case searchfilter.OperatorLike:
		return formatBinary(cond.Field, "LIKE", cond.Value, allowedFields)
	case searchfilter.OperatorNotLike:
		return formatBinary(cond.Field, "NOT LIKE", cond.Value, allowedFields)
	case searchfilter.OperatorBetween:
		return formatBetween(cond.Field, cond.Value, allowedFields)
	case searchfilter.OperatorAnd:
		return formatLogical(cond.Value, "AND", allowedFields)
	case searchfilter.OperatorOr:
		return formatLogical(cond.Value, "OR", allowedFields)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedOperator, cond.Operator)
	}
}

// formatBinary builds a binary comparison such as `field = 'val'` or `field > 1000`.
func formatBinary(field, op string, value any, allowedFields map[string]struct{}) (string, error) {
	if err := validateFieldName(field, allowedFields); err != nil {
		return "", err
	}
	lit, err := formatLiteral(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s", field, op, lit), nil
}

// formatIn builds a set expression such as `field IN ('a', 'b')` or `field NOT IN (1, 2, 3)`.
func formatIn(field, op string, value any, allowedFields map[string]struct{}) (string, error) {
	if err := validateFieldName(field, allowedFields); err != nil {
		return "", err
	}
	items, err := toAnySlice(value)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", ErrEmptyValueArray
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		lit, err := formatLiteral(item)
		if err != nil {
			return "", err
		}
		parts = append(parts, lit)
	}
	return fmt.Sprintf("%s %s (%s)", field, op, strings.Join(parts, ", ")), nil
}

// formatBetween builds a range expression such as `field BETWEEN 1 AND 10`.
func formatBetween(field string, value any, allowedFields map[string]struct{}) (string, error) {
	if err := validateFieldName(field, allowedFields); err != nil {
		return "", err
	}
	items, err := toAnySlice(value)
	if err != nil {
		return "", err
	}
	if len(items) != 2 {
		return "", fmt.Errorf("clickhouse: between value must be a 2-element array, got %d elements", len(items))
	}
	lo, err := formatLiteral(items[0])
	if err != nil {
		return "", err
	}
	hi, err := formatLiteral(items[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s BETWEEN %s AND %s", field, lo, hi), nil
}

// formatLogical joins subexpressions with AND or OR and preserves their precedence with parentheses.
func formatLogical(value any, op string, allowedFields map[string]struct{}) (string, error) {
	conds, err := toConditionSlice(value)
	if err != nil {
		return "", err
	}
	if len(conds) == 0 {
		return "", fmt.Errorf("clickhouse: %s requires at least one sub-condition", op)
	}
	parts := make([]string, 0, len(conds))
	for _, sub := range conds {
		s, err := buildFilterExpr(sub, allowedFields)
		if err != nil {
			return "", err
		}
		if s == "" {
			continue
		}
		parts = append(parts, "("+s+")")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, " "+op+" "), nil
}

// formatLiteral converts a value into a ClickHouse SQL literal.
// Strings are single-quoted; booleans become true/false; numbers are emitted directly.
func formatLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("clickhouse: filter literal must not be nil")
	case string:
		return quoteString(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("clickhouse: unsupported filter literal type %T", v)
	}
}

// quoteString wraps s in single quotes and escapes embedded backslashes and
// single quotes. Backslashes must be escaped first because ClickHouse also
// honors backslash escapes, so a value like `\' OR 1=1 --` could otherwise
// escape the literal.
func quoteString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + s + "'"
}

// toAnySlice normalizes an IN/NOT IN/BETWEEN value to []any.
func toAnySlice(v any) ([]any, error) {
	switch x := v.(type) {
	case []any:
		return x, nil
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out, nil
	case []int:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, nil
	case []int64:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, nil
	case []uint64:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, nil
	case []float64:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("clickhouse: in/not in value must not be nil")
	default:
		return []any{x}, nil
	}
}

// toConditionSlice normalizes a logical operator value to []*UniversalFilterCondition.
func toConditionSlice(v any) ([]*searchfilter.UniversalFilterCondition, error) {
	switch x := v.(type) {
	case []*searchfilter.UniversalFilterCondition:
		return x, nil
	case []searchfilter.UniversalFilterCondition:
		out := make([]*searchfilter.UniversalFilterCondition, len(x))
		for i := range x {
			out[i] = &x[i]
		}
		return out, nil
	case []any:
		out := make([]*searchfilter.UniversalFilterCondition, 0, len(x))
		for _, item := range x {
			c, ok := item.(*searchfilter.UniversalFilterCondition)
			if !ok {
				return nil, fmt.Errorf("clickhouse: logical operator value element must be *UniversalFilterCondition, got %T", item)
			}
			out = append(out, c)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("clickhouse: logical operator value must be []*UniversalFilterCondition, got %T", v)
	}
}

// allowedFilterFields builds the filter-field allowlist from fields explicitly
// registered through WithFilterFields and the four built-in fields name,
// content, created_at, and updated_at.
func (vs *VectorStore) allowedFilterFields() map[string]struct{} {
	o := vs.option
	m := make(map[string]struct{}, len(o.filterFields)+5)
	m[o.nameFieldName] = struct{}{}
	m[o.contentFieldName] = struct{}{}
	m[o.createdAtFieldName] = struct{}{}
	m[o.updatedAtFieldName] = struct{}{}
	for _, spec := range o.filterFields {
		m[spec.Name] = struct{}{}
	}
	return m
}

// metadataMapToExpr builds a ClickHouse SQL predicate by joining metadata
// equality predicates with AND.
func (vs *VectorStore) metadataMapToExpr(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	allowed := vs.allowedFilterFields()
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if err := validateFieldName(k, allowed); err != nil {
			return "", err
		}
		lit, err := formatLiteral(v)
		if err != nil {
			return "", fmt.Errorf("clickhouse: format metadata %q: %w", k, err)
		}
		parts = append(parts, fmt.Sprintf("%s = %s", k, lit))
	}
	return joinAnd(parts...), nil
}

// buildFilterFromSearch combines SearchFilter.Metadata and FilterCondition into
// a single SQL predicate. It returns "" when the filter is empty.
func (vs *VectorStore) buildFilterFromSearch(f *vectorstore.SearchFilter) (string, error) {
	if f == nil {
		return "", nil
	}
	var parts []string
	if len(f.Metadata) > 0 {
		expr, err := vs.metadataMapToExpr(f.Metadata)
		if err != nil {
			return "", err
		}
		if expr != "" {
			parts = append(parts, expr)
		}
	}
	if f.FilterCondition != nil {
		expr, err := buildFilterExpr(f.FilterCondition, vs.allowedFilterFields())
		if err != nil {
			return "", err
		}
		if expr != "" {
			parts = append(parts, expr)
		}
	}
	return joinAnd(parts...), nil
}

// joinAnd joins non-empty expression clauses with AND. Each clause keeps its
// surrounding parentheses, including when only one clause remains, so callers
// can safely AND-append the result to another predicate even if the clause
// contains a top-level OR.
func joinAnd(exprs ...string) string {
	parts := make([]string, 0, len(exprs))
	for _, e := range exprs {
		if e = strings.TrimSpace(e); e != "" {
			parts = append(parts, "("+e+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ")
}
