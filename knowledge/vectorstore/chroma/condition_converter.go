//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var (
	errUnsupportedOperator = fmt.Errorf("chroma: filter operator is not supported")
	errEmptyValueArray     = fmt.Errorf("chroma: in/not in value must be a non-empty array")
	errFieldNameInvalid    = fmt.Errorf("chroma: filter field name is invalid")
)

var chromaComparisonOp = map[string]string{
	searchfilter.OperatorEqual:              "$eq",
	searchfilter.OperatorNotEqual:           "$ne",
	searchfilter.OperatorGreaterThan:        "$gt",
	searchfilter.OperatorGreaterThanOrEqual: "$gte",
	searchfilter.OperatorLessThan:           "$lt",
	searchfilter.OperatorLessThanOrEqual:    "$lte",
}

// convertCondition translates a UniversalFilterCondition into a Chroma where clause.
func convertCondition(cond *searchfilter.UniversalFilterCondition) (map[string]any, error) {
	if cond == nil {
		return nil, nil
	}
	if op, ok := chromaComparisonOp[cond.Operator]; ok {
		return comparisonWhere(cond.Field, op, cond.Value)
	}
	switch cond.Operator {
	case searchfilter.OperatorIn:
		return inWhere(cond.Field, "$in", cond.Value)
	case searchfilter.OperatorNotIn:
		return inWhere(cond.Field, "$nin", cond.Value)
	case searchfilter.OperatorAnd, searchfilter.OperatorOr:
		return convertLogicalWhere(cond)
	case searchfilter.OperatorLike, searchfilter.OperatorNotLike, searchfilter.OperatorBetween:
		return nil, fmt.Errorf("%w: %s", errUnsupportedOperator, cond.Operator)
	default:
		return nil, fmt.Errorf("chroma: unknown filter operator %q", cond.Operator)
	}
}

// convertLogicalWhere converts an AND/OR condition tree into a Chroma $and/$or clause.
func convertLogicalWhere(cond *searchfilter.UniversalFilterCondition) (map[string]any, error) {
	op := "$and"
	if cond.Operator == searchfilter.OperatorOr {
		op = "$or"
	}
	subs, err := toConditionSlice(cond.Value)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("chroma: %s requires at least one sub-condition", cond.Operator)
	}
	parts := make([]any, 0, len(subs))
	for _, sub := range subs {
		m, err := convertCondition(sub)
		if err != nil {
			return nil, err
		}
		if len(m) > 0 {
			parts = append(parts, m)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) == 1 {
		if m, ok := parts[0].(map[string]any); ok {
			return m, nil
		}
	}
	return map[string]any{op: parts}, nil
}

// comparisonWhere builds a single-field comparison clause (eq, gt, etc.).
func comparisonWhere(field, op string, value any) (map[string]any, error) {
	field, err := chromaMetadataField(field)
	if err != nil {
		return nil, err
	}
	if field == metaCreatedAt || field == metaUpdatedAt {
		if ts, ok := value.(time.Time); ok {
			value = ts.Unix()
		}
	}
	if op == "$gt" || op == "$gte" || op == "$lt" || op == "$lte" {
		err = validateNumericValue(value)
	} else {
		err = validateEqualityValue(value)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{field: map[string]any{op: value}}, nil
}

// validateEqualityValue rejects nil, non-scalar, and non-finite filter values.
func validateEqualityValue(v any) error {
	if v == nil {
		return fmt.Errorf("chroma: filter equality value must not be nil")
	}
	switch x := v.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return nil
	case json.Number:
		if _, err := x.Float64(); err != nil {
			return fmt.Errorf("chroma: filter value is not a valid number: %w", err)
		}
		return nil
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return fmt.Errorf("chroma: filter value must be finite")
		}
		return nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("chroma: filter value must be finite")
		}
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return fmt.Errorf("chroma: filter equality value must be a scalar, got %T", v)
	default:
		return fmt.Errorf("chroma: filter equality value has unsupported type %T", v)
	}
}

