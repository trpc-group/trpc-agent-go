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
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// GenerateMemoryID generates a unique ID for memory based on content and user context.
// Uses SHA256 hash of memory content, sorted topics, app name, and user ID for consistent ID generation.
// This ensures that:
// 1. Same content with different topic order produces the same ID.
// 2. Different users with same content produce different IDs.
func GenerateMemoryID(mem *memory.Memory, appName, userID string) string {
	var builder strings.Builder
	builder.WriteString("memory:")
	builder.WriteString(mem.Memory)

	if len(mem.Topics) > 0 {
		// Sort topics to ensure consistent ordering.
		sortedTopics := make([]string, len(mem.Topics))
		copy(sortedTopics, mem.Topics)
		slices.Sort(sortedTopics)
		builder.WriteString("|topics:")
		builder.WriteString(strings.Join(sortedTopics, ","))
	}

	// Include app name and user ID to prevent cross-user conflicts.
	builder.WriteString("|app:")
	builder.WriteString(appName)
	builder.WriteString("|user:")
	builder.WriteString(userID)

	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

// AllToolCreators contains creators for all valid memory tools.
// This is shared between different memory service implementations.
var AllToolCreators = map[string]memory.ToolCreator{
	memory.AddToolName:    func() tool.Tool { return memorytool.NewAddTool() },
	memory.UpdateToolName: func() tool.Tool { return memorytool.NewUpdateTool() },
	memory.SearchToolName: func() tool.Tool { return memorytool.NewSearchTool() },
	memory.LoadToolName:   func() tool.Tool { return memorytool.NewLoadTool() },
	memory.DeleteToolName: func() tool.Tool { return memorytool.NewDeleteTool() },
	memory.ClearToolName:  func() tool.Tool { return memorytool.NewClearTool() },
}

// DefaultEnabledTools are the tool names that are enabled by default.
// This is shared between different memory service implementations.
var DefaultEnabledTools = map[string]struct{}{
	memory.AddToolName:    {},
	memory.UpdateToolName: {},
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
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

// autoModeDefaultEnabledTools defines default enabled tools for auto memory mode.
// When extractor is configured, these defaults are applied to enabledTools.
// In auto mode:
//   - Add/Delete/Update: run in background by extractor, not exposed to agent.
//   - Search/Load: can be exposed to agent via Tools().
//   - Clear: dangerous operation, disabled by default.
var autoModeDefaultEnabledTools = map[string]bool{
	memory.AddToolName:    true,  // Enabled for extractor background operations.
	memory.UpdateToolName: true,  // Enabled for extractor background operations.
	memory.DeleteToolName: true,  // Enabled for extractor background operations.
	memory.ClearToolName:  false, // Disabled by default, dangerous operation.
	memory.SearchToolName: true,  // Enabled and exposed to agent via Tools().
	memory.LoadToolName:   false, // Disabled by default, can be enabled by user.
}

// ApplyAutoModeDefaults applies auto mode default enabledTools settings.
// This function sets auto mode defaults only for tools that haven't been
// explicitly set by user via WithToolEnabled.
// User settings take precedence over auto mode defaults regardless of
// option order. The enabledTools map is modified in place.
// Parameters:
//   - enabledTools: set of enabled tool names.
//   - userExplicitlySet: set tracking which tools were explicitly set
//     by user.
func ApplyAutoModeDefaults(
	enabledTools map[string]struct{},
	userExplicitlySet map[string]bool,
) {
	if enabledTools == nil {
		return
	}
	// Apply auto mode defaults only for tools not explicitly set
	// by user.
	for toolName, defaultEnabled := range autoModeDefaultEnabledTools {
		if userExplicitlySet[toolName] {
			// User explicitly set this tool, don't override.
			continue
		}
		if defaultEnabled {
			enabledTools[toolName] = struct{}{}
		} else {
			delete(enabledTools, toolName)
		}
	}
}

// BuildToolsList builds the tools list based on configuration.
// This is a shared implementation for all memory service backends.
// Parameters:
//   - ext: the memory extractor (nil for agentic mode).
//   - toolCreators: map of tool name to creator function.
//   - enabledTools: set of enabled tool names.
//   - cachedTools: map to cache created tools (will be modified).
func BuildToolsList(
	ext extractor.MemoryExtractor,
	toolCreators map[string]memory.ToolCreator,
	enabledTools map[string]struct{},
	cachedTools map[string]tool.Tool,
) []tool.Tool {
	// Collect tool names and sort for stable order.
	names := make([]string, 0, len(toolCreators))
	for name := range toolCreators {
		if !shouldIncludeTool(name, ext, enabledTools) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)

	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if _, ok := cachedTools[name]; !ok {
			cachedTools[name] = toolCreators[name]()
		}
		tools = append(tools, cachedTools[name])
	}
	return tools
}

// shouldIncludeTool determines if a tool should be included based on mode and settings.
func shouldIncludeTool(
	name string,
	ext extractor.MemoryExtractor,
	enabledTools map[string]struct{},
) bool {
	// In auto memory mode, handle auto memory tools with special logic.
	if ext != nil {
		return shouldIncludeAutoMemoryTool(name, enabledTools)
	}

	// In agentic mode, respect enabledTools setting.
	_, ok := enabledTools[name]
	return ok
}

// autoModeExposedTools defines which tools can be exposed to agent in auto mode.
// Only Search and Load are front-end tools; others run in background.
var autoModeExposedTools = map[string]struct{}{
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
}

// shouldIncludeAutoMemoryTool checks if an auto memory tool should be
// included. In auto mode, only Search and Load tools can be exposed to
// agent. Other tools (Add/Update/Delete/Clear) run in background and
// are never exposed.
func shouldIncludeAutoMemoryTool(
	name string,
	enabledTools map[string]struct{},
) bool {
	// Only Search and Load tools can be exposed to agent in auto mode.
	if _, exposed := autoModeExposedTools[name]; !exposed {
		return false
	}
	// Check if the tool is enabled.
	_, ok := enabledTools[name]
	return ok
}

// BuildSearchTokens builds tokens for searching memory content.
// Notes:
//   - Stopwords and minimum token length are fixed defaults for now; future versions may expose configuration.
//   - CJK handling currently treats only unicode.Han as CJK. This is not the full CJK range
//     (does not include Hiragana/Katakana/Hangul). Adjust if broader coverage is desired.
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

// MatchMemoryEntry checks if a memory entry matches the given query.
// It uses token-based matching for better search accuracy.
// The function returns true if the query matches either the memory content or any of the topics.
func MatchMemoryEntry(entry *memory.Entry, query string) bool {
	if entry == nil || entry.Memory == nil {
		return false
	}

	// Handle empty or whitespace-only queries.
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}

	// Build tokens with shared EN and CJK handling.
	tokens := BuildSearchTokens(query)
	hasTokens := len(tokens) > 0

	contentLower := strings.ToLower(entry.Memory.Memory)
	matched := false

	if hasTokens {
		// OR match on any token against content or topics.
		for _, tk := range tokens {
			if tk == "" {
				continue
			}
			if strings.Contains(contentLower, tk) {
				matched = true
				break
			}
			for _, topic := range entry.Memory.Topics {
				if strings.Contains(strings.ToLower(topic), tk) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	} else {
		// Fallback to original substring match when no tokens built.
		ql := strings.ToLower(query)
		if strings.Contains(contentLower, ql) {
			matched = true
		} else {
			for _, topic := range entry.Memory.Topics {
				if strings.Contains(strings.ToLower(topic), ql) {
					matched = true
					break
				}
			}
		}
	}

	return matched
}
