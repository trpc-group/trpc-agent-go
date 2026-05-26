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
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// declaredCallableTool is a request-local tool wrapper that returns
// an augmented Declaration while delegating Call and optional interfaces
// (StreamableTool, SkipSummarization) to the original.
// It is NOT a persistent wrapper — it only lives within one
// BeforeModel callback's Request.Tools replacement. The original
// tool object is never mutated.
type declaredCallableTool struct {
	inner tool.CallableTool
	decl  *tool.Declaration
}

func newDeclaredCallableTool(inner tool.CallableTool, decl *tool.Declaration) *declaredCallableTool {
	return &declaredCallableTool{inner: inner, decl: decl}
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

// StreamableCall delegates to the inner tool if it implements StreamableTool.
func (t *declaredCallableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	if st, ok := t.inner.(tool.StreamableTool); ok {
		return st.StreamableCall(ctx, jsonArgs)
	}
	// Fallback: not streamable, call normally (caller should check interface first).
	return nil, fmt.Errorf("tool %q does not support streaming", t.decl.Name)
}

// SkipSummarization delegates to the inner tool if it implements the interface.
func (t *declaredCallableTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	if s, ok := t.inner.(skipper); ok {
		return s.SkipSummarization()
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

// augmentDeclaration creates a copy of the original declaration
// with the filter field added to its input schema.
func augmentDeclaration(orig *tool.Declaration, filterField string) *tool.Declaration {
	if orig == nil {
		return nil
	}
	augmented := &tool.Declaration{
		Name:         orig.Name,
		Description:  orig.Description,
		OutputSchema: orig.OutputSchema,
	}

	if orig.InputSchema == nil {
		augmented.InputSchema = &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				filterField: filterFieldSchema(),
			},
		}
	} else {
		augmented.InputSchema = copySchema(orig.InputSchema)
		if augmented.InputSchema.Properties == nil {
			augmented.InputSchema.Properties = make(map[string]*tool.Schema)
		}
		// Only add if not already present (avoid collision).
		if _, exists := augmented.InputSchema.Properties[filterField]; !exists {
			augmented.InputSchema.Properties[filterField] = filterFieldSchema()
		}
	}
	return augmented
}

// filterFieldSchema returns the schema for the filter field.
// Kept concise — detailed usage guidance is in the system prompt.
func filterFieldSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "string",
		Description: "Shell-like pipeline to filter this tool's output (e.g. \"jq -r '.field' | grep pattern | head 20\"). Applied before result enters context.",
	}
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
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// Compile-time interface checks.
var (
	_ tool.Tool         = (*declaredCallableTool)(nil)
	_ tool.CallableTool = (*declaredCallableTool)(nil)
)
