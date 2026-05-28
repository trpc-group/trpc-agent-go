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
	"fmt"
	"sort"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// Engine parses and executes filter expressions against tool output.
// It validates that only allowed operations are used and enforces
// size and timeout limits.
type Engine struct {
	cfg *config
}

// NewEngine creates a filter engine with the given configuration.
func NewEngine(cfg *config) *Engine {
	return &Engine{cfg: cfg}
}

// Apply parses the filter expression and applies it to the tool result.
func (e *Engine) Apply(ctx context.Context, result any, filterExpr string) (*ToolResult, error) {
	if filterExpr == "" {
		return &ToolResult{
			Content: resultToString(result),
		}, nil
	}

	pipeline, err := e.parse(filterExpr)
	if err != nil {
		return nil, fmt.Errorf("parse filter: %w", err)
	}

	return e.applyPipeline(ctx, result, pipeline)
}

// applyPipeline executes a pre-parsed pipeline against a tool result.
func (e *Engine) applyPipeline(ctx context.Context, result any, pipeline *Pipeline) (*ToolResult, error) {
	input := resultToString(result)
	inputTotalBytes := len(input)

	// Enforce max input size (UTF-8 safe).
	inputTruncated := false
	if e.cfg.maxInput > 0 && int64(len(input)) > e.cfg.maxInput {
		input = truncateUTF8(input, int(e.cfg.maxInput))
		inputTruncated = true
	}

	// Apply pipeline with timeout.
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	output, err := pipeline.Execute(execCtx, input)
	if err != nil {
		return nil, fmt.Errorf("execute filter: %w", err)
	}

	truncated := false
	totalFilteredBytes := len(output)
	if e.cfg.maxOutput > 0 && int64(len(output)) > e.cfg.maxOutput {
		output = windowOutput(output, int(e.cfg.maxOutput))
		truncated = true
	}

	tr := &ToolResult{
		Filter:    pipeline.expr,
		Truncated: truncated,
		Content:   output,
	}
	if truncated {
		tr.TotalBytes = totalFilteredBytes
	}
	if inputTruncated {
		tr.InputTruncated = true
		tr.InputTotalBytes = inputTotalBytes
	}
	return tr, nil
}

// Pipeline is a sequence of operations parsed from a shell-like expression.
type Pipeline struct {
	Ops  []Op
	expr string // original filter expression for metadata
}

