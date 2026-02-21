//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func truncate(s string, max int) (string, bool) {
	if max <= 0 {
		return "", false
	}
	if len(s) <= max {
		return s, false
	}
	return s[:max], true
}

func eventMessageText(e event.Event) (role string, text string) {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return "", ""
	}
	msg := e.Response.Choices[0].Message
	role = string(msg.Role)

	// Tool response.
	if msg.ToolID != "" {
		// Prefer content, but trim huge payloads upstream.
		return role, strings.TrimSpace(msg.Content)
	}

	// Tool calls: include function name + arguments as a hint.
	if len(msg.ToolCalls) > 0 {
		var parts []string
		for _, tc := range msg.ToolCalls {
			fn := tc.Function.Name
			args := strings.TrimSpace(string(tc.Function.Arguments))
			if args == "" {
				parts = append(parts, fn+"()")
				continue
			}
			parts = append(parts, fn+"("+args+")")
		}
		return role, strings.Join(parts, "\n")
	}

	// Regular message.
	return role, strings.TrimSpace(msg.Content)
}

func toUnixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano() / int64(time.Millisecond)
}
