//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptcore provides the internal text rendering engine behind the
// public prompt package.
package promptcore

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// SyntaxMode controls which placeholder delimiters are recognized.
type SyntaxMode int

const (
	// SyntaxModeMixedBrace recognizes both single-brace and double-brace
	// placeholders. In both forms, name itself matches the regexp
	// `[^\s{}'"`?]+`. A trailing '?' marks the placeholder optional and is not
	// part of name. Double-brace placeholders still ignore outer whitespace.
	SyntaxModeMixedBrace SyntaxMode = iota
	// SyntaxModeSingleBrace recognizes placeholders such as {name} or {name?}.
	//
	// Here name matches the regexp `[^\s{}'"`?]+`. A trailing '?' marks the
	// placeholder optional and is not part of name.
	SyntaxModeSingleBrace
	// SyntaxModeDoubleBrace recognizes placeholders such as {{name}},
	// {{name?}}, or {{user:name}}.
	//
	// Here name matches the regexp `[^\s{}'"`?]+`. A trailing '?' marks the
	// placeholder optional and is not part of name. Outer whitespace is ignored.
	SyntaxModeDoubleBrace
)

// UnknownBehavior controls how unresolved placeholders are handled.
type UnknownBehavior int

const (
	// PreserveUnknown keeps unresolved placeholders in the rendered output.
	PreserveUnknown UnknownBehavior = iota
	// ErrorOnUnknown returns an error when a non-optional placeholder cannot be resolved.
	ErrorOnUnknown
)

// ResolveFunc resolves a placeholder name to a value.
type ResolveFunc func(name string) (string, bool, error)

// Env contains the runtime values available during rendering.
type Env struct {
	Vars    map[string]string
	Resolve ResolveFunc
}

// Option customizes promptcore parsing or rendering behavior.
type Option func(*config)

type config struct {
	acceptName func(string) bool
}

// WithAcceptName filters which parsed placeholder names are treated as real placeholders.
//
// Returning false keeps the placeholder unresolved in its preserved output form,
// even for syntactically valid optional placeholders.
func WithAcceptName(fn func(string) bool) Option {
	return func(cfg *config) {
		cfg.acceptName = fn
	}
}

type textPart struct {
	literal     string
	placeholder *placeholderToken
}

type placeholderToken struct {
	raw      string
	name     string
	optional bool
	accepted bool
}

// Render replaces placeholders with values from env.
func Render(
	template string,
	syntax SyntaxMode,
	env Env,
	unknown UnknownBehavior,
	opts ...Option,
) (string, error) {
	cfg := buildConfig(opts...)
	parts := analyzeText(template, syntax, cfg)
	if len(parts) == 0 {
		return template, nil
	}

	var builder strings.Builder
	var unresolved []string
	for _, part := range parts {
		if part.placeholder == nil {
			builder.WriteString(part.literal)
			continue
		}
		if !part.placeholder.accepted {
			builder.WriteString(part.placeholder.raw)
			continue
		}

		value, ok, err := renderPlaceholder(*part.placeholder, env)
		if err != nil {
			return "", err
		}
		if ok {
			builder.WriteString(value)
			continue
		}
		if part.placeholder.optional {
			continue
		}
		builder.WriteString(part.placeholder.raw)
		if unknown == ErrorOnUnknown {
			unresolved = append(unresolved, part.placeholder.raw)
		}
	}

	if len(unresolved) > 0 {
		return builder.String(), fmt.Errorf(
			"prompt: unresolved placeholders: %s",
			strings.Join(uniqueSortedStrings(unresolved), ", "),
		)
	}
	return builder.String(), nil
}

// PlaceholderNames returns the unique placeholder names in template.
func PlaceholderNames(template string, syntax SyntaxMode, opts ...Option) []string {
	cfg := buildConfig(opts...)
	parts := analyzeText(template, syntax, cfg)
	if len(parts) == 0 {
		return nil
	}

	var names []string
	for _, part := range parts {
		if part.placeholder == nil {
			continue
		}
		if !part.placeholder.accepted {
			continue
		}
		names = append(names, part.placeholder.name)
	}
	return uniqueSortedStrings(names)
}

