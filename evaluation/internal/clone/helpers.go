//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clone provides deep copy helpers for evaluation data structures.
package clone

import (
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
)

func cloneEpochTime(src *epochtime.EpochTime) *epochtime.EpochTime {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	copied := make([]byte, len(src))
	copy(copied, src)
	return copied
}

func cloneStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	copied := make([]string, len(src))
	copy(copied, src)
	return copied
}

func cloneIntPtr(src *int) *int {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneFloat64Ptr(src *float64) *float64 {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneStringPtr(src *string) *string {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneAny(src any) (any, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return cloneBytes(v), nil
	case map[string]any:
		copied := make(map[string]any, len(v))
		for key, value := range v {
			cloned, err := cloneAny(value)
			if err != nil {
				return nil, err
			}
			copied[key] = cloned
		}
		return copied, nil
	case []any:
		copied := make([]any, len(v))
		for i := range v {
			cloned, err := cloneAny(v[i])
			if err != nil {
				return nil, err
			}
			copied[i] = cloned
		}
		return copied, nil
	default:
		kind := reflect.ValueOf(src).Kind()
		switch kind {
		case reflect.Chan, reflect.Func, reflect.UnsafePointer:
			return nil, fmt.Errorf("unsupported value type %T", src)
		}
		return src, nil
	}
}

func errNilInput(name string) error {
	return fmt.Errorf("%s is nil", name)
}
