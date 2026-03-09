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
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	seg     gse.Segmenter
	segOnce sync.Once
	segErr  error
)

func getSegmenter() (*gse.Segmenter, error) {
	segOnce.Do(func() {
		segErr = seg.LoadDict()
	})
	if segErr != nil {
		return nil, fmt.Errorf("load segmenter dict failed: %w", segErr)
	}
	return &seg, nil
}

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// GenerateMemoryID generates a unique ID for memory based on content and user context.
// Uses SHA256 hash of memory content, app name, and user ID.
// Topics are intentionally excluded so that the same content always
// produces the same ID regardless of topic variations, ensuring that
// ON CONFLICT upsert deduplication works correctly.
func GenerateMemoryID(mem *memory.Memory, appName, userID string) string {
	var builder strings.Builder
	builder.WriteString("memory:")
	builder.WriteString(mem.Memory)

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
// CJK text is segmented using jieba (gse) for accurate word boundaries.
// English text uses whitespace splitting with stopword removal.
func BuildSearchTokens(query string) []string {
	const minTokenLen = 2
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}
	hasCJK := false
	for _, r := range q {
		if isCJK(r) {
			hasCJK = true
			break
		}
	}
	if hasCJK {
		s, err := getSegmenter()
		if err != nil {
			return nil
		}
		words := s.CutSearch(q, true)
		toks := make([]string, 0, len(words))
		for _, w := range words {
			w = strings.TrimSpace(w)
			if w == "" || isCJKStopword(w) {
				continue
			}
			// Skip tokens that are purely punctuation or symbols.
			if isPunctToken(w) {
				continue
			}
			toks = append(toks, w)
		}
		if len(toks) == 0 {
			return nil
		}
		return dedupStrings(toks)
	}
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
	return unicode.Is(unicode.Han, r)
}

// cjkStopwords contains high-frequency CJK words that carry little
// search value. In memory entries, "用户" appears in nearly every
// record because the extraction prompt writes third-person statements.
var cjkStopwords = map[string]struct{}{
	"的": {}, "了": {}, "是": {}, "在": {}, "和": {},
	"有": {}, "我": {}, "他": {}, "她": {}, "它": {},
	"这": {}, "那": {}, "都": {}, "也": {}, "就": {},
	"不": {}, "会": {}, "到": {}, "说": {}, "对": {},
}

func isCJKStopword(w string) bool {
	_, ok := cjkStopwords[w]
	return ok
}

// isPunct reports if the rune is punctuation or symbol.
func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// isPunctToken reports if the string consists entirely of punctuation or
// symbol runes. This is used to filter out tokens like "，" or "！" that
// jieba may produce from CJK text.
func isPunctToken(s string) bool {
	for _, r := range s {
		if !isPunct(r) {
			return false
		}
	}
	return true
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
// Kept for backward compatibility; returns true when the relevance
// score is greater than zero.
func MatchMemoryEntry(entry *memory.Entry, query string) bool {
	return ScoreMemoryEntry(entry, query) > 0
}

// ScoreMemoryEntry returns a relevance score in [0, 1] indicating how
// well the entry matches the query. The score is the fraction of query
// tokens that appear in the entry's content or topics. A score of 0
// means no meaningful match.
func ScoreMemoryEntry(entry *memory.Entry, query string) float64 {
	if entry == nil || entry.Memory == nil {
		return 0
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}

	tokens := BuildSearchTokens(query)
	if len(tokens) == 0 {
		// Fallback to substring match with a lower score to avoid
		// distorting ranking when no tokens can be generated (e.g.
		// short queries or stopword-only queries).
		const fallbackScore = 0.5
		ql := strings.ToLower(query)
		contentLower := strings.ToLower(entry.Memory.Memory)
		if strings.Contains(contentLower, ql) {
			return fallbackScore
		}
		for _, topic := range entry.Memory.Topics {
			if strings.Contains(strings.ToLower(topic), ql) {
				return fallbackScore
			}
		}
		return 0
	}

	contentLower := strings.ToLower(entry.Memory.Memory)
	matched := 0
	for _, tk := range tokens {
		if tk == "" {
			continue
		}
		hit := false
		if strings.Contains(contentLower, tk) {
			hit = true
		} else {
			for _, topic := range entry.Memory.Topics {
				if strings.Contains(strings.ToLower(topic), tk) {
					hit = true
					break
				}
			}
		}
		if hit {
			matched++
		}
	}

	return float64(matched) / float64(len(tokens))
}

// SearchOptions controls score filtering and result truncation for
// keyword-based memory search.
type SearchOptions struct {
	MinScore   float64
	MaxResults int
}

const (
	// DefaultSearchMinScore is the default minimum keyword-search score.
	DefaultSearchMinScore = 0.3
	// DefaultMaxSearchResults is the default maximum number of keyword-search results.
	DefaultMaxSearchResults = 10
)

// SearchMemoryEntries ranks keyword-search matches using shared scoring
// and sorting semantics, while leaving backend-specific thresholds and
// truncation to the caller.
func SearchMemoryEntries(
	entries []*memory.Entry,
	query string,
	opts SearchOptions,
) []*memory.Entry {
	type scoredEntry struct {
		entry *memory.Entry
		score float64
	}

	candidates := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		score := ScoreMemoryEntry(entry, query)
		if !passesMinScore(score, opts.MinScore) {
			continue
		}
		candidates = append(candidates, scoredEntry{entry: entry, score: score})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].entry.UpdatedAt.Equal(candidates[j].entry.UpdatedAt) {
			return candidates[i].entry.UpdatedAt.After(candidates[j].entry.UpdatedAt)
		}
		if !candidates[i].entry.CreatedAt.Equal(candidates[j].entry.CreatedAt) {
			return candidates[i].entry.CreatedAt.After(candidates[j].entry.CreatedAt)
		}
		return candidates[i].entry.ID < candidates[j].entry.ID
	})

	if opts.MaxResults > 0 && len(candidates) > opts.MaxResults {
		candidates = candidates[:opts.MaxResults]
	}

	results := make([]*memory.Entry, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.entry)
	}
	return results
}

func passesMinScore(score float64, minScore float64) bool {
	if minScore > 0 {
		return score >= minScore
	}
	return score > 0
}
