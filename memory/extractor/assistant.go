//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/assistantmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	metadataKeyConversationExtraction = assistantmemory.MetadataKey
	assistantEpisodeMetadataValue     = assistantmemory.MetadataValue
	assistantEpisodeToolName          = "memory_assistant_episode"
	assistantEpisodePairIDKey         = "pair_id"
	assistantEpisodeMaxBytes          = 4096
	assistantEpisodeSourceMaxBytes    = 8192
	// These private limits bound the optional request; overflow is best effort.
	assistantEpisodeRequestMaxPairs       = 32
	assistantEpisodeRequestMaxSourceBytes = 64 * 1024
	assistantEpisodeDeadlineReserve       = 5 * time.Second
	assistantEpisodePrefix                = assistantmemory.Prefix
	assistantEpisodeTruncationMarker      = "\n...[truncated]...\n"
	assistantEpisodeCurrencyPattern       = `[$€£¥]|USD|EUR|GBP|JPY|CNY|RMB`
	assistantEpisodeUnitPattern           = `percent(?:age)?|%|°[ \t]*(?:C|F|K)|` +
		`kilograms?|milligrams?|grams?|pounds?|` +
		`kilometers?|kilometres?|miles?|` +
		`centimeters?|centimetres?|millimeters?|millimetres?|` +
		`meters?|metres?|milliliters?|millilitres?|liters?|litres?|` +
		`hours?|minutes?|seconds?|days?|weeks?|months?|years?|` +
		`kgs?|mg|lbs?|km|cm|mm|ml|hrs?|mins?|min|secs?|mi|g|l|h|s|m`
)

var (
	assistantEpisodeListItemPattern = regexp.MustCompile(
		`(?m)^\s*(?:[-*•]\s+|\d{1,2}[.)]\s+)\S`,
	)
	assistantEpisodeStructuredRequestPattern = regexp.MustCompile(
		`(?i)\b(?:recommend(?:ation)?s?|suggest(?:ion)?s?|examples?|list|` +
			`options?|choices?|give\s+me|show\s+me|what\s+(?:are|were)|` +
			`extract|identify|predict|classif(?:y|ication)|summari[sz]e|` +
			`translate|convert)\b|` +
			`推荐|建议|示例|例子|列出|清单|选项|有哪些|提取|识别|分类|总结|翻译|转换`,
	)
	assistantEpisodeQuantityRequestPattern = regexp.MustCompile(
		`(?i)\b(?:how\s+(?:many|much|long|often)|` +
			`what\s+(?:number|amount|duration|percentage|percent))\b|` +
			`多少|几次|多久|多长|百分之`,
	)
	assistantEpisodeNumberPattern   = regexp.MustCompile(`\b\d+(?:[.,]\d+)*(?:%|\b)`)
	assistantEpisodeQuantityPattern = regexp.MustCompile(
		`(?i)([+\-−]?)(?:((?:` + assistantEpisodeCurrencyPattern + `))[ \t]*)?` +
			`([+\-−]?)([0-9]+(?:[.,][0-9]+)*)` +
			`(?:[ \t]*((?:` + assistantEpisodeCurrencyPattern + `)|` +
			`(?:` + assistantEpisodeUnitPattern + `)` +
			`(?:[ \t]*/[ \t]*(?:` + assistantEpisodeUnitPattern + `))?))?`,
	)
	assistantEpisodeUnitAliases = buildAssistantEpisodeUnitAliases()
)

func buildAssistantEpisodeUnitAliases() map[string]string {
	groups := map[string][]string{
		"%":     {"%", "percent", "percentage"},
		"°c":    {"°c"},
		"°f":    {"°f"},
		"°k":    {"°k"},
		"kg":    {"kg", "kgs", "kilogram", "kilograms"},
		"mg":    {"mg", "milligram", "milligrams"},
		"g":     {"g", "gram", "grams"},
		"lb":    {"lb", "lbs", "pound", "pounds"},
		"km":    {"km", "kilometer", "kilometers", "kilometre", "kilometres"},
		"mi":    {"mi", "mile", "miles"},
		"cm":    {"cm", "centimeter", "centimeters", "centimetre", "centimetres"},
		"mm":    {"mm", "millimeter", "millimeters", "millimetre", "millimetres"},
		"m":     {"m", "meter", "meters", "metre", "metres"},
		"ml":    {"ml", "milliliter", "milliliters", "millilitre", "millilitres"},
		"l":     {"l", "liter", "liters", "litre", "litres"},
		"h":     {"h", "hr", "hrs", "hour", "hours"},
		"min":   {"min", "mins", "minute", "minutes"},
		"s":     {"s", "sec", "secs", "second", "seconds"},
		"day":   {"day", "days"},
		"week":  {"week", "weeks"},
		"month": {"month", "months"},
		"year":  {"year", "years"},
	}
	aliases := make(map[string]string)
	for canonical, names := range groups {
		for _, name := range names {
			aliases[name] = canonical
		}
	}
	return aliases
}

