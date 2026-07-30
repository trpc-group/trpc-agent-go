//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(
		`(?i)(api[_-]?key|access[_-]?token|token|password|passwd|secret|credential)(\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,;}&|]+)`,
	),
	regexp.MustCompile(
		`(?i)(bearer\s+)[a-z0-9._~+/=-]{16,}`,
	),
	regexp.MustCompile(
		`AKIA[0-9A-Z]{16}`,
	),
	regexp.MustCompile(
		`\bsk-[A-Za-z0-9_-]{16,}\b`,
	),
	regexp.MustCompile(
		`\bgh[pousr]_[A-Za-z0-9]{20,}\b`,
	),
	regexp.MustCompile(
		`\bgithub_pat_[A-Za-z0-9_]{20,}\b`,
	),
	regexp.MustCompile(
		`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`,
	),
	regexp.MustCompile(
		`(?i)((?:^|[[:space:]])(?:-u|--user)(?:=|[[:space:]]+)[^:[:space:]]+:)([^[:space:]]+)`,
	),
	regexp.MustCompile(
		`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]+:)([^@\s/]+)(@)`,
	),
}

var commandCredentialOptionPattern = regexp.MustCompile(
	`(?i)(?:^|[[:space:]])(?:--(?:http-|ftp-|proxy-)?password(?:=|[[:space:]]+)|(?:-u|-U)(?:=|[[:space:]]*)[^:[:space:]'"]+[[:space:]]*:|--(?:user|proxy-user)(?:=|[[:space:]]+)[^:[:space:]'"]+[[:space:]]*:)`,
)

func redactString(s string) (string, bool) {
	redacted := false
	for i, re := range secretPatterns {
		if !re.MatchString(s) {
			continue
		}
		redacted = true
		switch i {
		case 0:
			s = re.ReplaceAllString(s, `$1$2[REDACTED]`)
		case 1:
			s = re.ReplaceAllString(s, `$1[REDACTED]`)
		case 7:
			s = re.ReplaceAllString(s, `$1[REDACTED]`)
		case 8:
			s = re.ReplaceAllString(s, `$1[REDACTED]$3`)
		default:
			s = re.ReplaceAllString(s, `[REDACTED]`)
		}
	}
	return s, redacted
}

// RedactString removes common credential forms from text.
func RedactString(s string) (string, bool) {
	return redactString(s)
}

func redactCommand(command string) (string, bool) {
	redacted, changed := redactString(command)
	if commandTextHasCredentialOption(command) {
		return "[REDACTED]", true
	}
	pipe, err := shellsafe.Parse(command)
	if err == nil && commandHasInlineCredential(pipe) {
		return "[REDACTED]", true
	}
	return redacted, changed
}

func commandTextHasCredentialOption(command string) bool {
	return commandCredentialOptionPattern.MatchString(command)
}

// RedactValue returns a JSON-compatible copy with secrets removed from strings.
// The original value is never mutated.
func RedactValue(value any) (any, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	if text, ok := value.(string); ok {
		redacted, changed := redactString(text)
		return redacted, changed, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		redacted, changed := redactString(fmt.Sprint(value))
		if changed {
			return redacted, true, nil
		}
		return nil, false, fmt.Errorf("marshal value for redaction: %w", err)
	}
	var clone any
	if err := json.Unmarshal(b, &clone); err != nil {
		return nil, false, fmt.Errorf("unmarshal value for redaction: %w", err)
	}
	redacted, changed := redactJSONValue(clone)
	return redacted, changed, nil
}

// NewRedactingAfterToolCallback creates an optional post-execution safeguard.
// It replaces a result only when a secret-like string was found.
func NewRedactingAfterToolCallback() tool.AfterToolCallbackStructured {
	return func(
		_ context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		if args == nil {
			return &tool.AfterToolResult{}, nil
		}
		value, changed, err := RedactValue(args.Result)
		if err != nil {
			return nil, err
		}
		if !changed {
			return &tool.AfterToolResult{}, nil
		}
		return &tool.AfterToolResult{CustomResult: value}, nil
	}
}

func redactJSONValue(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		return redactString(typed)
	case []any:
		changed := false
		for i := range typed {
			var itemChanged bool
			typed[i], itemChanged = redactJSONValue(typed[i])
			changed = changed || itemChanged
		}
		return typed, changed
	case map[string]any:
		changed := false
		for key, item := range typed {
			itemText, itemIsText := item.(string)
			if isSecretFieldName(key) &&
				item != nil &&
				(!itemIsText || itemText != "[REDACTED]") {
				typed[key] = "[REDACTED]"
				changed = true
				continue
			}
			var itemChanged bool
			typed[key], itemChanged = redactJSONValue(item)
			changed = changed || itemChanged
		}
		return typed, changed
	default:
		return value, false
	}
}

func isSecretFieldName(key string) bool {
	normalized := strings.NewReplacer(
		"_", "",
		"-", "",
		".", "",
		" ", "",
	).Replace(strings.ToLower(strings.TrimSpace(key)))

	switch normalized {
	case "authorization":
		return true
	}

	for _, suffix := range []string{
		"password",
		"passwd",
		"apikey",
		"accesstoken",
		"authtoken",
		"clientsecret",
		"credential",
		"credentials",
		"privatekey",
		"refreshtoken",
		"secretaccesskey",
		"secretkey",
		"accesskey",
		"secret",
		"token",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
