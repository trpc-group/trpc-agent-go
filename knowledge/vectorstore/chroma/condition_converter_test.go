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
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestConvertCondition(t *testing.T) {
	tests := []struct {
		name    string
		cond    *searchfilter.UniversalFilterCondition
		wantKey string
		want    map[string]any
		wantErr error
	}{
		{name: "nil", cond: nil},
		{
			name:    "equal",
			cond:    &searchfilter.UniversalFilterCondition{Field: "category", Operator: searchfilter.OperatorEqual, Value: "guide"},
			wantKey: "category",
			want:    map[string]any{"category": map[string]any{"$eq": "guide"}},
		},
		{
			name: "not equal",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorNotEqual, Value: 1},
			want: map[string]any{"n": map[string]any{"$ne": 1}},
		},
		{
			name: "greater than",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorGreaterThan, Value: 1},
			want: map[string]any{"n": map[string]any{"$gt": 1}},
		},
		{
			name: "greater than or equal",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorGreaterThanOrEqual, Value: 1},
			want: map[string]any{"n": map[string]any{"$gte": 1}},
		},
		{
			name: "less than",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorLessThan, Value: 1},
			want: map[string]any{"n": map[string]any{"$lt": 1}},
		},
		{
			name: "less than or equal",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorLessThanOrEqual, Value: 1},
			want: map[string]any{"n": map[string]any{"$lte": 1}},
		},
		{
			name: "in ints",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorIn, Value: []int{1, 2}},
			want: map[string]any{"n": map[string]any{"$in": []any{1, 2}}},
		},
		{
			name: "nin strings",
			cond: &searchfilter.UniversalFilterCondition{Field: "n", Operator: searchfilter.OperatorNotIn, Value: []string{"a"}},
			want: map[string]any{"n": map[string]any{"$nin": []any{"a"}}},
		},
		{
			name: "and of equals",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{Field: "category", Operator: searchfilter.OperatorEqual, Value: "guide"},
					{Field: "lang", Operator: searchfilter.OperatorEqual, Value: "zh"},
				},
			},
			wantKey: "$and",
		},
		{
			name: "or one child unwraps",
			cond: &searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorOr, Value: []searchfilter.UniversalFilterCondition{
				{Field: "a", Operator: searchfilter.OperatorEqual, Value: "1"},
			}},
			wantKey: "a",
		},
		{
			name:    "between unsupported",
			cond:    &searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorBetween},
			wantErr: errUnsupportedOperator,
		},
		{
			name:    "like unsupported",
			cond:    &searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorLike},
			wantErr: errUnsupportedOperator,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertCondition(tt.cond)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.cond == nil && got != nil {
				t.Fatalf("nil cond = %#v", got)
			}
			if tt.wantKey != "" {
				if _, ok := got[tt.wantKey]; !ok {
					t.Fatalf("got = %#v, want key %q", got, tt.wantKey)
				}
			}
			if tt.want != nil && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestConvertConditionErrors(t *testing.T) {
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{Operator: "nope"}); err == nil {
		t.Fatal("unknown operator")
	}
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{Field: "$bad", Operator: searchfilter.OperatorEqual, Value: 1}); !errors.Is(err, errFieldNameInvalid) {
		t.Fatalf("invalid field = %v", err)
	}
	gotPrefixed, err := convertCondition(&searchfilter.UniversalFilterCondition{Field: "metadata.category", Operator: searchfilter.OperatorEqual, Value: "guide"})
	if err != nil || gotPrefixed["category"] == nil {
		t.Fatalf("prefixed metadata field = %#v, %v", gotPrefixed, err)
	}
	if err := validateFieldName(""); err == nil {
		t.Fatal("empty field")
	}
	if err := validateFieldName("ok_field"); err != nil {
		t.Fatalf("valid field = %v", err)
	}
	for _, field := range []string{"source-name", "1abc", "a.b", "a b", "来源"} {
		if err := validateFieldName(field); err != nil {
			t.Fatalf("validateFieldName(%q) = %v, want nil", field, err)
		}
	}
	for _, field := range []string{"$bad", "$and"} {
		if err := validateFieldName(field); !errors.Is(err, errFieldNameInvalid) {
			t.Fatalf("validateFieldName(%q) = %v, want errFieldNameInvalid", field, err)
		}
	}
	if _, err := inWhere("f", "$in", []any{}); !errors.Is(err, errEmptyValueArray) {
		t.Fatalf("empty in = %v", err)
	}
	if _, err := newChromaFilterConverter().Convert(&searchfilter.UniversalFilterCondition{
		Field: "id", Operator: searchfilter.OperatorIn, Value: []string{},
	}); !errors.Is(err, errEmptyValueArray) {
		t.Fatalf("empty id in = %v", err)
	}
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{
		Field: "tags", Operator: searchfilter.OperatorEqual, Value: []string{"a"},
	}); err == nil || !strings.Contains(err.Error(), "must be a scalar") {
		t.Fatalf("array equality = %v", err)
	}
	if _, err := inWhere("f", "$in", nil); err == nil {
		t.Fatal("nil in")
	}
	for _, value := range []any{"a", true} {
		if _, err := comparisonWhere("f", "$gt", value); err == nil {
			t.Fatalf("range comparison accepted %T", value)
		}
	}
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    []*searchfilter.UniversalFilterCondition{},
	}); err == nil {
		t.Fatal("empty and")
	}
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorOr, Value: "bad"}); err == nil {
		t.Fatal("bad logical value")
	}
	if _, err := convertCondition(&searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorOr, Value: []any{"bad"}}); err == nil {
		t.Fatal("bad logical element")
	}
	got, err := convertCondition(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    []*searchfilter.UniversalFilterCondition{nil, nil},
	})
	if err != nil || got != nil {
		t.Fatalf("and of nils = %#v %v", got, err)
	}
	unwrapped, err := convertCondition(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value: []*searchfilter.UniversalFilterCondition{
			nil,
			{Field: "a", Operator: searchfilter.OperatorEqual, Value: "1"},
		},
	})
	if err != nil || unwrapped["a"] == nil {
		t.Fatalf("and unwrap = %#v %v", unwrapped, err)
	}
}

