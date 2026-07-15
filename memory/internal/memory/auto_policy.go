//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	conservativeOldCoverage       = 0.95
	conservativeNewCoverage       = 0.70
	maxAutoMemorySearchQueryBytes = 7 * 1024
	searchQueryOmissionMarker     = "\n...\n"
)

var (
	criticalValuePattern = regexp.MustCompile(
		`(?i)\b[0-9]+(?:[.:/-][0-9]+)*\b|(?:\bnot\b|\bno\b|\bnever\b|\bwithout\b|n't|不再|不是|没有|从未|未|无)`,
	)
	changeMarkerPattern = regexp.MustCompile(
		`(?i)(?:\bnow\b|\bcurrently\b|\bno longer\b|\binstead\b|\bchanged?\b|\bused to\b|现在|目前|不再|改为|变成|而是|曾经)`,
	)
	negationPattern = regexp.MustCompile(
		`(?i)(?:\bnot\b|\bno\b|\bnever\b|\bwithout\b|n't|不再|不是|没有|从未|未|无)`,
	)
	capitalizedTokenPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]*\b`)
)

type conservativeCandidate struct {
	entry       *memory.Entry
	duplicate   bool
	oldCoverage float64
	newCoverage float64
}

func updatePolicyFor(ext extractor.MemoryExtractor) extractor.UpdatePolicy {
	provider, ok := ext.(extractor.UpdatePolicyProvider)
	if !ok {
		return extractor.UpdatePolicyLegacy
	}
	switch provider.UpdatePolicy() {
	case extractor.UpdatePolicyConservative, extractor.UpdatePolicyDisabled:
		return provider.UpdatePolicy()
	default:
		return extractor.UpdatePolicyLegacy
	}
}

func (w *AutoMemoryWorker) applyUpdatePolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	switch updatePolicyFor(w.config.Extractor) {
	case extractor.UpdatePolicyConservative:
		return w.reconcileConservativeOps(ctx, userKey, ops, existing)
	case extractor.UpdatePolicyDisabled:
		return w.disableExtractedUpdates(ctx, userKey, ops)
	default:
		return w.reconcileOps(ctx, userKey, ops)
	}
}

func (w *AutoMemoryWorker) disableExtractedUpdates(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
) []*extractor.Operation {
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		if op.Type != extractor.OperationUpdate {
			out = append(out, op)
			continue
		}
		add := *op
		add.Type = extractor.OperationAdd
		add.MemoryID = ""
		log.DebugfContext(ctx,
			"auto_memory: update policy disabled; converting update to add for user %s/%s",
			userKey.AppName, userKey.UserID,
		)
		out = append(out, &add)
	}
	return out
}

func (w *AutoMemoryWorker) reconcileConservativeOps(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	byID := make(map[string]*memory.Entry, len(existing))
	for _, entry := range existing {
		if entry != nil && entry.Memory != nil && entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.Type {
		case extractor.OperationAdd:
			out = appendConservativeAdd(ctx, w, userKey, out, op, existing)
		case extractor.OperationUpdate:
			out = appendConservativeUpdate(ctx, w, userKey, out, op, byID[op.MemoryID])
		default:
			out = append(out, op)
		}
	}
	return out
}

func appendConservativeAdd(
	ctx context.Context,
	w *AutoMemoryWorker,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if !w.isToolEnabled(memory.AddToolName) {
		return append(out, op)
	}
	match := selectConservativeCandidate(op, existing)
	if match == nil {
		logConservativeDecision(ctx, userKey, op, nil, "add", "no safe candidate")
		return append(out, op)
	}
	if match.duplicate {
		logConservativeDecision(ctx, userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.UpdateToolName) {
		logConservativeDecision(ctx, userKey, op, match, "add", "update tool disabled")
		return append(out, op)
	}
	updated := toUpdateOp(op, match.entry)
	logConservativeDecision(ctx, userKey, op, match, "update", "strict enrichment")
	return append(out, updated)
}

func appendConservativeUpdate(
	ctx context.Context,
	w *AutoMemoryWorker,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing *memory.Entry,
) []*extractor.Operation {
	match := classifyConservativeCandidate(op, existing)
	if match != nil && match.duplicate {
		logConservativeDecision(ctx, userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if match != nil && w.isToolEnabled(memory.UpdateToolName) {
		updated := toUpdateOp(op, existing)
		logConservativeDecision(ctx, userKey, op, match, "update", "strict enrichment")
		return append(out, updated)
	}
	add := *op
	add.Type = extractor.OperationAdd
	add.MemoryID = ""
	logConservativeDecision(ctx, userKey, op, match, "add", "unsafe or unknown update target")
	return append(out, &add)
}

func selectConservativeCandidate(
	op *extractor.Operation,
	existing []*memory.Entry,
) *conservativeCandidate {
	var best *conservativeCandidate
	for _, entry := range existing {
		candidate := classifyConservativeCandidate(op, entry)
		if candidate == nil {
			continue
		}
		if best == nil || conservativeCandidateLess(best, candidate) {
			best = candidate
		}
	}
	return best
}

func conservativeCandidateLess(left, right *conservativeCandidate) bool {
	if left.duplicate != right.duplicate {
		return right.duplicate
	}
	if left.oldCoverage != right.oldCoverage {
		return left.oldCoverage < right.oldCoverage
	}
	if left.newCoverage != right.newCoverage {
		return left.newCoverage < right.newCoverage
	}
	return left.entry.Score < right.entry.Score
}

func classifyConservativeCandidate(
	op *extractor.Operation,
	entry *memory.Entry,
) *conservativeCandidate {
	if op == nil || entry == nil || entry.Memory == nil || entry.ID == "" {
		return nil
	}
	if exactMemoryDuplicate(op, entry.Memory) {
		return &conservativeCandidate{
			entry:       entry,
			duplicate:   true,
			oldCoverage: 1,
			newCoverage: 1,
		}
	}
	if !metadataIdentityCompatible(op, entry.Memory) {
		return nil
	}
	oldCoverage, newCoverage := directionalTokenCoverage(entry.Memory.Memory, op.Memory)
	if oldCoverage < conservativeOldCoverage || newCoverage < conservativeNewCoverage {
		return nil
	}
	if !materialTokensPreserved(entry.Memory.Memory, op.Memory) {
		return nil
	}
	if !criticalValuesPreserved(entry.Memory.Memory, op.Memory) {
		return nil
	}
	if negationSignature(entry.Memory.Memory) != negationSignature(op.Memory) {
		return nil
	}
	if changeMarkerPattern.MatchString(op.Memory) && !changeMarkerPattern.MatchString(entry.Memory.Memory) {
		return nil
	}
	return &conservativeCandidate{
		entry:       entry,
		oldCoverage: oldCoverage,
		newCoverage: newCoverage,
	}
}

func materialTokensPreserved(oldText, newText string) bool {
	oldTokens := append(
		BuildSearchTokens(oldText),
		capitalizedTokenPattern.FindAllString(oldText, -1)...,
	)
	newTokens := stringSet(append(
		BuildSearchTokens(newText),
		capitalizedTokenPattern.FindAllString(newText, -1)...,
	))
	for token := range stringSet(oldTokens) {
		if _, ok := newTokens[token]; !ok {
			return false
		}
	}
	return true
}

func exactMemoryDuplicate(op *extractor.Operation, stored *memory.Memory) bool {
	if normalizeMemoryText(op.Memory) != normalizeMemoryText(stored.Memory) {
		return false
	}
	if operationKind(op) != EffectiveKind(stored) {
		return false
	}
	if !equalOptionalTime(op.EventTime, stored.EventTime) {
		return false
	}
	if !equalStringSet(op.Participants, stored.Participants) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(op.Location), strings.TrimSpace(stored.Location))
}

func metadataIdentityCompatible(op *extractor.Operation, stored *memory.Memory) bool {
	if operationKind(op) != EffectiveKind(stored) {
		return false
	}
	if !eventTimeCompatible(stored.EventTime, op.EventTime) {
		return false
	}
	if len(stored.Participants) > 0 && len(op.Participants) > 0 &&
		!isStringSubset(stored.Participants, op.Participants) {
		return false
	}
	if stored.Location != "" && op.Location != "" &&
		!strings.EqualFold(strings.TrimSpace(stored.Location), strings.TrimSpace(op.Location)) {
		return false
	}
	return true
}

func operationKind(op *extractor.Operation) memory.Kind {
	if op.MemoryKind == "" {
		return memory.KindFact
	}
	return op.MemoryKind
}

func eventTimeCompatible(stored, fresh *time.Time) bool {
	if stored == nil || fresh == nil || stored.Equal(*fresh) {
		return true
	}
	storedUTC := stored.UTC()
	freshUTC := fresh.UTC()
	if storedUTC.Year() != freshUTC.Year() ||
		storedUTC.YearDay() != freshUTC.YearDay() {
		return false
	}
	return isMidnight(storedUTC) && !isMidnight(freshUTC)
}

func isMidnight(value time.Time) bool {
	return value.Hour() == 0 && value.Minute() == 0 &&
		value.Second() == 0 && value.Nanosecond() == 0
}

func directionalTokenCoverage(oldText, newText string) (float64, float64) {
	oldTokens := textTokenSet(oldText)
	newTokens := textTokenSet(newText)
	if len(oldTokens) == 0 || len(newTokens) == 0 {
		return 0, 0
	}
	intersection := 0
	for token := range oldTokens {
		if _, ok := newTokens[token]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(oldTokens)),
		float64(intersection) / float64(len(newTokens))
}

func criticalValuesPreserved(oldText, newText string) bool {
	newValues := stringSet(criticalValuePattern.FindAllString(strings.ToLower(newText), -1))
	for value := range stringSet(criticalValuePattern.FindAllString(strings.ToLower(oldText), -1)) {
		if _, ok := newValues[value]; !ok {
			return false
		}
	}
	return true
}

func negationSignature(text string) string {
	values := negationPattern.FindAllString(strings.ToLower(text), -1)
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	sort.Strings(values)
	return strings.Join(values, "|")
}

func normalizeMemoryText(value string) string {
	var normalized strings.Builder
	spacePending := false
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			spacePending = normalized.Len() > 0
			continue
		}
		if spacePending {
			normalized.WriteByte(' ')
			spacePending = false
		}
		normalized.WriteRune(unicode.ToLower(r))
	}
	return normalized.String()
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalStringSet(left, right []string) bool {
	leftSet := stringSet(left)
	rightSet := stringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if _, ok := rightSet[value]; !ok {
			return false
		}
	}
	return true
}

func isStringSubset(subset, values []string) bool {
	valueSet := stringSet(values)
	for value := range stringSet(subset) {
		if _, ok := valueSet[value]; !ok {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

// buildPolicySearchQuery includes both conversational roles and bounds the
// derived retrieval query. It is used only by opt-in update policies so the
// legacy query remains byte-for-byte compatible.
func buildPolicySearchQuery(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
			continue
		}
		if msg.ToolID != "" || len(msg.ToolCalls) > 0 {
			continue
		}
		text := messageSearchText(msg)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return limitSearchQuery(strings.Join(parts, " "))
}

func limitSearchQuery(query string) string {
	if len(query) <= maxAutoMemorySearchQueryBytes {
		return query
	}
	contentBudget := maxAutoMemorySearchQueryBytes - len(searchQueryOmissionMarker)
	prefixBudget := contentBudget / 2
	suffixBudget := contentBudget - prefixBudget
	prefixEnd := utf8PrefixBoundary(query, prefixBudget)
	suffixStart := utf8SuffixBoundary(query, len(query)-suffixBudget)
	return strings.TrimSpace(
		query[:prefixEnd] + searchQueryOmissionMarker + query[suffixStart:],
	)
}

func utf8PrefixBoundary(text string, limit int) int {
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return limit
}

func utf8SuffixBoundary(text string, start int) int {
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return start
}

func logConservativeDecision(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
	match *conservativeCandidate,
	action string,
	reason string,
) {
	if match == nil {
		log.DebugfContext(ctx,
			"auto_memory: conservative decision action=%s reason=%s user=%s/%s operation=%s",
			action, reason, userKey.AppName, userKey.UserID, op.Type,
		)
		return
	}
	log.DebugfContext(ctx,
		"auto_memory: conservative decision action=%s reason=%s user=%s/%s operation=%s candidate=%s old_coverage=%.3f new_coverage=%.3f",
		action, reason, userKey.AppName, userKey.UserID, op.Type,
		match.entry.ID, match.oldCoverage, match.newCoverage,
	)
}
