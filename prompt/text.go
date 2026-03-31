//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package prompt

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Meta identifies a prompt template for observability or future registry use.
type Meta struct {
	Name    string
	Version string
}

// Vars stores runtime values used to render a prompt template.
type Vars map[string]string

// Text is a minimal text prompt template with optional metadata.
type Text struct {
	Template string
	Meta     Meta
}

// RenderEnv contains runtime values used to render a prompt template.
type RenderEnv struct {
	Vars     Vars
	Resolver Resolver
}

// Ref identifies a resolver-backed placeholder.
type Ref struct {
	Namespace string
	Name      string
}

// Resolver resolves prompt references discovered during rendering.
type Resolver interface {
	// Resolve returns the replacement text for ref, whether a value was found,
	// and any fatal resolution error.
	//
	// The found flag is intentionally separate from the string result so the
	// renderer can distinguish "resolved to an empty string" from "not found".
	//
	// Return contract:
	//   - value, true, nil: ref resolved successfully, even if value == ""
	//   - "", false, nil: ref was not found; renderer applies optional,
	//     preserve, or strict-unknown handling
	//   - _, _, err: resolution failed and rendering should stop
	Resolve(ref Ref) (string, bool, error)
}

// UnknownBehavior controls how unresolved placeholders are handled.
type UnknownBehavior int

const (
	// PreserveUnknown leaves unresolved placeholders untouched.
	PreserveUnknown UnknownBehavior = iota
	// ErrorOnUnknown returns an error when a non-optional placeholder cannot be resolved.
	ErrorOnUnknown
)

// RenderOption configures prompt rendering behavior.
type RenderOption func(*renderConfig)

// WithUnknownBehavior configures how unresolved placeholders are handled.
func WithUnknownBehavior(behavior UnknownBehavior) RenderOption {
	return func(cfg *renderConfig) {
		cfg.unknownBehavior = behavior
	}
}

type renderConfig struct {
	unknownBehavior UnknownBehavior
}

type textAnalysis struct {
	normalized string
	parts      []textPart
}

type textPart struct {
	literal     string
	placeholder *placeholderToken
}

type placeholderKind int

const (
	placeholderKindRef placeholderKind = iota
	placeholderKindArtifact
)

type placeholderToken struct {
	kind     placeholderKind
	raw      string
	ref      Ref
	optional bool
}

