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
	"fmt"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// OutputLimitCallback returns an AfterTool callback that enforces the policy's
// Limits.MaxOutputBytes at execution time by truncating a recognised exec
// tool's output once it exceeds the cap. This is the runtime half of the
// resource limit: the static scan decides allow/deny before execution, while
// this callback bounds output during execution, so max_output_bytes is a real,
// enforced limit rather than advisory config.
//
// Register it alongside the permission policy, for example:
//
//	pol := safety.NewPermissionPolicy(safety.NewScanner(policy))
//	ag := llmagent.New("agent",
//	    llmagent.WithTools(tools),
//	    llmagent.WithToolCallbacks(&tool.Callbacks{
//	        AfterTool: []tool.AfterToolCallbackStructured{pol.OutputLimitCallback()},
//	    }),
//	)
//	runner.Run(ctx, user, session, msg,
//	    agent.WithToolPermissionPolicyFunc(pol.CheckToolPermission))
//
// A zero MaxOutputBytes disables the callback (returns a no-op).
func (p *PermissionPolicy) OutputLimitCallback() tool.AfterToolCallbackStructured {
	limit := p.scanner.policy.Limits.MaxOutputBytes
	return func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		if limit <= 0 || args == nil || args.Result == nil {
			return nil, nil
		}
		// Only bound recognised exec tools; leave other tools' results intact.
		if p.backendFor(args.ToolName) == BackendUnknown {
			return nil, nil
		}
		limited, changed := limitResultOutput(args.Result, limit)
		if !changed {
			return nil, nil
		}
		return &tool.AfterToolResult{CustomResult: limited}, nil
	}
}

// limitResultOutput truncates the "output" field of a tool result to at most
// max bytes (on a UTF-8 rune boundary). It round-trips through JSON so it works
// for any exec result shape that carries an "output" string, preserving the
// result's other fields and adding an "output_truncated" marker. It returns the
// possibly-replaced result and whether a truncation happened.
func limitResultOutput(result any, max int64) (any, bool) {
	blob, err := json.Marshal(result)
	if err != nil {
		return result, false
	}
	var m map[string]any
	if err := json.Unmarshal(blob, &m); err != nil {
		return result, false
	}
	out, ok := m["output"].(string)
	if !ok || int64(len(out)) <= max {
		return result, false
	}
	m["output"] = truncateUTF8(out, int(max)) +
		fmt.Sprintf("\n...[truncated by tool safety guard: output exceeded max_output_bytes=%d]", max)
	m["output_truncated"] = true
	return m, true
}

// truncateUTF8 returns at most max bytes of s without splitting a multi-byte
// rune.
func truncateUTF8(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
