//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolretry

import "reflect"

type replayMarker = interface{ ToolCallReplaySafe() bool }

// IsReplayUnsafe reports whether any error in the wrapped or joined error tree
// forbids replaying the original tool call. Custom As methods are also honored.
// A replay-safe marker cannot hide an unsafe marker deeper in the tree.
func IsReplayUnsafe(err error) bool {
	return replayUnsafe(err, nil, 0)
}

func replayUnsafe(err error, seen map[error]struct{}, depth int) bool {
	if err == nil {
		return false
	}
	// A custom As method can manufacture a new wrapper on every call.
	// If traversal cannot establish safety, suppress replay.
	if depth >= 128 {
		return true
	}
	if seen == nil {
		seen = make(map[error]struct{})
	}
	if reflect.ValueOf(err).Comparable() {
		if _, ok := seen[err]; ok {
			return false
		}
		seen[err] = struct{}{}
	}
	if marker, ok := err.(replayMarker); ok && unsafeMarker(marker) {
		return true
	}
	if alias, ok := err.(interface{ As(any) bool }); ok {
		var marker replayMarker
		if alias.As(&marker) && marker != nil {
			if cause, ok := marker.(error); ok {
				if replayUnsafe(cause, seen, depth+1) {
					return true
				}
			} else if unsafeMarker(marker) {
				return true
			}
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() error }:
		return replayUnsafe(wrapped.Unwrap(), seen, depth+1)
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if replayUnsafe(child, seen, depth+1) {
				return true
			}
		}
	}
	return false
}

func unsafeMarker(marker replayMarker) bool {
	value := reflect.ValueOf(marker)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return true
		}
	}
	return !marker.ToolCallReplaySafe()
}
