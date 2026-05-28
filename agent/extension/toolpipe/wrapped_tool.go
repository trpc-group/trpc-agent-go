//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolpipe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// declaredCallableTool is a request-local tool wrapper that returns
// an augmented Declaration while delegating Call and optional interfaces
// (SkipSummarization) to the original.
// It is NOT a persistent wrapper — it only lives within one
// BeforeModel callback's Request.Tools replacement. The original
// tool object is never mutated.
//
// IMPORTANT: This wrapper does NOT implement StreamableTool. If the
// inner tool is streamable, use declaredStreamableCallableTool instead.
// This prevents the framework from routing non-streaming tools through
// the streaming execution path.
type declaredCallableTool struct {
	inner tool.CallableTool
	decl  *tool.Declaration
}

// declaredStreamableCallableTool extends declaredCallableTool for tools
// that implement StreamableTool. Only this type satisfies the
// tool.StreamableTool interface.
type declaredStreamableCallableTool struct {
	declaredCallableTool
	streamable tool.StreamableTool
}

// newDeclaredCallableTool creates the appropriate wrapper based on whether
// the REAL underlying tool implements StreamableTool.
//
// Critical: NamedTool (from ToolSet/MCP) always implements StreamableTool
// on its wrapper regardless of the actual inner tool. We must unwrap through
// Original() to check the true underlying tool's capabilities. Otherwise,
// all MCP tools would be incorrectly routed through the streaming path.
func newDeclaredCallableTool(inner tool.CallableTool, decl *tool.Declaration) tool.CallableTool {
	base := declaredCallableTool{inner: inner, decl: decl}

	// Determine the real tool for streamability check.
	realTool := unwrapOriginal(inner)

	if isReallyStreamable(realTool) {
		// Find the StreamableTool interface on the immediate inner
		// (it may be the NamedTool which forwards to the real streamable).
		st, ok := inner.(tool.StreamableTool)
		if !ok || st == nil {
			// Safety: if immediate inner doesn't satisfy StreamableTool
			// (shouldn't happen if realTool is streamable, but be defensive),
			// fall back to non-streaming wrapper.
			return &base
		}
		return &declaredStreamableCallableTool{
			declaredCallableTool: base,
			streamable:           st,
		}
	}
	return &base
}

// unwrapOriginal recursively unwraps tool wrappers that implement
// Original() tool.Tool (e.g. NamedTool) to find the true underlying tool.
func unwrapOriginal(t tool.Tool) tool.Tool {
	type originator interface{ Original() tool.Tool }
	if o, ok := t.(originator); ok {
		return unwrapOriginal(o.Original())
	}
	return t
}

// isReallyStreamable checks if a tool truly supports streaming,
// respecting the StreamInner preference.
func isReallyStreamable(t tool.Tool) bool {
	// Check StreamInner preference — if tool opts out, don't treat as streamable.
	type streamPref interface{ StreamInner() bool }
	if pref, ok := t.(streamPref); ok && !pref.StreamInner() {
		return false
	}
	_, ok := t.(tool.StreamableTool)
	return ok
}

// Declaration implements tool.Tool — returns the augmented schema.
func (t *declaredCallableTool) Declaration() *tool.Declaration {
	return t.decl
}

// Call implements tool.CallableTool — delegates directly to inner.
// Note: argument stripping is handled by BeforeTool, not here.
func (t *declaredCallableTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return t.inner.Call(ctx, jsonArgs)
}

// SkipSummarization delegates to the inner tool if it implements the interface.
func (t *declaredCallableTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	if s, ok := t.inner.(skipper); ok {
		return s.SkipSummarization()
	}
	return false
}

// StreamableCall implements tool.StreamableTool — only on the streamable wrapper.
func (t *declaredStreamableCallableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}

