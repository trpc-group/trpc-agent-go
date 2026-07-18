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
	extractorMetadataUpdatePolicy     = "update_policy"
	extractorMetadataAssistantResults = "assistant_result_extraction"

	historyOldCoverage = 0.95
	historyNewCoverage = 0.70

	maxPolicySearchQueryBytes = 7 * 1024
	searchQueryOmissionMarker = "\n...\n"
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

type historyCandidate struct {
	entry       *memory.Entry
	duplicate   bool
	oldCoverage float64
	newCoverage float64
}

func extractionPoliciesFromMetadata(
	ext extractor.MemoryExtractor,
) (extractor.UpdatePolicy, bool) {
	if ext == nil {
		return extractor.UpdatePolicyReconcile, false
	}
	metadata := ext.Metadata()
	assistantResults, _ := metadata[extractorMetadataAssistantResults].(bool)
	raw := metadata[extractorMetadataUpdatePolicy]
	var policy extractor.UpdatePolicy
	switch value := raw.(type) {
	case extractor.UpdatePolicy:
		policy = value
	case string:
		policy = extractor.UpdatePolicy(value)
	}
	switch policy {
	case extractor.UpdatePolicyHistoryPreserving,
		extractor.UpdatePolicyAddOnly:
		return policy, assistantResults
	default:
		return extractor.UpdatePolicyReconcile, assistantResults
	}
}

func (w *AutoMemoryWorker) applyUpdatePolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	switch w.updatePolicy {
	case extractor.UpdatePolicyHistoryPreserving:
		return w.applyHistoryPreservingPolicy(ctx, userKey, ops, existing)
	case extractor.UpdatePolicyAddOnly:
		return w.applyAddOnlyPolicy(ctx, userKey, ops, existing)
	default:
		return w.reconcileOps(ctx, userKey, ops)
	}
}

func (w *AutoMemoryWorker) applyAssistantResultPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if len(ops) == 0 {
		return nil
	}
	if w.updatePolicy == extractor.UpdatePolicyAddOnly {
		return w.applyAddOnlyPolicy(ctx, userKey, ops, existing)
	}
	return w.applyHistoryPreservingPolicy(ctx, userKey, ops, existing)
}

func (w *AutoMemoryWorker) applyHistoryPreservingPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	byID := make(map[string]*memory.Entry, len(existing))
	for _, entry := range existing {
		if validMemoryEntry(entry) {
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
			out = w.appendHistoryAdd(ctx, userKey, out, op, existing)
		case extractor.OperationUpdate:
			out = w.appendHistoryUpdate(ctx, userKey, out, op, byID[op.MemoryID])
		default:
			out = append(out, op)
		}
	}
	return out
}

func (w *AutoMemoryWorker) appendHistoryAdd(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if !w.isToolEnabled(memory.AddToolName) {
		return append(out, op)
	}
	match := selectHistoryCandidate(op, existing)
	if match == nil {
		logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
			userKey, op, nil, "add", "no safe candidate")
		return append(out, op)
	}
	if match.duplicate {
		logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
			userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.UpdateToolName) {
		logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
			userKey, op, match, "add", "update tool disabled")
		return append(out, op)
	}
	logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
		userKey, op, match, "update", "strict enrichment")
	return append(out, toUpdateOp(op, match.entry))
}

func (w *AutoMemoryWorker) appendHistoryUpdate(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing *memory.Entry,
) []*extractor.Operation {
	match := classifyHistoryCandidate(op, existing)
	if match != nil && match.duplicate {
		logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
			userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if match != nil && w.isToolEnabled(memory.UpdateToolName) {
		logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
			userKey, op, match, "update", "strict enrichment")
		return append(out, toUpdateOp(op, existing))
	}
	add := asAddOperation(op)
	logPolicyDecision(ctx, extractor.UpdatePolicyHistoryPreserving,
		userKey, op, match, "add", "unsafe or unknown update")
	return append(out, add)
}

func (w *AutoMemoryWorker) applyAddOnlyPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	known := append([]*memory.Entry(nil), existing...)
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.Type {
		case extractor.OperationAdd, extractor.OperationUpdate:
			add := asAddOperation(op)
			if selectExactDuplicate(add, known) != nil {
				logPolicyDecision(ctx, extractor.UpdatePolicyAddOnly,
					userKey, op, nil, "no-op", "exact duplicate")
				continue
			}
			out = append(out, add)
			known = append(known, entryForOperation(add))
		default:
			logPolicyDecision(ctx, extractor.UpdatePolicyAddOnly,
				userKey, op, nil, "no-op", "add-only policy")
		}
	}
	return out
}

func selectHistoryCandidate(
	op *extractor.Operation,
	existing []*memory.Entry,
) *historyCandidate {
	var best *historyCandidate
	for _, entry := range existing {
		candidate := classifyHistoryCandidate(op, entry)
		if candidate == nil {
			continue
		}
		if best == nil || historyCandidateLess(best, candidate) {
			best = candidate
		}
	}
	return best
}

