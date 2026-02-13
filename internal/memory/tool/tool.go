//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tool provides shared helpers for memory tool enablement
// checks and map copying. Both the extractor package and the
// internal/memory package use these helpers to avoid duplicating
// the enabled/disabled semantics.
package tool

// IsToolEnabled reports whether a tool identified by name is
// enabled according to the given map. A nil or empty map means
// all tools are enabled. A non-empty map requires the name to
// map to true; missing keys are treated as disabled.
func IsToolEnabled(
	enabledTools map[string]bool,
	name string,
) bool {
	if len(enabledTools) == 0 {
		return true
	}
	return enabledTools[name]
}

// CopyEnabledTools returns a defensive copy of the enabled tools
// map. A nil input returns nil.
func CopyEnabledTools(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
