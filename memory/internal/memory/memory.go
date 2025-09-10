//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package memory provides internal usage for memory service.
package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// DefaultEnabledTools are the creators of default memory tools to enable.
// This is shared between different memory service implementations.
var DefaultEnabledTools = map[string]memory.ToolCreator{
	memory.AddToolName:    func(service memory.Service) tool.Tool { return memorytool.NewAddTool(service) },
	memory.UpdateToolName: func(service memory.Service) tool.Tool { return memorytool.NewUpdateTool(service) },
	memory.SearchToolName: func(service memory.Service) tool.Tool { return memorytool.NewSearchTool(service) },
	memory.LoadToolName:   func(service memory.Service) tool.Tool { return memorytool.NewLoadTool(service) },
}

// validToolNames contains all valid memory tool names.
var validToolNames = map[string]struct{}{
	memory.AddToolName:    {},
	memory.UpdateToolName: {},
	memory.DeleteToolName: {},
	memory.ClearToolName:  {},
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
}

// IsValidToolName checks if the given tool name is valid.
func IsValidToolName(toolName string) bool {
	_, ok := validToolNames[toolName]
	return ok
}

// BuildSearchTokens tokenizes the query for EN and CJK in a simple way.
// For EN: split by non letters/digits, filter by min length and stopwords.
// For CJK: generate overlapping bigrams of CJK runes for better recall.
func BuildSearchTokens(query string) []string {
	const minTokenLen = 2
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}
	// Detect if contains any CJK rune.
	hasCJK := false
	for _, r := range q {
		if isCJK(r) {
			hasCJK = true
			break
		}
	}
	if hasCJK {
		// Build bigrams over CJK runes.
		runes := make([]rune, 0, utf8.RuneCountInString(q))
		for _, r := range q {
			if unicode.IsSpace(r) || isPunct(r) {
				continue
			}
			runes = append(runes, r)
		}
		if len(runes) == 0 {
			return nil
		}
		if len(runes) == 1 {
			return []string{string(runes[0])}
		}
		toks := make([]string, 0, len(runes)-1)
		for i := 0; i < len(runes)-1; i++ {
			toks = append(toks, string([]rune{runes[i], runes[i+1]}))
		}
		return dedupStrings(toks)
	}
	// English-like tokenization.
	// Replace non letter/digit with space.
	b := make([]rune, 0, len(q))
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b = append(b, r)
		} else {
			b = append(b, ' ')
		}
	}
	parts := strings.Fields(string(b))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < minTokenLen {
			continue
		}
		if isStopword(p) {
			continue
		}
		out = append(out, p)
	}
	return dedupStrings(out)
}

// isCJK reports if the rune is a CJK character.
func isCJK(r rune) bool {
	if unicode.Is(unicode.Han, r) {
		return true
	}
	return false
}

// isPunct reports if the rune is punctuation or symbol.
func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// dedupStrings returns a deduplicated copy of the input slice.
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// isStopword returns true for a minimal set of English stopwords.
func isStopword(s string) bool {
	switch s {
	case "a", "an", "the", "and", "or", "of", "in", "on", "to",
		"for", "with", "is", "are", "am", "be":
		return true
	default:
		return false
	}
}
