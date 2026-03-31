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

var textPlaceholderRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Render replaces known {name} placeholders with values from vars.
// Unknown placeholders are preserved so other framework stages can process them.
func (t Text) Render(vars Vars) string {
	if t.Template == "" {
		return ""
	}
	if len(vars) == 0 {
		return t.Template
	}
	return textPlaceholderRE.ReplaceAllStringFunc(
		t.Template,
		func(match string) string {
			name := match[1 : len(match)-1]
			value, ok := vars[name]
			if !ok {
				return match
			}
			return value
		},
	)
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
	if template == "" {
		return nil
	}
	matches := textPlaceholderRE.FindAllStringSubmatch(template, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		names = append(names, match[1])
	}
	return uniqueSortedNames(names)
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