func TestBuildWhereAndHelpers(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	selectors, err := vs.buildSelectors(&vectorstore.SearchFilter{
		Metadata: map[string]any{"a": "1", "b": "2"},
		FilterCondition: &searchfilter.UniversalFilterCondition{
			Field: "c", Operator: searchfilter.OperatorEqual, Value: "3",
		},
	})
	if err != nil || selectors.where["$and"] == nil {
		t.Fatalf("buildSelectors = %#v %v", selectors, err)
	}
	if got, err := vs.buildSelectors(nil); err != nil || got.where != nil {
		t.Fatalf("buildSelectors(nil) = %#v %v", got, err)
	}
	if _, err := vs.metadataMapToWhere(map[string]any{"$bad": 1}); err == nil {
		t.Fatal("invalid metadata key")
	}
	if got, err := vs.metadataMapToWhere(map[string]any{"metadata.a": "1"}); err != nil || got["a"] == nil {
		t.Fatalf("prefixed metadata map = %#v, %v", got, err)
	}
	for _, key := range []string{"source-name", "1abc", "a.b", "a b", "来源"} {
		got, err := vs.metadataMapToWhere(map[string]any{key: "value"})
		if err != nil || got[key] == nil {
			t.Fatalf("metadata key %q = %#v, %v", key, got, err)
		}
	}
	for _, value := range []any{nil, []string{"x"}, map[string]any{"x": 1}} {
		if _, err := vs.metadataMapToWhere(map[string]any{"a": value}); err == nil {
			t.Fatalf("metadata equality accepted %T", value)
		}
	}
	if got, err := toAnySlice([]float64{1, 2}); err != nil || len(got) != 2 {
		t.Fatal(err)
	}
	if _, err := toAnySlice("not-a-slice"); err == nil {
		t.Fatal("scalar in value")
	}
	if _, err := toConditionSlice([]any{&searchfilter.UniversalFilterCondition{Field: "a", Operator: searchfilter.OperatorEqual, Value: "1"}}); err != nil {
		t.Fatal(err)
	}

	selectors, err = vs.buildSelectors(&vectorstore.SearchFilter{
		IDs: []string{"a", "b"},
		FilterCondition: &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorAnd,
			Value: []*searchfilter.UniversalFilterCondition{
				{Field: "id", Operator: searchfilter.OperatorIn, Value: []string{"b", "c"}},
				{Field: "category", Operator: searchfilter.OperatorEqual, Value: "guide"},
				{Field: "content", Operator: searchfilter.OperatorLike, Value: "vector"},
			},
		},
	})
	if err != nil || !reflect.DeepEqual(selectors.ids, []string{"b"}) ||
		selectors.where["category"] == nil || selectors.whereDocument["$contains"] != "vector" {
		t.Fatalf("common selectors = %#v, %v", selectors, err)
	}
}