// validateNumericValue ensures a range comparison value is a numeric scalar.
func validateNumericValue(v any) error {
	if err := validateEqualityValue(v); err != nil {
		return err
	}
	switch v.(type) {
	case json.Number,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	default:
		return fmt.Errorf("chroma: range comparison value must be numeric, got %T", v)
	}
}

// inWhere builds a $in or $nin clause from a slice value.
func inWhere(field, op string, value any) (map[string]any, error) {
	field, err := chromaMetadataField(field)
	if err != nil {
		return nil, err
	}
	items, ok := unixTimes(value)
	if !ok || field != metaCreatedAt && field != metaUpdatedAt {
		items, err = toAnySlice(value)
	}
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errEmptyValueArray
	}
	return map[string]any{field: map[string]any{op: items}}, nil
}

// unixTimes converts a []time.Time to Unix seconds for timestamp filters.
func unixTimes(v any) ([]any, bool) {
	times, ok := v.([]time.Time)
	if !ok {
		return nil, false
	}
	out := make([]any, len(times))
	for i, ts := range times {
		out[i] = ts.Unix()
	}
	return out, true
}

// validateFieldName applies Chroma's metadata filter key constraints. Chroma
// reserves keys beginning with "$" for filter operators.
func validateFieldName(field string) error {
	if field == "" {
		return fmt.Errorf("chroma: filter field is required")
	}
	if strings.HasPrefix(field, "$") {
		return fmt.Errorf("%w: %q", errFieldNameInvalid, field)
	}
	return nil
}

// chromaMetadataField maps the common Document field vocabulary to the
// metadata keys persisted by this backend. Fields other than the common
// Document fields are treated as user metadata keys.
func chromaMetadataField(field string) (string, error) {
	if strings.HasPrefix(field, "metadata.") {
		field = strings.TrimPrefix(field, "metadata.")
		if err := validateFieldName(field); err != nil {
			return "", err
		}
		if isReservedKey(field) {
			return "", fmt.Errorf("%w: reserved metadata field %q", errFieldNameInvalid, field)
		}
		return field, nil
	}
	if err := validateFieldName(field); err != nil {
		return "", err
	}
	switch field {
	case "id":
		return "", fmt.Errorf("%w: field %q must be supplied through SearchFilter.IDs", errFieldNameInvalid, field)
	case "content":
		return "", fmt.Errorf("%w: field %q must use keyword search", errFieldNameInvalid, field)
	case "name":
		return metaName, nil
	case "created_at":
		return metaCreatedAt, nil
	case "updated_at":
		return metaUpdatedAt, nil
	}
	if isReservedKey(field) {
		return "", fmt.Errorf("%w: reserved field %q", errFieldNameInvalid, field)
	}
	return field, nil
}

// toAnySlice converts a typed slice to []any, validating element types.
func toAnySlice(v any) ([]any, error) {
	if v == nil {
		return nil, fmt.Errorf("chroma: in/not in value must not be nil")
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("chroma: in/not in value must be an array, got %T", v)
	}
	out := make([]any, rv.Len())
	var kind reflect.Kind
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		for item.Kind() == reflect.Interface {
			if item.IsNil() {
				return nil, fmt.Errorf("chroma: in/not in value contains nil")
			}
			item = item.Elem()
		}
		switch item.Kind() {
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
		default:
			return nil, fmt.Errorf("chroma: in/not in value element %d has unsupported type %s", i, item.Kind())
		}
		if i == 0 {
			kind = scalarKind(item.Kind())
		} else if scalarKind(item.Kind()) != kind {
			return nil, fmt.Errorf("chroma: in/not in values must have one scalar type")
		}
		out[i] = item.Interface()
	}
	return out, nil
}

// scalarKind normalizes integer and float kinds for homogeneous-type checks.
func scalarKind(k reflect.Kind) reflect.Kind {
	switch {
	case k >= reflect.Int && k <= reflect.Int64, k >= reflect.Uint && k <= reflect.Uint64:
		return reflect.Int64
	case k == reflect.Float32 || k == reflect.Float64:
		return reflect.Float64
	default:
		return k
	}
}

