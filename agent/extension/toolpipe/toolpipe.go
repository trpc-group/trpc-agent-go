//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolpipe provides an agent-scoped extension that augments
// selected tools with a shell-like result filtering capability.
//
// Models can write familiar shell pipeline syntax (e.g. "grep ERROR | head 20")
// in an injected result_filter parameter. The extension intercepts the tool
// call, strips the filter, executes the original tool, and returns only the
// filtered projection to the model — reducing context pollution without
// opening a real CLI environment.
//
// This is a callback-only MVP that does NOT modify the framework's core chain.
// It uses BeforeModel + BeforeTool + AfterTool to achieve tool schema
// augmentation, argument stripping, and result filtering.
//
// Usage:
//
//	agent := llmagent.New("researcher",
//	    llmagent.WithTools([]tool.Tool{webFetchTool, queryLogsTool}),
//	    llmagent.WithExtensions(
//	        toolpipe.New(
//	            toolpipe.WithToolNames("web_fetch", "query_logs"),
//	            toolpipe.WithAllowedOps(toolpipe.OpGrep, toolpipe.OpHead, toolpipe.OpTail, toolpipe.OpJQ),
//	        ),
//	    ),
//	)
package toolpipe

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Context keys for passing state between callbacks.
type (
	filterContextKey   struct{} // BeforeTool → AfterTool: filterState
	augmentedSetCtxKey struct{} // BeforeModel → BeforeTool: map[string]bool
)

// ToolPipe is an agent-scoped extension that wraps selected tools
// with a result filter capability. It implements extension.Extension.
//
// It registers three callbacks:
//   - BeforeModel: augments allowed tools' schema with result_filter field
//   - BeforeTool: extracts and strips result_filter from arguments
//   - AfterTool: applies the filter pipeline to the tool result
type ToolPipe struct {
	cfg    *config
	engine *Engine
}

// New creates a new ToolPipe extension with the given options.
// At least one of WithToolNames or WithToolScope must be
// provided; otherwise the extension wraps nothing.
func New(opts ...Option) *ToolPipe {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &ToolPipe{
		cfg:    cfg,
		engine: NewEngine(cfg),
	}
}

// Name implements extension.Extension.
func (p *ToolPipe) Name() string {
	return "toolpipe"
}

// Register implements extension.Extension.
func (p *ToolPipe) Register(r *extension.Registry) {
	r.BeforeModel(p.beforeModel)
	r.BeforeTool(p.beforeTool)
	r.AfterTool(p.afterTool)
}

// beforeModel augments allowed tools' schemas with the result_filter field,
// injects a system prompt fragment, and passes the augmented tool name set
// to BeforeTool via context.
func (p *ToolPipe) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}

	// --- Schema augmentation ---
	augmentedSet := p.augmentToolSchemas(args.Request)

	// --- System prompt injection (only if not already injected) ---
	if len(augmentedSet) > 0 && !p.promptInjected(args.Request) {
		names := sortedKeys(augmentedSet)
		prompt := p.resolvePrompt(names)
		if prompt != "" {
			p.injectSystemPrompt(args.Request, prompt)
		}
	}

	// Pass augmented set to BeforeTool via context so it knows exactly
	// which tools were augmented this round — no re-derivation needed.
	if len(augmentedSet) > 0 {
		newCtx := context.WithValue(ctx, augmentedSetCtxKey{}, augmentedSet)
		return &model.BeforeModelResult{Context: newCtx}, nil
	}
	return nil, nil
}

// toolpipeMarker is a private marker embedded in the injected prompt
// to detect duplicate injection. It is a zero-width space followed by
// an HTML-style comment; functionally it consumes minimal tokens.
const toolpipeMarker = "\u200B<!-- toolpipe -->"

// promptInjected checks whether toolpipe's prompt has already been
// injected, preventing duplicate injection in multi-turn conversations.
func (p *ToolPipe) promptInjected(req *model.Request) bool {
	for _, msg := range req.Messages {
		if msg.Role == model.RoleSystem && strings.Contains(msg.Content, toolpipeMarker) {
			return true
		}
	}
	return false
}

