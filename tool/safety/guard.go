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
	"strings"

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
	// reads "command", "code", "code_blocks", "stdin" and "chars" JSON
	// fields; callers can substitute their own to support non-JSON
	// tools or multi-field extraction. The toolName parameter enables
	// extraction to adapt its behaviour per tool (e.g. different
	// primary fields for exec_command vs write_stdin).
	extract func(args []byte, toolName string) ScanInput
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
// The toolName argument lets the extractor adapt per tool; it may be empty
// when called outside of CheckToolPermission (e.g. from wiring.go).
func WithExtractor(fn func(args []byte, toolName string) ScanInput) GuardOption {
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
	if g == nil {
		return tool.DenyPermission("safety guard is nil"), nil
	}
	extract := g.extract
	if extract == nil {
		extract = defaultExtractor
	}
	scanner := g.scanner
	if scanner == nil {
		scanner = NewScanner(
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
	input := extract(req.Arguments, req.ToolName)
	res := scanner.Scan(input)

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

// defaultExtractor reads the "command"/"code", "code_blocks", "stdin"
// and "chars" fields from JSON arguments, populating ScanInput.Command
// and ScanInput.CodeBlocks respectively.
//
// For exec-type tools (tool name ends with "_exec_command", "exec_command"
// or bare executor names) stdin content is folded into Command so it is
// scanned by command-line rules.
//
// For write_stdin-type tools the "chars" payload is placed into CodeBlocks
// (as an untagged code block) so it is scanned by code-level rules.
//
// For tools whose names end with "_stop_session" or "kill_session" the
// extractor returns immediately with an empty input because these sessions
// will be recycled and cannot inject new code.
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
//	{"command": "python3", "stdin": "import os; os.system('rm -rf /')"}
//	{"chars": "import os; os.system('rm -rf /')"}    // write_stdin continuation
//	{"command": "ls", "code_blocks": [
//	    {"language": "python", "code": "import os; os.system('rm -rf /')"},
//	    {"code": "print('hi')"},
//	]}
//	{"code_blocks": ["raw string 1", {"code": "..."}]}  // strings allowed
//
// Anything else falls through with Command = "" and CodeBlocks = nil,
// which is the same behaviour as the previous substring-only extractor.
func defaultExtractor(args []byte, toolName string) ScanInput {
	in := ScanInput{ExecutorType: "local"}
	raw, ok := parseExtractorArgs(args)
	if !ok {
		return in
	}

	// Tool-aware routing: different tools map their arguments to different
	// ScanInput fields so that the rules inspect the right payload shape.
	// - Code-exec tools (execute_tool_code, execute_python_code, ...):
	//   primary payload is "code", placed into CodeBlocks
	// - Web-fetch tools (web_fetch, web_search, ...):
	//   primary payload is "url" / "urls", placed into URLs
	// - Shell / exec tools (exec_command, hostexec, workspaceexec, ...):
	//   primary payload is "command", placed into Command
	// - write_stdin: continuation payload is "chars", placed into CodeBlocks
	if isCodeExecTool(toolName) {
		appendCharsCodeBlock(&in, raw)
		code := firstStringField(raw, "code", "command")
		if code != "" {
			in.CodeBlocks = append(in.CodeBlocks, CodeBlock{Code: code})
		}
		mergeStdinPayload(&in, raw, toolName)
		appendParsedCodeBlocks(&in, raw)
	} else if isWebFetchTool(toolName) {
		appendCharsCodeBlock(&in, raw)
		if urls := stringSliceField(raw, "urls"); len(urls) > 0 {
			in.URLs = urls
		} else if url := firstStringField(raw, "url"); url != "" {
			in.URLs = []string{url}
		}
		in.Command = firstStringField(raw, "command", "code")
		mergeStdinPayload(&in, raw, toolName)
		appendParsedCodeBlocks(&in, raw)
	} else {
		appendCharsCodeBlock(&in, raw)
		in.Command = firstStringField(raw, "command", "code")
		mergeStdinPayload(&in, raw, toolName)
		appendParsedCodeBlocks(&in, raw)
	}

	// Extract workdir so sensitive-path rules can resolve relative
	// traversals ("../.ssh/id_rsa") against the actual working directory.
	in.Workdir = firstStringField(raw, "workdir", "work_dir", "cwd")

	// Compute a shell-normalised form of Command so that quoted
	// command names ("c''url", "b""ash") are resolved to their plain
	// argv tokens before substring matching. Only populated when
	// shellsafe parsing succeeds.
	if in.Command != "" && in.NormalizedCommand == "" {
		if parsed, err := ParseCommand(in.Command); err == nil && len(parsed.Segments) > 0 {
			var argv []string
			for _, seg := range parsed.Segments {
				argv = append(argv, seg...)
			}
			in.NormalizedCommand = strings.Join(argv, " ")
		}
	}

	return in
}

func parseExtractorArgs(args []byte) (map[string]json.RawMessage, bool) {
	if len(args) == 0 {
		return nil, false
	}
	args = bytes.TrimLeft(args, " \t\n\r\v\f")
	if len(args) == 0 {
		return nil, false
	}
	// Fast path: a non-JSON blob (e.g. raw shell) — return empty so the
	// scan pipeline still runs with a zero-value input. We deliberately
	// do not try to parse it as JSON to avoid a misleading panic on
	// malformed payloads.
	if args[0] != '{' && args[0] != '[' {
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

func appendCharsCodeBlock(in *ScanInput, raw map[string]json.RawMessage) {
	// "chars" is the primary payload for write_stdin-type tools and
	// interactive session continuations (host/workspace/skill).
	// It should be scanned like code.
	if s, ok := stringField(raw, "chars"); ok && s != "" {
		in.CodeBlocks = append(in.CodeBlocks, CodeBlock{Code: s})
	}
}

func firstStringField(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if s, ok := stringField(raw, key); ok {
			return s
		}
	}
	return ""
}

func stringField(raw map[string]json.RawMessage, key string) (string, bool) {
	v, ok := raw[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

func mergeStdinPayload(in *ScanInput, raw map[string]json.RawMessage, toolName string) {
	_ = toolName
	// "stdin" piped to exec-command tools is executable code. Push it into
	// CodeBlocks so code-aware rules can inspect it. We intentionally avoid
	// constructing a shell here-doc ("<<<") because that is not how the
	// executor actually interprets stdin at runtime — the Guard must reason
	// about the real execution model, not a synthetic shell snippet.
	stdin, ok := stringField(raw, "stdin")
	if !ok || stdin == "" {
		return
	}
	// For write_stdin / session-continuation tools, the executor may send
	// incremental "chars" alongside the accumulated "stdin". Concatenate
	// them so the scanner evaluates the full payload rather than the
	// latest chunk alone, closing the incremental-stdin evasion gap
	// reported in PR #2044 section 2 ("do not scan continuation chunks
	// independently").
	if chars, ok := stringField(raw, "chars"); ok && chars != "" && !strings.HasSuffix(stdin, chars) {
		stdin = stdin + chars
	}
	if in.Command == "" {
		in.Command = stdin
		return
	}
	if isInterpreterCommand(in.Command) {
		// Interactive interpreter: stdin is the real payload.
		// Use stdin as the primary scan target and also push it into
		// CodeBlocks so code-aware rules see it.
		in.Command = stdin
		in.CodeBlocks = append(in.CodeBlocks, CodeBlock{Code: stdin})
		return
	}
	// Non-interpreter: push stdin as a CodeBlock so rules inspect it
	// independently, rather than fusing it into Command with a <<< here-doc.
	in.CodeBlocks = append(in.CodeBlocks, CodeBlock{Code: stdin})
}

func isInterpreterCommand(cmd string) bool {
	switch cmd {
	case "python3", "python", "python2", "node", "ruby", "perl", "bash":
		return true
	default:
		return false
	}
}

func appendParsedCodeBlocks(in *ScanInput, raw map[string]json.RawMessage) {
	// "code_blocks" is the canonical list shape used by tool/codeexec.
	// It may be a normal array, a single object (instead of an array),
	// or a double-encoded JSON string containing either of the above.
	v, ok := raw["code_blocks"]
	if !ok {
		return
	}
	for _, cb := range parseCodeBlocks(v) {
		if cb.Code != "" {
			in.CodeBlocks = append(in.CodeBlocks, cb)
		}
	}
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

// codeExecToolSuffixes lists tool names whose primary payload is a code
// block. When the extractor sees any of these suffixes in the tool name
// it routes the "code" field into CodeBlocks rather than Command.
var codeExecToolSuffixes = []string{
	"code_exec", "codeexec",
	"execute_code", "execute_tool_code",
	"run_code", "run_python_code",
	"python_code", "shell_command",
	"execute_python", "execute_shell", "execute_javascript",
}

// isCodeExecTool reports whether toolName matches a code-execution tool.
func isCodeExecTool(toolName string) bool {
	toolName = strings.ToLower(toolName)
	for _, suffix := range codeExecToolSuffixes {
		if strings.HasSuffix(toolName, suffix) {
			return true
		}
	}
	return false
}

// webFetchToolKeywords lists tool names whose primary payload is a URL.
// When the extractor sees any of these substrings in the tool name it
// routes the "url" / "urls" field into ScanInput.URLs.
var webFetchToolKeywords = []string{
	"web_fetch", "web_fetch_url",
	"web_search", "web_search_url",
	"fetch_url", "fetch_page",
	"http_fetch", "http_get",
}

// isWebFetchTool reports whether toolName matches a web-fetch / URL tool.
func isWebFetchTool(toolName string) bool {
	toolName = strings.ToLower(toolName)
	for _, kw := range webFetchToolKeywords {
		if strings.HasSuffix(toolName, kw) {
			return true
		}
	}
	return false
}

// stringSliceField reads a JSON array-of-strings from raw.
// If the key is missing or the value is not valid, it returns nil.
func stringSliceField(raw map[string]json.RawMessage, key string) []string {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err != nil {
		return nil
	}
	return arr
}