// toConditionSlice coerces the Value of an AND/OR condition into a typed slice.
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
				return nil, fmt.Errorf("chroma: logical operator value element must be *UniversalFilterCondition, got %T", item)
			}
			out = append(out, c)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("chroma: logical operator value must be []*UniversalFilterCondition, got %T", v)
	}
}

// metadataMapToWhere converts a simple key-value metadata map into a Chroma where clause.
func (vs *VectorStore) metadataMapToWhere(m map[string]any) (map[string]any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	parts := make([]any, 0, len(m))
	for k, v := range m {
		k = strings.TrimPrefix(k, "metadata.")
		if err := validateFieldName(k); err != nil {
			return nil, err
		}
		if isReservedKey(k) {
			return nil, fmt.Errorf("%w: reserved field %q", errFieldNameInvalid, k)
		}
		if err := validateEqualityValue(v); err != nil {
			return nil, fmt.Errorf("chroma: metadata filter %q: %w", k, err)
		}
		parts = append(parts, map[string]any{k: map[string]any{"$eq": v}})
	}
	if len(parts) == 1 {
		return parts[0].(map[string]any), nil
	}
	return map[string]any{"$and": parts}, nil
}

type filterSelectors struct {
	ids           []string
	idsSet        bool
	noMatch       bool
	where         map[string]any
	whereDocument map[string]any
}

func (s filterSelectors) hasSelector() bool {
	return s.idsSet || len(s.where) > 0 || len(s.whereDocument) > 0
}

// buildSelectors partitions a SearchFilter into ID, where, and whereDocument selectors.
func (vs *VectorStore) buildSelectors(f *vectorstore.SearchFilter) (filterSelectors, error) {
	if f == nil {
		return filterSelectors{}, nil
	}
	var parts []any
	if len(f.Metadata) > 0 {
		w, err := vs.metadataMapToWhere(f.Metadata)
		if err != nil {
			return filterSelectors{}, err
		}
		if len(w) > 0 {
			parts = append(parts, w)
		}
	}
	selectors := filterSelectors{
		ids:    append([]string(nil), f.IDs...),
		idsSet: len(f.IDs) > 0,
	}
	if f.FilterCondition != nil {
		converted, err := vs.filterConverter.Convert(f.FilterCondition)
		if err != nil {
			return filterSelectors{}, err
		}
		mergeSelectorIDs(&selectors, converted)
		selectors.noMatch = selectors.noMatch || converted.noMatch
		selectors.whereDocument = converted.whereDocument
		if len(converted.where) > 0 {
			parts = append(parts, converted.where)
		}
	}
	if len(parts) == 0 {
		return selectors, nil
	}
	selectors.where = joinWhere("$and", parts)
	return selectors, nil
}

// chromaFilterConverter converts universal filter conditions to Chroma
// selectors. It implements searchfilter.Converter.
type chromaFilterConverter struct{}

var _ searchfilter.Converter[filterSelectors] = (*chromaFilterConverter)(nil)

// newChromaFilterConverter creates a new chromaFilterConverter.
func newChromaFilterConverter() *chromaFilterConverter {
	return &chromaFilterConverter{}
}

// Convert partitions a condition into ID, metadata, and document selectors
// that map to separate Chroma request fields.
func (c *chromaFilterConverter) Convert(cond *searchfilter.UniversalFilterCondition) (filterSelectors, error) {
	return c.selectors(cond)
}

