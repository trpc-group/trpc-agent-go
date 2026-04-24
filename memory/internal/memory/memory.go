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
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	minEnglishTokenLen = 2
	minCJKTokenLen     = 2
	cjkFallbackGramLen = 3

	queryTokenWeight      = 1.0
	cjkTrigramTokenWeight = 0.45

	keywordBM25K1 = 1.2
	keywordBM25B  = 0.75

	contentFieldWeight = 1.0
	topicFieldWeight   = 0.65

	keywordCoverageWeight = 0.40
	keywordRarityWeight   = 0.25
	keywordStrengthWeight = 0.25
	keywordPhraseWeight   = 0.10

	exactPhraseFallbackScore = 0.35
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
//   - Add/Delete/Update: run in background by extractor by default.
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
	userExplicitlySet map[string]struct{},
) {
	if enabledTools == nil {
		return
	}
	// Apply auto mode defaults only for tools not explicitly set
	// by user.
	for toolName, defaultEnabled := range autoModeDefaultEnabledTools {
		if _, ok := userExplicitlySet[toolName]; ok {
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
//   - exposedTools: explicit agent-facing exposure overrides for Tools().
//   - hiddenTools: explicit agent-facing hide overrides for Tools().
//   - cachedTools: map to cache created tools (will be modified).
func BuildToolsList(
	ext extractor.MemoryExtractor,
	toolCreators map[string]memory.ToolCreator,
	enabledTools map[string]struct{},
	exposedTools map[string]struct{},
	hiddenTools map[string]struct{},
	cachedTools map[string]tool.Tool,
) []tool.Tool {
	// Collect tool names and sort for stable order.
	names := make([]string, 0, len(toolCreators))
	for name := range toolCreators {
		if !shouldIncludeTool(name, ext, enabledTools, exposedTools, hiddenTools) {
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
	exposedTools map[string]struct{},
	hiddenTools map[string]struct{},
) bool {
	// In auto memory mode, handle auto memory tools with special logic.
	if ext != nil {
		return shouldIncludeAutoMemoryTool(name, enabledTools, exposedTools, hiddenTools)
	}

	return shouldIncludeAgenticTool(name, enabledTools, exposedTools, hiddenTools)
}

// shouldIncludeAgenticTool checks whether a tool should be exposed to the
// agent in agentic mode.
func shouldIncludeAgenticTool(
	name string,
	enabledTools map[string]struct{},
	exposedTools map[string]struct{},
	hiddenTools map[string]struct{},
) bool {
	_, ok := enabledTools[name]
	if !ok {
		return false
	}
	if _, ok := hiddenTools[name]; ok {
		return false
	}
	if _, ok := exposedTools[name]; ok {
		return true
	}
	return true
}

// autoModeExposedTools defines the default auto-mode tools exposed to the
// agent. Other tools still run in background and can be selectively exposed
// via per-tool overrides.
var autoModeExposedTools = map[string]struct{}{
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
}

// shouldIncludeAutoMemoryTool checks if an auto memory tool should be
// included. In auto mode, Search and Load are exposed by default while other
// enabled tools require an explicit exposure override.
func shouldIncludeAutoMemoryTool(
	name string,
	enabledTools map[string]struct{},
	exposedTools map[string]struct{},
	hiddenTools map[string]struct{},
) bool {
	// The tool must be enabled before it can be exposed.
	if _, ok := enabledTools[name]; !ok {
		return false
	}
	if _, ok := hiddenTools[name]; ok {
		return false
	}
	if _, ok := exposedTools[name]; ok {
		return true
	}
	_, ok := autoModeExposedTools[name]
	return ok
}

type tokenOptions struct {
	deduplicate       bool
	keepSingleCJKRune bool
}

type weightedQueryToken struct {
	text   string
	weight float64
}

type fieldSearchStats struct {
	tokens         []string
	termFreq       map[string]int
	length         int
	normalizedText string
}

// BuildSearchTokens builds the primary lexical tokens for keyword search.
// CJK text uses gse word segmentation when available, and mixed-language
// text always keeps Latin word tokens.
func BuildSearchTokens(query string) []string {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}
	return tokenizePrimarySearchText(q, tokenOptions{
		deduplicate:       true,
		keepSingleCJKRune: shouldKeepSingleCJKToken(q),
	})
}

// isCJK reports if the rune belongs to a CJK script family.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// isHan reports if the rune is a Han character.
func isHan(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// cjkStopwords contains high-frequency CJK tokens that usually carry low
// retrieval value for memory search.
var cjkStopwords = map[string]struct{}{
	"的": {}, "了": {}, "是": {}, "在": {}, "和": {},
	"有": {}, "我": {}, "他": {}, "她": {}, "它": {},
	"这": {}, "那": {}, "都": {}, "也": {}, "就": {},
	"不": {}, "会": {}, "到": {}, "说": {}, "对": {},
	"一个": {}, "一些": {}, "一种": {}, "这个": {}, "那个": {},
	"我们": {}, "你们": {}, "他们": {}, "她们": {}, "它们": {},
	"自己": {}, "已经": {}, "还是": {}, "如果": {}, "因为": {},
	"所以": {}, "然后": {}, "用户": {},
}

func isCJKStopword(w string) bool {
	if w == "" {
		return false
	}
	if _, ok := cjkStopwords[w]; ok {
		return true
	}
	if !isCJKToken(w) {
		return false
	}
	for _, r := range w {
		if _, ok := cjkStopwords[string(r)]; !ok {
			return false
		}
	}
	return true
}

// isPunct reports if the rune is punctuation or symbol.
func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// isPunctToken reports if the string consists entirely of punctuation or
// symbol runes.
func isPunctToken(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !isPunct(r) {
			return false
		}
	}
	return true
}

func shouldKeepSingleCJKToken(text string) bool {
	var kept rune
	count := 0
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsSpace(r) || isPunct(r) {
			continue
		}
		count++
		kept = r
		if count > 1 {
			return false
		}
	}
	return count == 1 && isCJK(kept)
}

func tokenizePrimarySearchText(text string, opts tokenOptions) []string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil
	}
	tokens := make([]string, 0, 8)
	if containsHan(text) {
		if segTokens := segmentCJKTokens(text, opts.keepSingleCJKRune); len(segTokens) > 0 {
			tokens = append(tokens, segTokens...)
		}
	} else if containsCJKText(text) {
		tokens = append(tokens,
			collectRawCJKSegments(text, opts.keepSingleCJKRune)...)
	}
	tokens = append(tokens, collectEnglishTokens(text)...)
	if opts.deduplicate {
		return dedupStrings(tokens)
	}
	return tokens
}

// containsHan reports whether the text contains any Han (Chinese)
// character. Short-circuits on the first match.
func containsHan(text string) bool {
	for _, r := range text {
		if isHan(r) {
			return true
		}
	}
	return false
}

// containsCJKText reports whether the text contains any CJK-family
// character (Han / Hiragana / Katakana / Hangul). Short-circuits.
func containsCJKText(text string) bool {
	for _, r := range text {
		if isCJK(r) {
			return true
		}
	}
	return false
}

func segmentCJKTokens(text string, keepSingleCJKRune bool) []string {
	s, err := getSegmenter()
	if err != nil {
		return collectRawCJKSegments(text, keepSingleCJKRune)
	}
	words := s.CutSearch(text, true)
	tokens := make([]string, 0, len(words))
	for _, word := range words {
		token := normalizeSegmentToken(word)
		if token == "" || isPunctToken(token) {
			continue
		}
		if isASCIIAlnumToken(token) {
			// Latin tokens inside mixed Han/Latin text are emitted by
			// collectEnglishTokens later in tokenizePrimarySearchText.
			// Skipping them here prevents double-counting in the
			// deduplicate=false scoring paths (buildFieldSearchStats),
			// which would otherwise inflate BM25 term frequencies and
			// document lengths for mixed-language memories.
			continue
		}
		if isCJKToken(token) {
			if !keepSingleCJKRune &&
				utf8.RuneCountInString(token) < minCJKTokenLen {
				continue
			}
			if isCJKStopword(token) {
				continue
			}
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func normalizeSegmentToken(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	if token == "" {
		return ""
	}
	if isASCIIAlnumToken(token) {
		return token
	}
	if isCJKToken(token) {
		return token
	}
	var builder strings.Builder
	for _, r := range token {
		switch {
		case isCJK(r):
			builder.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func collectEnglishTokens(text string) []string {
	var (
		builder strings.Builder
		tokens  []string
	)
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := builder.String()
		builder.Reset()
		if len(token) < minEnglishTokenLen || isStopword(token) {
			return
		}
		tokens = append(tokens, token)
	}
	for _, r := range text {
		switch {
		case isCJK(r):
			flush()
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func collectRawCJKSegments(
	text string,
	keepSingleCJKRune bool,
) []string {
	segments := collectCJKSegments(text)
	tokens := make([]string, 0, len(segments))
	for _, segment := range segments {
		if !keepSingleCJKRune &&
			utf8.RuneCountInString(segment) < minCJKTokenLen {
			continue
		}
		if isCJKStopword(segment) {
			continue
		}
		tokens = append(tokens, segment)
	}
	return tokens
}

func collectCJKSegments(text string) []string {
	segments := make([]string, 0, 4)
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		segments = append(segments, builder.String())
		builder.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if isCJK(r) {
			builder.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return segments
}

func buildFallbackCJKTrigrams(text string) []string {
	segments := collectCJKSegments(strings.ToLower(text))
	ngrams := make([]string, 0, len(segments)*2)
	for _, segment := range segments {
		runes := []rune(segment)
		if len(runes) < cjkFallbackGramLen {
			continue
		}
		for i := 0; i <= len(runes)-cjkFallbackGramLen; i++ {
			token := string(runes[i : i+cjkFallbackGramLen])
			if isCJKStopword(token) {
				continue
			}
			ngrams = append(ngrams, token)
		}
	}
	return ngrams
}

func buildWeightedQueryTokens(query string) []weightedQueryToken {
	primaryTokens := BuildSearchTokens(query)
	weighted := make([]weightedQueryToken, 0, len(primaryTokens)+4)
	weights := make(map[string]float64, len(primaryTokens)+4)
	add := func(token string, weight float64) {
		if token == "" {
			return
		}
		if existing, ok := weights[token]; ok {
			if weight > existing {
				weights[token] = weight
			}
			return
		}
		weights[token] = weight
		weighted = append(weighted, weightedQueryToken{
			text:   token,
			weight: weight,
		})
	}
	for _, token := range primaryTokens {
		add(token, queryTokenWeight)
	}
	for _, token := range buildFallbackCJKTrigrams(query) {
		if _, ok := weights[token]; ok {
			continue
		}
		add(token, cjkTrigramTokenWeight)
	}
	return weighted
}

func buildFieldSearchStats(text string) fieldSearchStats {
	primaryTokens := tokenizePrimarySearchText(text, tokenOptions{
		deduplicate:       false,
		keepSingleCJKRune: false,
	})
	termFreq := make(map[string]int, len(primaryTokens))
	for _, token := range primaryTokens {
		termFreq[token]++
	}
	length := len(primaryTokens)
	for _, token := range buildFallbackCJKTrigrams(text) {
		if termFreq[token] > 0 {
			continue
		}
		termFreq[token]++
		length++
	}
	return fieldSearchStats{
		tokens:         primaryTokens,
		termFreq:       termFreq,
		length:         length,
		normalizedText: normalizePhraseText(text),
	}
}

func normalizePhraseText(text string) string {
	if text == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case isCJK(r):
			builder.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		default:
			builder.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func isASCIIAlnumToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if isCJK(r) || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isCJKToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !isCJK(r) {
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

var englishStopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {},
	"of": {}, "in": {}, "on": {}, "to": {}, "for": {},
	"with": {}, "is": {}, "are": {}, "am": {}, "be": {},
	"been": {}, "being": {}, "was": {}, "were": {},
	"this": {}, "that": {}, "these": {}, "those": {},
}

// isStopword returns true for a lightweight set of English stopwords.
func isStopword(s string) bool {
	_, ok := englishStopwords[s]
	return ok
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

// ScoreMemoryEntry returns a normalized keyword relevance score in [0, 1].
// The score combines BM25-style weighting, query coverage, and an ordered
// phrase bonus.
func ScoreMemoryEntry(entry *memory.Entry, query string) float64 {
	if entry == nil || entry.Memory == nil {
		return 0
	}
	scorer := newKeywordSearchScorer([]*memory.Entry{entry}, query)
	if len(scorer.docs) == 0 {
		return 0
	}
	return scorer.scoreDoc(scorer.docs[0])
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
	// DefaultHybridRRFK is the standard Reciprocal Rank Fusion constant.
	DefaultHybridRRFK = 60
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

type entrySearchStats struct {
	entry   *memory.Entry
	content fieldSearchStats
	topics  fieldSearchStats
}

type keywordSearchScorer struct {
	query          string
	primaryTokens  []string
	weightedTokens []weightedQueryToken
	totalWeight    float64
	totalIDFWeight float64
	idf            map[string]float64
	avgContentLen  float64
	avgTopicLen    float64
	docs           []entrySearchStats
}

func newKeywordSearchScorer(
	entries []*memory.Entry,
	query string,
) *keywordSearchScorer {
	query = strings.TrimSpace(query)
	scorer := &keywordSearchScorer{
		query:          query,
		primaryTokens:  BuildSearchTokens(query),
		weightedTokens: buildWeightedQueryTokens(query),
		idf:            make(map[string]float64),
	}
	if query == "" {
		return scorer
	}

	scorer.docs = make([]entrySearchStats, 0, len(entries))
	docFreq := make(map[string]int, len(scorer.weightedTokens))
	var totalContentLen float64
	var totalTopicLen float64
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		doc := entrySearchStats{
			entry:   entry,
			content: buildFieldSearchStats(entry.Memory.Memory),
			topics: buildFieldSearchStats(strings.Join(
				entry.Memory.Topics, " ",
			)),
		}
		scorer.docs = append(scorer.docs, doc)
		totalContentLen += float64(doc.content.length)
		totalTopicLen += float64(doc.topics.length)
		incrementDocumentFrequency(docFreq, scorer.weightedTokens, doc)
	}
	if len(scorer.docs) > 0 {
		scorer.avgContentLen = math.Max(
			totalContentLen/float64(len(scorer.docs)),
			1,
		)
		scorer.avgTopicLen = math.Max(
			totalTopicLen/float64(len(scorer.docs)),
			1,
		)
	}
	for _, token := range scorer.weightedTokens {
		scorer.totalWeight += token.weight
		scorer.idf[token.text] = inverseDocumentFrequency(
			len(scorer.docs),
			docFreq[token.text],
		)
		scorer.totalIDFWeight += token.weight * scorer.idf[token.text]
	}
	return scorer
}

func incrementDocumentFrequency(
	docFreq map[string]int,
	tokens []weightedQueryToken,
	doc entrySearchStats,
) {
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token.text]; ok {
			continue
		}
		if doc.content.termFreq[token.text] > 0 ||
			doc.topics.termFreq[token.text] > 0 {
			docFreq[token.text]++
			seen[token.text] = struct{}{}
		}
	}
}

func inverseDocumentFrequency(docCount int, docFreq int) float64 {
	if docCount <= 0 {
		return 0
	}
	numerator := float64(docCount-docFreq) + 0.5
	denominator := float64(docFreq) + 0.5
	return math.Log1p(numerator / denominator)
}

func bm25TermScore(tf int, docLen int, avgDocLen float64) float64 {
	if tf <= 0 || docLen <= 0 || avgDocLen <= 0 {
		return 0
	}
	normalizer := keywordBM25K1 * (1 - keywordBM25B +
		keywordBM25B*float64(docLen)/avgDocLen)
	return float64(tf) * (keywordBM25K1 + 1) /
		(float64(tf) + normalizer)
}

func (s *keywordSearchScorer) scoreDoc(doc entrySearchStats) float64 {
	if doc.entry == nil || doc.entry.Memory == nil || s.query == "" {
		return 0
	}
	if len(s.weightedTokens) == 0 {
		return fallbackPhraseScore(doc, s.query)
	}

	var raw float64
	var matchedPotential float64
	var matchedWeight float64
	var matchedIDFWeight float64
	for _, token := range s.weightedTokens {
		idf := s.idf[token.text]
		if idf <= 0 {
			continue
		}
		contentScore := bm25TermScore(
			doc.content.termFreq[token.text],
			doc.content.length,
			s.avgContentLen,
		)
		topicScore := bm25TermScore(
			doc.topics.termFreq[token.text],
			doc.topics.length,
			s.avgTopicLen,
		)
		termScore := token.weight * idf *
			(contentFieldWeight*contentScore +
				topicFieldWeight*topicScore)
		if termScore > 0 {
			raw += termScore
			matchedWeight += token.weight
			matchedIDFWeight += token.weight * idf
			matchedPotential += token.weight * idf *
				contentFieldWeight * (keywordBM25K1 + 1)
		}
	}
	if matchedWeight == 0 {
		return fallbackPhraseScore(doc, s.query)
	}

	var strengthScore float64
	if matchedPotential > 0 {
		strengthScore = math.Min(raw/matchedPotential, 1)
	}
	coverage := matchedWeight / math.Max(s.totalWeight, 1)
	rarityCoverage := matchedIDFWeight / math.Max(s.totalIDFWeight, 1)
	phraseBonus := orderedPhraseBonus(doc, s.primaryTokens)
	score := keywordCoverageWeight*coverage +
		keywordRarityWeight*rarityCoverage +
		keywordStrengthWeight*strengthScore +
		keywordPhraseWeight*phraseBonus
	return math.Min(score, 1)
}

func orderedPhraseBonus(
	doc entrySearchStats,
	queryTokens []string,
) float64 {
	if len(queryTokens) < 2 {
		return 0
	}
	switch {
	case containsOrderedTokens(doc.content.tokens, queryTokens):
		return 1
	case containsOrderedTokens(doc.topics.tokens, queryTokens):
		return 0.8
	default:
		return 0
	}
}

func containsOrderedTokens(docTokens, queryTokens []string) bool {
	if len(docTokens) == 0 || len(queryTokens) == 0 {
		return false
	}
	idx := 0
	for _, token := range docTokens {
		if token != queryTokens[idx] {
			continue
		}
		idx++
		if idx == len(queryTokens) {
			return true
		}
	}
	return false
}

func fallbackPhraseScore(doc entrySearchStats, query string) float64 {
	rawQuery := strings.TrimSpace(query)
	if shouldAllowRawExactFallback(rawQuery) {
		lowerRawQuery := strings.ToLower(rawQuery)
		if strings.Contains(
			strings.ToLower(doc.entry.Memory.Memory),
			lowerRawQuery,
		) {
			return exactPhraseFallbackScore
		}
		if strings.Contains(
			strings.ToLower(strings.Join(doc.entry.Memory.Topics, " ")),
			lowerRawQuery,
		) {
			return exactPhraseFallbackScore * topicFieldWeight
		}
	}
	if !shouldAllowExactFallback(query) {
		return 0
	}
	normalizedQuery := normalizePhraseText(query)
	if normalizedQuery == "" {
		return 0
	}
	if strings.Contains(doc.content.normalizedText, normalizedQuery) {
		return exactPhraseFallbackScore
	}
	if strings.Contains(doc.topics.normalizedText, normalizedQuery) {
		return exactPhraseFallbackScore * topicFieldWeight
	}
	return 0
}

func shouldAllowExactFallback(query string) bool {
	normalized := normalizePhraseText(query)
	if normalized == "" {
		return false
	}
	fields := strings.Fields(normalized)
	if len(fields) == 1 && isASCIIAlnumToken(fields[0]) {
		return false
	}
	return true
}

func shouldAllowRawExactFallback(query string) bool {
	if query == "" || len(BuildSearchTokens(query)) > 0 {
		return false
	}
	var hasAlnum bool
	var hasSpecial bool
	for _, r := range query {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			hasAlnum = true
		case !unicode.IsSpace(r):
			hasSpecial = true
		}
	}
	return hasAlnum && hasSpecial
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
	scorer := newKeywordSearchScorer(entries, query)
	candidates := make([]scoredEntry, 0, len(scorer.docs))
	for _, doc := range scorer.docs {
		score := scorer.scoreDoc(doc)
		if !passesMinScore(score, minScore) {
			continue
		}
		candidates = append(candidates, scoredEntry{
			entry: doc.entry,
			score: score,
		})
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
		return lessSearchEntry(
			filtered[i].entry,
			filtered[j].entry,
			filtered[i].score,
			filtered[j].score,
			opts.OrderByEventTime,
		)
	})
	return filtered
}

func lessSearchEntry(
	left *memory.Entry,
	right *memory.Entry,
	leftScore float64,
	rightScore float64,
	orderByEventTime bool,
) bool {
	switch {
	case left == nil && right == nil:
		return false
	case left == nil:
		return false
	case right == nil:
		return true
	}
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	if orderByEventTime {
		ti := entryEventTime(left)
		tj := entryEventTime(right)
		switch {
		case ti == nil && tj != nil:
			return false
		case ti != nil && tj == nil:
			return true
		case ti != nil && tj != nil && !ti.Equal(*tj):
			return ti.Before(*tj)
		}
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	return left.ID < right.ID
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

// SortSearchResults sorts scored memory entries by relevance first and then
// applies event_time as a tie-breaker when requested.
func SortSearchResults(results []*memory.Entry, orderByEventTime bool) {
	sort.SliceStable(results, func(i, j int) bool {
		var leftScore float64
		if results[i] != nil {
			leftScore = results[i].Score
		}
		var rightScore float64
		if results[j] != nil {
			rightScore = results[j].Score
		}
		return lessSearchEntry(
			results[i],
			results[j],
			leftScore,
			rightScore,
			orderByEventTime,
		)
	})
}

// SortSearchResultsWithKindPriority sorts results within preferred and fallback
// kind groups separately, then keeps the preferred kind group ahead.
func SortSearchResultsWithKindPriority(
	results []*memory.Entry,
	preferredKind memory.Kind,
	orderByEventTime bool,
) {
	if preferredKind == "" || len(results) < 2 {
		SortSearchResults(results, orderByEventTime)
		return
	}
	preferred := make([]*memory.Entry, 0, len(results))
	fallback := make([]*memory.Entry, 0, len(results))
	for _, entry := range results {
		if entry != nil && EffectiveKind(entry.Memory) == preferredKind {
			preferred = append(preferred, entry)
			continue
		}
		fallback = append(fallback, entry)
	}
	SortSearchResults(preferred, orderByEventTime)
	SortSearchResults(fallback, orderByEventTime)
	pos := copy(results, preferred)
	copy(results[pos:], fallback)
}

// MergeSearchResults merges kind-filtered results with fallback results.
// Results matching the preferred kind are ranked higher. Duplicates are
// removed by memory ID.
func MergeSearchResults(
	primary, fallback []*memory.Entry,
	preferredKind memory.Kind,
	maxResults int,
) []*memory.Entry {
	seen := make(map[string]struct{}, len(primary))
	for _, e := range primary {
		seen[e.ID] = struct{}{}
	}

	var kindMatch, kindOther []*memory.Entry
	for _, e := range fallback {
		if _, ok := seen[e.ID]; ok {
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

// MergeHybridResults combines ranked result lists using Reciprocal Rank Fusion.
// Scores are assigned using 1 / (k + rank) and summed across result lists.
func MergeHybridResults(
	primary []*memory.Entry,
	secondary []*memory.Entry,
	k int,
	maxResults int,
) []*memory.Entry {
	if k <= 0 {
		k = DefaultHybridRRFK
	}

	type rrfEntry struct {
		entry *memory.Entry
		score float64
	}

	scores := make(map[string]*rrfEntry, len(primary)+len(secondary))
	accumulate := func(results []*memory.Entry) {
		for rank, entry := range results {
			if entry == nil || entry.ID == "" {
				continue
			}
			rrfScore := 1.0 / float64(k+rank+1)
			if existing, ok := scores[entry.ID]; ok {
				existing.score += rrfScore
				continue
			}
			cloned := *entry
			scores[entry.ID] = &rrfEntry{
				entry: &cloned,
				score: rrfScore,
			}
		}
	}
	accumulate(primary)
	accumulate(secondary)

	merged := make([]*memory.Entry, 0, len(scores))
	for _, scored := range scores {
		scored.entry.Score = scored.score
		merged = append(merged, scored.entry)
	}
	SortSearchResults(merged, false)
	if maxResults > 0 && len(merged) > maxResults {
		merged = merged[:maxResults]
	}
	return merged
}

// DeduplicateResults removes near-duplicate memories based on word-level
// Jaccard similarity. When two results have >80% word overlap, the
// lower-scored one is dropped.
//
// Implementation notes:
//   - Decisions use a score-descending pass so the highest-scored entry
//     of each near-duplicate cluster survives, regardless of the input
//     ordering.
//   - Output preserves the caller's original slice ordering among the
//     survivors, matching the long-standing contract of this helper.
//   - Each candidate is compared against every higher-scored entry
//     (whether already kept or already dropped) so pairwise semantics
//     hold: a chain such as A~B, B~C, A!~C still drops C because C has
//     a higher-scored near-duplicate (B) in the input.
func DeduplicateResults(results []*memory.Entry) []*memory.Entry {
	const jaccardThreshold = 0.80
	if len(results) < 2 {
		return results
	}

	// Build token sets once per entry; reused across all comparisons.
	sets := make([]map[string]struct{}, len(results))
	for i, r := range results {
		sets[i] = entryTokenSet(r)
	}

	// Visit indices in score-descending order so that the first
	// representative we pick for any duplicate cluster is the one
	// with the highest score. Stable ordering on ties keeps behavior
	// deterministic for equal-scored near-duplicates.
	order := make([]int, len(results))
	for i := range order {
		order[i] = i
	}
	// Treat nil entries as having the lowest possible score so they
	// sort last instead of panicking in the comparator. The Jaccard
	// pass below is already nil-safe via entryTokenSet, so keeping
	// nils at the tail is enough to preserve overall safety.
	entryScore := func(e *memory.Entry) float64 {
		if e == nil {
			return math.Inf(-1)
		}
		return e.Score
	}
	sort.SliceStable(order, func(a, b int) bool {
		return entryScore(results[order[a]]) > entryScore(results[order[b]])
	})

	// Compare each candidate against every already-visited index, not
	// just survivors. Without this, a chain A~B and B~C (with A not
	// similar to C) can leave both A and C in the output, which would
	// contradict the documented pairwise semantics: every dropped
	// entry must have at least one higher-scored near-duplicate in
	// the final output *or* in the set of already-dropped duplicates.
	// Comparing against all higher-scored entries (via the prefix of
	// the sorted order) preserves that invariant.
	kept := make([]bool, len(results))
	kept[order[0]] = true
	for pos := 1; pos < len(order); pos++ {
		idx := order[pos]
		isDup := false
		for prev := 0; prev < pos; prev++ {
			k := order[prev]
			if jaccardAtLeast(sets[idx], sets[k], jaccardThreshold) {
				isDup = true
				break
			}
		}
		if isDup {
			continue
		}
		kept[idx] = true
	}

	// Emit survivors in the original input order so callers relying on
	// the historical ordering semantics keep working.
	deduped := make([]*memory.Entry, 0, len(results))
	for i, r := range results {
		if kept[i] {
			deduped = append(deduped, r)
		}
	}
	return deduped
}

// entryTokenSet builds a Jaccard-friendly token set from an entry's
// memory content. Returns an empty set for nil / empty entries.
func entryTokenSet(e *memory.Entry) map[string]struct{} {
	if e == nil || e.Memory == nil {
		return map[string]struct{}{}
	}
	// Pre-size the map to avoid rehashing; the typical short memory
	// has ~10-30 unique tokens after dedup.
	set := make(map[string]struct{}, 16)
	for _, w := range BuildSearchTokens(e.Memory.Memory) {
		set[w] = struct{}{}
	}
	for _, w := range buildFallbackCJKTrigrams(e.Memory.Memory) {
		set[w] = struct{}{}
	}
	return set
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	la, lb := len(a), len(b)
	if la == 0 && lb == 0 {
		return 1.0
	}
	if la == 0 || lb == 0 {
		return 0
	}
	// Iterate over the smaller set to minimize probe count, and look
	// up in the larger one.
	small, large := a, b
	ls, ll := la, lb
	if lb < la {
		small, large = b, a
		ls, ll = lb, la
	}
	intersection := 0
	for w := range small {
		if _, ok := large[w]; ok {
			intersection++
		}
	}
	union := ls + ll - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// jaccardAtLeast reports whether Jaccard(a, b) >= threshold, doing as
// little work as possible. It returns early on size mismatch (the
// sets cannot reach the threshold when their cardinalities differ
// beyond a ratio bound) and on running-intersection lower bounds.
// This is what DeduplicateResults actually needs — the exact ratio
// is never read — so the helper avoids the full intersection scan
// in the common "clearly not similar" case.
//
// Two empty sets are treated as non-comparable (returns false) for
// dedup purposes: entryTokenSet yields an empty set for nil /
// punctuation-only / stopword-only memories, and without this guard
// a pair of such unrelated entries would be collapsed purely because
// neither produced any lexical evidence.
func jaccardAtLeast(a, b map[string]struct{}, threshold float64) bool {
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		return false
	}
	small, large := a, b
	ls, ll := la, lb
	if lb < la {
		small, large = b, a
		ls, ll = lb, la
	}
	// Upper bound on Jaccard given only set sizes: |small| / |large|.
	// If even that cannot hit the threshold, skip the probe entirely.
	if float64(ls)/float64(ll) < threshold {
		return false
	}
	// Minimum intersection count that satisfies
	// |I| / (|a| + |b| - |I|) >= T, solved for |I|:
	//   |I| >= T * (|a| + |b|) / (1 + T)
	needed := int(math.Ceil(threshold * float64(la+lb) / (1 + threshold)))
	inter := 0
	// maxPossible tracks how many matches are still reachable; stop
	// early when even matching every remaining small-set token cannot
	// reach the needed count.
	remaining := ls
	for w := range small {
		if _, ok := large[w]; ok {
			inter++
			if inter >= needed {
				return true
			}
		}
		remaining--
		if inter+remaining < needed {
			return false
		}
	}
	return inter >= needed
}

func passesMinScore(score float64, minScore float64) bool {
	if minScore > 0 {
		return score >= minScore
	}
	return score > 0
}
