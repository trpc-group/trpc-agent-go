//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Strategy defines the interface for document chunking strategies.
type Strategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc *document.Document) ([]*document.Document, error)
}

var (
	defaultChunkSize = 1024
	defaultOverlap   = 0
)

func validateChunkConfig(chunkSize, overlap int) error {
	switch {
	case chunkSize <= 0:
		return ErrInvalidChunkSize
	case overlap < 0:
		return ErrInvalidOverlap
	case overlap >= chunkSize:
		return ErrOverlapTooLarge
	default:
		return nil
	}
}

func measureTextLength(
	lengthFunc func(string) (int, error),
	content string,
) (int, error) {
	if lengthFunc == nil {
		return encoding.RuneCount(content), nil
	}
	length, err := lengthFunc(content)
	if err != nil {
		return 0, fmt.Errorf("measure text length: %w", err)
	}
	if length < 0 {
		return 0, fmt.Errorf(
			"measure text length: length function returned negative length %d",
			length,
		)
	}
	return length, nil
}

// cleanText normalizes whitespace in text content while ensuring UTF-8 safety.
// It automatically detects encoding and converts to UTF-8 if necessary.
func cleanText(content string) string {
	// Intelligently process text based on detected encoding
	processed, encodingInfo := encoding.SmartProcessText(content)

	// Log encoding information for debugging.
	if encodingInfo.Encoding != encoding.EncodingUTF8 || !encodingInfo.IsValid {
		log.Debugf("Text encoding detected: %s (confidence: %.2f, valid: %v)",
			encodingInfo.Encoding, encodingInfo.Confidence, encodingInfo.IsValid)
	}

	// Trim leading and trailing whitespace.
	processed = strings.TrimSpace(processed)

	// Normalize line breaks.
	processed = strings.ReplaceAll(processed, "\r\n", "\n")
	processed = strings.ReplaceAll(processed, "\r", "\n")

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(processed, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *document.Document, content string, chunkNumber int) *document.Document {
	// Create a copy of the original metadata.
	metadata := make(map[string]any)
	for k, v := range originalDoc.Metadata {
		metadata[k] = v
	}

	// Add chunk-specific metadata.
	metadata[source.MetaChunkIndex] = chunkNumber
	metadata[source.MetaChunkSize] = encoding.RuneCount(content)

	// Generate chunk ID.
	var chunkID string
	if originalDoc.ID != "" {
		chunkID = originalDoc.ID + "_" + strconv.Itoa(chunkNumber)
	} else if originalDoc.Name != "" {
		chunkID = originalDoc.Name + "_" + strconv.Itoa(chunkNumber)
	} else {
		chunkID = "chunk_" + strconv.Itoa(chunkNumber)
	}

	return &document.Document{
		ID:        chunkID,
		Name:      originalDoc.Name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func joinWithOverlap(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
) (string, int) {
	return joinWithOverlapSeparator(
		previous,
		current,
		maxOverlap,
		maxSize,
		separator,
		false,
	)
}

func joinWithOverlapSeparator(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
	preserveSeparator bool,
) (string, int) {
	currentSize := encoding.RuneCount(current)
	separatorSize := encoding.RuneCount(separator)
	availableWithoutSeparator := maxSize - currentSize
	if maxOverlap <= 0 || availableWithoutSeparator <= 0 {
		return current, 0
	}
	availableOverlap := availableWithoutSeparator - separatorSize
	if availableOverlap <= 0 {
		if preserveSeparator {
			return current, 0
		}
		separator = ""
		availableOverlap = availableWithoutSeparator
	}

	overlapSize := min(
		maxOverlap,
		min(availableOverlap, encoding.RuneCount(previous)),
	)
	if overlapSize <= 0 {
		return current, 0
	}
	overlapContent, naturalBoundary := naturalTextSuffix(previous, overlapSize)
	if !naturalBoundary && separator != "" && !preserveSeparator {
		// For an unbroken token, preserve the exact source text rather than
		// inserting a separator in the middle of the token.
		separator = ""
		availableOverlap = availableWithoutSeparator
		overlapSize = min(
			maxOverlap,
			min(availableOverlap, encoding.RuneCount(previous)),
		)
		overlapContent, _ = naturalTextSuffix(previous, overlapSize)
	}
	actualOverlap := encoding.RuneCount(overlapContent)
	if actualOverlap <= 0 {
		return current, 0
	}
	return overlapContent + separator + current, actualOverlap
}

func joinWithOverlapSeparatorByLength(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
	preserveSeparator bool,
	lengthFunc func(string) (int, error),
) (string, int, error) {
	currentSize, err := measureTextLength(lengthFunc, current)
	if err != nil {
		return "", 0, err
	}
	if currentSize > maxSize {
		return "", 0, fmt.Errorf(
			"current content length %d exceeds chunk size %d",
			currentSize,
			maxSize,
		)
	}
	if maxOverlap <= 0 || previous == "" {
		return current, 0, nil
	}

	previousRunes := []rune(previous)
	previousSize, err := measureTextLength(lengthFunc, previous)
	if err != nil {
		return "", 0, err
	}
	estimatedRunes := len(previousRunes)
	if previousSize > maxOverlap {
		estimatedRunes = max(
			1,
			len(previousRunes)*maxOverlap/previousSize,
		)
	}
	estimatedStart := len(previousRunes) - estimatedRunes
	starts := lengthBoundaryCandidates(previousRunes, estimatedStart)
	if estimatedStart <= 0 {
		starts = append([]int{0}, starts...)
	}

	bestContent := ""
	bestSize := 0
	bestRunes := 0
	bestNatural := false
	for _, start := range starts {
		combined, overlapSize, runeSize, natural, ok, err :=
			evaluateOverlapCandidateByLength(
				previousRunes,
				current,
				start,
				maxOverlap,
				maxSize,
				separator,
				preserveSeparator,
				lengthFunc,
			)
		if err != nil {
			return "", 0, err
		}
		if !ok {
			continue
		}
		if betterOverlapCandidate(
			natural,
			bestNatural,
			overlapSize,
			bestSize,
			runeSize,
			bestRunes,
		) {
			bestContent = combined
			bestSize = overlapSize
			bestRunes = runeSize
			bestNatural = natural
		}
	}

	if bestContent != "" {
		return bestContent, bestSize, nil
	}
	return current, 0, nil
}

func evaluateOverlapCandidateByLength(
	previous []rune,
	current string,
	start int,
	maxOverlap int,
	maxSize int,
	separator string,
	preserveSeparator bool,
	lengthFunc func(string) (int, error),
) (string, int, int, bool, bool, error) {
	overlapContent := strings.TrimLeftFunc(
		string(previous[start:]),
		unicode.IsSpace,
	)
	if overlapContent == "" {
		return "", 0, 0, false, false, nil
	}
	overlapSize, err := measureTextLength(lengthFunc, overlapContent)
	if err != nil {
		return "", 0, 0, false, false, err
	}
	if overlapSize <= 0 || overlapSize > maxOverlap {
		return "", 0, 0, false, false, nil
	}

	natural := isNaturalTextStart(previous, start)
	if !natural && !preserveSeparator {
		separator = ""
	}
	combined := overlapContent + separator + current
	combinedSize, err := measureTextLength(lengthFunc, combined)
	if err != nil {
		return "", 0, 0, false, false, err
	}
	if combinedSize > maxSize {
		return "", 0, 0, false, false, nil
	}
	return combined, overlapSize, len([]rune(overlapContent)), natural, true, nil
}

func betterOverlapCandidate(
	natural bool,
	bestNatural bool,
	size int,
	bestSize int,
	runes int,
	bestRunes int,
) bool {
	if natural != bestNatural {
		return natural
	}
	if size != bestSize {
		return size > bestSize
	}
	return runes > bestRunes
}

func sourceChunkSeparators(
	content string,
	chunks []string,
	fallback string,
) []string {
	separators := make([]string, len(chunks))
	searchFrom := 0
	previousEnd := 0
	for i, chunk := range chunks {
		position := strings.Index(content[searchFrom:], chunk)
		if position < 0 {
			if i > 0 {
				separators[i] = fallback
			}
			continue
		}
		start := searchFrom + position
		if i > 0 {
			separators[i] = sourceGapSeparator(
				content[previousEnd:start],
				fallback,
			)
		}
		previousEnd = start + len(chunk)
		searchFrom = previousEnd
	}
	return separators
}

func sourceGapSeparator(gap string, fallback string) string {
	if gap == "" {
		return ""
	}
	if strings.TrimSpace(gap) != "" {
		return fallback
	}
	if strings.Contains(gap, "\n\n") {
		return "\n\n"
	}
	if strings.ContainsRune(gap, '\n') {
		return "\n"
	}
	return " "
}

// splitTextAtNaturalBoundary splits one prefix from content without exceeding
// maxSize. It prefers line, sentence, punctuation, and whitespace boundaries,
// falling back to an exact rune boundary only when the text has no suitable
// natural boundary.
func splitTextAtNaturalBoundary(content string, maxSize int) (string, string) {
	content = strings.TrimSpace(content)
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) <= maxSize {
		return content, ""
	}

	splitPosition := preferredTextBoundary(contentRunes, maxSize)
	prefix := strings.TrimSpace(string(contentRunes[:splitPosition]))
	remaining := strings.TrimSpace(string(contentRunes[splitPosition:]))
	return prefix, remaining
}

func splitTextAtNaturalBoundaryByLength(
	content string,
	maxSize int,
	lengthFunc func(string) (int, error),
) (string, string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", "", nil
	}
	if maxSize <= 0 {
		return "", content, nil
	}
	contentRunes := []rune(content)
	estimate, searchEnd, fits, err := estimateTextSplitByLength(
		contentRunes,
		maxSize,
		lengthFunc,
	)
	if err != nil {
		return "", "", err
	}
	if fits {
		return content, "", nil
	}
	candidateContent := contentRunes[:searchEnd+1]
	candidates := lengthBoundaryCandidates(candidateContent, estimate)
	best, err := findTextSplitPositionByLength(
		contentRunes,
		candidates,
		maxSize,
		lengthFunc,
	)
	if err != nil {
		return "", "", err
	}
	if best == 0 {
		first := string(contentRunes[:1])
		firstSize, err := measureTextLength(lengthFunc, first)
		if err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf(
			"indivisible rune %q has length %d, exceeds chunk size %d",
			first,
			firstSize,
			maxSize,
		)
	}

	prefix := strings.TrimSpace(string(contentRunes[:best]))
	prefixSize, err := measureTextLength(lengthFunc, prefix)
	if err != nil {
		return "", "", err
	}
	if prefix == "" || prefixSize > maxSize {
		return "", "", fmt.Errorf(
			"unable to split text within chunk size %d",
			maxSize,
		)
	}
	remaining := strings.TrimSpace(string(contentRunes[best:]))
	return prefix, remaining, nil
}

func estimateTextSplitByLength(
	content []rune,
	maxSize int,
	lengthFunc func(string) (int, error),
) (int, int, bool, error) {
	position := min(len(content), max(1, maxSize))
	for {
		prefix := strings.TrimSpace(string(content[:position]))
		prefixSize, err := measureTextLength(lengthFunc, prefix)
		if err != nil {
			return 0, 0, false, err
		}
		if prefixSize > maxSize {
			estimate := position
			if prefixSize > 0 {
				estimate = max(1, position*maxSize/prefixSize)
			}
			searchEnd := min(len(content)-1, position+32)
			return estimate, searchEnd, false, nil
		}
		if position == len(content) {
			return position, position - 1, true, nil
		}
		position = min(len(content), position*2)
	}
}

func findTextSplitPositionByLength(
	content []rune,
	candidates []int,
	maxSize int,
	lengthFunc func(string) (int, error),
) (int, error) {
	bestHardPosition := 0
	bestNaturalPosition := 0
	bestNaturalTier := 0
	minimumNaturalSize := max(1, maxSize/2)
	for _, position := range candidates {
		prefix := strings.TrimSpace(string(content[:position]))
		if prefix == "" {
			continue
		}
		prefixSize, err := measureTextLength(lengthFunc, prefix)
		if err != nil {
			return 0, err
		}
		if prefixSize <= 0 || prefixSize > maxSize {
			continue
		}
		if position > bestHardPosition {
			bestHardPosition = position
		}
		tier := textBoundaryTier(content, position)
		if tier > 0 && prefixSize >= minimumNaturalSize &&
			(tier > bestNaturalTier ||
				(tier == bestNaturalTier &&
					position > bestNaturalPosition)) {
			bestNaturalTier = tier
			bestNaturalPosition = position
		}
	}

	best := bestNaturalPosition
	if best == 0 {
		best = bestHardPosition
	}
	return best, nil
}

// lengthBoundaryCandidates returns a bounded set of rune positions around an
// estimated split. Length functions such as BPE tokenizers are not guaranteed
// to be monotonic for every prefix, so callers measure every returned complete
// candidate instead of relying on binary search.
func lengthBoundaryCandidates(content []rune, estimate int) []int {
	if len(content) <= 1 {
		return nil
	}
	estimate = max(1, min(estimate, len(content)-1))
	seen := make(map[int]struct{})
	candidates := make([]int, 0, 64)
	add := func(position int) {
		position = max(1, min(position, len(content)-1))
		position = safeTextSplitPosition(content, position)
		if _, ok := seen[position]; ok {
			return
		}
		seen[position] = struct{}{}
		candidates = append(candidates, position)
	}

	add(estimate)
	add(1)
	add(len(content) - 1)
	for _, offset := range []int{1, 2, 4, 8, 16, 32} {
		add(estimate - offset)
		add(estimate + offset)
	}
	for position := estimate; position > 1; {
		next := max(1, position/2)
		add(next)
		if next == position {
			break
		}
		position = next
	}
	for position := estimate; position < len(content)-1; {
		next := position + max(1, (len(content)-position)/2)
		add(next)
		if next == position || next >= len(content)-1 {
			break
		}
		position = next
	}

	var before [5][]int
	var after [5][]int
	for position := 1; position < len(content); position++ {
		tier := textBoundaryTier(content, position)
		if tier == 0 {
			continue
		}
		if position <= estimate {
			before[tier] = append(before[tier], position)
			if len(before[tier]) > 4 {
				before[tier] = before[tier][1:]
			}
			continue
		}
		if len(after[tier]) < 4 {
			after[tier] = append(after[tier], position)
		}
	}
	for tier := 4; tier >= 1; tier-- {
		for _, position := range before[tier] {
			add(position)
		}
		for _, position := range after[tier] {
			add(position)
		}
	}
	return candidates
}

func textBoundaryTier(content []rune, position int) int {
	if position <= 0 || position >= len(content) {
		return 0
	}
	previous := content[position-1]
	switch {
	case previous == '\n':
		return 4
	case isSentenceBoundary(content, position-1):
		return 3
	case strings.ContainsRune(",，;；:：、", previous):
		return 2
	case unicode.IsSpace(previous):
		return 1
	default:
		return 0
	}
}

// splitTextWithBalancedTail avoids leaving a very small final piece when one
// logical block needs multiple chunks. It first keeps the splitter's normal
// boundary choice. Only when that choice would leave less than half a chunk
// does it move the boundary earlier, without crossing the size budget.
func splitTextWithBalancedTail(
	content string,
	maxSize int,
	split func(string, int) (string, string),
) (string, string) {
	prefix, remaining := split(content, maxSize)
	if remaining == "" || maxSize <= 1 {
		return prefix, remaining
	}

	minimumSize := max(1, maxSize/2)
	if encoding.RuneCount(remaining) >= minimumSize {
		return prefix, remaining
	}

	contentSize := encoding.RuneCount(strings.TrimSpace(content))
	balancedLimit := contentSize - minimumSize
	if balancedLimit <= 0 || balancedLimit >= maxSize {
		return prefix, remaining
	}
	balancedPrefix, balancedRemaining := split(content, balancedLimit)
	minimumNaturalSize := max(1, maxSize*2/5)
	contentRunes := []rune(strings.TrimSpace(content))
	balancedPrefixSize := encoding.RuneCount(balancedPrefix)
	balancedRemainingSize := encoding.RuneCount(balancedRemaining)
	balancedStart := len(contentRunes) - balancedRemainingSize
	if balancedPrefixSize >= minimumNaturalSize &&
		balancedRemainingSize >= minimumNaturalSize &&
		isNaturalTextStart(contentRunes, balancedStart) {
		return balancedPrefix, balancedRemaining
	}

	// The balanced target can fall just before a nearby natural boundary, for
	// example before the closing delimiter of a Markdown table row. Prefer the
	// next boundary that still fits instead of hard-splitting the row.
	maxPosition := min(maxSize, len(contentRunes)-1)
	for position := max(balancedLimit+1, 1); position <= maxPosition; position++ {
		if !isNaturalTextStart(contentRunes, position) {
			continue
		}
		naturalPrefix := strings.TrimSpace(string(contentRunes[:position]))
		naturalRemaining := strings.TrimSpace(string(contentRunes[position:]))
		if encoding.RuneCount(naturalPrefix) < minimumNaturalSize ||
			encoding.RuneCount(naturalRemaining) < minimumNaturalSize {
			continue
		}
		return naturalPrefix, naturalRemaining
	}

	for position := min(balancedLimit, len(contentRunes)-1); position >= minimumSize; position-- {
		position = safeTextSplitPosition(contentRunes, position)
		hardPrefix := strings.TrimSpace(
			string(contentRunes[:position]),
		)
		hardRemaining := strings.TrimSpace(
			string(contentRunes[position:]),
		)
		if encoding.RuneCount(hardPrefix) < minimumSize ||
			encoding.RuneCount(hardRemaining) < minimumSize {
			continue
		}
		return hardPrefix, hardRemaining
	}
	return prefix, remaining
}

func splitTextWithBalancedTailByLength(
	content string,
	maxSize int,
	lengthFunc func(string) (int, error),
) (string, string, error) {
	prefix, remaining, err := splitTextAtNaturalBoundaryByLength(
		content,
		maxSize,
		lengthFunc,
	)
	if err != nil || remaining == "" || maxSize <= 1 {
		return prefix, remaining, err
	}

	largeEnough, err := textLengthAtLeastByLength(
		remaining,
		max(1, maxSize/2),
		lengthFunc,
	)
	if err != nil {
		return "", "", err
	}
	if largeEnough {
		return prefix, remaining, nil
	}

	contentRunes := []rune(strings.TrimSpace(content))
	candidates := lengthBoundaryCandidates(contentRunes, len(contentRunes)/2)
	minimumNaturalSize := max(1, maxSize*2/5)
	bestPosition := 0
	bestTier := -1
	bestDifference := int(^uint(0) >> 1)
	for _, position := range candidates {
		candidatePrefix := strings.TrimSpace(
			string(contentRunes[:position]),
		)
		candidateRemaining := strings.TrimSpace(
			string(contentRunes[position:]),
		)
		if candidatePrefix == "" || candidateRemaining == "" {
			continue
		}
		prefixSize, err := measureTextLength(
			lengthFunc,
			candidatePrefix,
		)
		if err != nil {
			return "", "", err
		}
		remainingSize, err := measureTextLength(
			lengthFunc,
			candidateRemaining,
		)
		if err != nil {
			return "", "", err
		}
		if prefixSize > maxSize ||
			prefixSize < minimumNaturalSize ||
			remainingSize < minimumNaturalSize {
			continue
		}
		tier := textBoundaryTier(contentRunes, position)
		difference := absInt(prefixSize - remainingSize)
		if tier > bestTier ||
			(tier == bestTier && difference < bestDifference) {
			bestPosition = position
			bestTier = tier
			bestDifference = difference
		}
	}
	if bestPosition > 0 {
		return strings.TrimSpace(string(contentRunes[:bestPosition])),
			strings.TrimSpace(string(contentRunes[bestPosition:])), nil
	}
	return prefix, remaining, nil
}

func textLengthAtLeastByLength(
	content string,
	minimum int,
	lengthFunc func(string) (int, error),
) (bool, error) {
	contentRunes := []rune(strings.TrimSpace(content))
	if len(contentRunes) == 0 {
		return false, nil
	}
	position := min(len(contentRunes), max(1, minimum))
	for {
		prefix := strings.TrimSpace(string(contentRunes[:position]))
		prefixSize, err := measureTextLength(lengthFunc, prefix)
		if err != nil {
			return false, err
		}
		if prefixSize >= minimum {
			return true, nil
		}
		if position == len(contentRunes) {
			return false, nil
		}
		position = min(len(contentRunes), position*2)
	}
}

func preferredTextBoundary(content []rune, maxSize int) int {
	minPreferredSize := max(1, maxSize/2)
	lineBoundary := -1
	sentenceBoundary := -1
	punctuationBoundary := -1
	whitespaceBoundary := -1

	for i := 0; i < maxSize && i < len(content); i++ {
		current := content[i]
		switch {
		case current == '\n':
			lineBoundary = i + 1
		case isSentenceBoundary(content, i):
			sentenceBoundary = i + 1
		case strings.ContainsRune(",，;；:：、", current):
			punctuationBoundary = i + 1
		case unicode.IsSpace(current):
			whitespaceBoundary = i
		}
	}

	for _, boundary := range []int{
		lineBoundary,
		sentenceBoundary,
		punctuationBoundary,
		whitespaceBoundary,
	} {
		if boundary >= minPreferredSize {
			return boundary
		}
	}
	return safeTextSplitPosition(content, min(maxSize, len(content)))
}

func isSentenceBoundary(content []rune, position int) bool {
	if position+1 < len(content) &&
		isSentencePunctuation(content[position+1]) {
		return false
	}
	switch content[position] {
	case '。', '！', '？':
		return true
	case '.', '!', '?':
		return position+1 == len(content) ||
			unicode.IsSpace(content[position+1])
	default:
		return false
	}
}

func isSentencePunctuation(r rune) bool {
	return strings.ContainsRune(".!?。！？", r)
}

func safeTextSplitPosition(content []rune, position int) int {
	if position <= 0 || position >= len(content) {
		return position
	}
	originalPosition := position
	for position > 0 &&
		isSentencePunctuation(content[position-1]) &&
		isSentencePunctuation(content[position]) {
		position--
	}
	if position == 0 {
		return originalPosition
	}
	return position
}

// naturalTextSuffix returns at most maxSize trailing runes. When the exact
// start would cut through a word, it advances to the next natural boundary.
// An unbroken token still uses an exact rune suffix so overlap remains useful.
func naturalTextSuffix(content string, maxSize int) (string, bool) {
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) == 0 {
		return "", false
	}
	if len(contentRunes) <= maxSize {
		return content, true
	}

	start := len(contentRunes) - maxSize
	if isNaturalTextStart(contentRunes, start) {
		return strings.TrimLeftFunc(
			string(contentRunes[start:]),
			unicode.IsSpace,
		), true
	}
	for candidate := start + 1; candidate < len(contentRunes); candidate++ {
		if isNaturalTextStart(contentRunes, candidate) {
			return strings.TrimLeftFunc(
				string(contentRunes[candidate:]),
				unicode.IsSpace,
			), true
		}
	}
	return string(contentRunes[start:]), false
}

func isNaturalTextStart(content []rune, position int) bool {
	if position <= 0 {
		return true
	}
	previous := content[position-1]
	return unicode.IsSpace(previous) ||
		isSentenceBoundary(content, position-1) ||
		strings.ContainsRune(",，;；:：、", previous)
}
