//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	toolArgumentMaxRunes       = 180
	toolArgumentStringMaxRunes = 80
	toolArgumentMaxDepth       = 3
	toolArgumentMaxItems       = 6
	toolArgumentRedacted       = "[redacted]"
	toolArgumentOmitted        = "..."

	toolArgumentKeyAccessKey     = "access_key"
	toolArgumentKeyAPIKey        = "api_key"
	toolArgumentKeyAuthorization = "authorization"
	toolArgumentKeyCookie        = "cookie"
	toolArgumentKeyCredential    = "credential"
	toolArgumentKeyPasswd        = "passwd"
	toolArgumentKeyPassword      = "password"
	toolArgumentKeyPrivateKey    = "private_key"
	toolArgumentKeySecret        = "secret"
	toolArgumentKeyToken         = "token"
)

var sensitiveToolArgumentKeys = [...]string{
	toolArgumentKeyAccessKey,
	toolArgumentKeyAPIKey,
	toolArgumentKeyAuthorization,
	toolArgumentKeyCookie,
	toolArgumentKeyCredential,
	toolArgumentKeyPasswd,
	toolArgumentKeyPassword,
	toolArgumentKeyPrivateKey,
	toolArgumentKeySecret,
	toolArgumentKeyToken,
}

func summarizeToolArguments(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	value, ok := parseToolArgumentJSON(text)
	if !ok {
		return summarizeRawToolArgumentText(text)
	}
	if isEmptyToolArgumentValue(value) {
		return ""
	}
	sanitized := sanitizeToolArgumentValue(value, 0)
	body, err := json.Marshal(sanitized)
	if err != nil {
		return summarizeRawToolArgumentText(text)
	}
	return truncateToolArgumentRunes(string(body), toolArgumentMaxRunes)
}

func summarizeRawToolArgumentText(text string) string {
	if containsSensitiveToolArgumentKey(text) {
		return toolArgumentRedacted
	}
	return truncateToolArgumentRunes(text, toolArgumentMaxRunes)
}

func parseToolArgumentJSON(text string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return value, true
}

func sanitizeToolArgumentValue(value any, depth int) any {
	if depth >= toolArgumentMaxDepth {
		return toolArgumentOmitted
	}
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeToolArgumentMap(typed, depth)
	case []any:
		return sanitizeToolArgumentList(typed, depth)
	case string:
		return truncateToolArgumentRunes(
			typed,
			toolArgumentStringMaxRunes,
		)
	default:
		return typed
	}
}

func sanitizeToolArgumentMap(
	value map[string]any,
	depth int,
) map[string]any {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > toolArgumentMaxItems {
		keys = keys[:toolArgumentMaxItems]
	}
	sanitized := make(map[string]any, len(keys))
	for _, key := range keys {
		if isSensitiveToolArgumentKey(key) {
			sanitized[key] = toolArgumentRedacted
			continue
		}
		sanitized[key] = sanitizeToolArgumentValue(
			value[key],
			depth+1,
		)
	}
	return sanitized
}

func sanitizeToolArgumentList(value []any, depth int) []any {
	if len(value) > toolArgumentMaxItems {
		value = value[:toolArgumentMaxItems]
	}
	sanitized := make([]any, 0, len(value))
	for _, item := range value {
		sanitized = append(
			sanitized,
			sanitizeToolArgumentValue(item, depth+1),
		)
	}
	return sanitized
}

func isSensitiveToolArgumentKey(key string) bool {
	normalized := canonicalToolArgumentKey(key)
	for _, sensitive := range sensitiveToolArgumentKeys {
		if strings.Contains(
			normalized,
			canonicalToolArgumentKey(sensitive),
		) {
			return true
		}
	}
	return false
}

func containsSensitiveToolArgumentKey(text string) bool {
	normalized := canonicalToolArgumentKey(text)
	for _, sensitive := range sensitiveToolArgumentKeys {
		if strings.Contains(
			normalized,
			canonicalToolArgumentKey(sensitive),
		) {
			return true
		}
	}
	return false
}

func canonicalToolArgumentKey(text string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func isEmptyToolArgumentValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func truncateToolArgumentRunes(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	suffixRunes := []rune(toolArgumentOmitted)
	if maxRunes <= len(suffixRunes) {
		return string(runes[:maxRunes])
	}
	return strings.TrimSpace(
		string(runes[:maxRunes-len(suffixRunes)]),
	) + toolArgumentOmitted
}