// isFrameworkTool detects tools that implement framework control interfaces
// or are known framework-internal tools by name. Detected categories:
//   - Known names: transfer_to_agent, await_user_reply
//   - StreamInner() — sub-agent streaming control (AgentTool)
//   - InnerTextMode() — inner text forwarding (AgentTool)
//   - LongRunning() returning true — long-running lifecycle tools
//   - StateDelta / StateDeltaForInvocation — session state mutation tools
//
// These tools should NOT be augmented by toolpipe. Their output is either
// framework-semantic or consumed by framework state machinery that expects
// the raw tool result format.
//
// This is a CONSERVATIVE heuristic: matching tools are skipped even if
// explicitly in the allowlist.
//
// This check uses the REAL underlying tool (unwrapped from NamedTool).
func isFrameworkTool(t tool.Tool) bool {
	real := unwrapOriginal(t)

	// Known framework tool names.
	if decl := real.Declaration(); decl != nil {
		switch decl.Name {
		case "transfer_to_agent", "await_user_reply":
			return true
		}
	}

	// StreamInner — AgentTool, any tool that controls sub-agent streaming.
	type streamInner interface{ StreamInner() bool }
	if _, ok := real.(streamInner); ok {
		return true
	}

	// InnerTextMode — controls inner text forwarding behavior.
	type innerTextMode interface{ InnerTextMode() tool.InnerTextMode }
	if _, ok := real.(innerTextMode); ok {
		return true
	}

	// LongRunning — tools with special execution lifecycle.
	// Only skip if the tool is ACTUALLY long-running (returns true).
	type longRunner interface{ LongRunning() bool }
	if lr, ok := real.(longRunner); ok && lr.LongRunning() {
		return true
	}

	// StateDelta / StateDeltaForInvocation — tools that produce session state
	// mutations (todo lists, artifacts, skill selections, etc.). Wrapping these
	// would hide the interface from the framework AND corrupt the state delta
	// input (it would receive ToolResult envelope instead of original output).
	type stateDeltaProvider interface {
		StateDelta(string, []byte, []byte) map[string][]byte
	}
	if _, ok := real.(stateDeltaProvider); ok {
		return true
	}
	type invocationStateDeltaProvider interface {
		StateDeltaForInvocation(*agent.Invocation, string, []byte, []byte) map[string][]byte
	}
	if _, ok := real.(invocationStateDeltaProvider); ok {
		return true
	}

	return false
}

// ToolResult is the structured result returned by toolpipe to the model.
// It covers two scenarios:
//   - Filtered: model used result_filter, content is the filtered projection.
//   - Windowed: model didn't use filter, but output exceeded maxOutput and was
//     automatically truncated to a preview window.
//
// Design principle: minimal token footprint in the happy path.
// Only non-empty fields are serialized (omitempty).
type ToolResult struct {
	// Filter is the expression that was applied. Present only when model used result_filter.
	Filter string `json:"filter,omitempty"`
	// Content is the output (filtered or windowed).
	Content string `json:"content"`
	// Truncated is true when output was cut to fit the window.
	Truncated bool `json:"truncated,omitempty"`
	// TotalBytes is the original output size before truncation. Helps model
	// understand how much data is available for filter-based extraction.
	TotalBytes int `json:"total_bytes,omitempty"`
	// InputTruncated is true when the tool output was truncated BEFORE filtering
	// because it exceeded maxInput. The filter operated on partial data.
	InputTruncated bool `json:"input_truncated,omitempty"`
	// InputTotalBytes is the original tool output size before input truncation.
	InputTotalBytes int `json:"input_total_bytes,omitempty"`
	// Error describes a filter execution or parse error.
	Error string `json:"error,omitempty"`
	// EmptyReason explains why content is empty (filter matched nothing, etc).
	EmptyReason string `json:"empty_reason,omitempty"`
	// OriginalPreview shows a snippet when filter failed or matched nothing.
	OriginalPreview string `json:"original_preview,omitempty"`
}

// truncateForPreview truncates a string to maxLen with UTF-8 safety.
func truncateForPreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return truncateUTF8(s, maxLen) + "...(truncated)"
}

// truncateUTF8 truncates s to at most maxBytes while respecting UTF-8
// character boundaries. It never returns an invalid UTF-8 string.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards from maxBytes to find a valid rune boundary.
	for maxBytes > 0 && !isRuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// isRuneStart reports whether byte b is the start of a UTF-8 rune.
// In UTF-8, continuation bytes have the form 10xxxxxx.
func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// suffixUTF8 returns the last maxBytes bytes of s, adjusted forward to
// start at a UTF-8 rune boundary. Preserves the END of the string (unlike
// truncateUTF8 which preserves the start).
func suffixUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	// Advance past any continuation bytes to find a valid rune start.
	for start < len(s) && !isRuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// augmentDeclaration creates a copy of the original declaration
// with the filter field added to its input schema.
// It also clears OutputSchema since toolpipe may replace the output
// with a ToolResult envelope.
func augmentDeclaration(orig *tool.Declaration, filterField string, allowedOps map[OpType]bool) *tool.Declaration {
	if orig == nil {
		return nil
	}
	augmented := &tool.Declaration{
		Name:        orig.Name,
		Description: orig.Description,
		// OutputSchema intentionally NOT copied: once toolpipe is active,
		// the output may be the original result OR a ToolResult envelope.
		// Keeping the original OutputSchema would mislead the model.
	}

	if orig.InputSchema == nil {
		augmented.InputSchema = &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				filterField: filterFieldSchema(allowedOps),
			},
		}
	} else {
		augmented.InputSchema = copySchema(orig.InputSchema)
		if augmented.InputSchema.Properties == nil {
			augmented.InputSchema.Properties = make(map[string]*tool.Schema)
		}
		// Only add if not already present (avoid collision).
		if _, exists := augmented.InputSchema.Properties[filterField]; !exists {
			augmented.InputSchema.Properties[filterField] = filterFieldSchema(allowedOps)
		}
	}
	return augmented
}

