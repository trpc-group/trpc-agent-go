//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"fmt"
	"reflect"
	"sort"
)

// sanitizeForCacheKey removes volatile or unsafe keys from a State map so that
// the cache key depends only on deterministic inputs.
func sanitizeForCacheKey(input any) any {
	st, ok := input.(State)
	if !ok {
		return input
	}
	return st.safeClone()
}

// toCanonicalValue converts an arbitrary value into a form that produces
// deterministic JSON when marshaled: maps are converted to sorted key-value arrays,
// slices are canonicalized element-wise.
func toCanonicalValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	// Unwrap State for convenience.
	if st, ok := v.(State); ok {
		return canonicalizeMap(st)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		// Only handle map[string]any and map[string]T consistently; otherwise stringify keys.
		iter := rv.MapRange()
		tmp := make([]kv, 0, rv.Len())
		for iter.Next() {
			k := iter.Key().Interface()
			val := iter.Value().Interface()
			keyStr := fmt.Sprintf("%v", k)
			cv, err := toCanonicalValue(val)
			if err != nil {
				return nil, err
			}
			tmp = append(tmp, kv{K: keyStr, V: cv})
		}
		sort.Slice(tmp, func(i, j int) bool { return tmp[i].K < tmp[j].K })
		return tmp, nil
	case reflect.Slice, reflect.Array:
		n := rv.Len()
		out := make([]any, n)
		for i := 0; i < n; i++ {
			cv, err := toCanonicalValue(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	case reflect.Struct:
		// Best-effort: iterate exported fields in name order.
		t := rv.Type()
		names := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			names = append(names, f.Name)
		}
		sort.Strings(names)
		tmp := make([]kv, 0, len(names))
		for _, name := range names {
			f, _ := t.FieldByName(name)
			cv, err := toCanonicalValue(rv.FieldByIndex(f.Index).Interface())
			if err != nil {
				return nil, err
			}
			tmp = append(tmp, kv{K: name, V: cv})
		}
		return tmp, nil
	default:
		// Primitive types are fine as-is.
		return v, nil
	}
}

func canonicalizeMap(m map[string]any) (any, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		cv, err := toCanonicalValue(m[k])
		if err != nil {
			return nil, err
		}
		out = append(out, kv{K: k, V: cv})
	}
	return out, nil
}

type kv struct {
	K string `json:"k"`
	V any    `json:"v"`
}
