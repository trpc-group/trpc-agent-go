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
	"time"
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
		return nil, fmt.Errorf(
			"load segmenter dict failed: %w", segErr,
		)
	}
	return &seg, nil
}

// resetSegmenter resets the segmenter state so that the next
// getSegmenter call re-initialises. This is only intended for
// testing error paths.
func resetSegmenter() {
	segOnce = sync.Once{}
	segErr = nil
	seg = gse.Segmenter{}
}

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// GenerateMemoryID generates a unique ID for memory based on content,
// user context, and canonical episodic metadata.
// Topics are intentionally excluded so that topic drift does not change
// identity, while event metadata is included so distinct episodes with
// the same text do not collapse into a single upsert key.
func GenerateMemoryID(mem *memory.Memory, appName, userID string) string {
	var builder strings.Builder
	builder.WriteString("memory:")
	builder.WriteString(mem.Memory)

	// Include app name and user ID to prevent cross-user conflicts.
	builder.WriteString("|app:")
	builder.WriteString(appName)
	builder.WriteString("|user:")
	builder.WriteString(userID)

	if kind := metadataIdentityKind(mem); kind != "" {
		builder.WriteString("|kind:")
		builder.WriteString(string(kind))
	}
	if mem != nil && mem.EventTime != nil {
		builder.WriteString("|event_time:")
		builder.WriteString(mem.EventTime.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}
	if participants := metadataIdentityParticipants(mem); len(participants) > 0 {
		builder.WriteString("|participants:")
		builder.WriteString(strings.Join(participants, ","))
	}
	if location := metadataIdentityLocation(mem); location != "" {
		builder.WriteString("|location:")
		builder.WriteString(location)
	}

	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

func metadataIdentityKind(mem *memory.Memory) memory.Kind {
	if mem == nil {
		return ""
	}
	hasEventMetadata := mem.EventTime != nil || len(mem.Participants) > 0 ||
		strings.TrimSpace(mem.Location) != ""
	if mem.Kind != "" && mem.Kind != memory.KindFact {
		return mem.Kind
	}
	if hasEventMetadata {
		return memory.KindFact
	}
	return ""
}

// EffectiveKind returns the runtime memory kind. Legacy records that did not
// persist kind explicitly are treated as facts.
func EffectiveKind(mem *memory.Memory) memory.Kind {
	if mem == nil {
		return ""
	}
	if mem.Kind != "" {
		return mem.Kind
	}
	return memory.KindFact
}

func metadataIdentityParticipants(mem *memory.Memory) []string {
	if mem == nil {
		return nil
	}
	participants := make([]string, 0, len(mem.Participants))
	for _, participant := range mem.Participants {
		participant = strings.TrimSpace(participant)
		if participant == "" {
			continue
		}
		participants = append(participants, participant)
	}
	if len(participants) == 0 {
		return nil
	}
	sort.Slice(participants, func(i, j int) bool {
		li := strings.ToLower(participants[i])
		lj := strings.ToLower(participants[j])
		if li != lj {
			return li < lj
		}
		return participants[i] < participants[j]
	})
	out := make([]string, 0, len(participants))
	var prevFold string
	for _, participant := range participants {
		folded := strings.ToLower(participant)
		if len(out) > 0 && folded == prevFold {
			continue
		}
		out = append(out, participant)
		prevFold = folded
	}
	return out
}

func metadataIdentityLocation(mem *memory.Memory) string {
	if mem == nil {
		return ""
	}
	return strings.TrimSpace(mem.Location)
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

// ApplyMetadata populates episodic metadata on a Memory
// object. This is used by JSON-based backends (mysql,
// postgres, sqlite, etc.) where the Memory struct is
// serialized as JSON and the episodic fields are already
// part of the struct definition.
// If ep is nil, no fields are modified.
func ApplyMetadata(mem *memory.Memory, ep *memory.Metadata) {
	if mem == nil {
		return
	}
	if ep != nil {
		ep = normalizeAddMetadata(ep)
		if ep.Kind != "" {
			mem.Kind = ep.Kind
		}
		mem.EventTime = ep.EventTime
		mem.Participants = ep.Participants
		mem.Location = ep.Location
	}
	NormalizeMemory(mem)
}

// ApplyMetadataPatch updates only the metadata fields that are explicitly
// present on ep. Zero values are treated as "not provided" so update paths
// preserve stored metadata unless the caller supplied a replacement value.
func ApplyMetadataPatch(mem *memory.Memory, ep *memory.Metadata) {
	if mem == nil {
		return
	}
	if ep != nil {
		ep = normalizeUpdateMetadata(ep)
		if ep.Kind != "" {
			mem.Kind = ep.Kind
		}
		if ep.EventTime != nil {
			mem.EventTime = ep.EventTime
		}
		if len(ep.Participants) > 0 {
			mem.Participants = ep.Participants
		}
		if ep.Location != "" {
			mem.Location = ep.Location
		}
	}
	NormalizeMemory(mem)
}

func normalizeAddMetadata(ep *memory.Metadata) *memory.Metadata {
	if ep == nil {
		return nil
	}
	normalized := &memory.Metadata{
		Kind:         ep.Kind,
		EventTime:    ep.EventTime,
		Participants: metadataIdentityParticipants(&memory.Memory{Participants: ep.Participants}),
		Location:     strings.TrimSpace(ep.Location),
	}
	if normalized.Kind == "" && (normalized.EventTime != nil ||
		len(normalized.Participants) > 0 ||
		normalized.Location != "") {
		normalized.Kind = memory.KindFact
	}
	return normalized
}

func normalizeUpdateMetadata(ep *memory.Metadata) *memory.Metadata {
	if ep == nil {
		return nil
	}
	return &memory.Metadata{
		Kind:         ep.Kind,
		EventTime:    ep.EventTime,
		Participants: metadataIdentityParticipants(&memory.Memory{Participants: ep.Participants}),
		Location:     strings.TrimSpace(ep.Location),
	}
}

// NormalizeMemory canonicalizes memory metadata for runtime use and new writes.
func NormalizeMemory(mem *memory.Memory) {
	if mem == nil {
		return
	}
	mem.Kind = EffectiveKind(mem)
	mem.Participants = metadataIdentityParticipants(mem)
	mem.Location = strings.TrimSpace(mem.Location)
}

// NormalizeEntry canonicalizes the in-memory representation of an entry.
func NormalizeEntry(entry *memory.Entry) {
	if entry == nil {
		return
	}
	NormalizeMemory(entry.Memory)
}

// ApplyMemoryUpdate applies an update patch in-place and returns the effective
// canonical memory ID after the updated content and metadata are normalized.
func ApplyMemoryUpdate(
	entry *memory.Entry,
	appName, userID, memoryStr string,
	topics []string,
	ep *memory.Metadata,
	now time.Time,
) string {
	if entry == nil {
		return ""
	}
	if entry.Memory == nil {
		entry.Memory = &memory.Memory{}
	}
	entry.AppName = appName
	entry.UserID = userID
	entry.Memory.Memory = memoryStr
	entry.Memory.Topics = topics
	entry.Memory.LastUpdated = &now
	ApplyMetadataPatch(entry.Memory, ep)
	entry.UpdatedAt = now
	entry.ID = GenerateMemoryID(entry.Memory, appName, userID)
	return entry.ID
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
	// MinKindFallbackResults matches the pgvector behavior: when a kind-filtered
	// search returns fewer than this many results, a second unfiltered search can
	// be merged back in if KindFallback is enabled.
	MinKindFallbackResults = 3
)

// SearchMemoryEntries ranks keyword-search matches using shared scoring
// and sorting semantics, while leaving backend-specific thresholds and
// truncation to the caller.
func SearchMemoryEntries(
	entries []*memory.Entry,
	query string,
	opts SearchOptions,
) []*memory.Entry {
	return SearchEntries(entries, memory.SearchOptions{
		Query:      query,
		MaxResults: opts.MaxResults,
	}, opts.MinScore, opts.MaxResults)
}

type scoredEntry struct {
	entry *memory.Entry
	score float64
}

// SearchEntries applies keyword ranking together with public episodic-aware
// search options. This is used by non-vector backends after they materialize
// candidate entries in memory.
func SearchEntries(
	entries []*memory.Entry,
	opts memory.SearchOptions,
	minScore float64,
	defaultMaxResults int,
) []*memory.Entry {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return []*memory.Entry{}
	}

	limit := defaultMaxResults
	if opts.MaxResults > 0 {
		limit = opts.MaxResults
	}

	threshold := minScore
	if opts.SimilarityThreshold > 0 {
		threshold = opts.SimilarityThreshold
	}

	candidates := scoreEntries(entries, query, threshold)
	filtered := filterAndSortEntries(candidates, opts)
	results := cloneScoredEntries(filtered)

	if opts.Kind != "" && opts.KindFallback &&
		len(results) < MinKindFallbackResults {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallback := cloneScoredEntries(filterAndSortEntries(candidates, fallbackOpts))
		results = MergeSearchResults(results, fallback, opts.Kind, limit)
	}

	if opts.Deduplicate && len(results) > 1 {
		results = DeduplicateResults(results)
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func scoreEntries(
	entries []*memory.Entry,
	query string,
	minScore float64,
) []scoredEntry {
	candidates := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		score := ScoreMemoryEntry(entry, query)
		if !passesMinScore(score, minScore) {
			continue
		}
		candidates = append(candidates, scoredEntry{entry: entry, score: score})
	}
	return candidates
}

func filterAndSortEntries(
	candidates []scoredEntry,
	opts memory.SearchOptions,
) []scoredEntry {
	filtered := make([]scoredEntry, 0, len(candidates))
	for _, candidate := range candidates {
		if !matchesSearchFilters(candidate.entry, opts) {
			continue
		}
		filtered = append(filtered, candidate)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if opts.OrderByEventTime {
			ti := entryEventTime(filtered[i].entry)
			tj := entryEventTime(filtered[j].entry)
			switch {
			case ti == nil && tj != nil:
				return false
			case ti != nil && tj == nil:
				return true
			case ti != nil && tj != nil && !ti.Equal(*tj):
				return ti.Before(*tj)
			}
		}
		if filtered[i].score != filtered[j].score {
			return filtered[i].score > filtered[j].score
		}
		if !filtered[i].entry.UpdatedAt.Equal(filtered[j].entry.UpdatedAt) {
			return filtered[i].entry.UpdatedAt.After(filtered[j].entry.UpdatedAt)
		}
		if !filtered[i].entry.CreatedAt.Equal(filtered[j].entry.CreatedAt) {
			return filtered[i].entry.CreatedAt.After(filtered[j].entry.CreatedAt)
		}
		return filtered[i].entry.ID < filtered[j].entry.ID
	})
	return filtered
}

func matchesSearchFilters(entry *memory.Entry, opts memory.SearchOptions) bool {
	if entry == nil || entry.Memory == nil {
		return false
	}
	if opts.Kind != "" && EffectiveKind(entry.Memory) != opts.Kind {
		return false
	}
	if opts.TimeAfter != nil && entry.Memory.EventTime != nil &&
		entry.Memory.EventTime.Before(*opts.TimeAfter) {
		return false
	}
	if opts.TimeBefore != nil && entry.Memory.EventTime != nil &&
		entry.Memory.EventTime.After(*opts.TimeBefore) {
		return false
	}
	return true
}

func entryEventTime(entry *memory.Entry) *time.Time {
	if entry == nil || entry.Memory == nil {
		return nil
	}
	return entry.Memory.EventTime
}

func cloneScoredEntries(candidates []scoredEntry) []*memory.Entry {
	results := make([]*memory.Entry, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.entry == nil {
			continue
		}
		cloned := *candidate.entry
		cloned.Score = candidate.score
		results = append(results, &cloned)
	}
	return results
}

// MergeSearchResults merges kind-filtered results with fallback results.
// Results matching the preferred kind are ranked higher. Duplicates are
// removed by memory ID.
func MergeSearchResults(
	primary, fallback []*memory.Entry,
	preferredKind memory.Kind,
	maxResults int,
) []*memory.Entry {
	seen := make(map[string]bool, len(primary))
	for _, e := range primary {
		seen[e.ID] = true
	}

	var kindMatch, kindOther []*memory.Entry
	for _, e := range fallback {
		if seen[e.ID] {
			continue
		}
		if EffectiveKind(e.Memory) == preferredKind {
			kindMatch = append(kindMatch, e)
		} else {
			kindOther = append(kindOther, e)
		}
	}

	merged := make([]*memory.Entry, 0, len(primary)+len(kindMatch)+len(kindOther))
	merged = append(merged, primary...)
	merged = append(merged, kindMatch...)
	merged = append(merged, kindOther...)

	if maxResults > 0 && len(merged) > maxResults {
		merged = merged[:maxResults]
	}
	return merged
}

// DeduplicateResults removes near-duplicate memories based on word-level
// Jaccard similarity. When two results have >80% word overlap, the
// lower-scored one is dropped.
func DeduplicateResults(results []*memory.Entry) []*memory.Entry {
	const jaccardThreshold = 0.80

	type wordSet map[string]struct{}
	sets := make([]wordSet, len(results))
	for i, r := range results {
		ws := make(wordSet)
		if r != nil && r.Memory != nil {
			for _, w := range strings.Fields(strings.ToLower(r.Memory.Memory)) {
				ws[w] = struct{}{}
			}
		}
		sets[i] = ws
	}

	keep := make([]bool, len(results))
	for i := range keep {
		keep[i] = true
	}

	for i := 0; i < len(results); i++ {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(results); j++ {
			if !keep[j] {
				continue
			}
			if jaccardSimilarity(sets[i], sets[j]) >= jaccardThreshold {
				if results[i].Score >= results[j].Score {
					keep[j] = false
				} else {
					keep[i] = false
					break
				}
			}
		}
	}

	deduped := make([]*memory.Entry, 0, len(results))
	for i, r := range results {
		if keep[i] {
			deduped = append(deduped, r)
		}
	}
	return deduped
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for w := range a {
		if _, ok := b[w]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func passesMinScore(score float64, minScore float64) bool {
	if minScore > 0 {
		return score >= minScore
	}
	return score > 0
}