type assistantEpisodePair struct {
	user      model.Message
	assistant model.Message
}

type assistantEpisodeSource struct {
	id            string
	userText      string
	assistantText string
}

type assistantEpisodeQuantity struct {
	sign     string
	value    string
	unit     string
	currency string
}

// WithAssistantEpisodeExtraction enables extraction of reusable assistant
// responses as ordinary episodic memory. The setting is fixed when the
// extractor is constructed. It does not add a memory kind or change storage
// schemas.
func WithAssistantEpisodeExtraction() Option {
	return func(e *memoryExtractor) {
		e.assistantEpisodeExtraction = true
	}
}

func (e *memoryExtractor) extractWithAssistantEpisodes(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*Operation, error) {
	if e.enabledTools != nil && len(e.enabledTools) == 0 {
		return nil, nil
	}
	ordinaryMessages := assistantEpisodeUserMessages(messages)
	var ordinaryOps []*Operation
	nextCtx := ctx
	if len(ordinaryMessages) > 0 {
		ordinaryExtractor := *e
		ordinaryExtractor.assistantEpisodeExtraction = false
		ordinaryTools := filterTools(
			backgroundTools,
			ordinaryExtractor.enabledTools,
		)
		if len(ordinaryTools) == 0 {
			return nil, nil
		}
		req := &model.Request{
			Messages: ordinaryExtractor.buildMessages(
				ctx,
				ordinaryMessages,
				withoutAssistantEpisodeEntries(existing),
			),
			Tools: ordinaryTools,
		}
		var err error
		nextCtx, err = ordinaryExtractor.runExtractionRequest(
			ctx,
			req,
			func(callCtx context.Context, call model.ToolCall) {
				if op := ordinaryExtractor.parseToolCall(callCtx, call); op != nil {
					ordinaryOps = append(ordinaryOps, op)
				}
			},
		)
		if err != nil {
			return nil, err
		}
	}

	if containsForgetOperation(ordinaryOps) || !e.assistantEpisodeAddEnabled() {
		return ordinaryOps, nil
	}
	pairs := selectAssistantEpisodePairs(messages)
	candidates := make([]assistantEpisodePair, 0, len(pairs))
	for _, pair := range pairs {
		if !strongAssistantEpisodeCandidate(
			assistantEpisodeMessageText(pair.user),
			assistantEpisodeMessageText(pair.assistant),
		) {
			continue
		}
		candidates = append(candidates, pair)
	}
	if len(candidates) == 0 {
		return ordinaryOps, nil
	}
	candidates, skipped := boundAssistantEpisodeCandidates(candidates)
	if skipped > 0 {
		log.WarnfContext(
			nextCtx,
			"extractor: assistant episode request budget skipped %d of %d candidates",
			skipped,
			skipped+len(candidates),
		)
	}

	assistantCallCtx, cancel := assistantEpisodeRequestContext(nextCtx)
	defer cancel()
	_, assistantOps, err := e.extractAssistantEpisodes(assistantCallCtx, candidates)
	if err != nil {
		if nextCtx.Err() != nil {
			return nil, fmt.Errorf("extract assistant episodes: %w", err)
		}
		log.WarnfContext(
			nextCtx,
			"extractor: optional assistant episode extraction failed: %v",
			err,
		)
		return ordinaryOps, nil
	}
	ordinaryOps = append(ordinaryOps, assistantOps...)
	return ordinaryOps, nil
}

