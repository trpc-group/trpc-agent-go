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

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Guard wraps a Scanner as a tool.PermissionPolicy so it can be
// plugged into a Runner before every tool call.
//
// Usage:
//
//	guard := safety.NewGuard(safety.WithRules(
//	    safety.NewDangerousCommandRule(),
//	    safety.NewNetworkAccessRule(),
//	    ...
//	))
//	// Then pass to Runner as a per-run option:
//	//   runner.Run(ctx, userID, sessionID, msg,
//	//       agent.WithToolPermissionPolicy(guard))
type Guard struct {
	// scanner runs the configured rule set on every tool call.
	scanner *Scanner
	// extract converts raw tool arguments into a ScanInput. The default
	// reads a "command" JSON field; callers can substitute their own to
	// support non-JSON tools or multi-field extraction.
	extract func(args []byte) ScanInput
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithRules sets the rules used by the guard's Scanner.
func WithRules(rules ...Rule) GuardOption {
	return func(g *Guard) { g.scanner = NewScanner(rules...) }
}

// WithScanner uses an existing Scanner.
func WithScanner(s *Scanner) GuardOption {
	return func(g *Guard) { g.scanner = s }
}

// WithExtractor sets a custom function to extract ScanInput from tool arguments.
// The default extractor looks for a "command" field in the JSON arguments.
func WithExtractor(fn func(args []byte) ScanInput) GuardOption {
	return func(g *Guard) { g.extract = fn }
}

// NewGuard creates a Guard that implements tool.PermissionPolicy.
func NewGuard(opts ...GuardOption) *Guard {
	g := &Guard{extract: defaultExtractor}
	for _, o := range opts {
		o(g)
	}
	if g.scanner == nil {
		g.scanner = NewScanner(
			NewParseFailureRule(),
			NewShellWrapperRule(),
			NewDangerousCommandRule(),
			NewNetworkAccessRule(),
			NewShellBypassRule(),
			NewInstallAndMutateRule(),
			NewHostExecRiskRule(),
			NewResourceAbuseRule(),
			NewSensitiveInfoLeakRule(),
			NewAskForReviewRule(),
		)
	}
	return g
}

// CheckToolPermission implements tool.PermissionPolicy.
//
// It extracts the command from the request arguments, runs the configured
// Scanner, and translates the resulting Decision into a tool.PermissionDecision.
func (g *Guard) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	_ = ctx // reserved for future per-context policy overrides (e.g. user-specific allowlists).
	input := g.extract(req.Arguments)
	res := g.scanner.Scan(input)

	switch res.Decision {
	case DecisionAllow:
		return tool.AllowPermission(), nil
	case DecisionDeny:
		return tool.DenyPermission(res.Reason), nil
	case DecisionAsk:
		return tool.AskPermission(res.Reason), nil
	default:
		// Decision is an exported string type and Rule is a public extension
		// point; a custom rule or version mismatch can return an unknown
		// value. Treat it as denial so the safety boundary never fails open.
		return tool.DenyPermission(fmt.Sprintf("unknown safety decision %q", res.Decision)), nil
	}
}

