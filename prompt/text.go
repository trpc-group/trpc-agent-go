//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	promptcore "trpc.group/trpc-go/trpc-agent-go/internal/prompt/core"
)

// Source fetches a prompt template dynamically, for example from a remote
// prompt management service. Implementations should handle caching internally
// when low-latency access is required.
type Source interface {
	FetchPrompt(ctx context.Context) (Text, error)
}

// Meta identifies a prompt template for observability or future registry use.
type Meta struct {
	Name    string
	Version string
}

// Vars stores runtime values used to render a prompt template.
type Vars map[string]string

// Syntax controls which placeholder delimiters are recognized.
type Syntax int

const (
	// SyntaxMixedBrace recognizes both {name} and {{name}} placeholders.
	//
	// This is the default syntax mode. It treats single-brace and double-brace
	// tokens as equivalent placeholder delimiters in the same template. In both
	// forms, name itself matches the regexp `[^\s{}'"`?]+`. A trailing '?'
	// marks the placeholder optional and is not part of name. Double-brace
	// placeholders still ignore outer whitespace.
	SyntaxMixedBrace Syntax = iota
	// SyntaxSingleBrace recognizes {name} placeholders only.
	// Double-brace tokens such as {{name}} are treated as literal text.
	//
	// Here name matches the regexp `[^\s{}'"`?]+`. A trailing '?' marks the
	// placeholder optional and is not part of name.
	SyntaxSingleBrace
	// SyntaxDoubleBrace recognizes {{name}} placeholders only.
	// Single-brace tokens such as {name} are treated as literal text.
	//
	// This supports double-brace variable substitution only. It does not
	// implement full Mustache syntax such as sections or partials. Here name
	// matches the regexp `[^\s{}'"`?]+`. A trailing '?' marks the placeholder
	// optional and is not part of name, and outer whitespace is ignored.
	SyntaxDoubleBrace
)

// Text is a minimal text prompt template with optional metadata.
type Text struct {
	Template string
	Meta     Meta
	Syntax   Syntax
}

// RenderEnv contains runtime values used to render a prompt template.
type RenderEnv struct {
	Vars     Vars
	Resolver Resolver
}

// Ref identifies a resolver-backed placeholder using the raw extracted name.
type Ref struct {
	Name string
}

// Resolver resolves prompt references discovered during rendering.
type Resolver interface {
	// Resolve returns the replacement text for ref.Name, whether a value was
	// found, and any fatal resolution error.
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
	// PreserveUnknown keeps unresolved placeholders in the rendered output.
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

// Render replaces known placeholders with values from the render environment.
// Unknown placeholders are preserved by default so later stages can still see them.
func (t Text) Render(env RenderEnv, opts ...RenderOption) (string, error) {
	cfg := renderConfig{unknownBehavior: PreserveUnknown}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return promptcore.Render(
		t.Template,
		toCoreSyntax(t.Syntax),
		promptcore.Env{
			Vars: env.Vars,
			Resolve: func(name string) (string, bool, error) {
				if env.Resolver == nil {
					return "", false, nil
				}
				return env.Resolver.Resolve(Ref{Name: name})
			},
		},
		toCoreUnknownBehavior(cfg.unknownBehavior),
	)
}

// ValidateRequired checks that the template contains all required placeholders.
func (t Text) ValidateRequired(names ...string) error {
	if len(names) == 0 {
		return nil
	}
	present := make(map[string]struct{})
	for _, name := range promptcore.PlaceholderNames(
		t.Template,
		toCoreSyntax(t.Syntax),
	) {
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

func toCoreSyntax(s Syntax) promptcore.SyntaxMode {
	switch s {
	case SyntaxSingleBrace:
		return promptcore.SyntaxModeSingleBrace
	case SyntaxDoubleBrace:
		return promptcore.SyntaxModeDoubleBrace
	default:
		return promptcore.SyntaxModeMixedBrace
	}
}

func toCoreUnknownBehavior(b UnknownBehavior) promptcore.UnknownBehavior {
	switch b {
	case ErrorOnUnknown:
		return promptcore.ErrorOnUnknown
	default:
		return promptcore.PreserveUnknown
	}
}