func boundAssistantEpisodeCandidates(
	pairs []assistantEpisodePair,
) ([]assistantEpisodePair, int) {
	limit := min(len(pairs), assistantEpisodeRequestMaxPairs)
	bounded := make([]assistantEpisodePair, 0, limit)
	sourceBytes := 0
	for _, pair := range pairs[:limit] {
		pairBytes := len(assistantEpisodeSourceExcerpt(
			assistantEpisodeMessageText(pair.user),
		)) + len(assistantEpisodeSourceExcerpt(
			assistantEpisodeMessageText(pair.assistant),
		))
		if sourceBytes+pairBytes > assistantEpisodeRequestMaxSourceBytes {
			break
		}
		bounded = append(bounded, pair)
		sourceBytes += pairBytes
	}
	return bounded, len(pairs) - len(bounded)
}

func assistantEpisodeRequestContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.WithDeadline(ctx, deadline)
	}
	reserve := min(assistantEpisodeDeadlineReserve, remaining/2)
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}

func (e *memoryExtractor) extractAssistantEpisodes(
	ctx context.Context,
	pairs []assistantEpisodePair,
) (context.Context, []*Operation, error) {
	sources := make([]assistantEpisodeSource, 0, len(pairs))
	messages := make([]model.Message, 0, len(pairs)*2+2)
	messages = append(messages, model.NewSystemMessage(e.assistantEpisodePrompt(ctx)))
	for i, pair := range pairs {
		source := assistantEpisodeSource{
			id: fmt.Sprintf("pair-%d", i+1),
			userText: assistantEpisodeSourceExcerpt(
				assistantEpisodeMessageText(pair.user),
			),
			assistantText: assistantEpisodeSourceExcerpt(
				assistantEpisodeMessageText(pair.assistant),
			),
		}
		sources = append(sources, source)
		messages = append(
			messages,
			model.NewUserMessage(source.id+" user request:\n"+source.userText),
			model.NewAssistantMessage(source.id+" assistant response:\n"+source.assistantText),
		)
	}
	messages = append(messages, model.NewUserMessage(
		"Extract every eligible assistant result. Set pair_id to the pair label "+
			"shown with its source, and call the tool at most once for each pair.",
	))
	sourceByID := make(map[string]assistantEpisodeSource, len(sources))
	indexByID := make(map[string]int, len(sources))
	for i, source := range sources {
		sourceByID[source.id] = source
		indexByID[source.id] = i
	}
	operations := make([]*Operation, len(sources))
	seenIDs := make(map[string]struct{}, len(sources))
	nextCtx, err := e.runExtractionRequest(ctx, &model.Request{
		Messages: messages,
		Tools: map[string]tool.Tool{
			assistantEpisodeToolName: newAssistantEpisodeTool(sources),
		},
	}, func(callCtx context.Context, call model.ToolCall) {
		if call.Function.Name != assistantEpisodeToolName {
			return
		}
		var args map[string]any
		if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
			logAssistantEpisodeRejection(callCtx, "invalid tool arguments")
			return
		}
		pairID, _ := args[assistantEpisodePairIDKey].(string)
		source, ok := sourceByID[pairID]
		if !ok {
			logAssistantEpisodeRejection(callCtx, "unknown pair id")
			return
		}
		if _, ok := seenIDs[pairID]; ok {
			logAssistantEpisodeRejection(callCtx, "duplicate pair id")
			return
		}
		operation, parseErr := e.parseAssistantEpisode(
			callCtx,
			args,
			source.userText+"\n"+source.assistantText,
		)
		if parseErr != nil {
			logAssistantEpisodeRejection(callCtx, "content validation failed")
			return
		}
		seenIDs[pairID] = struct{}{}
		operations[indexByID[pairID]] = operation
	})
	if err != nil {
		return nextCtx, nil, err
	}
	result := make([]*Operation, 0, len(operations))
	for _, operation := range operations {
		if operation != nil {
			result = append(result, operation)
		}
	}
	return nextCtx, result, nil
}

func logAssistantEpisodeRejection(ctx context.Context, reason string) {
	log.WarnfContext(ctx, "extractor: skipped invalid assistant episode: %s", reason)
}

func (e *memoryExtractor) assistantEpisodePrompt(ctx context.Context) string {
	base := assistantEpisodeSystemPrompt
	if e.prompt == defaultPrompt {
		return base
	}
	return base + `

The following application-defined extraction policy also applies to this
request. If it conflicts with the assistant episode instructions, do not
extract the conflicting information:

` + e.renderPrompt(referenceDate(ctx))
}

