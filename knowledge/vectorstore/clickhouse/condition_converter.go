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
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// ErrInvalidFilter reports that a caller-supplied filter cannot be turned into
// a ClickHouse predicate. Every filter validation failure wraps it, so callers
// can match the whole family with errors.Is.
//
// The individual reasons are unexported: they are implementation details of
// this converter, and only the fact that the filter is invalid is a stable
// contract.
var ErrInvalidFilter = errors.New("clickhouse: invalid filter")

// Filter validation reasons, each wrapped by ErrInvalidFilter.
var (
	errUnsupportedOperator = errors.New("filter operator is not supported by ClickHouse SQL")
	errEmptyValueArray     = errors.New("in/not in value must be a non-empty array")
	errFieldNotAllowed     = errors.New("filter field is not in the allowed list")
	errFieldNameInvalid    = errors.New("filter field name is not a valid identifier")
	errFieldRequired       = errors.New("filter field is required")
)

// invalidFilter wraps reason, and an optional detail, as an ErrInvalidFilter so
// callers can match every filter validation failure with errors.Is.
func invalidFilter(reason error, detail string) error {
	if detail == "" {
		return fmt.Errorf("%w: %w", ErrInvalidFilter, reason)
	}
	return fmt.Errorf("%w: %w: %s", ErrInvalidFilter, reason, detail)
}

// safeFilterIdentifier restricts field names to a conservative ASCII identifier subset.
var safeFilterIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateFieldName checks the field name syntax and, when provided, its allowlist membership.
func validateFieldName(field string, allowedFields map[string]struct{}) error {
	if field == "" {
		return invalidFilter(errFieldRequired, "")
	}
	if !safeFilterIdentifier.MatchString(field) {
		return invalidFilter(errFieldNameInvalid, fmt.Sprintf("%q", field))
	}
	if allowedFields != nil {
		if _, ok := allowedFields[field]; !ok {
			return invalidFilter(errFieldNotAllowed, fmt.Sprintf("%q", field))
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
		return "", invalidFilter(errUnsupportedOperator, fmt.Sprintf("%q", cond.Operator))
	}
}

// formatBinary builds a binary comparison such as `field = 'val'` or `field > 1000`.
func formatBinary(field, op string, value any, allowedFields map[string]struct{}) (string, error) {
	if err := validateFieldName(field, allowedFields); err != nil {
		return "", err
	}
	lit, err := formatLiteral(value)
	if err != nil {
		return "", invalidFilter(err, "")
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
		return "", invalidFilter(err, "")
	}
	if len(items) == 0 {
		return "", invalidFilter(errEmptyValueArray, "")
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		lit, err := formatLiteral(item)
		if err != nil {
			return "", invalidFilter(err, "")
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
		return "", invalidFilter(err, "")
	}
	if len(items) != 2 {
		return "", invalidFilter(
			fmt.Errorf("between value must be a 2-element array, got %d elements", len(items)), "")
	}
	lo, err := formatLiteral(items[0])
	if err != nil {
		return "", invalidFilter(err, "")
	}
	hi, err := formatLiteral(items[1])
	if err != nil {
		return "", invalidFilter(err, "")
	}
	return fmt.Sprintf("%s BETWEEN %s AND %s", field, lo, hi), nil
}

// formatLogical joins subexpressions with AND or OR and preserves their precedence with parentheses.
func formatLogical(value any, op string, allowedFields map[string]struct{}) (string, error) {
	conds, err := toConditionSlice(value)
	if err != nil {
		return "", invalidFilter(err, "")
	}
	if len(conds) == 0 {
		return "", invalidFilter(fmt.Errorf("%s requires at least one sub-condition", op), "")
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

// dateTimeLiteralLayout formats time.Time values for the DateTime64(6) columns
// the store creates. The layout is fixed to six fractional digits because
// ClickHouse requires a constant scale in toDateTime64.
const dateTimeLiteralLayout = "2006-01-02 15:04:05.000000"

// formatLiteral converts a value into a ClickHouse SQL literal.
// Strings are single-quoted; booleans become true/false; numbers are emitted
// directly; time.Time becomes a toDateTime64 call.
func formatLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("clickhouse: filter literal must not be nil")
	case string:
		return quoteString(x), nil
	case time.Time:
		// Times are normalized to UTC so a literal does not depend on the
		// caller's location, which keeps comparisons and tests deterministic.
		return fmt.Sprintf("toDateTime64('%s', 6, 'UTC')", x.UTC().Format(dateTimeLiteralLayout)), nil
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
				return nil, fmt.Errorf("logical operator value element must be *UniversalFilterCondition, got %T", item)
			}
			out = append(out, c)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("logical operator value must be []*UniversalFilterCondition, got %T", v)
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

// metadataMapToExpr builds a predicate joining metadata equality comparisons
// with AND. Values are inlined as literals, so the predicate carries no
// arguments.
func (vs *VectorStore) metadataMapToExpr(m map[string]any) (predicate, error) {
	if len(m) == 0 {
		return predicate{}, nil
	}
	allowed := vs.allowedFilterFields()
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if err := validateFieldName(k, allowed); err != nil {
			return predicate{}, err
		}
		lit, err := formatLiteral(v)
		if err != nil {
			return predicate{}, invalidFilter(err, fmt.Sprintf("metadata %q", k))
		}
		parts = append(parts, fmt.Sprintf("%s = %s", k, lit))
	}
	return literalPredicate(joinAnd(parts...)), nil
}

// buildFilterFromSearch combines SearchFilter.Metadata and FilterCondition into
// one predicate. It is empty when the filter carries no constraint.
func (vs *VectorStore) buildFilterFromSearch(f *vectorstore.SearchFilter) (predicate, error) {
	if f == nil {
		return predicate{}, nil
	}
	var parts []predicate
	if len(f.Metadata) > 0 {
		md, err := vs.metadataMapToExpr(f.Metadata)
		if err != nil {
			return predicate{}, err
		}
		parts = append(parts, md)
	}
	if f.FilterCondition != nil {
		expr, err := buildFilterExpr(f.FilterCondition, vs.allowedFilterFields())
		if err != nil {
			return predicate{}, err
		}
		parts = append(parts, literalPredicate(expr))
	}
	if len(parts) == 0 {
		return predicate{}, nil
	}
	return parts[0].and(parts[1:]...), nil
}

// joinList joins fragments with ", ".
func joinList(parts []string) string {
	return strings.Join(parts, ", ")
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