var (
	bracePlaceholderRE          = regexp.MustCompile(`\{([^{}]+)\}`)
	legacyMustachePlaceholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*(?::[A-Za-z_][A-Za-z0-9_]*)?)(\?)?\s*\}\}`)
)

// Render replaces known placeholders with values from the render environment.
// Unknown placeholders are preserved by default so later stages can still see them.
func (t Text) Render(env RenderEnv, opts ...RenderOption) (string, error) {
	cfg := renderConfig{unknownBehavior: PreserveUnknown}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	analysis := analyzeText(t.Template)
	if len(analysis.parts) == 0 {
		return analysis.normalized, nil
	}

	var builder strings.Builder
	var unresolved []string
	for _, part := range analysis.parts {
		if part.placeholder == nil {
			builder.WriteString(part.literal)
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
		if cfg.unknownBehavior == ErrorOnUnknown {
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

// ValidateRequired checks that the template contains all required placeholders.
func (t Text) ValidateRequired(names ...string) error {
	if len(names) == 0 {
		return nil
	}
	present := make(map[string]struct{})
	for _, name := range collectPlaceholderNames(t.Template) {
		present[name] = struct{}{}
	}

	var missing []string
	for _, name := range normalizeNames(names) {
		if _, ok := present[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"prompt: missing required placeholders: %s",
		strings.Join(formatPlaceholderNames(missing), ", "),
	)
}

func collectPlaceholderNames(template string) []string {
	analysis := analyzeText(template)
	if len(analysis.parts) == 0 {
		return nil
	}
	var names []string
	for _, part := range analysis.parts {
		if part.placeholder == nil || part.placeholder.kind != placeholderKindRef {
			continue
		}
		names = append(names, placeholderName(part.placeholder.ref))
	}
	return uniqueSortedNames(names)
}

func analyzeText(template string) textAnalysis {
	normalized := normalizeLegacyMustache(template)
	if normalized == "" {
		return textAnalysis{normalized: normalized}
	}

	matches := bracePlaceholderRE.FindAllStringSubmatchIndex(normalized, -1)
	if len(matches) == 0 {
		return textAnalysis{normalized: normalized}
	}

	parts := make([]textPart, 0, len(matches)*2+1)
	last := 0
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		start, end := match[0], match[1]
		innerStart, innerEnd := match[2], match[3]
		if start > last {
			parts = append(parts, textPart{literal: normalized[last:start]})
		}
		raw := normalized[start:end]
		token, ok := parsePlaceholder(raw, normalized[innerStart:innerEnd])
		if ok {
			tokenCopy := token
			parts = append(parts, textPart{placeholder: &tokenCopy})
		} else {
			parts = append(parts, textPart{literal: raw})
		}
		last = end
	}
	if last < len(normalized) {
		parts = append(parts, textPart{literal: normalized[last:]})
	}
	return textAnalysis{
		normalized: normalized,
		parts:      parts,
	}
}

func normalizeLegacyMustache(template string) string {
	if template == "" {
		return template
	}
	return legacyMustachePlaceholderRE.ReplaceAllString(template, `{$1$2}`)
}

func parsePlaceholder(raw, inner string) (placeholderToken, bool) {
	name := inner
	optional := false
	if strings.HasSuffix(name, "?") {
		optional = true
		name = strings.TrimSuffix(name, "?")
	}
	if name == "" {
		return placeholderToken{}, false
	}
	if strings.HasPrefix(name, "artifact.") {
		return placeholderToken{
			kind:     placeholderKindArtifact,
			raw:      raw,
			optional: optional,
		}, true
	}

	ref, ok := parseRef(name)
	if !ok {
		return placeholderToken{}, false
	}
	return placeholderToken{
		kind:     placeholderKindRef,
		raw:      raw,
		ref:      ref,
		optional: optional,
	}, true
}

func parseRef(name string) (Ref, bool) {
	namespace, key, ok := strings.Cut(name, ":")
	if ok {
		if !isIdentifier(namespace) || !isIdentifier(key) {
			return Ref{}, false
		}
		return Ref{Namespace: namespace, Name: key}, true
	}
	if !isIdentifier(name) {
		return Ref{}, false
	}
	return Ref{Name: name}, true
}

func renderPlaceholder(token placeholderToken, env RenderEnv) (string, bool, error) {
	switch token.kind {
	case placeholderKindArtifact:
		return "", false, nil
	case placeholderKindRef:
		if token.ref.Namespace == "" && env.Vars != nil {
			if value, ok := env.Vars[token.ref.Name]; ok {
				return value, true, nil
			}
		}
		if env.Resolver == nil {
			return "", false, nil
		}
		return env.Resolver.Resolve(token.ref)
	default:
		return "", false, nil
	}
}

func normalizeNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		trimmed = append(trimmed, name)
	}
	return uniqueSortedNames(trimmed)
}

func uniqueSortedNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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

func formatPlaceholderNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	formatted := make([]string, len(names))
	for i, name := range names {
		formatted[i] = "{" + name + "}"
	}
	return formatted
}

func placeholderName(ref Ref) string {
	if ref.Namespace == "" {
		return ref.Name
	}
	return ref.Namespace + ":" + ref.Name
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	if !isLetterOrUnderscore(rune(s[0])) {
		return false
	}
	for _, r := range s[1:] {
		if !isLetterOrDigitOrUnderscore(r) {
			return false
		}
	}
	return true
}

func isLetterOrUnderscore(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

func isLetterOrDigitOrUnderscore(r rune) bool {
	return isLetterOrUnderscore(r) || (r >= '0' && r <= '9')
}
