//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
)

func prettyJSONBytes(raw []byte, limit int) string {
	if len(raw) == 0 {
		return "<empty>"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return clip(string(raw), limit)
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return clip(string(raw), limit)
	}
	return clip(string(formatted), limit)
}

func clip(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...<truncated>"
}

func emptyAsDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func intPtrString(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *v)
}

func floatPtrString(v *float64) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%.4f", *v)
}