// augmentToolSchemas adds the result_filter field to eligible tools' schemas.
// Returns the set of tool names that were actually augmented.
func (p *ToolPipe) augmentToolSchemas(req *model.Request) map[string]bool {
	if len(req.Tools) == 0 {
		return nil
	}
	augmented := make(map[string]bool)
	for name, t := range req.Tools {
		if !p.shouldWrap(t) {
			continue
		}
		callable, ok := t.(tool.CallableTool)
		if !ok {
			continue
		}
		// Skip framework/orchestration tools (AgentTool, etc.) — their output
		// is framework-semantic, not user-data suitable for grep/jq.
		if isFrameworkTool(t) {
			continue
		}
		// Skip if the tool already has a field with the same name as
		// our filter field — we must not collide with existing schema.
		if toolHasField(t, p.cfg.filterField) {
			continue
		}
		// Skip if the tool's input schema is not object-compatible.
		if !canAugmentSchema(t) {
			continue
		}
		req.Tools[name] = newDeclaredCallableTool(
			callable,
			augmentDeclaration(t.Declaration(), p.cfg.filterField, p.cfg.allowedOps),
		)
		augmented[name] = true
	}
	if len(augmented) == 0 {
		return nil
	}
	return augmented
}

// toolHasField checks whether a tool's input schema already defines
// a property with the given name.
func toolHasField(t tool.Tool, field string) bool {
	decl := t.Declaration()
	if decl == nil || decl.InputSchema == nil || decl.InputSchema.Properties == nil {
		return false
	}
	_, exists := decl.InputSchema.Properties[field]
	return exists
}

// injectSystemPrompt appends a guidance fragment to the system message.
func (p *ToolPipe) injectSystemPrompt(req *model.Request, prompt string) {
	// Embed the marker so we can detect re-injection in multi-turn.
	tagged := prompt + toolpipeMarker

	// Find existing system message and append; or prepend a new one.
	for i, msg := range req.Messages {
		if msg.Role == model.RoleSystem {
			req.Messages[i].Content += "\n\n" + tagged
			return
		}
	}
	// No system message found — prepend one.
	req.Messages = append(
		[]model.Message{model.NewSystemMessage(tagged)},
		req.Messages...,
	)
}

// resolvePrompt determines the prompt to inject based on user config.
// Returns empty string when injection should be skipped.
func (p *ToolPipe) resolvePrompt(toolNames []string) string {
	if p.cfg.customPrompt != nil {
		return *p.cfg.customPrompt // may be "" to disable
	}
	return p.defaultSystemPrompt(toolNames)
}

// Prompt returns the default prompt that toolpipe would inject for
// the configured tools and ops. Useful for users who want to
// reference or embed this text in their own WithInstruction.
//
// Note: When using WithToolScope, the actual prompt injected at
// runtime may include additional tool names discovered dynamically.
// This method only shows tools from WithToolNames.
func (p *ToolPipe) Prompt() string {
	// No ops = no augmentation at runtime, so no prompt either.
	if len(p.cfg.allowedOps) == 0 {
		return ""
	}
	// No tools configured = nothing will be wrapped at runtime.
	if len(p.cfg.allowedNames) == 0 && p.cfg.predicate == nil {
		return ""
	}
	names := make([]string, 0, len(p.cfg.allowedNames))
	for n := range p.cfg.allowedNames {
		names = append(names, n)
	}
	if len(names) == 0 && p.cfg.predicate != nil {
		names = append(names, "<tools matching scope>")
	}
	sort.Strings(names)
	return p.defaultSystemPrompt(names)
}