func buildConfig(opts ...Option) config {
	cfg := config{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func analyzeText(template string, syntax SyntaxMode, cfg config) []textPart {
	if template == "" {
		return nil
	}

	parts := make([]textPart, 0)
	last := 0
	for i := 0; i < len(template); {
		span, token := scanPlaceholder(template, i, syntax, cfg)
		if span == 0 {
			i++
			continue
		}

		if i > last {
			parts = append(parts, textPart{literal: template[last:i]})
		}
		if token == nil {
			parts = append(parts, textPart{literal: template[i : i+span]})
		} else {
			tokenCopy := *token
			parts = append(parts, textPart{placeholder: &tokenCopy})
		}

		i += span
		last = i
	}
	if last < len(template) {
		parts = append(parts, textPart{literal: template[last:]})
	}
	return parts
}

func scanPlaceholder(
	template string,
	start int,
	syntax SyntaxMode,
	cfg config,
) (int, *placeholderToken) {
	switch syntax {
	case SyntaxModeDoubleBrace:
		if strings.HasPrefix(template[start:], "{{") {
			return parseDoubleCurlyAt(template, start, cfg)
		}
	case SyntaxModeMixedBrace:
		if strings.HasPrefix(template[start:], "{{") {
			return parseDoubleCurlyAt(template, start, cfg)
		}
		if template[start] == '{' {
			return parseSingleBraceAt(template, start, cfg)
		}
	default:
		if strings.HasPrefix(template[start:], "{{") {
			return literalDoubleCurlySpan(template, start), nil
		}
		if template[start] == '{' {
			return parseSingleBraceAt(template, start, cfg)
		}
	}
	return 0, nil
}

func literalDoubleCurlySpan(template string, start int) int {
	if !strings.HasPrefix(template[start:], "{{") {
		return 0
	}
	end := strings.Index(template[start+2:], "}}")
	if end < 0 {
		return 0
	}
	return end + 4
}

func parseSingleBraceAt(
	template string,
	start int,
	cfg config,
) (int, *placeholderToken) {
	if template[start] != '{' || strings.HasPrefix(template[start:], "{{") {
		return 0, nil
	}

	end := strings.IndexByte(template[start+1:], '}')
	if end < 0 {
		return 0, nil
	}
	span := end + 2
	raw := template[start : start+span]
	token, ok := parsePlaceholder(
		raw,
		template[start+1:start+span-1],
		singleBraceDelimiter,
		cfg,
	)
	if !ok {
		return span, nil
	}
	return span, &token
}

func parseDoubleCurlyAt(
	template string,
	start int,
	cfg config,
) (int, *placeholderToken) {
	if !strings.HasPrefix(template[start:], "{{") {
		return 0, nil
	}

	end := strings.Index(template[start+2:], "}}")
	if end < 0 {
		return 0, nil
	}
	span := end + 4
	raw := template[start : start+span]
	token, ok := parsePlaceholder(
		raw,
		template[start+2:start+span-2],
		doubleBraceDelimiter,
		cfg,
	)
	if !ok {
		return span, nil
	}
	return span, &token
}

type delimiterKind int

const (
	singleBraceDelimiter delimiterKind = iota
	doubleBraceDelimiter
)

func parsePlaceholder(
	raw, inner string,
	delimiter delimiterKind,
	cfg config,
) (placeholderToken, bool) {
	name, optional, ok := parseName(inner, delimiter)
	if !ok {
		return placeholderToken{}, false
	}
	accepted := true
	if cfg.acceptName != nil {
		accepted = cfg.acceptName(name)
	}
	return placeholderToken{
		raw:      raw,
		name:     name,
		optional: optional,
		accepted: accepted,
	}, true
}

func parseName(inner string, delimiter delimiterKind) (string, bool, bool) {
	switch delimiter {
	case doubleBraceDelimiter:
		return parseDoubleBraceName(inner)
	default:
		return parseSingleBraceName(inner)
	}
}

func parseSingleBraceName(inner string) (string, bool, bool) {
	name := inner
	if name == "" {
		return "", false, false
	}

	optional := false
	if strings.HasSuffix(name, "?") {
		optional = true
		name = strings.TrimSuffix(name, "?")
	}
	if name == "" || strings.Contains(name, "?") {
		return "", false, false
	}
	if !isValidName(name) {
		return "", false, false
	}
	return name, optional, true
}

func parseDoubleBraceName(inner string) (string, bool, bool) {
	name := strings.TrimSpace(inner)
	if name == "" {
		return "", false, false
	}

	optional := false
	if strings.HasSuffix(name, "?") {
		optional = true
		name = strings.TrimSuffix(name, "?")
	}
	if name == "" || strings.Contains(name, "?") {
		return "", false, false
	}
	if !isValidName(name) {
		return "", false, false
	}
	return name, optional, true
}

func isValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case unicode.IsSpace(r):
			return false
		case r == '{' || r == '}' || r == '\'' || r == '"' || r == '`':
			return false
		}
	}
	return true
}

func renderPlaceholder(token placeholderToken, env Env) (string, bool, error) {
	if env.Vars != nil {
		if value, ok := env.Vars[token.name]; ok {
			return value, true, nil
		}
	}
	if env.Resolve == nil {
		return "", false, nil
	}
	return env.Resolve(token.name)
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
