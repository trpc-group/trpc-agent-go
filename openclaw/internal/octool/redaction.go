//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	redactedValue           = "[REDACTED]"
	redactedNameFormat      = "[REDACTED:%s]"
	minSensitiveValueLength = 6
)

var (
	sensitiveEnvNamePattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|` +
			`ACCESS_KEY|PRIVATE_KEY)[A-Z0-9_]*\b`,
	)

	sensitiveAssignmentPattern = regexp.MustCompile(
		`^\s*((?:export|declare -x)\s+)?` +
			`([A-Za-z_][A-Za-z0-9_]*)(\s*=\s*)(.*)$`,
	)

	sensitiveColonPattern = regexp.MustCompile(
		`^\s*([{"']?\s*)([A-Za-z_][A-Za-z0-9_]*)` +
			`(["']?\s*:\s*)(.*)$`,
	)

	sensitiveInlineAssignPattern = regexp.MustCompile(
		`(?i)(?:^|[\s;|&])(?:export\s+)?` +
			`([A-Za-z_][A-Za-z0-9_]*)=` +
			`("[^"]*"|'[^']*'|[^\s;|&]+)`,
	)
)

// OutputRedactor rewrites command output before it is returned.
type OutputRedactor func(CommandRequest, string) string

type sensitiveValue struct {
	Name       string
	Value      string
	AllowShort bool
}

// NewChatCommandOutputRedactor redacts sensitive env values from output.
func NewChatCommandOutputRedactor() OutputRedactor {
	return redactCommandOutput
}

func redactCommandOutput(req CommandRequest, output string) string {
	if strings.TrimSpace(output) == "" {
		return output
	}
	redacted := redactSensitiveKeyValueLines(output)
	return redactSensitiveValues(redacted, knownSensitiveValues(req))
}

func knownSensitiveValues(req CommandRequest) []sensitiveValue {
	byName := make(map[string]sensitiveValue)
	addSensitiveEnvValues(byName, req.Env)
	addInlineSensitiveValues(byName, req.Command)
	if len(byName) == 0 {
		return nil
	}

	out := make([]sensitiveValue, 0, len(byName))
	for _, item := range byName {
		if !item.AllowShort &&
			len(item.Value) < minSensitiveValueLength {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Value) == len(out[j].Value) {
			return out[i].Name < out[j].Name
		}
		return len(out[i].Value) > len(out[j].Value)
	})
	return out
}

func addSensitiveEnvValues(
	out map[string]sensitiveValue,
	env map[string]string,
) {
	for name, value := range env {
		if !isSensitiveEnvName(name) {
			continue
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[strings.ToUpper(name)] = sensitiveValue{
			Name:       strings.ToUpper(name),
			Value:      value,
			AllowShort: true,
		}
	}
}

func addInlineSensitiveValues(
	out map[string]sensitiveValue,
	command string,
) {
	matches := sensitiveInlineAssignPattern.FindAllStringSubmatch(
		command,
		-1,
	)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		name := strings.ToUpper(match[1])
		if !isSensitiveEnvName(name) {
			continue
		}
		value := trimMatchingQuotes(strings.TrimSpace(match[2]))
		if value == "" {
			continue
		}
		out[name] = sensitiveValue{
			Name:  name,
			Value: value,
		}
	}
}

func redactSensitiveValues(
	output string,
	values []sensitiveValue,
) string {
	redacted := output
	for _, item := range values {
		if item.Value == "" {
			continue
		}
		redacted = strings.ReplaceAll(
			redacted,
			item.Value,
			redactedName(item.Name),
		)
	}
	return redacted
}

func redactSensitiveKeyValueLines(output string) string {
	lines := strings.SplitAfter(output, "\n")
	for i, line := range lines {
		hasNewline := strings.HasSuffix(line, "\n")
		raw := strings.TrimSuffix(line, "\n")
		lines[i] = redactSensitiveKeyValueLine(raw)
		if hasNewline {
			lines[i] += "\n"
		}
	}
	return strings.Join(lines, "")
}

func redactSensitiveKeyValueLine(line string) string {
	if redacted, ok := redactAssignmentLine(line); ok {
		return redacted
	}
	if redacted, ok := redactColonLine(line); ok {
		return redacted
	}
	return line
}

func redactAssignmentLine(line string) (string, bool) {
	match := sensitiveAssignmentPattern.FindStringSubmatch(line)
	if len(match) != 5 {
		return "", false
	}
	if !isSensitiveEnvName(match[2]) {
		return "", false
	}
	return match[1] + match[2] + match[3] +
		redactedStructuredValue(match[4]), true
}

func redactColonLine(line string) (string, bool) {
	match := sensitiveColonPattern.FindStringSubmatch(line)
	if len(match) != 5 {
		return "", false
	}
	if !isSensitiveEnvName(match[2]) {
		return "", false
	}
	return match[1] + match[2] + match[3] +
		redactedStructuredValue(match[4]), true
}

func redactedStructuredValue(raw string) string {
	trimmedRight := strings.TrimRight(raw, " \t")
	suffix := raw[len(trimmedRight):]
	body := trimmedRight
	trailing := ""

	if strings.HasSuffix(body, ",") {
		body = strings.TrimSpace(strings.TrimSuffix(body, ","))
		trailing = ","
	}

	switch {
	case hasWrappedQuotes(body, '"'):
		return `"` + redactedValue + `"` + trailing + suffix
	case hasWrappedQuotes(body, '\''):
		return `'` + redactedValue + `'` + trailing + suffix
	default:
		return redactedValue + trailing + suffix
	}
}

func hasWrappedQuotes(value string, quote byte) bool {
	if len(value) < 2 {
		return false
	}
	return value[0] == quote && value[len(value)-1] == quote
}

func trimMatchingQuotes(value string) string {
	switch {
	case hasWrappedQuotes(value, '"'):
		return value[1 : len(value)-1]
	case hasWrappedQuotes(value, '\''):
		return value[1 : len(value)-1]
	default:
		return value
	}
}

func isSensitiveEnvName(name string) bool {
	return sensitiveEnvNamePattern.MatchString(name)
}

func redactedName(name string) string {
	return fmt.Sprintf(
		redactedNameFormat,
		strings.ToUpper(strings.TrimSpace(name)),
	)
}