func (e *memoryExtractor) parseAssistantEpisode(
	ctx context.Context,
	args map[string]any,
	source string,
) (*Operation, error) {
	memoryText, _ := args[argKeyMemory].(string)
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return nil, errors.New("memory is required")
	}
	if len(memoryText) > assistantEpisodeMaxBytes {
		return nil, fmt.Errorf("assistant episode exceeds %d bytes", assistantEpisodeMaxBytes)
	}
	if err := validateAssistantEpisodeQuantities(memoryText, source); err != nil {
		return nil, err
	}
	op := &Operation{
		Type:         OperationAdd,
		Memory:       assistantmemory.Prefix + memoryText,
		Topics:       toStringSlice(args[argKeyTopics]),
		MemoryKind:   memory.KindEpisode,
		Participants: []string{"User", "Assistant"},
	}
	if eventTime, ok := ReferenceDateFromContext(ctx); ok {
		eventTime = eventTime.UTC()
		op.EventTime = &eventTime
	}
	return op, nil
}

func selectAssistantEpisodePairs(messages []model.Message) []assistantEpisodePair {
	pairs := make([]assistantEpisodePair, 0, len(messages)/2)
	var pendingUser model.Message
	var pendingAssistant model.Message
	hasPendingUser := false
	hasPendingAssistant := false
	for _, message := range messages {
		if message.Role == model.RoleUser {
			if hasPendingUser && hasPendingAssistant {
				pairs = append(pairs, assistantEpisodePair{
					user:      pendingUser,
					assistant: pendingAssistant,
				})
			}
			hasPendingUser = eligibleAssistantEpisodeMessage(message, model.RoleUser)
			if hasPendingUser {
				pendingUser = message
			}
			hasPendingAssistant = false
			continue
		}
		if !eligibleAssistantEpisodeMessage(message, model.RoleAssistant) || !hasPendingUser {
			continue
		}
		pendingAssistant = message
		hasPendingAssistant = true
	}
	if hasPendingUser && hasPendingAssistant {
		pairs = append(pairs, assistantEpisodePair{
			user:      pendingUser,
			assistant: pendingAssistant,
		})
	}
	return pairs
}

func eligibleAssistantEpisodeMessage(message model.Message, role model.Role) bool {
	return message.Role == role && message.ToolID == "" &&
		len(message.ToolCalls) == 0 && assistantEpisodeMessageText(message) != ""
}

func assistantEpisodeUserMessages(messages []model.Message) []model.Message {
	result := make([]model.Message, 0, len(messages))
	for _, message := range messages {
		if eligibleAssistantEpisodeMessage(message, model.RoleUser) {
			result = append(result, message)
		}
	}
	return result
}