// defaultSystemPrompt generates the built-in guidance based on augmented tools and allowed ops.
// Design principle: describe capability and output format only. Do NOT prescribe
// usage strategy — that belongs in the user's instruction, not the framework.
func (p *ToolPipe) defaultSystemPrompt(toolNames []string) string {
	ops := make([]string, 0, len(p.cfg.allowedOps))
	for op := range p.cfg.allowedOps {
		ops = append(ops, string(op))
	}
	sort.Strings(ops)
	sort.Strings(toolNames)

	field := p.cfg.filterField
	r := strings.NewReplacer(
		"{field}", field,
		"{tools}", strings.Join(toolNames, ", "),
		"{ops}", strings.Join(ops, ", "),
	)

	// Build the structured data hint only if jq is enabled.
	structuredHint := "Structured results are serialized as pretty JSON for line-based filtering."
	if p.cfg.allowedOps[OpJQ] {
		// Only mention ops that are actually enabled alongside jq.
		var companions []string
		if p.cfg.allowedOps[OpGrep] {
			companions = append(companions, "grep")
		}
		if p.cfg.allowedOps[OpHead] || p.cfg.allowedOps[OpTail] {
			companions = append(companions, "head/tail")
		}
		if len(companions) > 0 {
			structuredHint = fmt.Sprintf("Structured results are filtered as pretty JSON; use jq -r to extract fields before %s. Text results can be filtered directly.", strings.Join(companions, "/"))
		} else {
			structuredHint = "Structured results are filtered as pretty JSON; use jq -r to extract fields. Text results can be filtered directly."
		}
	}

	return r.Replace(`[toolpipe] Tools with {field}: {tools}
Accepts shell-like pipeline syntax. Ops: {ops}. Combine with pipes.
` + structuredHint + `
Large output is automatically windowed (head+tail with middle omitted, total_bytes shown).
Response format when {field} is used: {"filter":"<the expr you wrote>", "content":"<filtered output>", "truncated":bool, "total_bytes":N}
Response format without {field} (large output): {"content":"<head>...omitted...<tail>", "truncated":true, "total_bytes":N}
Without {field} (small output): original tool response unchanged.
If input_truncated is true, the filter ran on partial data — refine the filter or use narrower tool parameters.
{field} applies a targeted projection to large or structured results.`)
}

// beforeTool extracts the result_filter from arguments, parses it,
// stores the compiled pipeline in context, and returns cleaned args.
//
// Critical design: eligibility is determined SOLELY by whether the tool
// name is in the augmented set passed from BeforeModel via context.
// This guarantees consistency: only tools whose schema was actually
// augmented in this request will have their arguments processed.
func (p *ToolPipe) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	// Only process tools that were actually augmented in this request's
	// BeforeModel pass. This single check replaces all name/predicate/
	// native-field re-derivation and guarantees no false positives.
	augmented, _ := ctx.Value(augmentedSetCtxKey{}).(map[string]bool)
	if !augmented[args.ToolName] {
		return nil, nil
	}

	cleanArgs, filterExpr, present, err := extractFilterEx(args.Arguments, p.cfg.filterField)
	if err != nil {
		// Extraction error — still strip the field to be safe.
		return &tool.BeforeToolResult{
			ModifiedArguments: cleanArgs,
		}, nil
	}
	if !present {
		// Field not in args at all — nothing to do.
		return nil, nil
	}
	if filterExpr == "" {
		// Field present but empty — strip it so strict-schema tools don't reject.
		return &tool.BeforeToolResult{
			ModifiedArguments: cleanArgs,
		}, nil
	}

	// Parse and validate the filter expression.
	pipeline, err := p.engine.parse(filterExpr)
	if err != nil {
		// Parse failed — still strip the filter field so it doesn't reach
		// the original tool, but store error state for AfterTool to report.
		newCtx := context.WithValue(ctx, filterContextKey{}, &filterState{
			filterExpr: filterExpr,
			parseError: err.Error(),
		})
		return &tool.BeforeToolResult{
			Context:           newCtx,
			ModifiedArguments: cleanArgs,
		}, nil
	}

	// Store pipeline in context for AfterTool to pick up.
	newCtx := context.WithValue(ctx, filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: filterExpr,
	})

	return &tool.BeforeToolResult{
		Context:           newCtx,
		ModifiedArguments: cleanArgs,
	}, nil
}

