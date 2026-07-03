//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	envBlockedToolArgumentSubstrings = "OPENCLAW_BLOCKED_TOOL_ARGUMENT_SUBSTRINGS"
)

var errToolArgumentBlocked = errors.New(
	"tool call blocked by runtime guard: " +
		"argument matched a configured blocked benchmark/eval artifact pattern",
)

func registerToolArgumentGuardCallback(
	callbacks *tool.Callbacks,
	rawPatterns string,
) {
	if callbacks == nil {
		return
	}
	callback := newToolArgumentGuardCallback(
		splitToolArgumentGuardPatterns(rawPatterns),
	)
	if callback == nil {
		return
	}
	callbacks.RegisterBeforeTool(callback)
}

func newToolArgumentGuardCallback(
	patterns []string,
) tool.BeforeToolCallbackStructured {
	patterns = normalizeToolArgumentGuardPatterns(patterns)
	if len(patterns) == 0 {
		return nil
	}
	return func(
		_ context.Context,
		args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		if args == nil || len(args.Arguments) == 0 {
			return nil, nil
		}
		lowerArgs := strings.ToLower(string(args.Arguments))
		for _, pattern := range patterns {
			if strings.Contains(lowerArgs, pattern) {
				return nil, errToolArgumentBlocked
			}
		}
		return nil, nil
	}
}

func splitToolArgumentGuardPatterns(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
}

func normalizeToolArgumentGuardPatterns(patterns []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	return out
}