// defaultExtractor reads the "command" string and "code_blocks" array
// from JSON arguments, populating ScanInput.Command and
// ScanInput.CodeBlocks respectively.
//
// This is the default Guard argument extractor; it is intentionally
// permissive: any JSON-decode failure returns a ScanInput with
// ExecutorType set and both fields empty, so a later rule can still
// fire on empty input rather than silently allowing the call. Callers
// that need a richer argument shape (e.g. nested structs, raw bytes)
// should override the extractor with WithExtractor / WithGuardedExtractor.
//
// Recognized shapes:
//
//	{"command": "rm -rf /tmp/x"}
//	{"code": "rm -rf /tmp/x"}                       // legacy "code" alias
//	{"command": "ls", "code_blocks": [
//	    {"language": "python", "code": "import os; os.system('rm -rf /')"},
//	    {"code": "print('hi')"},
//	]}
//	{"code_blocks": ["raw string 1", {"code": "..."}]}  // strings allowed
//
// Anything else falls through with Command = "" and CodeBlocks = nil,
// which is the same behaviour as the previous substring-only extractor.
func defaultExtractor(args []byte) ScanInput {
	in := ScanInput{ExecutorType: "local"}
	if len(args) == 0 {
		return in
	}
	args = bytes.TrimLeft(args, " \t\n\r\v\f")
	if len(args) == 0 {
		return in
	}
	// Fast path: a non-JSON blob (e.g. raw shell) — return empty so the
	// scan pipeline still runs with a zero-value input. We deliberately
	// do not try to parse it as JSON to avoid a misleading panic on
	// malformed payloads.
	if args[0] != '{' && args[0] != '[' {
		return in
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return in
	}
	// "command" is the primary field; "code" is a legacy alias kept
	// for back-compat with callers that pre-date the code_blocks
	// support added in response to WineChord's review on PR #2044.
	for _, key := range []string{"command", "code"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			in.Command = s
			break
		}
	}
	// "code_blocks" is the canonical list shape used by tool/codeexec.
	// It may be a normal array, a single object (instead of an array),
	// or a double-encoded JSON string containing either of the above.
	if v, ok := raw["code_blocks"]; ok {
		blocks := parseCodeBlocks(v)
		for _, cb := range blocks {
			if cb.Code != "" {
				in.CodeBlocks = append(in.CodeBlocks, cb)
			}
		}
	}
	return in
}

// parseCodeBlocks mirrors tool/codeexec's unmarshalCodeBlocks so the
// guard accepts the same payload shapes the executor will accept.
// The value may be a normal array, a single object, or a double-encoded
// JSON string containing either of the above.
func parseCodeBlocks(raw json.RawMessage) []CodeBlock {
	val, ok := unmarshalJSONAny(raw)
	if !ok {
		return nil
	}
	// If the LLM double-encoded the value as a JSON string, unwrap and re-parse.
	if s, ok := val.(string); ok {
		val, ok = unmarshalJSONAny(json.RawMessage(s))
		if !ok {
			return nil
		}
	}
	switch v := val.(type) {
	case []any:
		return parseCodeBlockArray(v)
	case map[string]any:
		if cb, ok := codeBlockFromMap(v); ok {
			return []CodeBlock{cb}
		}
		return nil
	default:
		return nil
	}
}

// unmarshalJSONAny unmarshals a non-empty JSON blob into a Go any value.
// It returns false for empty or invalid payloads.
func unmarshalJSONAny(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil || val == nil {
		return nil, false
	}
	return val, true
}

// parseCodeBlockArray converts a JSON array of objects/strings into CodeBlocks.
func parseCodeBlockArray(arr []any) []CodeBlock {
	out := make([]CodeBlock, 0, len(arr))
	for _, elem := range arr {
		if cb, ok := codeBlockFromAny(elem); ok {
			out = append(out, cb)
		}
	}
	return out
}

// codeBlockFromAny converts a single JSON element (object or string) into a CodeBlock.
func codeBlockFromAny(v any) (CodeBlock, bool) {
	if obj, ok := v.(map[string]any); ok {
		return codeBlockFromMap(obj)
	}
	if s, ok := v.(string); ok && s != "" {
		return CodeBlock{Code: s}, true
	}
	return CodeBlock{}, false
}

// codeBlockFromMap extracts a CodeBlock from a JSON object, supporting both
// "language" and "lang" keys.
func codeBlockFromMap(m map[string]any) (CodeBlock, bool) {
	cb := CodeBlock{}
	if s, ok := m["code"].(string); ok {
		cb.Code = s
	}
	if s, ok := m["language"].(string); ok {
		cb.Language = s
	} else if s, ok := m["lang"].(string); ok {
		cb.Language = s
	}
	return cb, cb.Code != ""
}