func TestCommonFieldSelectors(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	condition := func(field, op string, value any) *searchfilter.UniversalFilterCondition {
		return &searchfilter.UniversalFilterCondition{Field: field, Operator: op, Value: value}
	}

	t.Run("metadata fields and timestamps", func(t *testing.T) {
		for _, tc := range []struct {
			field string
			want  string
		}{
			{field: "source_name", want: "source_name"},
			{field: "name", want: metaName},
			{field: "created_at", want: metaCreatedAt},
			{field: "updated_at", want: metaUpdatedAt},
		} {
			got, err := chromaMetadataField(tc.field)
			if err != nil || got != tc.want {
				t.Fatalf("chromaMetadataField(%q) = %q, %v", tc.field, got, err)
			}
		}
		got, err := comparisonWhere("created_at", "$gte", time.Unix(10, 0))
		if err != nil || got[metaCreatedAt].(map[string]any)["$gte"] != int64(10) {
			t.Fatalf("timestamp comparison = %#v, %v", got, err)
		}
		got, err = inWhere("updated_at", "$in", []time.Time{time.Unix(20, 0)})
		if err != nil || got[metaUpdatedAt].(map[string]any)["$in"].([]any)[0] != int64(20) {
			t.Fatalf("timestamp in = %#v, %v", got, err)
		}
	})

	t.Run("id OR and intersection", func(t *testing.T) {
		got, err := vs.buildSelectors(&vectorstore.SearchFilter{
			IDs: []string{"b", "c"},
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					condition("id", searchfilter.OperatorEqual, "a"),
					condition("id", searchfilter.OperatorIn, []string{"b"}),
				},
			},
		})
		if err != nil || !reflect.DeepEqual(got.ids, []string{"b"}) {
			t.Fatalf("id selectors = %#v, %v", got, err)
		}
	})

	t.Run("OR skips impossible branches", func(t *testing.T) {
		impossible := func(a, b string) *searchfilter.UniversalFilterCondition {
			return &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					condition("id", searchfilter.OperatorEqual, a),
					condition("id", searchfilter.OperatorEqual, b),
				},
			}
		}
		converter := newChromaFilterConverter()
		got, err := converter.Convert(&searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorOr,
			Value: []*searchfilter.UniversalFilterCondition{
				impossible("a", "b"),
				condition("id", searchfilter.OperatorEqual, "c"),
			},
		})
		if err != nil || got.noMatch || !reflect.DeepEqual(got.ids, []string{"c"}) {
			t.Fatalf("OR with impossible branch = %#v, %v", got, err)
		}
		got, err = converter.Convert(&searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorOr,
			Value: []*searchfilter.UniversalFilterCondition{
				impossible("a", "b"),
				impossible("c", "d"),
			},
		})
		if err != nil || !got.noMatch {
			t.Fatalf("all-impossible OR = %#v, %v", got, err)
		}
	})

	t.Run("metadata and content OR", func(t *testing.T) {
		for _, tc := range []struct {
			field string
			op    string
			value any
			check func(filterSelectors) bool
		}{
			{
				field: "kind", op: searchfilter.OperatorEqual, value: "a",
				check: func(s filterSelectors) bool { return s.where["$or"] != nil },
			},
			{
				field: "content", op: searchfilter.OperatorLike, value: "a",
				check: func(s filterSelectors) bool { return s.whereDocument["$or"] != nil },
			},
		} {
			got, err := vs.buildSelectors(&vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Operator: searchfilter.OperatorOr,
					Value: []*searchfilter.UniversalFilterCondition{
						condition(tc.field, tc.op, tc.value),
						condition(tc.field, tc.op, "b"),
					},
				},
			})
			if err != nil || !tc.check(got) {
				t.Fatalf("%s OR = %#v, %v", tc.field, got, err)
			}
		}
	})

	t.Run("unsupported selector combinations", func(t *testing.T) {
		cases := []*searchfilter.UniversalFilterCondition{
			condition("id", searchfilter.OperatorNotEqual, "a"),
			condition("id", searchfilter.OperatorEqual, 1),
			condition("content", searchfilter.OperatorEqual, "a"),
			condition("content", searchfilter.OperatorLike, ""),
			{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					condition("id", searchfilter.OperatorEqual, "a"),
					condition("kind", searchfilter.OperatorEqual, "a"),
				},
			},
		}
		converter := newChromaFilterConverter()
		for _, cond := range cases {
			if _, err := converter.Convert(cond); err == nil {
				t.Fatalf("Convert(%#v): want error", cond)
			}
		}
	})
}