// selectors partitions a condition tree into ID, metadata, and document
// selectors that map to separate Chroma request fields.
func (c *chromaFilterConverter) selectors(cond *searchfilter.UniversalFilterCondition) (filterSelectors, error) {
	if cond == nil {
		return filterSelectors{}, nil
	}
	if cond.Operator == searchfilter.OperatorAnd || cond.Operator == searchfilter.OperatorOr {
		subs, err := toConditionSlice(cond.Value)
		if err != nil {
			return filterSelectors{}, err
		}
		if len(subs) == 0 {
			return filterSelectors{}, fmt.Errorf("chroma: %s requires at least one sub-condition", cond.Operator)
		}
		children := make([]filterSelectors, 0, len(subs))
		for _, sub := range subs {
			converted, err := c.selectors(sub)
			if err != nil {
				return filterSelectors{}, err
			}
			children = append(children, converted)
		}
		if len(children) == 1 {
			return children[0], nil
		}
		if cond.Operator == searchfilter.OperatorOr {
			return joinORSelectors(children)
		}
		return joinANDSelectors(children), nil
	}

	switch cond.Field {
	case "id":
		ids, err := idsFromCondition(cond)
		return filterSelectors{ids: ids, idsSet: err == nil}, err
	case "content":
		whereDocument, err := documentWhere(cond)
		return filterSelectors{whereDocument: whereDocument}, err
	default:
		where, err := convertCondition(cond)
		return filterSelectors{where: where}, err
	}
}

// idsFromCondition extracts document IDs from an eq or in condition on the id field.
func idsFromCondition(cond *searchfilter.UniversalFilterCondition) ([]string, error) {
	switch cond.Operator {
	case searchfilter.OperatorEqual:
		id, ok := cond.Value.(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("chroma: id equality requires a non-empty string")
		}
		return []string{id}, nil
	case searchfilter.OperatorIn:
		items, err := toAnySlice(cond.Value)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, errEmptyValueArray
		}
		ids := make([]string, len(items))
		for i, item := range items {
			id, ok := item.(string)
			if !ok || id == "" {
				return nil, fmt.Errorf("chroma: id in values must be non-empty strings")
			}
			ids[i] = id
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("%w: operator %s for id", errUnsupportedOperator, cond.Operator)
	}
}

// documentWhere builds a $contains or $not_contains clause from a content condition.
func documentWhere(cond *searchfilter.UniversalFilterCondition) (map[string]any, error) {
	value, ok := cond.Value.(string)
	if !ok || value == "" {
		return nil, fmt.Errorf("chroma: content filter requires a non-empty string")
	}
	switch cond.Operator {
	case searchfilter.OperatorLike:
		return map[string]any{"$contains": value}, nil
	case searchfilter.OperatorNotLike:
		return map[string]any{"$not_contains": value}, nil
	default:
		return nil, fmt.Errorf("%w: operator %s for content", errUnsupportedOperator, cond.Operator)
	}
}

// joinANDSelectors merges child selectors with AND semantics, intersecting IDs.
func joinANDSelectors(children []filterSelectors) filterSelectors {
	var out filterSelectors
	var where, whereDocument []any
	for _, child := range children {
		mergeSelectorIDs(&out, child)
		out.noMatch = out.noMatch || child.noMatch
		if len(child.where) > 0 {
			where = append(where, child.where)
		}
		if len(child.whereDocument) > 0 {
			whereDocument = append(whereDocument, child.whereDocument)
		}
	}
	out.where = joinWhere("$and", where)
	out.whereDocument = joinWhere("$and", whereDocument)
	return out
}

// joinORSelectors merges child selectors with OR semantics within one domain.
func joinORSelectors(children []filterSelectors) (filterSelectors, error) {
	var domain string
	var ids []string
	var where, whereDocument []any
	validChildren := 0
	for _, child := range children {
		if child.noMatch {
			continue
		}
		childDomain := selectorDomain(child)
		if childDomain == "" {
			if child.hasSelector() {
				return filterSelectors{}, errors.New("chroma: OR child spans multiple selector types")
			}
			continue
		}
		validChildren++
		if domain != "" && domain != childDomain {
			return filterSelectors{}, errors.New("chroma: OR across id, metadata, and content fields is not supported")
		}
		domain = childDomain
		switch domain {
		case "ids":
			ids = unionIDs(ids, child.ids)
		case "where":
			where = append(where, child.where)
		case "document":
			whereDocument = append(whereDocument, child.whereDocument)
		}
	}
	if validChildren == 0 {
		return filterSelectors{noMatch: true}, nil
	}
	return filterSelectors{
		ids:           ids,
		idsSet:        domain == "ids",
		where:         joinWhere("$or", where),
		whereDocument: joinWhere("$or", whereDocument),
	}, nil
}