// Execute runs the pipeline over input sequentially.
func (p *Pipeline) Execute(ctx context.Context, input string) (string, error) {
	current := input
	for _, op := range p.Ops {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		var err error
		current, err = op.Apply(ctx, current)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

// parse uses mvdan.cc/sh/v3/syntax to parse the filter expression,
// then validates and converts it to our internal Pipeline.
func (e *Engine) parse(expr string) (*Pipeline, error) {
	reader := strings.NewReader(expr)
	parser := syntax.NewParser()
	file, err := parser.Parse(reader, "filter")
	if err != nil {
		return nil, fmt.Errorf("shell parse error: %w", err)
	}

	if len(file.Stmts) == 0 {
		return nil, fmt.Errorf("empty filter expression")
	}
	if len(file.Stmts) > 1 {
		return nil, fmt.Errorf("only single pipeline expressions are supported, got %d statements", len(file.Stmts))
	}

	stmt := file.Stmts[0]

	// Reject redirections.
	if len(stmt.Redirs) > 0 {
		return nil, fmt.Errorf("redirections are not allowed in filter expressions")
	}

	// Reject background, negation, coprocess — these are shell execution
	// modifiers that have no meaning in our filter DSL.
	if stmt.Negated {
		return nil, fmt.Errorf("negation (!) is not allowed in filter expressions")
	}
	if stmt.Background {
		return nil, fmt.Errorf("background (&) is not allowed in filter expressions")
	}
	if stmt.Coprocess {
		return nil, fmt.Errorf("coprocess is not allowed in filter expressions")
	}

	// The command must be a pipeline (or single call command).
	var calls []*syntax.CallExpr
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		calls = append(calls, cmd)
	case *syntax.BinaryCmd:
		if cmd.Op != syntax.Pipe {
			return nil, fmt.Errorf("only pipe (|) operator is allowed, got %v", cmd.Op)
		}
		collected, err := collectPipelineCalls(cmd)
		if err != nil {
			return nil, err
		}
		calls = collected
	default:
		return nil, fmt.Errorf("unsupported command type %T; only simple commands and pipes are allowed", cmd)
	}

	// Limit pipeline length.
	if len(calls) > 10 {
		return nil, fmt.Errorf("pipeline too long: %d stages (max 10)", len(calls))
	}

	ops := make([]Op, 0, len(calls))
	for _, call := range calls {
		op, err := e.callToOp(call)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return &Pipeline{Ops: ops, expr: expr}, nil
}

// collectPipelineCalls recursively collects CallExpr nodes from a
// BinaryCmd pipeline tree.
func collectPipelineCalls(bin *syntax.BinaryCmd) ([]*syntax.CallExpr, error) {
	var result []*syntax.CallExpr

	// Left side.
	switch left := bin.X.Cmd.(type) {
	case *syntax.CallExpr:
		result = append(result, left)
	case *syntax.BinaryCmd:
		if left.Op != syntax.Pipe {
			return nil, fmt.Errorf("only pipe (|) operator is allowed")
		}
		sub, err := collectPipelineCalls(left)
		if err != nil {
			return nil, err
		}
		result = append(result, sub...)
	default:
		return nil, fmt.Errorf("unsupported command type %T in pipeline", left)
	}

	// Right side.
	switch right := bin.Y.Cmd.(type) {
	case *syntax.CallExpr:
		result = append(result, right)
	case *syntax.BinaryCmd:
		if right.Op != syntax.Pipe {
			return nil, fmt.Errorf("only pipe (|) operator is allowed")
		}
		sub, err := collectPipelineCalls(right)
		if err != nil {
			return nil, err
		}
		result = append(result, sub...)
	default:
		return nil, fmt.Errorf("unsupported command type %T in pipeline", right)
	}
	return result, nil
}

// callToOp converts a parsed shell CallExpr into an internal Op.
func (e *Engine) callToOp(call *syntax.CallExpr) (Op, error) {
	if len(call.Args) == 0 {
		return nil, fmt.Errorf("empty command in pipeline")
	}

	// Reject assignments and redirections at call level.
	if len(call.Assigns) > 0 {
		return nil, fmt.Errorf("variable assignments are not allowed")
	}

	// Extract command name and arguments.
	parts := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		s, err := wordToString(word)
		if err != nil {
			return nil, err
		}
		parts = append(parts, s)
	}

	cmdName := parts[0]
	cmdArgs := parts[1:]

	opType := OpType(cmdName)
	if !e.cfg.allowedOps[opType] {
		return nil, fmt.Errorf("operation %q is not allowed; allowed: %s", cmdName, e.allowedOpsString())
	}

	switch opType {
	case OpGrep:
		return parseGrepOp(cmdArgs)
	case OpHead:
		return parseHeadOp(cmdArgs)
	case OpTail:
		return parseTailOp(cmdArgs)
	case OpJQ:
		return parseJQOp(cmdArgs)
	default:
		return nil, fmt.Errorf("unknown operation: %q", cmdName)
	}
}

// wordToString converts a syntax.Word to a plain string.
// Returns an error for unsupported shell constructs (variable expansion,
// command substitution, process substitution, etc.) — fail closed.
func wordToString(word *syntax.Word) (string, error) {
	var buf bytes.Buffer
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			buf.WriteString(p.Value)
		case *syntax.SglQuoted:
			buf.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, sub := range p.Parts {
				switch s := sub.(type) {
				case *syntax.Lit:
					buf.WriteString(s.Value)
				default:
					return "", fmt.Errorf("unsupported shell construct in double-quoted string: %T", sub)
				}
			}
		default:
			return "", fmt.Errorf("unsupported shell construct: %T (only literals and quoted strings are allowed)", part)
		}
	}
	return buf.String(), nil
}

func (e *Engine) allowedOpsString() string {
	ops := make([]string, 0, len(e.cfg.allowedOps))
	for op := range e.cfg.allowedOps {
		ops = append(ops, string(op))
	}
	sort.Strings(ops)
	return strings.Join(ops, ", ")
}

// windowOutput applies head+tail windowing to output that exceeds maxBytes.
// The marker is included in the budget so the total content never exceeds maxBytes.
func windowOutput(content string, maxBytes int) string {
	// The marker format is "\n\n...(NNNNN bytes omitted)...\n\n".
	// Max realistic omitted count is ~10 digits. Marker overhead ≈ 38 chars.
	// Use a conservative estimate; post-verify and trim if needed.
	markerOverhead := len("\n\n...(1234567890 bytes omitted)...\n\n") // 38
	if maxBytes <= markerOverhead*2 {
		// Extremely small budget — just prefix-truncate.
		return truncateUTF8(content, maxBytes)
	}

	usable := maxBytes - markerOverhead
	headBudget := usable / 2
	tailBudget := usable - headBudget

	head := truncateUTF8(content, headBudget)
	tail := content[len(content)-tailBudget:]
	// Ensure tail starts at a UTF-8 boundary.
	for len(tail) > 0 && !isRuneStart(tail[0]) {
		tail = tail[1:]
	}

	omitted := len(content) - len(head) - len(tail)
	middle := fmt.Sprintf("\n\n...(%d bytes omitted)...\n\n", omitted)
	result := head + middle + tail

	// Post-verify: if actual marker was longer than estimate, trim tail from
	// its FRONT (not back) to preserve the "end of output" semantics.
	// Loop until within budget (marker digit count may shift on each iteration).
	for len(result) > maxBytes {
		excess := len(result) - maxBytes
		newTailLen := len(tail) - excess
		if newTailLen <= 0 {
			tail = ""
		} else {
			tail = suffixUTF8(tail, newTailLen)
		}
		omitted = len(content) - len(head) - len(tail)
		middle = fmt.Sprintf("\n\n...(%d bytes omitted)...\n\n", omitted)
		result = head + middle + tail
	}
	return result
}
