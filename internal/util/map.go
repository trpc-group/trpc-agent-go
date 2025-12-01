//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides internal utilities
// management in the trpc-agent-go framework.
package util

// GetMapValue get map value with type check
func GetMapValue[K comparable, V any](m map[K]any, key K) (V, bool) {
	var zero V

	if m == nil {
		return zero, false
	}
	val, ok := m[key]
	if !ok {
		return zero, false
	}

	if val == nil {
		return zero, false
	}

	typedVal, ok := val.(V)
	if !ok {
		return zero, false
	}
	return typedVal, true
}