func withoutAssistantEpisodeEntries(entries []*memory.Entry) []*memory.Entry {
	filtered := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry != nil && entry.Memory != nil &&
			assistantmemory.IsText(entry.Memory.Memory) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func assistantEpisodeMessageText(message model.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+1)
	if content := strings.TrimSpace(message.Content); content != "" {
		parts = append(parts, content)
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			if text := strings.TrimSpace(*part.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func assistantEpisodeSourceExcerpt(text string) string {
	if len(text) <= assistantEpisodeSourceMaxBytes {
		return text
	}
	available := assistantEpisodeSourceMaxBytes -
		len(assistantEpisodeTruncationMarker)
	headBytes := available / 2
	headEnd := trimSplitUTF8End(text, headBytes)
	tailStart := trimSplitUTF8Start(text, len(text)-(available-headBytes))
	return text[:headEnd] + assistantEpisodeTruncationMarker + text[tailStart:]
}

func trimSplitUTF8End(text string, end int) int {
	if end <= 0 || end >= len(text) || utf8.RuneStart(text[end]) {
		return end
	}
	start := end - 1
	lowerBound := max(0, end-(utf8.UTFMax-1))
	for start > lowerBound && !utf8.RuneStart(text[start]) {
		start--
	}
	if !utf8.RuneStart(text[start]) {
		return end
	}
	_, size := utf8.DecodeRuneInString(text[start:])
	if size > end-start {
		return start
	}
	return end
}

func trimSplitUTF8Start(text string, start int) int {
	if start <= 0 || start >= len(text) || utf8.RuneStart(text[start]) {
		return start
	}
	runeStart := start - 1
	lowerBound := max(0, start-(utf8.UTFMax-1))
	for runeStart > lowerBound && !utf8.RuneStart(text[runeStart]) {
		runeStart--
	}
	if !utf8.RuneStart(text[runeStart]) {
		return start
	}
	_, size := utf8.DecodeRuneInString(text[runeStart:])
	if size > start-runeStart {
		return runeStart + size
	}
	return start
}

func strongAssistantEpisodeCandidate(userText, assistantText string) bool {
	if assistantEpisodeStructuredRequestPattern.MatchString(userText) &&
		len(assistantEpisodeListItemPattern.FindAllStringIndex(assistantText, 3)) >= 2 {
		return true
	}
	if !assistantEpisodeQuantityRequestPattern.MatchString(userText) {
		return false
	}
	userNumbers := make(map[string]struct{})
	for _, value := range assistantEpisodeNumberPattern.FindAllString(userText, -1) {
		userNumbers[value] = struct{}{}
	}
	for _, value := range assistantEpisodeNumberPattern.FindAllString(assistantText, -1) {
		if _, ok := userNumbers[value]; !ok {
			return true
		}
	}
	return false
}

func validateAssistantEpisodeQuantities(memoryText, source string) error {
	sourceQuantities := make(map[assistantEpisodeQuantity]struct{})
	for _, quantity := range assistantEpisodeQuantities(source) {
		sourceQuantities[quantity] = struct{}{}
	}
	for _, quantity := range assistantEpisodeQuantities(memoryText) {
		if _, ok := sourceQuantities[quantity]; !ok {
			return fmt.Errorf(
				"assistant episode quantity %q is not present in the source",
				formatAssistantEpisodeQuantity(quantity),
			)
		}
	}
	return nil
}

func assistantEpisodeQuantities(text string) []assistantEpisodeQuantity {
	matches := assistantEpisodeQuantityPattern.FindAllStringSubmatchIndex(text, -1)
	quantities := make([]assistantEpisodeQuantity, 0, len(matches))
	for _, match := range matches {
		quantity, ok := parseAssistantEpisodeQuantity(text, match)
		if ok {
			quantities = append(quantities, quantity)
		}
	}
	return quantities
}

func parseAssistantEpisodeQuantity(
	text string,
	match []int,
) (assistantEpisodeQuantity, bool) {
	if len(match) != 12 {
		return assistantEpisodeQuantity{}, false
	}
	sign := assistantEpisodeMatchGroup(text, match, 1) +
		assistantEpisodeMatchGroup(text, match, 3)
	prefix := assistantEpisodeMatchGroup(text, match, 2)
	value := assistantEpisodeMatchGroup(text, match, 4)
	suffix := assistantEpisodeMatchGroup(text, match, 5)
	start := match[0]
	end := match[1]
	if suffix != "" && end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		last, _ := utf8.DecodeLastRuneInString(suffix)
		if unicode.IsLetter(last) && assistantEpisodeIdentifierRune(next) {
			// A short unit alternative can match the beginning of an ordinary
			// word (for example, "m" in "meals"). Treat that case as an
			// unqualified number instead of discarding the number entirely.
			suffix = ""
		}
	}
	if prefix == "" && sign != "" && start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsDigit(previous) {
			sign = ""
		}
	}

	prefixCurrency := normalizeAssistantEpisodeCurrency(prefix)
	suffixCurrency := normalizeAssistantEpisodeCurrency(suffix)
	currency := prefixCurrency
	if suffixCurrency != "" {
		if currency == "" {
			currency = suffixCurrency
		} else if currency != suffixCurrency {
			currency += "/" + suffixCurrency
		}
	}
	unit := ""
	if suffixCurrency == "" {
		unit = normalizeAssistantEpisodeUnit(suffix)
	}
	return assistantEpisodeQuantity{
		sign:     strings.ReplaceAll(sign, "−", "-"),
		value:    normalizeAssistantEpisodeNumber(value),
		unit:     unit,
		currency: currency,
	}, true
}

func assistantEpisodeMatchGroup(text string, match []int, group int) string {
	start := match[group*2]
	end := match[group*2+1]
	if start < 0 || end < 0 {
		return ""
	}
	return text[start:end]
}

func assistantEpisodeIdentifierRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func normalizeAssistantEpisodeNumber(value string) string {
	normalized := value
	if strings.Contains(normalized, ",") {
		integer, fraction, hasFraction := strings.Cut(normalized, ".")
		parts := strings.Split(integer, ",")
		if len(parts[0]) > 0 && len(parts[0]) <= 3 {
			grouped := true
			for _, part := range parts[1:] {
				if len(part) != 3 {
					grouped = false
					break
				}
			}
			if grouped {
				normalized = strings.Join(parts, "")
				if hasFraction {
					normalized += "." + fraction
				}
			}
		}
	}
	integer, fraction, hasFraction := strings.Cut(normalized, ".")
	if !hasFraction {
		return normalized
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return integer
	}
	return integer + "." + fraction
}

func normalizeAssistantEpisodeCurrency(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "$", "USD":
		return "usd"
	case "€", "EUR":
		return "eur"
	case "£", "GBP":
		return "gbp"
	case "¥":
		return "yen-yuan"
	case "JPY":
		return "jpy"
	case "CNY", "RMB":
		return "cny"
	default:
		return ""
	}
}

