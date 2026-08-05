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
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/updatepolicy"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	preserveHistoryOldCoverage    = 0.95
	preserveHistoryNewCoverage    = 0.70
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
	capitalizedTokenPattern           = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]*\b`)
	negatedDestructiveRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(
			`(?i)\b(?:do\s+not|don't|dont|never|should\s+not|shouldn't)\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?i)\b(?:do\s+not|don't|dont)\s+(?:want|need)\s+(?:me\s+|you\s+)?to\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(`(?:不要|别|请勿|不必|不应该)(?:再)?(?:忘记|删除|移除|清除|清空)`),
	}
	explicitDestructiveRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?im)^\s*(?:please\s+)?(?:forget|delete|remove|erase|clear)\b`),
		regexp.MustCompile(`(?i)\bplease\s+(?:forget|delete|remove|erase|clear)\b`),
		regexp.MustCompile(
			`(?i)\b(?:can|could|would|will)\s+you\s+(?:please\s+)?(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?i)\bi\s+(?:want|need|would\s+like)\s+you\s+to\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?:^|[\n。！？!?])\s*(?:(?:请|麻烦)(?:你)?|帮我)?(?:忘记|删除|移除|清除|清空)`,
		),
		regexp.MustCompile(`(?:我(?:想|希望|要求)(?:让)?你|请你|麻烦你|帮我)(?:忘记|删除|移除|清除|清空)`),
	}
	explicitClearAllRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bforget\s+(?:absolutely\s+)?everything\b`),
		regexp.MustCompile(
			`(?i)\b(?:delete|remove|erase|clear)\s+(?:(?:all(?:\s+of)?\s+)(?:my\s+)?(?:memories|memory|data|information)|(?:my\s+)?memories|everything)\b`,
		),
		regexp.MustCompile(`忘记.{0,8}(?:关于我(?:的)?(?:一切|全部|所有)|(?:一切|全部|所有)(?:记忆|信息|数据)?)`),
		regexp.MustCompile(`(?:删除|移除|清除|清空).{0,8}(?:(?:全部|所有)(?:的)?(?:记忆|信息|数据)|记忆库)`),
		regexp.MustCompile(`清空.{0,4}(?:记忆|信息|数据)`),
	}
	partialClearRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:except|excluding|other\s+than|but\s+keep)\b`),
		regexp.MustCompile(`(?:除了|除去|排除).{0,12}(?:以外|之外|外)?|(?:但是|但要|保留).{0,12}`),
	}
	destructiveRequestGenericTokens = stringSet([]string{
		"a", "absolutely", "about", "all", "an", "and", "any", "anything", "can", "clear", "completely",
		"could", "data", "delete", "detail", "details", "entirely", "erase", "ever", "everything",
		"fact", "facts", "forget", "from", "have", "i", "information", "know", "memory", "memories",
		"me", "my", "need", "or", "please", "regarding", "related", "remove", "said", "shared",
		"should", "stored", "the", "to", "told", "want", "will", "would", "you",
		"全部", "关于", "记忆", "请", "清除", "清空", "删除", "数据", "所有", "忘记",
		"帮我", "麻烦", "我的", "我", "希望", "信息", "移除", "细节",
	})
)

type preserveHistoryCandidate struct {
	entry       *memory.Entry
	duplicate   bool
	oldCoverage float64
	newCoverage float64
}

type destructiveRequest struct {
	text     string
	explicit bool
	clearAll bool
	partial  bool
}

func updatePolicyFor(ext extractor.MemoryExtractor) extractor.UpdatePolicy {
	policy := extractor.UpdatePolicy(updatepolicy.From(ext))
	switch policy {
	case extractor.UpdatePolicyPreserveHistory,
		extractor.UpdatePolicyAppendOnly:
		return policy
	default:
		return extractor.UpdatePolicyMergeSimilar
	}
}

func (w *AutoMemoryWorker) applyUpdatePolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
	messages []model.Message,
) []*extractor.Operation {
	switch w.updatePolicy {
	case extractor.UpdatePolicyPreserveHistory:
		return w.reconcilePreserveHistoryOps(ctx, userKey, ops, existing, messages)
	case extractor.UpdatePolicyAppendOnly:
		return w.applyAppendOnlyPolicy(ctx, userKey, ops, existing)
	default:
		return w.reconcileOps(ctx, userKey, ops)
	}
}

func (w *AutoMemoryWorker) applyAppendOnlyPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		var add *extractor.Operation
		switch op.Type {
		case extractor.OperationAdd:
			add = op
		case extractor.OperationUpdate:
			converted := *op
			converted.Type = extractor.OperationAdd
			converted.MemoryID = ""
			add = &converted
			log.DebugfContext(ctx,
				"auto_memory: append_only policy; converting update to add for user %s/%s",
				userKey.AppName, userKey.UserID,
			)
		default:
			log.DebugfContext(ctx,
				"auto_memory: append_only policy; filtering %s operation for user %s/%s",
				op.Type, userKey.AppName, userKey.UserID,
			)
			continue
		}
		if hasExactMemoryDuplicate(add, existing, out) {
			log.DebugfContext(ctx,
				"auto_memory: append_only policy; filtering exact duplicate for user %s/%s",
				userKey.AppName, userKey.UserID,
			)
			continue
		}
		out = append(out, add)
	}
	return out
}

func (w *AutoMemoryWorker) reconcilePreserveHistoryOps(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
	messages []model.Message,
) []*extractor.Operation {
	request := latestExplicitDestructiveRequest(messages)
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
			out = appendPreserveHistoryAdd(ctx, w, userKey, out, op, existing)
		case extractor.OperationUpdate:
			out = appendPreserveHistoryUpdate(ctx, w, userKey, out, op, byID[op.MemoryID])
		case extractor.OperationDelete:
			if request.authorizesDelete(byID[op.MemoryID]) {
				out = append(out, op)
				continue
			}
			logPreserveHistoryDestructiveRejection(ctx, userKey, op)
		case extractor.OperationClear:
			if request.clearAll && !request.partial {
				out = append(out, op)
				continue
			}
			logPreserveHistoryDestructiveRejection(ctx, userKey, op)
		default:
			out = append(out, op)
		}
	}
	return out
}

func hasExactMemoryDuplicate(
	op *extractor.Operation,
	existing []*memory.Entry,
	accepted []*extractor.Operation,
) bool {
	for _, entry := range existing {
		if entry != nil && entry.Memory != nil && exactMemoryDuplicate(op, entry.Memory) {
			return true
		}
	}
	for _, candidate := range accepted {
		if candidate != nil && exactMemoryDuplicate(op, operationMemory(candidate)) {
			return true
		}
	}
	return false
}

func operationMemory(op *extractor.Operation) *memory.Memory {
	return &memory.Memory{
		Memory:       op.Memory,
		Topics:       op.Topics,
		Kind:         operationKind(op),
		EventTime:    op.EventTime,
		Participants: op.Participants,
		Location:     op.Location,
	}
}

func latestExplicitDestructiveRequest(messages []model.Message) destructiveRequest {
	for index := len(messages) - 1; index >= 0; index-- {
		msg := messages[index]
		if msg.Role != model.RoleUser {
			continue
		}
		text := messageSearchText(msg)
		negated := false
		for _, pattern := range negatedDestructiveRequestPatterns {
			negated = negated || pattern.MatchString(text)
			text = pattern.ReplaceAllString(text, "")
		}
		explicit := matchesAnyPattern(text, explicitDestructiveRequestPatterns)
		if !explicit && !negated {
			continue
		}
		if !explicit {
			return destructiveRequest{}
		}
		clearAll := matchesAnyPattern(text, explicitClearAllRequestPatterns) &&
			len(destructiveTargetTokens(text)) == 0
		return destructiveRequest{
			text:     text,
			explicit: true,
			clearAll: clearAll,
			partial:  matchesAnyPattern(text, partialClearRequestPatterns),
		}
	}
	return destructiveRequest{}
}

func (r destructiveRequest) authorizesDelete(entry *memory.Entry) bool {
	if !r.explicit || r.partial {
		return false
	}
	if r.clearAll {
		return true
	}
	if entry == nil || entry.Memory == nil {
		return false
	}
	targetTokens := destructiveTargetTokens(r.text)
	if len(targetTokens) == 0 {
		return false
	}
	entryText := entry.Memory.Memory + " " + strings.Join(entry.Memory.Topics, " ")
	entryTokens := stringSet(BuildSearchTokens(entryText))
	for token := range targetTokens {
		if _, ok := entryTokens[token]; !ok {
			return false
		}
	}
	return true
}

func destructiveTargetTokens(text string) map[string]struct{} {
	tokens := stringSet(BuildSearchTokens(text))
	for token := range destructiveRequestGenericTokens {
		delete(tokens, token)
	}
	return tokens
}

func matchesAnyPattern(text string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func logPreserveHistoryDestructiveRejection(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
) {
	log.DebugfContext(ctx,
		"auto_memory: preserve_history policy; filtering %s without a matching explicit user request for user %s/%s",
		op.Type, userKey.AppName, userKey.UserID,
	)
}

func appendPreserveHistoryAdd(
	ctx context.Context,
	w *AutoMemoryWorker,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if hasExactMemoryDuplicate(op, existing, out) {
		logPreserveHistoryDecision(ctx, userKey, op, nil, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.AddToolName) {
		return append(out, op)
	}
	match := selectPreserveHistoryCandidate(op, existing)
	if match == nil {
		logPreserveHistoryDecision(ctx, userKey, op, nil, "add", "no safe candidate")
		return append(out, op)
	}
	if match.duplicate {
		logPreserveHistoryDecision(ctx, userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.UpdateToolName) {
		logPreserveHistoryDecision(ctx, userKey, op, match, "add", "update tool disabled")
		return append(out, op)
	}
	updated := toUpdateOp(op, match.entry)
	logPreserveHistoryDecision(ctx, userKey, op, match, "update", "safe enrichment")
	return append(out, updated)
}

func appendPreserveHistoryUpdate(
	ctx context.Context,
	w *AutoMemoryWorker,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing *memory.Entry,
) []*extractor.Operation {
	match := classifyPreserveHistoryCandidate(op, existing)
	if match != nil && match.duplicate {
		logPreserveHistoryDecision(ctx, userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if match != nil && w.isToolEnabled(memory.UpdateToolName) {
		updated := toUpdateOp(op, existing)
		logPreserveHistoryDecision(ctx, userKey, op, match, "update", "safe enrichment")
		return append(out, updated)
	}
	add := *op
	add.Type = extractor.OperationAdd
	add.MemoryID = ""
	logPreserveHistoryDecision(ctx, userKey, op, match, "add", "unsafe or unknown update target")
	return append(out, &add)
}

func selectPreserveHistoryCandidate(
	op *extractor.Operation,
	existing []*memory.Entry,
) *preserveHistoryCandidate {
	var best *preserveHistoryCandidate
	for _, entry := range existing {
		candidate := classifyPreserveHistoryCandidate(op, entry)
		if candidate == nil {
			continue
		}
		if best == nil || preserveHistoryCandidateLess(best, candidate) {
			best = candidate
		}
	}
	return best
}

func preserveHistoryCandidateLess(left, right *preserveHistoryCandidate) bool {
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

func classifyPreserveHistoryCandidate(
	op *extractor.Operation,
	entry *memory.Entry,
) *preserveHistoryCandidate {
	if op == nil || entry == nil || entry.Memory == nil || entry.ID == "" {
		return nil
	}
	if exactMemoryDuplicate(op, entry.Memory) {
		return &preserveHistoryCandidate{
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
	if oldCoverage < preserveHistoryOldCoverage || newCoverage < preserveHistoryNewCoverage {
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
	return &preserveHistoryCandidate{
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

// buildPolicySearchQuery includes both sides of the conversation because
// opt-in policies reconcile memories extracted from both user and assistant
// messages. Tool protocol messages are excluded, and the query is bounded to
// stay within embedding-provider input limits.
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

func logPreserveHistoryDecision(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
	match *preserveHistoryCandidate,
	action string,
	reason string,
) {
	if match == nil {
		log.DebugfContext(ctx,
			"auto_memory: preserve_history decision action=%s reason=%s user=%s/%s operation=%s",
			action, reason, userKey.AppName, userKey.UserID, op.Type,
		)
		return
	}
	log.DebugfContext(ctx,
		"auto_memory: preserve_history decision action=%s reason=%s user=%s/%s operation=%s candidate=%s old_coverage=%.3f new_coverage=%.3f",
		action, reason, userKey.AppName, userKey.UserID, op.Type,
		match.entry.ID, match.oldCoverage, match.newCoverage,
	)
}