// selectorDomain returns which single domain a selector uses, or "" if mixed.
func selectorDomain(s filterSelectors) string {
	n := 0
	domain := ""
	if s.idsSet {
		n++
		domain = "ids"
	}
	if len(s.where) > 0 {
		n++
		domain = "where"
	}
	if len(s.whereDocument) > 0 {
		n++
		domain = "document"
	}
	if n == 1 {
		return domain
	}
	return ""
}

// searchWhereFilter builds a Chroma Search API where expression from metadata
// where and classic Get where_document clauses. /search requires document
// predicates on #document; a top-level $contains is rejected as invalid.
func searchWhereFilter(where, whereDocument map[string]any) (map[string]any, error) {
	doc, err := searchDocumentFilter(whereDocument)
	if err != nil {
		return nil, err
	}
	return joinWhere("$and", []any{where, doc}), nil
}

// searchDocumentFilter rewrites a Get where_document tree into Search #document
// predicates. Logical $and / $or groups are preserved at the top level.
func searchDocumentFilter(whereDocument map[string]any) (map[string]any, error) {
	if len(whereDocument) == 0 {
		return nil, nil
	}
	if raw, ok := whereDocument["$and"]; ok {
		if len(whereDocument) != 1 {
			return nil, fmt.Errorf("chroma: search document filter mixes $and with other keys")
		}
		return convertLogicalSearchDocument("$and", raw)
	}
	if raw, ok := whereDocument["$or"]; ok {
		if len(whereDocument) != 1 {
			return nil, fmt.Errorf("chroma: search document filter mixes $or with other keys")
		}
		return convertLogicalSearchDocument("$or", raw)
	}
	if len(whereDocument) != 1 {
		return nil, fmt.Errorf("chroma: search document filter must be a single operator")
	}
	for op, val := range whereDocument {
		switch op {
		case "$contains", "$not_contains", "$regex", "$not_regex":
			s, ok := val.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("chroma: search document filter %s requires a non-empty string", op)
			}
			return map[string]any{"#document": map[string]any{op: s}}, nil
		default:
			return nil, fmt.Errorf("chroma: unsupported search document filter operator %q", op)
		}
	}
	return nil, nil
}

func convertLogicalSearchDocument(op string, raw any) (map[string]any, error) {
	parts, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("chroma: %s document filter requires an array", op)
	}
	converted := make([]any, 0, len(parts))
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("chroma: %s document filter child must be an object", op)
		}
		child, err := searchDocumentFilter(m)
		if err != nil {
			return nil, err
		}
		if len(child) > 0 {
			converted = append(converted, child)
		}
	}
	return joinWhere(op, converted), nil
}

// joinWhere combines non-empty clause parts under op, unwrapping single clauses.
func joinWhere(op string, parts []any) map[string]any {
	filtered := parts[:0]
	for _, part := range parts {
		if m, ok := part.(map[string]any); ok && len(m) > 0 {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0].(map[string]any)
	}
	return map[string]any{op: filtered}
}

// intersectIDs returns the IDs present in both a and b.
func intersectIDs(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, id := range b {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, id := range a {
		if _, ok := set[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// mergeSelectorIDs intersects src IDs into dst, setting noMatch when empty.
func mergeSelectorIDs(dst *filterSelectors, src filterSelectors) {
	if !src.idsSet {
		return
	}
	if !dst.idsSet {
		dst.ids = append([]string(nil), src.ids...)
		dst.idsSet = true
		return
	}
	dst.ids = intersectIDs(dst.ids, src.ids)
	if len(dst.ids) == 0 {
		dst.noMatch = true
	}
}

// unionIDs returns the deduplicated union of a and b.
func unionIDs(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, ids := range [][]string{a, b} {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