func normalizeAssistantEpisodeUnit(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), ""))
	parts := strings.Split(normalized, "/")
	if len(parts) == 1 {
		return assistantEpisodeUnitAliases[normalized]
	}
	if len(parts) != 2 {
		return ""
	}
	numerator := assistantEpisodeUnitAliases[parts[0]]
	denominator := assistantEpisodeUnitAliases[parts[1]]
	if numerator == "" || denominator == "" {
		return ""
	}
	return numerator + "/" + denominator
}

func formatAssistantEpisodeQuantity(quantity assistantEpisodeQuantity) string {
	var builder strings.Builder
	if quantity.currency != "" {
		builder.WriteString(quantity.currency)
		builder.WriteByte(' ')
	}
	builder.WriteString(quantity.sign)
	builder.WriteString(quantity.value)
	if quantity.unit != "" {
		builder.WriteByte(' ')
		builder.WriteString(quantity.unit)
	}
	return builder.String()
}

func (e *memoryExtractor) assistantEpisodeAddEnabled() bool {
	if e.enabledTools == nil {
		return true
	}
	_, ok := e.enabledTools[memory.AddToolName]
	return ok
}

func containsForgetOperation(operations []*Operation) bool {
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		if operation.Type == OperationDelete || operation.Type == OperationClear {
			return true
		}
	}
	return false
}

func newAssistantEpisodeTool(
	sources []assistantEpisodeSource,
) *declarationOnlyTool {
	properties := map[string]*tool.Schema{
		argKeyMemory: {
			Type: "string",
			Description: "A concise, self-contained account of the user's " +
				"request and the result supplied by the assistant.",
		},
		argKeyTopics: {
			Type:        "array",
			Description: "Optional retrieval topics.",
			Items:       &tool.Schema{Type: "string"},
		},
	}
	pairIDs := make([]any, 0, len(sources))
	for _, source := range sources {
		pairIDs = append(pairIDs, source.id)
	}
	properties[assistantEpisodePairIDKey] = &tool.Schema{
		Type:        "string",
		Description: "The pair label associated with this result.",
		Enum:        pairIDs,
	}
	return &declarationOnlyTool{decl: &tool.Declaration{
		Name: assistantEpisodeToolName,
		Description: "Record one durable, reusable result supplied by an " +
			"assistant response as attributed conversation history.",
		InputSchema: &tool.Schema{
			Type:                 "object",
			Properties:           properties,
			Required:             []string{argKeyMemory, assistantEpisodePairIDKey},
			AdditionalProperties: false,
		},
	}}
}

const assistantEpisodeSystemPrompt = `You extract durable results previously
provided by the assistant as attributed conversation episodes.

- Call memory_assistant_episode at most once for each labeled pair that
  contains a durable, reusable result.
- Preserve the paired user request and the assistant's exact material result,
  including names, quantities, dates, durations, negation, and qualifications.
- Preserve item-to-detail relationships in lists and procedures.
- Record attributed conversation history, not verified truth or a user fact.
- Skip acknowledgments, refusals, follow-up questions, generic explanations,
  hidden reasoning, credentials, tool arguments, and raw tool results.`