func historyCandidateLess(left, right *historyCandidate) bool {
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

func classifyHistoryCandidate(
	op *extractor.Operation,
	entry *memory.Entry,
) *historyCandidate {
	if op == nil || !validMemoryEntry(entry) {
		return nil
	}
	if exactMemoryDuplicate(op, entry.Memory) {
		return &historyCandidate{
			entry:       entry,
			duplicate:   true,
			oldCoverage: 1,
			newCoverage: 1,
		}
	}
	if !metadataIdentityCompatible(op, entry.Memory) {
		return nil
	}
	oldCoverage, newCoverage := directionalTokenCoverage(
		entry.Memory.Memory, op.Memory,
	)
	if oldCoverage < historyOldCoverage || newCoverage < historyNewCoverage {
		return nil
	}
	if !materialTokensPreserved(entry.Memory.Memory, op.Memory) ||
		!criticalValuesPreserved(entry.Memory.Memory, op.Memory) ||
		negationSignature(entry.Memory.Memory) != negationSignature(op.Memory) {
		return nil
	}
	if changeMarkerPattern.MatchString(op.Memory) &&
		!changeMarkerPattern.MatchString(entry.Memory.Memory) {
		return nil
	}
	return &historyCandidate{
		entry:       entry,
		oldCoverage: oldCoverage,
		newCoverage: newCoverage,
	}
}

func selectExactDuplicate(
	op *extractor.Operation,
	entries []*memory.Entry,
) *memory.Entry {
	for _, entry := range entries {
		if validMemoryEntry(entry) && exactMemoryDuplicate(op, entry.Memory) {
			return entry
		}
	}
	return nil
}

func validMemoryEntry(entry *memory.Entry) bool {
	return entry != nil && entry.ID != "" && entry.Memory != nil
}

func asAddOperation(op *extractor.Operation) *extractor.Operation {
	add := *op
	add.Type = extractor.OperationAdd
	add.MemoryID = ""
	return &add
}

func entryForOperation(op *extractor.Operation) *memory.Entry {
	return &memory.Entry{
		ID: "pending",
		Memory: &memory.Memory{
			Memory:       op.Memory,
			Topics:       op.Topics,
			Kind:         operationKind(op),
			EventTime:    op.EventTime,
			Participants: op.Participants,
			Location:     op.Location,
		},
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
	return normalizeMemoryText(op.Memory) == normalizeMemoryText(stored.Memory) &&
		operationKind(op) == EffectiveKind(stored) &&
		equalOptionalTime(op.EventTime, stored.EventTime) &&
		equalStringSet(op.Participants, stored.Participants) &&
		strings.EqualFold(strings.TrimSpace(op.Location), strings.TrimSpace(stored.Location))
}

func metadataIdentityCompatible(op *extractor.Operation, stored *memory.Memory) bool {
	if operationKind(op) != EffectiveKind(stored) ||
		!eventTimeCompatible(stored.EventTime, op.EventTime) {
		return false
	}
	if len(stored.Participants) > 0 && len(op.Participants) > 0 &&
		!isStringSubset(stored.Participants, op.Participants) {
		return false
	}
	return stored.Location == "" || op.Location == "" ||
		strings.EqualFold(strings.TrimSpace(stored.Location), strings.TrimSpace(op.Location))
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
	return storedUTC.Year() == freshUTC.Year() &&
		storedUTC.YearDay() == freshUTC.YearDay() &&
		isMidnight(storedUTC) && !isMidnight(freshUTC)
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
	newValues := stringSet(criticalValuePattern.FindAllString(
		strings.ToLower(newText), -1,
	))
	for value := range stringSet(criticalValuePattern.FindAllString(
		strings.ToLower(oldText), -1,
	)) {
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

// buildPolicySearchQuery gives opt-in policies and assistant-result extraction
// enough context to reconcile assistant-produced results while keeping the
// legacy user-only query intact.
func buildPolicySearchQuery(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
			continue
		}
		if msg.ToolID != "" || len(msg.ToolCalls) > 0 {
			continue
		}
		if text := messageSearchText(msg); text != "" {
			parts = append(parts, text)
		}
	}
	return limitPolicySearchQuery(strings.Join(parts, " "))
}

func limitPolicySearchQuery(query string) string {
	if len(query) <= maxPolicySearchQueryBytes {
		return query
	}
	contentBudget := maxPolicySearchQueryBytes - len(searchQueryOmissionMarker)
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

func logPolicyDecision(
	ctx context.Context,
	policy extractor.UpdatePolicy,
	userKey memory.UserKey,
	op *extractor.Operation,
	match *historyCandidate,
	action string,
	reason string,
) {
	if match == nil {
		log.DebugfContext(ctx,
			"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s",
			policy, action, reason,
			userKey.AppName, userKey.UserID, op.Type,
		)
		return
	}
	log.DebugfContext(ctx,
		"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s candidate=%s old_coverage=%.3f new_coverage=%.3f",
		policy, action, reason,
		userKey.AppName, userKey.UserID, op.Type, match.entry.ID,
		match.oldCoverage, match.newCoverage,
	)
}
