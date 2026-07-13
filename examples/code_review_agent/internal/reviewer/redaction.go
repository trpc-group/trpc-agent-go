// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewer

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// newRedactingToolCallbacks masks tool results before the framework converts
// them into model-visible and Session-persisted tool response events. The
// Session AppendEventHook remains a final safety net for every event type.
func newRedactingToolCallbacks(sanitizer *redact.Sanitizer) *tool.Callbacks {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		args *tool.AfterToolArgs,
	) (result *tool.AfterToolResult, err error) {
		if sanitizer == nil || args == nil {
			return nil, nil
		}
		masked, count, err := sanitizer.MaskValue(args.Result)
		if err != nil {
			return nil, err
		}

		var (
			maskedError string
			errorCount  int
		)

		if args.Error != nil {
			maskedError, errorCount = sanitizer.MaskString(args.Error.Error())
		}
		if count == 0 && errorCount == 0 {
			return nil, nil
		}

		if args.Error != nil {
			// AfterToolResult cannot directly replace an error. Returning a
			// non-nil custom result is the framework-supported way to replace a
			// failed tool response and clear its original, potentially sensitive
			// error before the response event is produced.
			return &tool.AfterToolResult{CustomResult: map[string]any{
				"status": "error",
				"error":  maskedError,
				"result": masked,
			}}, nil
		}

		if masked == nil {
			return nil, fmt.Errorf("redaction detected tool output but produced an empty replacement")
		}

		return &tool.AfterToolResult{CustomResult: masked}, nil
	})
	return callbacks
}