// afterTool applies the filter pipeline to the tool result.
// For augmented tools, it also enforces output size protection even
// when no filter is used — mimicking the "truncated stream" behavior
// of CLI environments where large output is always windowed.
func (p *ToolPipe) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if args == nil {
		return nil, nil
	}

	// If the tool itself failed, skip entirely.
	if args.Error != nil {
		return nil, nil
	}

	// Check if this tool was augmented (in the augmented set).
	augmented, _ := ctx.Value(augmentedSetCtxKey{}).(map[string]bool)
	isAugmented := augmented[args.ToolName]

	state, _ := ctx.Value(filterContextKey{}).(*filterState)

	// Case 1: No filter state — tool was augmented but model didn't use filter.
	// Apply output size protection so large results don't flood context.
	if state == nil {
		if !isAugmented {
			return nil, nil
		}
		return p.truncateUnfilteredResult(args.Result)
	}

	// Case 2: Filter parse error — return error annotation with preview.
	if state.parseError != "" {
		raw := resultToString(args.Result)
		return &tool.AfterToolResult{
			CustomResult: &ToolResult{
				Filter:          state.filterExpr,
				Error:           "parse error: " + state.parseError,
				Content:         truncateForPreview(raw, 2048),
				OriginalPreview: truncateForPreview(raw, 1024),
			},
		}, nil
	}

	// Case 3: Normal filter — apply pipeline.
	filtered, err := p.engine.applyPipeline(ctx, args.Result, state.pipeline)
	if err != nil {
		raw := resultToString(args.Result)
		errResult := &ToolResult{
			Filter:          state.filterExpr,
			Error:           err.Error(),
			Content:         truncateForPreview(raw, 2048),
			OriginalPreview: truncateForPreview(raw, 1024),
		}
		// If input would have been truncated, include that context —
		// it may be the root cause of the error (e.g., jq invalid JSON).
		if p.cfg.maxInput > 0 && int64(len(raw)) > p.cfg.maxInput {
			errResult.InputTruncated = true
			errResult.InputTotalBytes = len(raw)
		}
		return &tool.AfterToolResult{
			CustomResult: errResult,
		}, nil
	}

	// If filter produced empty content but original had data.
	if filtered.Content == "" {
		original := resultToString(args.Result)
		if len(original) > 0 {
			filtered.OriginalPreview = truncateForPreview(original, 1024)
			filtered.EmptyReason = "filter produced no output from non-empty result"
		}
	}

	return &tool.AfterToolResult{
		CustomResult: filtered,
	}, nil
}

// truncateUnfilteredResult enforces output size protection using head+tail
// strategy. The model sees the beginning and end of the output
// with a middle-truncation marker, giving it structural overview without
// flooding context. This discourages "multi-round reconstruction" behavior.
func (p *ToolPipe) truncateUnfilteredResult(result any) (*tool.AfterToolResult, error) {
	maxBytes := int(p.cfg.maxOutput)

	// Fast path: for string/[]byte results, check length without extra allocation.
	switch v := result.(type) {
	case string:
		if len(v) <= maxBytes {
			return nil, nil
		}
	case []byte:
		if len(v) <= maxBytes {
			return nil, nil
		}
	}

	content := resultToString(result)

	// If output fits within limit, don't wrap — return unchanged.
	if len(content) <= maxBytes {
		return nil, nil
	}

	// Head+tail strategy using shared helper (marker budget accounted for).
	windowed := windowOutput(content, maxBytes)

	return &tool.AfterToolResult{
		CustomResult: &ToolResult{
			Content:    windowed,
			Truncated:  true,
			TotalBytes: len(content),
		},
	}, nil
}

// shouldWrap checks whether a tool matches the configured allowlist or predicate.
func (p *ToolPipe) shouldWrap(t tool.Tool) bool {
	if t == nil || t.Declaration() == nil {
		return false
	}
	// No ops configured — nothing useful can be filtered.
	if len(p.cfg.allowedOps) == 0 {
		return false
	}
	name := t.Declaration().Name
	if p.cfg.allowedNames[name] {
		return true
	}
	if p.cfg.predicate != nil && p.cfg.predicate(t) {
		return true
	}
	return false
}

// filterState holds the parsed pipeline between BeforeTool and AfterTool.
type filterState struct {
	pipeline   *Pipeline
	filterExpr string
	parseError string // non-empty if filter parsing failed
}

// sortedKeys returns sorted keys from a map.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compile-time interface check.
var _ extension.Extension = (*ToolPipe)(nil)
