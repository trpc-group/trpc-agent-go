//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RedactText masks credential-like substrings for reports, audit sinks, or
// post-execution result scrubbing. Prefer this helper over wrapping ToolSet:
// PermissionPolicy cannot see tool outputs, so hosts should redact results
// themselves without dropping Tool / ToolSet interface capabilities.
func RedactText(s string) string {
	return redactSecrets(s)
}

// RedactMap returns a shallow copy with string values passed through RedactText.
// Nested maps/slices are left unchanged; use RedactValue for deep scrubbing.
// Map keys are not rewritten.
func RedactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = RedactText(t)
		default:
			out[k] = v
		}
	}
	return out
}

// RedactValue deep-walks maps and slices, passing string leaves through
// RedactText. Other scalar types are copied as-is.
func RedactValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return RedactText(t)
	case []byte:
		return RedactJSON(t)
	case json.RawMessage:
		return json.RawMessage(RedactJSON(t))
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = RedactValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = RedactValue(vv)
		}
		return out
	default:
		return v
	}
}

// RedactJSON redacts string leaves inside a JSON document. If raw is not valid
// JSON, it is treated as plain text via RedactText.
func RedactJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []byte(RedactText(string(raw)))
	}
	out, err := json.Marshal(RedactValue(v))
	if err != nil {
		return []byte(RedactText(string(raw)))
	}
	return out
}

// AfterToolRedact returns a structured after-tool callback that scrubs
// string / []byte / json.RawMessage / map[string]any / []any results before
// they are fed back to the model. Wire it next to Guard:
//
//	cbs := tool.NewCallbacks()
//	cbs.RegisterAfterTool(safety.AfterToolRedact())
//	llmagent.New(..., llmagent.WithToolCallbacks(cbs),
//	    llmagent.WithToolPermissionPolicy(guard))
//
// PermissionPolicy still cannot see outputs; this is the host-side half of
// the same safety story without wrapping ToolSet.
func AfterToolRedact() tool.AfterToolCallbackStructured {
	return func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		if args == nil || args.Result == nil {
			return nil, nil
		}
		switch args.Result.(type) {
		case string, []byte, json.RawMessage, map[string]any, []any:
			return &tool.AfterToolResult{CustomResult: RedactValue(args.Result)}, nil
		default:
			return nil, nil
		}
	}
}