// filterFieldSchema returns the schema for the filter field.
// The description is dynamically generated based on allowed ops.
func filterFieldSchema(allowedOps map[OpType]bool) *tool.Schema {
	ops := make([]string, 0, len(allowedOps))
	for op := range allowedOps {
		ops = append(ops, string(op))
	}
	sort.Strings(ops)

	// Build example using only actually-enabled ops.
	var exampleParts []string
	if allowedOps[OpJQ] {
		exampleParts = append(exampleParts, "jq -r '.field'")
	}
	if allowedOps[OpGrep] {
		exampleParts = append(exampleParts, "grep pattern")
	}
	if allowedOps[OpHead] {
		exampleParts = append(exampleParts, "head 20")
	}
	example := strings.Join(exampleParts, " | ")
	if example == "" {
		example = strings.Join(ops, " | ")
	}

	return &tool.Schema{
		Type: "string",
		Description: fmt.Sprintf(
			`Shell-like pipeline to filter this tool's output (e.g. "%s"). Supported ops: %s. Applied before result enters context.`,
			example,
			strings.Join(ops, ", "),
		),
	}
}

// canAugmentSchema checks whether a tool's input schema can safely have
// a property added. Only nil or object-type schemas are safe.
func canAugmentSchema(t tool.Tool) bool {
	decl := t.Declaration()
	if decl == nil {
		return false
	}
	schema := decl.InputSchema
	if schema == nil {
		return true // nil schema → we create an object schema
	}
	// Only "object" type (or unspecified type with properties) can have properties added.
	return schema.Type == "object" || (schema.Type == "" && schema.Properties != nil)
}

// copySchema makes a shallow copy of a Schema (enough for our augmentation).
func copySchema(s *tool.Schema) *tool.Schema {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Properties != nil {
		cp.Properties = make(map[string]*tool.Schema, len(s.Properties))
		for k, v := range s.Properties {
			cp.Properties[k] = v
		}
	}
	if s.Required != nil {
		cp.Required = append([]string(nil), s.Required...)
	}
	return &cp
}

// extractFilterEx extracts and removes the filter field from JSON args.
// Returns (cleanArgs, filterExpr, present, error).
// present indicates whether the field existed in args at all.
func extractFilterEx(jsonArgs []byte, filterField string) ([]byte, string, bool, error) {
	if len(jsonArgs) == 0 {
		return jsonArgs, "", false, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		// Not a JSON object — pass through unchanged.
		return jsonArgs, "", false, nil
	}
	filterRaw, present := args[filterField]
	if !present {
		return jsonArgs, "", false, nil
	}
	var filterExpr string
	if err := json.Unmarshal(filterRaw, &filterExpr); err != nil {
		// filter field is present but not a string — still remove it.
		delete(args, filterField)
		clean, _ := json.Marshal(args)
		return clean, "", true, nil
	}
	// Remove the filter field and re-marshal.
	delete(args, filterField)
	clean, err := json.Marshal(args)
	if err != nil {
		return jsonArgs, "", true, fmt.Errorf("re-marshal args: %w", err)
	}
	return clean, filterExpr, true, nil
}

// extractFilter is a simplified wrapper for tests and backward compat.
func extractFilter(jsonArgs []byte, filterField string) ([]byte, string, error) {
	clean, expr, _, err := extractFilterEx(jsonArgs, filterField)
	return clean, expr, err
}

// resultToString converts an arbitrary tool result to a string for filtering.
// For structured results (JSON), it uses indented formatting so that grep
// and other line-based ops can work naturally on the output.
// HTML-special characters are NOT escaped (unlike json.Marshal default).
func resultToString(result any) string {
	if result == nil {
		return ""
	}
	switch v := result.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return fmt.Sprintf("%v", v)
		}
		// Encoder.Encode appends a trailing newline; trim it.
		out := buf.Bytes()
		if len(out) > 0 && out[len(out)-1] == '\n' {
			out = out[:len(out)-1]
		}
		return string(out)
	}
}

// Compile-time interface checks.
var (
	_ tool.Tool           = (*declaredCallableTool)(nil)
	_ tool.CallableTool   = (*declaredCallableTool)(nil)
	_ tool.Tool           = (*declaredStreamableCallableTool)(nil)
	_ tool.CallableTool   = (*declaredStreamableCallableTool)(nil)
	_ tool.StreamableTool = (*declaredStreamableCallableTool)(nil)
)
