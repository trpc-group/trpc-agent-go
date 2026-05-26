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
		// Skip if the tool already has a field with the same name as
		// our filter field — we must not collide with existing schema.
		if toolHasField(t, p.cfg.filterField) {
			continue
		}
		req.Tools[name] = newDeclaredCallableTool(
			callable,
			augmentDeclaration(t.Declaration(), p.cfg.filterField),
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

	return fmt.Sprintf(`[toolpipe] Tools with %s: %s
Accepts shell-like pipeline syntax. Ops: %s. Combine with pipes.
Structured results are filtered as pretty JSON; use jq -r to extract fields before grep/head/tail. Text results can be filtered directly.
Large output is automatically windowed (head+tail with middle omitted, total_bytes shown).
With %s: {"filter":"<expr>", "content":"<filtered text>"}
Without %s (large output): {"content":"<head>...omitted...<tail>", "truncated":true, "total_bytes":N}
Without %s (small output): original tool response unchanged.
Use %s when you need a specific slice from a large or structured result.`,
		p.cfg.filterField,
		strings.Join(toolNames, ", "),
		strings.Join(ops, ", "),
		p.cfg.filterField,
		p.cfg.filterField,
		p.cfg.filterField,
		p.cfg.filterField,
	)
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
		return &tool.AfterToolResult{
			CustomResult: &ToolResult{
				Filter:          state.filterExpr,
				Error:           "parse error: " + state.parseError,
				Content:         truncateForPreview(resultToString(args.Result), 2048),
				OriginalPreview: truncateForPreview(resultToString(args.Result), 1024),
			},
		}, nil
	}

	// Case 3: Normal filter — apply pipeline.
	filtered, err := p.engine.applyPipeline(ctx, args.Result, state.pipeline)
	if err != nil {
		return &tool.AfterToolResult{
			CustomResult: &ToolResult{
				Filter:          state.filterExpr,
				Error:           err.Error(),
				Content:         truncateForPreview(resultToString(args.Result), 2048),
				OriginalPreview: truncateForPreview(resultToString(args.Result), 1024),
			},
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
	content := resultToString(result)
	maxBytes := int(p.cfg.maxOutput)

	// If output fits within limit, don't wrap — return unchanged.
	if len(content) <= maxBytes {
		return nil, nil
	}

	// Head+tail strategy: split budget 50/50, keep start and end.
	headBudget := maxBytes / 2
	tailBudget := maxBytes - headBudget

	head := truncateUTF8(content, headBudget)
	tail := content[len(content)-tailBudget:]
	// Ensure tail starts at a UTF-8 boundary.
	for len(tail) > 0 && !isRuneStart(tail[0]) {
		tail = tail[1:]
	}

	omitted := len(content) - len(head) - len(tail)
	middle := fmt.Sprintf("\n\n...(%d bytes omitted)...\n\n", omitted)
	windowed := head + middle + tail

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
