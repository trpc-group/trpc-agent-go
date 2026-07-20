//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// OutputLimitCallback returns an AfterTool callback that enforces the policy's
// Limits.MaxOutputBytes as a RESULT-SIZE limit: it truncates a recognised exec
// tool's captured output before the result is returned to the model, once the
// output exceeds the cap. It bounds what the model sees, not what the executor
// produces (AfterTool runs after the tool has already generated and captured
// its output), so it is not a runtime resource ceiling — pair it with an
// executor-level cap for that.
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
// Register this callback LAST: returning a truncated result short-circuits the
// remaining AfterTool callbacks. A negative MaxOutputBytes disables the cap;
// zero is treated as unset and defaults to 1 MiB (see LimitsPolicy).
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
//
// Replacing the typed result with a map is safe: RunAfterTool passes the
// original args.Result to every callback (it does not feed one callback's
// CustomResult into the next) and stops at the first CustomResult, so no later
// callback ever receives this map; the framework then serialises it to JSON for
// the model, where it is identical to the original result apart from the
// truncated output and the added marker.
func limitResultOutput(result any, max int64) (any, bool) {
	blob, err := json.Marshal(result)
	if err != nil {
		return result, false
	}
	// UseNumber keeps numeric fields (exit_code, offset, ...) exact instead of
	// widening them to float64, so the re-serialised result matches the original.
	dec := json.NewDecoder(bytes.NewReader(blob))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return result, false
	}
	out, ok := m["output"].(string)
	if !ok || int64(len(out)) <= max {
		return result, false
	}
	marker := fmt.Sprintf("\n...[truncated by tool safety guard: output exceeded max_output_bytes=%d]", max)
	// Budget for the marker so the final output field stays within the cap
	// (best-effort: a cap smaller than the marker itself cannot be honoured).
	budget := max - int64(len(marker))
	if budget < 0 {
		budget = 0
	}
	m["output"] = truncateUTF8(out, int(budget)) + marker
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
