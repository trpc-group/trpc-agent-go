//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides JSON normalization helpers for replay comparisons.
package tool

import "encoding/json"

// NormalizeJSON canonicalizes JSON payloads for stable comparison.
func NormalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	// 先解析 再转成json
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(normalized)
}