func TestSearchDocumentFilter(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]any
		want    map[string]any
		wantErr string
	}{
		{name: "nil"},
		{name: "empty", in: map[string]any{}},
		{
			name: "contains",
			in:   map[string]any{"$contains": "internal only"},
			want: map[string]any{"#document": map[string]any{"$contains": "internal only"}},
		},
		{
			name: "not contains",
			in:   map[string]any{"$not_contains": "internal only"},
			want: map[string]any{"#document": map[string]any{"$not_contains": "internal only"}},
		},
		{
			name: "regex",
			in:   map[string]any{"$regex": "(?i)internal"},
			want: map[string]any{"#document": map[string]any{"$regex": "(?i)internal"}},
		},
		{
			name: "and of contains",
			in: map[string]any{"$and": []any{
				map[string]any{"$contains": "vector"},
				map[string]any{"$contains": "guide"},
			}},
			want: map[string]any{"$and": []any{
				map[string]any{"#document": map[string]any{"$contains": "vector"}},
				map[string]any{"#document": map[string]any{"$contains": "guide"}},
			}},
		},
		{
			name: "or unwraps one child",
			in: map[string]any{"$or": []any{
				map[string]any{"$contains": "guide"},
			}},
			want: map[string]any{"#document": map[string]any{"$contains": "guide"}},
		},
		{
			name:    "classic top-level eq is invalid",
			in:      map[string]any{"$eq": "guide"},
			wantErr: "unsupported search document filter operator",
		},
		{
			name:    "empty contains",
			in:      map[string]any{"$contains": ""},
			wantErr: "non-empty string",
		},
		{
			name:    "mixed and",
			in:      map[string]any{"$and": []any{map[string]any{"$contains": "a"}}, "$contains": "b"},
			wantErr: "mixes $and",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := searchDocumentFilter(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSearchWhereFilter(t *testing.T) {
	where := map[string]any{"category": map[string]any{"$eq": "guide"}}
	doc := map[string]any{"$contains": "vector"}
	got, err := searchWhereFilter(where, doc)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"$and": []any{
		where,
		map[string]any{"#document": map[string]any{"$contains": "vector"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if got, err := searchWhereFilter(where, nil); err != nil || !reflect.DeepEqual(got, where) {
		t.Fatalf("metadata only = %#v %v", got, err)
	}
	if _, err := searchWhereFilter(where, map[string]any{"$eq": "x"}); err == nil {
		t.Fatal("invalid document filter")
	}
}

func TestValidateEqualityValueTypes(t *testing.T) {
	valid := []any{
		json.Number("1.5"),
		float32(1),
		float64(1),
		uint8(1),
	}
	for _, v := range valid {
		if err := validateEqualityValue(v); err != nil {
			t.Fatalf("validateEqualityValue(%T) = %v, want nil", v, err)
		}
	}
	invalid := []struct {
		name  string
		value any
		want  string
	}{
		{name: "invalid json number", value: json.Number("abc"), want: "not a valid number"},
		{name: "float32 nan", value: float32(math.NaN()), want: "must be finite"},
		{name: "float32 inf", value: float32(math.Inf(-1)), want: "must be finite"},
		{name: "float64 nan", value: math.NaN(), want: "must be finite"},
		{name: "float64 inf", value: math.Inf(1), want: "must be finite"},
		{name: "map value", value: map[string]any{"a": 1}, want: "must be a scalar"},
		{name: "struct value", value: struct{}{}, want: "unsupported type"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEqualityValue(tt.value)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
	if _, err := comparisonWhere("f", "$gt", nil); err == nil {
		t.Fatal("nil range value")
	}
	if _, err := comparisonWhere("f", "$gt", math.NaN()); err == nil {
		t.Fatal("nan range value")
	}
	if _, err := inWhere("$bad", "$in", []int{1}); !errors.Is(err, errFieldNameInvalid) {
		t.Fatalf("in with invalid field = %v", err)
	}
}

func TestChromaMetadataFieldReserved(t *testing.T) {
	for _, field := range []string{"id", "content", "_json", "metadata._json"} {
		if _, err := chromaMetadataField(field); !errors.Is(err, errFieldNameInvalid) {
			t.Fatalf("chromaMetadataField(%q) = %v, want errFieldNameInvalid", field, err)
		}
	}
	if _, err := chromaMetadataField("metadata."); err == nil {
		t.Fatal("empty metadata field name")
	}
}

func TestToAnySliceValidation(t *testing.T) {
	if got, err := toAnySlice([]any{1, 2}); err != nil || len(got) != 2 {
		t.Fatalf("ints = %#v, %v", got, err)
	}
	invalid := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil element", value: []any{nil}, want: "contains nil"},
		{name: "unsupported element", value: []any{struct{}{}}, want: "unsupported type"},
		{name: "mixed scalar types", value: []any{1, "a"}, want: "one scalar type"},
		{name: "mixed int widths", value: []any{int8(1), int64(2)}, want: ""},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toAnySlice(tt.value)
			if tt.want == "" {
				if err != nil || len(got) != 2 {
					t.Fatalf("toAnySlice(%v) = %#v, %v", tt.value, got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestMetadataMapToWhereReserved(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	if got, err := vs.metadataMapToWhere(map[string]any{}); err != nil || got != nil {
		t.Fatalf("empty map = %#v, %v", got, err)
	}
	if _, err := vs.metadataMapToWhere(map[string]any{"_json": 1}); !errors.Is(err, errFieldNameInvalid) {
		t.Fatalf("reserved key = %v", err)
	}
	if _, err := vs.buildSelectors(&vectorstore.SearchFilter{
		FilterCondition: &searchfilter.UniversalFilterCondition{Field: "a", Operator: "nope"},
	}); err == nil {
		t.Fatal("bad filter condition")
	}
}

func TestConverterSelectorTreeErrors(t *testing.T) {
	converter := newChromaFilterConverter()
	condition := func(field, op string, value any) *searchfilter.UniversalFilterCondition {
		return &searchfilter.UniversalFilterCondition{Field: field, Operator: op, Value: value}
	}

	errCases := []struct {
		name string
		cond *searchfilter.UniversalFilterCondition
		want string
	}{
		{
			name: "bad logical value",
			cond: &searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorAnd, Value: "bad"},
			want: "must be []*UniversalFilterCondition",
		},
		{
			name: "empty logical value",
			cond: &searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorAnd, Value: []*searchfilter.UniversalFilterCondition{}},
			want: "at least one sub-condition",
		},
		{
			name: "nested error",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value:    []*searchfilter.UniversalFilterCondition{condition("a", "nope", 1)},
			},
			want: "unknown filter operator",
		},
		{
			name: "id in scalar",
			cond: condition("id", searchfilter.OperatorIn, "bad"),
			want: "must be an array",
		},
		{
			name: "id in non-string",
			cond: condition("id", searchfilter.OperatorIn, []any{1}),
			want: "non-empty strings",
		},
		{
			name: "or child spans selector types",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Operator: searchfilter.OperatorAnd,
						Value: []*searchfilter.UniversalFilterCondition{
							condition("id", searchfilter.OperatorEqual, "a"),
							condition("kind", searchfilter.OperatorEqual, "b"),
						},
					},
					condition("id", searchfilter.OperatorEqual, "c"),
				},
			},
			want: "spans multiple selector types",
		},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := converter.Convert(tt.cond)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}

	t.Run("single child unwraps", func(t *testing.T) {
		got, err := converter.Convert(&searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorAnd,
			Value:    []*searchfilter.UniversalFilterCondition{condition("kind", searchfilter.OperatorEqual, "a")},
		})
		if err != nil || got.where["kind"] == nil {
			t.Fatalf("single child = %#v, %v", got, err)
		}
	})

	t.Run("or skips empty children", func(t *testing.T) {
		got, err := converter.Convert(&searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorOr,
			Value: []*searchfilter.UniversalFilterCondition{
				{Operator: searchfilter.OperatorAnd, Value: []*searchfilter.UniversalFilterCondition{nil, nil}},
				condition("kind", searchfilter.OperatorEqual, "a"),
			},
		})
		if err != nil || got.noMatch || got.where["kind"] == nil {
			t.Fatalf("or with empty child = %#v, %v", got, err)
		}
	})
}

func TestSearchDocumentFilterLogicalErrors(t *testing.T) {
	errCases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{name: "mixed or", in: map[string]any{"$or": []any{map[string]any{"$contains": "a"}}, "$contains": "b"}, want: "mixes $or"},
		{name: "multiple operators", in: map[string]any{"$contains": "a", "$regex": "b"}, want: "single operator"},
		{name: "and not array", in: map[string]any{"$and": "x"}, want: "requires an array"},
		{name: "and child not object", in: map[string]any{"$and": []any{"x"}}, want: "child must be an object"},
		{name: "and child invalid", in: map[string]any{"$and": []any{map[string]any{"$eq": 1}}}, want: "unsupported search document filter operator"},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := searchDocumentFilter(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestUnionIDsDeduplicates(t *testing.T) {
	got := unionIDs([]string{"a", "b"}, []string{"b", "c"})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("unionIDs = %#v", got)
	}
}
