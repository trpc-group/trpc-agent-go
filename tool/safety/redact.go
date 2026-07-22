//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	// RedactedValue replaces secret material in reports, audit events, and logs.
	RedactedValue = "[REDACTED]"
)

// Redactor removes secret material without mutating the supplied value.
// Implementations must be safe for concurrent use.
type Redactor interface {
	RedactString(string) (string, int)
	RedactBytes([]byte) ([]byte, int)
	RedactValue(any) (any, int)
}

type patternRedactor struct {
	patterns     []redactionPattern
	sensitiveKey *regexp.Regexp
}

type redactionPattern struct {
	expression  *regexp.Regexp
	replacement string
}

// NewRedactor returns a concurrency-safe redactor for common credential forms.
func NewRedactor() Redactor {
	core := `(?:api[_-]?key|access[_-]?key|access[_-]?token|auth[_-]?token|authorization|client[_-]?secret|credential|password|passwd|pwd|refresh[_-]?token|secret|token|aws[_-]?secret[_-]?access[_-]?key)`
	key := `(?:(?:[A-Za-z0-9]+)[_-])*` + core
	assignment := `(\b` + key + `\b"?\s*[:=]\s*)`
	flag := `(?i)(--` + key + `(?:=|\s+))`
	return &patternRedactor{
		patterns: []redactionPattern{
			newRedactionPattern(`(?s)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----.*?-----END(?: [A-Z0-9]+)* PRIVATE KEY-----`, RedactedValue),
			newRedactionPattern(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]{8,}`, `${1}`+RedactedValue),
			newRedactionPattern(`(?i)(\b[a-z][a-z0-9+.-]*://[^:/\s]+:)[^@\s/]+@`, `${1}`+RedactedValue+`@`),
			newRedactionPattern(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{8,}\b`, RedactedValue),
			newRedactionPattern(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,}\b`, RedactedValue),
			newRedactionPattern(`\bsk-ant-[A-Za-z0-9_-]{16,}\b`, RedactedValue),
			newRedactionPattern(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`, RedactedValue),
			newRedactionPattern(`\b(?:A3T|AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}\b`, RedactedValue),
			newRedactionPattern(`\bAIza[0-9A-Za-z_-]{35}\b`, RedactedValue),
			newRedactionPattern(`\b(?:xox[baprs]-[A-Za-z0-9-]{10,}|glpat-[A-Za-z0-9_-]{16,}|npm_[A-Za-z0-9]{16,})\b`, RedactedValue),
			newRedactionPattern(`(?i)`+assignment+`"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(`(?i)`+assignment+`'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(`(?i)`+assignment+`[^\s,;}\]"']+`, `${1}`+RedactedValue),
			newRedactionPattern(flag+`"[^"]*"`, `${1}"`+RedactedValue+`"`),
			newRedactionPattern(flag+`'[^']*'`, `${1}'`+RedactedValue+`'`),
			newRedactionPattern(flag+`[^\s,;|"']+`, `${1}`+RedactedValue),
		},
		sensitiveKey: regexp.MustCompile(`(?i)^` + key + `$`),
	}
}

func newRedactionPattern(expression, replacement string) redactionPattern {
	return redactionPattern{
		expression:  regexp.MustCompile(expression),
		replacement: replacement,
	}
}

func (r *patternRedactor) RedactString(value string) (string, int) {
	if r == nil || value == "" {
		return value, 0
	}
	parts := strings.Split(value, RedactedValue)
	count := 0
	for partIndex := range parts {
		redacted := parts[partIndex]
		for _, pattern := range r.patterns {
			matches := pattern.expression.FindAllStringIndex(redacted, -1)
			if len(matches) == 0 {
				continue
			}
			count += len(matches)
			redacted = pattern.expression.ReplaceAllString(redacted, pattern.replacement)
		}
		parts[partIndex] = redacted
	}
	return strings.Join(parts, RedactedValue), count
}

func (r *patternRedactor) RedactBytes(value []byte) ([]byte, int) {
	if value == nil {
		return nil, 0
	}
	redacted, count := r.RedactString(string(value))
	return []byte(redacted), count
}

func (r *patternRedactor) RedactValue(value any) (any, int) {
	if r == nil {
		return value, 0
	}
	return r.redactValue(value)
}

func (r *patternRedactor) redactValue(value any) (any, int) {
	switch typed := value.(type) {
	case nil:
		return nil, 0
	case string:
		return r.RedactString(typed)
	case json.RawMessage:
		return r.redactRawMessage(typed)
	case []byte:
		return r.RedactBytes(typed)
	case map[string]any:
		return r.redactAnyMap(typed)
	case map[string]string:
		return r.redactStringMap(typed)
	case []any:
		return r.redactAnySlice(typed)
	case []string:
		return r.redactStringSlice(typed)
	default:
		return value, 0
	}
}

func (r *patternRedactor) redactRawMessage(value json.RawMessage) (any, int) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		redacted, count := r.RedactBytes(value)
		return json.RawMessage(redacted), count
	}
	redacted, count := r.redactValue(decoded)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		fallback, fallbackCount := r.RedactBytes(value)
		return json.RawMessage(fallback), count + fallbackCount
	}
	return json.RawMessage(encoded), count
}

func (r *patternRedactor) redactAnyMap(value map[string]any) (any, int) {
	redacted := make(map[string]any, len(value))
	count := 0
	for key, item := range value {
		if r.isSensitiveKey(key) {
			redacted[key] = RedactedValue
			if item != RedactedValue {
				count++
			}
			continue
		}
		clean, itemCount := r.redactValue(item)
		redacted[key] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactStringMap(value map[string]string) (any, int) {
	redacted := make(map[string]string, len(value))
	count := 0
	for key, item := range value {
		if r.isSensitiveKey(key) {
			redacted[key] = RedactedValue
			if item != RedactedValue {
				count++
			}
			continue
		}
		clean, itemCount := r.RedactString(item)
		redacted[key] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactAnySlice(value []any) (any, int) {
	redacted := make([]any, len(value))
	count := 0
	for index, item := range value {
		clean, itemCount := r.redactValue(item)
		redacted[index] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) redactStringSlice(value []string) (any, int) {
	redacted := make([]string, len(value))
	count := 0
	for index, item := range value {
		clean, itemCount := r.RedactString(item)
		redacted[index] = clean
		count += itemCount
	}
	return redacted, count
}

func (r *patternRedactor) isSensitiveKey(key string) bool {
	normalized := strings.TrimSpace(key)
	return r.sensitiveKey.MatchString(normalized)
}
