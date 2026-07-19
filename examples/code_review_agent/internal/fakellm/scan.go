//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakellm

import (
	"encoding/json"
	"strings"
)

// Finding is the JSON-serialisable shape the fake model emits in its
// response. Field names mirror rules.Finding so the pipeline can parse
// the LLM output with the same code path it uses for rule findings.
type Finding struct {
	RuleID         string  `json:"rule_id"`
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
}

// scan applies a small set of high-signal heuristics to the diff text
// and returns the findings an LLM might plausibly surface. The patterns
// are intentionally simple and deterministic — the goal is to exercise
// the LLM integration path, not to compete with the regex/AST engines.
//
// Recognised patterns:
//   - "password" / "passwd" / "secret" / "api_key" / "apikey" assignment
//     -> LLM-001 hardcoded credential (critical)
//   - "TODO" / "FIXME" / "XXX" comment
//     -> LLM-002 unresolved TODO (low)
//   - "fmt.Println" / "fmt.Printf" in non-test file
//     -> LLM-003 debug print left in production code (medium)
func scan(diffText string) []Finding {
	if diffText == "" {
		return nil
	}
	var out []Finding
	currentFile := ""
	lineNo := 0
	for _, raw := range strings.Split(diffText, "\n") {
		lineNo++
		switch {
		case strings.HasPrefix(raw, "+++ b/"):
			currentFile = strings.TrimPrefix(raw, "+++ b/")
		case strings.HasPrefix(raw, "--- a/"):
			// old path; ignore
		case strings.HasPrefix(raw, "+"):
			content := strings.TrimPrefix(raw, "+")
			out = append(out, scanLine(currentFile, lineNo, content)...)
		}
	}
	return out
}

// scanLine checks a single added line for the fake-model patterns. The
// line number passed in is the line's position in the diff text, not in
// the source file; this is good enough for the fake model's purposes
// (callers care about the file + rule, not the exact line).
func scanLine(file string, line int, content string) []Finding {
	var out []Finding
	low := strings.ToLower(content)

	if hasCredentialPattern(low) {
		out = append(out, Finding{
			RuleID:         "LLM-001",
			Severity:       "critical",
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "Possible hardcoded credential (LLM heuristic)",
			Evidence:       truncate(content, 120),
			Recommendation: "Move secrets to environment variables or a secret manager; never commit them to source.",
			Confidence:     0.78,
		})
	}

	if hasTODOComment(content) {
		out = append(out, Finding{
			RuleID:         "LLM-002",
			Severity:       "low",
			Category:       "quality",
			File:           file,
			Line:           line,
			Title:          "Unresolved TODO/FIXME comment",
			Evidence:       truncate(content, 120),
			Recommendation: "Either resolve the TODO before merging or file a tracking issue with the context.",
			Confidence:     0.90,
		})
	}

	if hasDebugPrint(content, file) {
		out = append(out, Finding{
			RuleID:         "LLM-003",
			Severity:       "medium",
			Category:       "quality",
			File:           file,
			Line:           line,
			Title:          "Debug print statement left in production code",
			Evidence:       truncate(content, 120),
			Recommendation: "Remove the print statement or gate it behind a debug flag; prefer structured logging.",
			Confidence:     0.72,
		})
	}

	return out
}

// hasCredentialPattern reports whether the lowercased line looks like a
// hardcoded credential assignment. It matches identifiers such as
// password, passwd, secret, api_key, apikey, token, bearer, followed
// by '=' or ':' and a non-empty value.
func hasCredentialPattern(low string) bool {
	for _, kw := range []string{"password", "passwd", "secret", "api_key", "apikey", "token", "bearer"} {
		// Look for "kw=" or "kw :" or `kw="` style assignments. The
		// keyword must be at a word boundary: preceded by start-of-line,
		// whitespace, or a quote.
		idx := strings.Index(low, kw)
		for idx >= 0 {
			if atWordBoundary(low, idx, len(kw)) {
				rest := low[idx+len(kw):]
				rest = strings.TrimLeft(rest, " \t")
				if strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":") {
					// Must have a non-empty value after the separator.
					val := strings.TrimSpace(rest[1:])
					if val != "" && val != "\"\"" && val != "''" {
						return true
					}
				}
			}
			next := idx + 1
			if next >= len(low) {
				break
			}
			idx = strings.Index(low[next:], kw)
			if idx < 0 {
				break
			}
			idx += next
		}
	}
	return false
}

// atWordBoundary reports whether the substring low[idx:idx+kwLen] is
// bounded on both sides by non-word characters (or string edges).
func atWordBoundary(low string, idx, kwLen int) bool {
	if idx > 0 {
		prev := low[idx-1]
		if isWordChar(prev) {
			return false
		}
	}
	end := idx + kwLen
	if end < len(low) {
		nxt := low[end]
		if isWordChar(nxt) {
			return false
		}
	}
	return true
}

// isWordChar reports whether c is a word character (letter, digit, or
// underscore). Identifiers in most languages use this set.
func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

// hasTODOComment reports whether the line contains a TODO/FIXME/XXX
// comment marker. It matches both // and /* */ styles and is
// case-insensitive.
func hasTODOComment(content string) bool {
	upper := strings.ToUpper(content)
	return strings.Contains(upper, "TODO") ||
		strings.Contains(upper, "FIXME") ||
		strings.Contains(upper, "XXX")
}

// hasDebugPrint reports whether the line calls fmt.Println/Printf in a
// non-test file. Test files are exempt because debug prints in tests
// are normal.
func hasDebugPrint(content, file string) bool {
	if strings.HasSuffix(file, "_test.go") {
		return false
	}
	return strings.Contains(content, "fmt.Println") ||
		strings.Contains(content, "fmt.Printf") ||
		strings.Contains(content, "fmt.Print(")
}

// truncate caps s at n characters, appending "..." if truncation
// occurred. It keeps evidence strings short enough for a single-line
// report cell.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// encodeFindings marshals the findings slice to a JSON string. The
// pipeline parses this back into findings when merging LLM results
// with rule results. An empty slice is encoded as "[]" rather than
// "null" so the JSON is always a valid array.
func encodeFindings(findings []Finding) string {
	if len(findings) == 0 {
		return "[]"
	}
	b, err := json.Marshal(findings)
	if err != nil {
		// Finding is a simple struct of primitives; json.Marshal only
		// fails on unsupported types, which cannot happen here.
		return "[]"
	}
	return string(b)
}

// ParseFindings decodes the JSON content of a FakeModel response back
// into a slice of Finding. The pipeline uses this to merge LLM findings
// with rule findings. Unknown fields in the JSON are ignored so the
// schema can grow without breaking older parsers.
func ParseFindings(content string) []Finding {
	if content == "" || content == "[]" {
		return nil
	}
	var out []Finding
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil
	}
	return out
}
