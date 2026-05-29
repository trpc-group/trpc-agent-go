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
	"regexp"
	"strconv"
	"strings"

	"github.com/itchyny/gojq"
)

// Op is a single filter operation in a pipeline.
type Op interface {
	Apply(ctx context.Context, input string) (string, error)
}

// --- Grep ---

type grepOp struct {
	pattern *regexp.Regexp
	invert  bool
}

func parseGrepOp(args []string) (Op, error) {
	op := &grepOp{}
	var patternStr string
	ignoreCase := false
	endOfFlags := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !endOfFlags && arg == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] != '-' {
			// Parse combined short flags like -Ei, -iv, etc.
			for _, ch := range arg[1:] {
				switch ch {
				case 'i':
					ignoreCase = true
				case 'v':
					op.invert = true
				case 'E':
					// -E (extended regex) — Go's regexp is already extended, no-op.
				case 'h':
					// -h (suppress filename) — no-op in our context (no filenames).
				default:
					return nil, fmt.Errorf("grep: unsupported flag -%c", ch)
				}
			}
		} else {
			if patternStr != "" {
				return nil, fmt.Errorf("grep: multiple patterns not supported")
			}
			patternStr = arg
		}
	}
	if patternStr == "" {
		return nil, fmt.Errorf("grep: pattern required")
	}

	if ignoreCase {
		patternStr = "(?i)" + patternStr
	}

	// Limit pattern length for safety.
	if len(patternStr) > 1024 {
		return nil, fmt.Errorf("grep: pattern too long (max 1024 chars)")
	}

	re, err := regexp.Compile(patternStr)
	if err != nil {
		return nil, fmt.Errorf("grep: invalid regex %q: %w", patternStr, err)
	}
	op.pattern = re
	return op, nil
}

func (g *grepOp) Apply(_ context.Context, input string) (string, error) {
	lines := strings.Split(input, "\n")
	var result []string
	for _, line := range lines {
		matches := g.pattern.MatchString(line)
		if g.invert {
			matches = !matches
		}
		if matches {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n"), nil
}

// --- Head ---

type headOp struct {
	n int
}

func parseHeadOp(args []string) (Op, error) {
	n := 10 // default
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-n" && i+1 < len(args):
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("head: invalid count %q", args[i])
			}
			n = v
		case strings.HasPrefix(args[i], "-"):
			// Try parsing -N format (e.g. -20).
			v, err := strconv.Atoi(args[i][1:])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("head: invalid flag %q", args[i])
			}
			n = v
		default:
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("head: invalid count %q", args[i])
			}
			n = v
		}
	}
	return &headOp{n: n}, nil
}

func (h *headOp) Apply(_ context.Context, input string) (string, error) {
	lines := strings.Split(input, "\n")
	if len(lines) <= h.n {
		return input, nil
	}
	return strings.Join(lines[:h.n], "\n"), nil
}

// --- Tail ---

type tailOp struct {
	n int
}

func parseTailOp(args []string) (Op, error) {
	n := 10 // default
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-n" && i+1 < len(args):
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("tail: invalid count %q", args[i])
			}
			n = v
		case strings.HasPrefix(args[i], "-"):
			v, err := strconv.Atoi(args[i][1:])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("tail: invalid flag %q", args[i])
			}
			n = v
		default:
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("tail: invalid count %q", args[i])
			}
			n = v
		}
	}
	return &tailOp{n: n}, nil
}

func (t *tailOp) Apply(_ context.Context, input string) (string, error) {
	lines := strings.Split(input, "\n")
	if len(lines) <= t.n {
		return input, nil
	}
	return strings.Join(lines[len(lines)-t.n:], "\n"), nil
}

// --- JQ ---

type jqOp struct {
	code      *gojq.Code
	rawOutput bool
}

func parseJQOp(args []string) (Op, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("jq: expression required")
	}

	rawOutput := false
	var exprParts []string
	for _, arg := range args {
		if arg == "-r" || arg == "--raw-output" {
			rawOutput = true
		} else {
			exprParts = append(exprParts, arg)
		}
	}

	if len(exprParts) == 0 {
		return nil, fmt.Errorf("jq: expression required")
	}

	// Join remaining args as the jq expression.
	expr := strings.Join(exprParts, " ")

	if len(expr) > 2048 {
		return nil, fmt.Errorf("jq: expression too long (max 2048 chars)")
	}

	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jq: parse error: %w", err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("jq: compile error: %w", err)
	}
	return &jqOp{code: code, rawOutput: rawOutput}, nil
}

func (j *jqOp) Apply(ctx context.Context, input string) (string, error) {
	// Try to parse input as JSON.
	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return "", fmt.Errorf("jq: input is not valid JSON: %w", err)
	}

	const iterLimit = 10000
	var results []string
	iter := j.code.RunWithContext(ctx, data)
	truncated := false
	for i := 0; ; i++ {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if i >= iterLimit {
			truncated = true
			break
		}
		if err, isErr := v.(error); isErr {
			// Provide a friendlier error for common cases.
			errMsg := err.Error()
			if strings.Contains(errMsg, "iterate over: null") {
				return "", fmt.Errorf("jq: field is null or missing (no data to iterate)")
			}
			if strings.Contains(errMsg, "null") && strings.Contains(errMsg, "cannot") {
				return "", fmt.Errorf("jq: cannot operate on null value — the field may be empty")
			}
			return "", fmt.Errorf("jq: %w", err)
		}
		// Skip null values silently (common with optional fields).
		if v == nil {
			continue
		}
		// With -r flag, output strings without quotes.
		if j.rawOutput {
			if s, ok := v.(string); ok {
				results = append(results, s)
				continue
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			results = append(results, fmt.Sprintf("%v", v))
		} else {
			results = append(results, string(b))
		}
	}
	output := strings.Join(results, "\n")
	if truncated {
		output += fmt.Sprintf("\n...(truncated: iteration limit %d reached)", iterLimit)
	}
	return output, nil
}
