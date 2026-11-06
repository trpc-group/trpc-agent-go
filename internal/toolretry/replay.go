//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolretry

// IsReplayUnsafe reports whether any error in the wrapped or joined error tree
// forbids replaying the original tool call. Custom As methods are also honored.
// A replay-safe marker cannot hide an unsafe marker deeper in the tree.
func IsReplayUnsafe(err error) bool {
	if err == nil {
		return false
	}
	if marker, ok := err.(interface{ ToolCallReplaySafe() bool }); ok {
		if !marker.ToolCallReplaySafe() {
			return true
		}
	} else if alias, ok := err.(interface{ As(any) bool }); ok {
		var marker interface{ ToolCallReplaySafe() bool }
		if alias.As(&marker) && marker != nil && !marker.ToolCallReplaySafe() {
			return true
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() error }:
		return IsReplayUnsafe(wrapped.Unwrap())
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if IsReplayUnsafe(child) {
				return true
			}
		}
	}
	return false
}
