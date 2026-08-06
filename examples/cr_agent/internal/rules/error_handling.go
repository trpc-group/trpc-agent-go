//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// --- ERR-001: Ignored error return value ---
//
// Detects function calls whose error return value is discarded with
// _ = or simply ignored (no assignment at all). Common patterns:
//   - json.Marshal(x)  // no assignment
//   - _ = someFunc()
//   - w.Write(data)    // io.Writer.Write returns (int, error)

var ignoredErrorPatterns = []string{
	"json.Marshal(",
	"json.Unmarshal(",
	"json.NewEncoder(",
	"json.NewDecoder(",
	"fmt.Fprint(",
	"fmt.Fprintf(",
	"fmt.Fprintln(",
	"w.Write(",
	"conn.Write(",
	"buf.Write(",
}

// ignoredErrorFuncRe matches bare function calls that look like they
// return an error but the result is not captured. The pattern looks
// for a line where an identifier-like function call starts the
// statement and is not preceded by an assignment.
var ignoredErrorFuncRe = compileRegexp(
	`^\s*(?:[A-Z]\w*\.)?[A-Z]\w*\s*\([^)]*\)\s*(?: //.*)?$`,
)

type ignoredErrorRule struct{}

func (r *ignoredErrorRule) ID() string               { return "ERR-001" }
func (r *ignoredErrorRule) Category() types.Category { return types.CategoryErrorHandling }

func (r *ignoredErrorRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		trimmed := strings.TrimSpace(li.content)
		// Skip comments and non-code lines.
		if strings.HasPrefix(trimmed, "//") || trimmed == "" {
			continue
		}
		matched := false
		for _, pat := range ignoredErrorPatterns {
			if !strings.Contains(trimmed, pat) {
				continue
			}
			// Check if the result is assigned (contains = before the
			// pattern).
			idx := strings.Index(trimmed, pat)
			prefix := strings.TrimSpace(trimmed[:idx])
			if prefix == "" || strings.HasSuffix(prefix, "_ =") {
				f := finding(r.ID(), types.SeverityMedium, r.Category(),
					fc, li.lineNum,
					"Error return value ignored",
					trimmed,
					"Check the returned error and handle it appropriately "+
						"(log, wrap, or propagate).",
				)
				f.Confidence = 0.7
				findings = append(findings, f)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// Check for bare exported function calls that are likely
		// returning an error (e.g. ValidateConfig(cfg) without
		// checking the result).
		if ignoredErrorFuncRe.MatchString(li.content) &&
			!strings.Contains(trimmed, "//") {
			// Exclude known void functions by name.
			if isLikelyVoidCall(trimmed) {
				continue
			}
			f := finding(r.ID(), types.SeverityMedium, r.Category(),
				fc, li.lineNum,
				"Possible ignored error from bare function call",
				trimmed,
				"If this function returns an error, assign it to a "+
					"variable and check it. If it is void, consider "+
					"adding a comment to clarify.",
			)
			f.Confidence = 0.5
			findings = append(findings, f)
		}
	}
	return findings
}

// isLikelyVoidCall returns true for function names that are known to
// not return an error, to reduce false positives.
func isLikelyVoidCall(line string) bool {
	knownVoid := []string{
		"Print(", "Println(", "Printf(",
		"Panic(", "Fatal(",
		"append(", "copy(", "delete(",
		"close(", "clear(",
	}
	for _, v := range knownVoid {
		if strings.Contains(line, v) {
			return true
		}
	}
	return false
}

// --- ERR-002: Error shadowing ---
//
// Detects `err :=` inside an if or for block that shadows an outer
// err variable, which can hide errors from the outer scope.

var errorShadowRe = compileRegexp(
	`(?:if|for)\s+.*\{[^}]*err\s*:=`,
)

type errorShadowingRule struct{}

func (r *errorShadowingRule) ID() string               { return "ERR-002" }
func (r *errorShadowingRule) Category() types.Category { return types.CategoryErrorHandling }

func (r *errorShadowingRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	// We check the full content (context + added) since shadowing
	// often involves the if/for line as context.
	content := fc.AllContent()
	if errorShadowRe.MatchString(content) {
		// Find the approximate line.
		for _, li := range addedLines(fc) {
			if strings.Contains(li.content, "err :=") {
				f := finding(r.ID(), types.SeverityLow, r.Category(),
					fc, li.lineNum,
					"Variable 'err' may shadow an outer 'err' declaration",
					strings.TrimSpace(li.content),
					"Use = instead of := to assign to the outer err, or "+
						"rename the inner variable to avoid shadowing.",
				)
				f.Confidence = 0.5
				findings = append(findings, f)
				break
			}
		}
	}
	return findings
}
