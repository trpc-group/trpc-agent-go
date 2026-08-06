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
// Limits.MaxOutputBytes as a RESULT-SIZE limit: before a recognised exec tool's
// result reaches the model, it truncates the model-facing payload — the captured
// output and, for codeexec, the returned file contents — down to one shared
// budget. It bounds what the model sees, not what the executor produces
// (AfterTool runs after the tool has already generated and captured its output),
// so it is not a runtime resource ceiling — pair it with an executor-level cap
// for that.
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

// limitResultOutput bounds every model-facing text field of a tool result to a
// SINGLE shared budget of max bytes: the top-level "output" string first, then
// each codeexec "output_files[*].content" from whatever remains. Sharing one
// budget matters — codeexec returns file contents to the model alongside stdout
// (codeexecutor.CodeExecutionResult), so a per-field cap would let a short
// "output" plus arbitrarily many or arbitrarily large files sail past the limit.
//
// It round-trips through JSON so it works for any exec result shape that carries
// those fields, preserving the result's other fields and marking what it cut
// ("output_truncated" on the result, "truncated" on a file, which is already
// part of the codeexec file schema). It returns the possibly-replaced result and
// whether anything was truncated.
//
// Replacing the typed result with a map is safe: RunAfterTool passes the
// original args.Result to every callback (it does not feed one callback's
// CustomResult into the next) and stops at the first CustomResult, so no later
// callback ever receives this map; the framework then serialises it to JSON for
// the model, where it is identical to the original result apart from the
// truncated fields and the added markers.
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
	remaining, changed := max, false
	if out, ok := m["output"].(string); ok {
		kept, cut := clampText(out, remaining, max)
		if cut {
			m["output"] = kept
			m["output_truncated"] = true
			changed = true
		}
		remaining = spend(remaining, kept)
	}
	files, _ := m["output_files"].([]any)
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		content, ok := fm["content"].(string)
		if !ok || content == "" {
			continue
		}
		kept, cut := clampText(content, remaining, max)
		if cut {
			fm["content"] = kept
			fm["truncated"] = true
			changed = true
		}
		remaining = spend(remaining, kept)
	}
	if !changed {
		return result, false
	}
	return m, true
}

// clampText truncates s to budget bytes on a UTF-8 rune boundary and appends a
// marker naming the configured cap. It reports whether it truncated. A budget
// smaller than the marker itself cannot be honoured, so the marker is kept and
// the text dropped: the model is told the field was cut rather than being shown
// a silently partial value.
func clampText(s string, budget, max int64) (string, bool) {
	if int64(len(s)) <= budget {
		return s, false
	}
	marker := fmt.Sprintf("\n...[truncated by tool safety guard: result exceeded max_output_bytes=%d]", max)
	room := budget - int64(len(marker))
	if room < 0 {
		room = 0
	}
	return truncateUTF8(s, int(room)) + marker, true
}

// spend deducts the bytes a kept field consumed from the shared budget, never
// going below zero.
func spend(budget int64, kept string) int64 {
	if budget -= int64(len(kept)); budget < 0 {
		return 0
	}
	return budget
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
