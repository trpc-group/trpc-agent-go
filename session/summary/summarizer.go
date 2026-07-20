//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelcontext"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
	isummaryscope "trpc.group/trpc-go/trpc-agent-go/session/internal/summaryscope"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

var _ SessionSummarizer = (*sessionSummarizer)(nil)
var _ ContextAwareSummarizer = (*sessionSummarizer)(nil)

// Common metadata field keys.
const (
	// metadataKeyModelName is the key for model name in metadata.
	metadataKeyModelName = "model_name"
	// metadataKeySummarizerName is the key for summarizer name in metadata.
	metadataKeySummarizerName = "summarizer_name"
	// metadataKeyMaxSummaryWords is the key for max summary words in metadata.
	metadataKeyMaxSummaryWords = "max_summary_words"
	// metadataKeyModelAvailable is the key for model availability in metadata.
	metadataKeyModelAvailable = "model_available"
	// metadataKeyCheckFunctions is the key for check functions count in metadata.
	metadataKeyCheckFunctions = "check_functions"
	// metadataKeySkipRecentEnabled indicates whether skip recent logic is configured.
	metadataKeySkipRecentEnabled = "skip_recent_enabled"
	// metadataKeyCacheSafeForking indicates whether cache-safe forking is enabled.
	metadataKeyCacheSafeForking = "cache_safe_forking"
)

const (
	// lastIncludedTsKey is the key for last included timestamp in summary.
	lastIncludedTsKey = session.SummaryLastIncludedTimestampStateKey
	// lastIncludedEventIDKey is the key for last included event ID in summary.
	lastIncludedEventIDKey = session.SummaryLastIncludedEventIDStateKey

	// conversationTextVar is the prompt variable name for conversation text (without braces).
	conversationTextVar = "conversation_text"
	// conversationTextPlaceholder is the placeholder for conversation text in templates.
	conversationTextPlaceholder = "{" + conversationTextVar + "}"
	// previousSummaryVar is the prompt variable name for the previous rolling summary.
	previousSummaryVar = "previous_summary"
	// previousSummaryPlaceholder is the placeholder for the previous rolling summary.
	previousSummaryPlaceholder = "{" + previousSummaryVar + "}"
	// maxSummaryWordsVar is the prompt variable name for max summary words (without braces).
	maxSummaryWordsVar = "max_summary_words"
	// maxSummaryWordsPlaceholder is the placeholder for max summary words in templates.
	maxSummaryWordsPlaceholder = "{" + maxSummaryWordsVar + "}"

	// authorUser is the user author.
	authorUser = "user"
	// authorSystem is the system author.
	authorSystem = "system"
	// authorUnknown is the unknown author.
	authorUnknown = "unknown"

	// summaryRequestInputRatio is a conservative ceiling for models that do not
	// expose a smaller provider-side input budget.
	summaryRequestInputRatio = 0.7
	// summaryRequestRetryRatio reduces the semantic input on a bounded retry
	// when the provider still reports a context overflow.
	summaryRequestRetryRatio = 0.5

	summaryToolArgumentsOmitted = `{"_trpc_summary_note":"tool arguments omitted to fit the summary context"}`
	summaryToolResultOmittedFmt = "[Tool result omitted to fit the summary context; " +
		"tool_name=%q, tool_call_id=%q. The tool call completed before summarization.]"
	summaryConversationOmitted = "\n[... middle conversation omitted to fit the summary context ...]\n"
	summaryPreviousOmitted     = "\n[... previous summary omitted to fit the summary context ...]\n"
)

// formatResponseError formats a model.ResponseError into a human-readable error.
func formatResponseError(e *model.ResponseError) error {
	if e == nil {
		return nil
	}
	msg := e.Message
	if e.Type != "" {
		msg = fmt.Sprintf("[%s] %s", e.Type, msg)
	}
	if e.Code != nil && *e.Code != "" {
		msg = fmt.Sprintf("%s (code: %s)", msg, *e.Code)
	}
	return fmt.Errorf("model error during summarization: %s", msg)
}

// ToolCallFormatter formats a tool call for inclusion in the summary input.
// It receives the tool call and returns a formatted string.
// Return empty string to exclude this tool call from the summary.
type ToolCallFormatter func(tc model.ToolCall) string

// ToolResultFormatter formats a tool result for inclusion in the summary input.
// It receives the message containing the tool result and returns a formatted string.
// Return empty string to exclude this tool result from the summary.
type ToolResultFormatter func(msg model.Message) string

// defaultToolCallFormatter is the default formatter for tool calls.
// It formats as "[Called tool: name with args: {args}]".
func defaultToolCallFormatter(tc model.ToolCall) string {
	name := tc.Function.Name
	if name == "" {
		return ""
	}
	args := string(tc.Function.Arguments)
	if args == "" || args == "{}" {
		return fmt.Sprintf("[Called tool: %s]", name)
	}
	return fmt.Sprintf("[Called tool: %s with args: %s]", name, args)
}

// defaultToolResultFormatter is the default formatter for tool results.
// It formats as "[toolName returned: content]".
func defaultToolResultFormatter(msg model.Message) string {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	toolName := msg.ToolName
	if toolName == "" {
		toolName = "tool"
	}
	return fmt.Sprintf("[%s returned: %s]", toolName, content)
}

// validatePrompt validates that the user prompt contains the conversation
// placeholder required to inject the extracted conversation text.
func validatePrompt(template string) error {
	textPrompt := prompt.Text{Template: template}
	if err := textPrompt.ValidateRequired(
		conversationTextVar,
	); err != nil {
		return fmt.Errorf("prompt must include %s placeholder", conversationTextPlaceholder)
	}
	return nil
}

// validateSystemPrompt validates that the system prompt does not include
// conversation payload placeholders. Keep the conversation content in the user
// prompt so the system message stays instruction-only.
func validateSystemPrompt(template string) error {
	textPrompt := prompt.Text{Template: template}
	for _, item := range []struct {
		name        string
		placeholder string
	}{
		{name: conversationTextVar, placeholder: conversationTextPlaceholder},
		{name: previousSummaryVar, placeholder: previousSummaryPlaceholder},
	} {
		if textPrompt.ValidateRequired(item.name) == nil {
			return fmt.Errorf(
				"system prompt must not include %s placeholder",
				item.placeholder,
			)
		}
	}
	return nil
}

// validateCacheSafeForkPrompt validates that the cache-safe fork instruction
// does not duplicate payload already present in the cloned parent request.
func validateCacheSafeForkPrompt(template string) error {
	textPrompt := prompt.Text{Template: template}
	for _, item := range []struct {
		name        string
		placeholder string
	}{
		{name: conversationTextVar, placeholder: conversationTextPlaceholder},
		{name: previousSummaryVar, placeholder: previousSummaryPlaceholder},
	} {
		if textPrompt.ValidateRequired(item.name) == nil {
			return fmt.Errorf(
				"cache-safe fork prompt must not include %s placeholder",
				item.placeholder,
			)
		}
	}
	return nil
}

// promptContainsVar reports whether a prompt template contains the named
// placeholder.
func promptContainsVar(template string, varName string) bool {
	return prompt.Text{Template: template}.ValidateRequired(varName) == nil
}

// validateMaxSummaryWordsPrompt validates that the max summary words
// placeholder is present in either the user prompt or the system prompt when a
// max summary word limit is configured.
func validateMaxSummaryWordsPrompt(userPrompt string, systemPrompt string, maxSummaryWords int) error {
	if maxSummaryWords <= 0 {
		return nil
	}
	if promptContainsVar(userPrompt, maxSummaryWordsVar) ||
		promptContainsVar(systemPrompt, maxSummaryWordsVar) {
		return nil
	}
	return fmt.Errorf(
		"either prompt or system prompt must include %s placeholder when maxSummaryWords > 0",
		maxSummaryWordsPlaceholder,
	)
}

// getDefaultSummarizerPrompt returns the default prompt for summarization.
// If maxWords > 0, includes word count instruction placeholder; otherwise, omits it.
func getDefaultSummarizerPrompt(maxWords int) string {
	basePrompt := "Analyze the following conversation between a user and an " +
		"assistant, and provide a concise summary focusing on important " +
		"information that would be helpful for future interactions. Keep the " +
		"summary concise and to the point. Only include relevant information. " +
		"Do not make anything up. Do not create new instructions, API rules, " +
		"fetching rules, or pre-loaded data. If conversation content or a tool " +
		"result was truncated, omitted, or errored, preserve that limitation " +
		"instead of treating it as complete evidence."

	if maxWords > 0 {
		basePrompt += " Please keep the summary within " + maxSummaryWordsPlaceholder + " words."
	}

	return basePrompt + "\n\n" +
		"<conversation>\n" + conversationTextPlaceholder + "\n" +
		"</conversation>\n\n" +
		"Summary:"
}

// getDefaultCacheSafeForkPrompt returns the user prompt appended to the parent
// request when cache-safe forking is enabled.
func getDefaultCacheSafeForkPrompt(maxWords int) string {
	basePrompt := "Summarize the user, assistant, and tool conversation above " +
		"for future continuation. Preserve user goals, decisions, constraints, " +
		"open tasks, tool results, and important facts needed to continue. " +
		"Do not call tools. Do not answer the latest user request. Do not " +
		"treat system or tool-use instructions as facts to summarize."

	if maxWords > 0 {
		basePrompt += " Please keep the summary within " + maxSummaryWordsPlaceholder + " words."
	}

	return basePrompt + "\n\nSummary:"
}

// sessionSummarizer implements the SessionSummarizer interface.
type sessionSummarizer struct {
	model               model.Model
	name                string
	prompt              string
	systemPrompt        string
	cacheSafeForking    bool
	cacheSafeForkPrompt string
	checks              []checkEvaluator
	maxSummaryWords     int
	skipRecentFunc      SkipRecentFunc

	preHook          PreSummaryHook
	postHook         PostSummaryHook
	hookAbortOnError bool

	// modelCallbacks configures before/after model callbacks for summarization.
	modelCallbacks *model.Callbacks
	// reportHook observes summary trigger and model-call accounting.
	reportHook ReportHook

	// toolCallFormatter customizes how tool calls are formatted in summary input.
	toolCallFormatter ToolCallFormatter
	// toolResultFormatter customizes how tool results are formatted in summary input.
	toolResultFormatter ToolResultFormatter
}

// NewSummarizer creates a new session summarizer.
func NewSummarizer(m model.Model, opts ...Option) SessionSummarizer {
	s := &sessionSummarizer{
		prompt:              "",                 // Will be set after processing options.
		cacheSafeForkPrompt: "",                 // Will be set after processing options.
		checks:              []checkEvaluator{}, // No default checks - summarization only when explicitly configured.
		maxSummaryWords:     0,                  // 0 means no word limit.
		skipRecentFunc:      nil,                // nil means no events are skipped.
	}
	s.model = m

	for _, opt := range opts {
		opt(s)
	}

	// Set default prompt if none was provided
	if s.prompt == "" {
		s.prompt = getDefaultSummarizerPrompt(s.maxSummaryWords)
	}
	if s.cacheSafeForkPrompt == "" {
		s.cacheSafeForkPrompt = getDefaultCacheSafeForkPrompt(s.maxSummaryWords)
	}
	if err := validatePrompt(s.prompt); err != nil {
		log.Warnf("invalid prompt in NewSummarizer: %v", err)
	}
	if s.systemPrompt != "" {
		if err := validateSystemPrompt(s.systemPrompt); err != nil {
			log.Warnf("invalid system prompt in NewSummarizer: %v", err)
		}
	}
	if err := validateCacheSafeForkPrompt(s.cacheSafeForkPrompt); err != nil {
		log.Warnf("invalid cache-safe fork prompt in NewSummarizer: %v", err)
	}
	if err := validateMaxSummaryWordsPrompt(s.prompt, s.systemPrompt, s.maxSummaryWords); err != nil {
		log.Warnf("invalid prompt in NewSummarizer: %v", err)
	}

	return s
}

// ShouldSummarize checks if the session should be summarized.
func (s *sessionSummarizer) ShouldSummarize(sess *session.Session) bool {
	return s.ShouldSummarizeWithContext(context.Background(), sess)
}

// ShouldSummarizeWithContext evaluates configured checks using the current
// request context when available.
func (s *sessionSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	trigger := s.evaluateTrigger(ctx, sess)
	if report, ok := reportFromContext(ctx); ok {
		report.Trigger = trigger
	}
	return trigger.Fired
}

func (s *sessionSummarizer) evaluateTrigger(
	ctx context.Context,
	sess *session.Session,
) Trigger {
	if sess == nil {
		return Trigger{}
	}
	visible := sess.GetVisibleEvents()
	if len(visible) == 0 {
		return Trigger{}
	}
	summaryInputEvents := filterSummaryInputEventsForSession(
		s.filterEventsForSummary(visible),
		sess,
	)
	if !s.hasSummarizableContent(summaryInputEvents) {
		return Trigger{}
	}

	checkSess := s.buildCheckSession(sess)
	if len(s.checks) == 0 {
		return Trigger{
			Fired:     true,
			Name:      checkNameAlways,
			Metric:    metricCustom,
			FilterKey: triggerFilterKey(checkSess),
		}
	}

	checks := make([]Check, 0, len(s.checks))
	for _, check := range s.checks {
		result := check(ctx, checkSess)
		checks = append(checks, result)
		if !result.Passed {
			trigger := triggerFromCheck(result)
			trigger.Fired = false
			trigger.FilterKey = triggerFilterKey(checkSess)
			trigger.Checks = checks
			return trigger
		}
	}
	trigger := triggerFromCheck(preferredTriggerCheck(checks))
	trigger.Fired = true
	trigger.FilterKey = triggerFilterKey(checkSess)
	trigger.Checks = checks
	return trigger
}

func triggerFilterKey(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	return isummaryscope.GetScopeFilterKey(sess)
}

func triggerFromCheck(check Check) Trigger {
	return Trigger{
		Fired:          check.Passed,
		Name:           check.Name,
		Metric:         check.Metric,
		Value:          check.Value,
		Threshold:      check.Threshold,
		Unit:           check.Unit,
		ContextWindow:  check.ContextWindow,
		ThresholdRatio: check.ThresholdRatio,
	}
}

func preferredTriggerCheck(checks []Check) Check {
	for _, name := range []string{
		checkNameContextThreshold,
		checkNameTokenThreshold,
		checkNameEventThreshold,
		checkNameTimeThreshold,
	} {
		for _, check := range checks {
			if check.Passed && check.Name == name {
				return check
			}
		}
	}
	for _, check := range checks {
		if check.Passed {
			return check
		}
	}
	if len(checks) > 0 {
		return checks[len(checks)-1]
	}
	return Check{
		Name:   checkNameAlways,
		Metric: metricCustom,
		Passed: true,
	}
}

type summaryPromptInput struct {
	conversationText string
	previousSummary  string
}

func (in summaryPromptInput) characterCount() int {
	return len(in.conversationText) + len(in.previousSummary)
}

// Summarize generates a summary without modifying the session events.
func (s *sessionSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if s.model == nil {
		return "", fmt.Errorf("no model configured for summarization for session %s", sess.ID)
	}
	ctx = s.ensureReportContext(ctx)
	previousSummary, _ := isummarycontext.PreviousSummary(ctx)
	separatePreviousSummary := promptContainsVar(s.prompt, previousSummaryVar)
	if len(sess.Events) == 0 && (!separatePreviousSummary || previousSummary == "") {
		return "", fmt.Errorf("no events to summarize for session %s (events=0)", sess.ID)
	}

	// Extract conversation text from visible events. Use filtered events for summarization
	// to skip recent events while ensuring proper context.
	eventsToSummarize := filterSummaryInputEventsForSession(
		s.filterEventsForSummary(visible),
		sess,
	)
	conversationEvents := eventsToSummarize
	input := summaryPromptInput{}
	if separatePreviousSummary {
		conversationEvents = removePreviousSummaryEvent(
			conversationEvents,
			previousSummary,
		)
		input.previousSummary = previousSummary
	}

	input.conversationText = s.extractConversationText(conversationEvents)
	ctx, input, err := s.runPreSummaryHook(
		ctx,
		sess,
		conversationEvents,
		input,
		separatePreviousSummary,
	)
	if err != nil {
		return "", err
	}
	if input.conversationText == "" && input.previousSummary == "" {
		return "", fmt.Errorf("no conversation text extracted for session %s (events=%d)", sess.ID, len(eventsToSummarize))
	}

	ctx, summaryText, err := s.generateSummary(ctx, sess, input)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary for session %s: %w", sess.ID, err)
	}

	s.recordLastIncludedBoundary(sess, eventsToSummarize)

	if s.postHook != nil {
		hookCtx := &PostSummaryHookContext{
			Ctx:     ctx,
			Session: sess,
			Summary: summaryText,
		}
		hookErr := s.postHook(hookCtx)
		if hookErr != nil && s.hookAbortOnError {
			return "", fmt.Errorf("post-summary hook failed: %w", hookErr)
		}
		if hookErr == nil && hookCtx.Summary != "" {
			summaryText = hookCtx.Summary
		}
	}

	return summaryText, nil
}

// runPreSummaryHook applies pre-summary input and context changes while
// preserving the original input when a non-fatal hook error occurs.
func (s *sessionSummarizer) runPreSummaryHook(
	ctx context.Context,
	sess *session.Session,
	events []event.Event,
	input summaryPromptInput,
	separatePreviousSummary bool,
) (context.Context, summaryPromptInput, error) {
	if s.preHook == nil {
		return ctx, input, nil
	}
	hookCtx := &PreSummaryHookContext{
		Ctx:             ctx,
		Session:         sess,
		Events:          events,
		Text:            input.conversationText,
		PreviousSummary: input.previousSummary,
	}
	if err := s.preHook(hookCtx); err != nil {
		if s.hookAbortOnError {
			return ctx, input, fmt.Errorf("pre-summary hook failed: %w", err)
		}
		return ctx, input, nil
	}

	ctx = inheritReportContext(hookCtx.Ctx, ctx)
	if separatePreviousSummary {
		input.previousSummary = hookCtx.PreviousSummary
	}
	if hookCtx.Text != "" {
		input.conversationText = hookCtx.Text
	} else if len(hookCtx.Events) > 0 {
		input.conversationText = s.extractConversationText(hookCtx.Events)
	} else {
		input.conversationText = ""
	}
	return ctx, input, nil
}

// removePreviousSummaryEvent removes the synthetic event inserted by the
// session service when the previous summary is rendered through its own prompt
// placeholder. Direct callers that attach a previous summary without a
// matching synthetic event keep their supplied events unchanged.
func removePreviousSummaryEvent(
	events []event.Event,
	previousSummary string,
) []event.Event {
	if previousSummary == "" || len(events) == 0 {
		return events
	}
	first := events[0]
	if first.Author != authorSystem || first.ID != "" || first.RequestID != "" ||
		first.InvocationID != "" || first.FilterKey != "" || first.Response == nil ||
		len(first.Response.Choices) != 1 ||
		first.Response.Choices[0].Message.Content != previousSummary {
		return events
	}
	return events[1:]
}

func (s *sessionSummarizer) ensureReportContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if report, ok := reportFromContext(ctx); ok {
		seedManualTrigger(report)
		return ctx
	}
	if s.reportHook == nil {
		return ctx
	}
	report := &Report{}
	seedManualTrigger(report)
	return ContextWithReport(ctx, report)
}

func seedManualTrigger(report *Report) {
	if report == nil || !triggerIsEmpty(report.Trigger) {
		return
	}
	report.Trigger = Trigger{
		Fired:  true,
		Name:   "manual",
		Metric: metricCustom,
	}
}

func triggerIsEmpty(trigger Trigger) bool {
	return !trigger.Fired &&
		trigger.Name == "" &&
		trigger.Metric == "" &&
		trigger.Value == 0 &&
		trigger.Threshold == 0 &&
		trigger.Unit == "" &&
		trigger.ContextWindow == 0 &&
		trigger.ThresholdRatio == 0 &&
		trigger.FilterKey == "" &&
		len(trigger.Checks) == 0
}

// recordLastIncludedBoundary records the last included summary boundary in the session state.
func (s *sessionSummarizer) recordLastIncludedBoundary(sess *session.Session, events []event.Event) {
	if sess == nil || len(events) == 0 {
		return
	}
	last := events[len(events)-1]
	lastTimestamp := last.Timestamp.UTC()
	sess.SetState(lastIncludedTsKey, []byte(lastTimestamp.Format(time.RFC3339Nano)))
	if last.ID == "" {
		sess.DeleteState(lastIncludedEventIDKey)
		return
	}
	sess.SetState(lastIncludedEventIDKey, []byte(last.ID))
}

func (s *sessionSummarizer) buildCheckSession(
	sess *session.Session,
) *session.Session {
	if sess == nil {
		return nil
	}
	checkSess := sess.Clone()
	delta := filterDeltaEvents(checkSess)
	filtered := s.filterEventsForSummary(delta)
	thresholdEvents := filterThresholdEventsForSession(filtered, checkSess)
	var thresholdMessage model.Message
	summaryInputEvents := filterSummaryInputEventsForSession(filtered, checkSess)
	if s.hasSummarizableContent(summaryInputEvents) {
		thresholdMessage = extractTokenThresholdMessage(thresholdEvents)
	}
	checkSess.SetState(
		tokenThresholdConversationTextStateKey,
		[]byte(thresholdMessage.Content),
	)
	checkSess.SetState(
		tokenThresholdReasoningContentStateKey,
		[]byte(thresholdMessage.ReasoningContent),
	)
	return checkSess
}

// filterEventsForSummary filters events for summarization, excluding recent events
// and ensuring that retained events still have enough context to summarize.
func (s *sessionSummarizer) filterEventsForSummary(events []event.Event) []event.Event {
	if s.skipRecentFunc == nil {
		return events
	}

	skipCount := s.skipRecentFunc(events)
	if skipCount <= 0 {
		return events
	}
	if len(events) <= skipCount {
		return []event.Event{}
	}

	filteredEvents := events[:len(events)-skipCount]

	if hasUserMessageForSummary(filteredEvents) {
		return filteredEvents
	}

	// Delta summarization can prepend the previous summary as a synthetic
	// system event. Preserve assistant/tool follow-ups when that summary is
	// still present and at least one real event remains after it.
	if s.hasPrependedSummaryContext(filteredEvents) {
		return filteredEvents
	}

	return []event.Event{}
}

func hasUserMessageForSummary(events []event.Event) bool {
	for _, e := range events {
		if e.Author != authorUser || !eventHasTextContent(e) {
			continue
		}
		return true
	}
	return false
}

func eventHasTextContent(e event.Event) bool {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return false
	}
	for _, choice := range e.Response.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			return true
		}
	}
	return false
}

func eventHasSummarizableContent(
	e event.Event,
	toolCallFmt ToolCallFormatter,
	toolResultFmt ToolResultFormatter,
) bool {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return false
	}
	for _, choice := range e.Response.Choices {
		msg := choice.Message
		for _, tc := range msg.ToolCalls {
			if toolCallFmt(tc) != "" {
				return true
			}
		}
		if msg.ToolID != "" {
			if toolResultFmt(msg) != "" {
				return true
			}
			continue
		}
		if strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

func (s *sessionSummarizer) hasSummarizableContent(events []event.Event) bool {
	toolCallFmt := s.toolCallFormatter
	if toolCallFmt == nil {
		toolCallFmt = defaultToolCallFormatter
	}
	toolResultFmt := s.toolResultFormatter
	if toolResultFmt == nil {
		toolResultFmt = defaultToolResultFormatter
	}
	for _, e := range events {
		if eventHasSummarizableContent(e, toolCallFmt, toolResultFmt) {
			return true
		}
	}
	return false
}

func (s *sessionSummarizer) hasPrependedSummaryContext(events []event.Event) bool {
	if len(events) < 2 {
		return false
	}
	first := events[0]
	if first.Author != authorSystem || !eventHasTextContent(first) {
		return false
	}
	// prependPrevSummary inserts a synthetic system event at the head while
	// preserving the original delta event timestamps after it.
	if first.Timestamp.Before(events[1].Timestamp) {
		return false
	}
	return s.hasSummarizableContent(events[1:])
}

// SetPrompt updates the summarizer's prompt dynamically.
// The prompt must include the placeholder {conversation_text}, which will be
// replaced with the extracted conversation when generating the summary. It may
// also include {previous_summary} to position the previous rolling summary
// separately from newly uncovered conversation text.
// If maxSummaryWords > 0, either the user prompt or the configured system
// prompt must include {max_summary_words}. If an empty prompt is provided, it
// will be ignored and the current prompt will remain unchanged.
func (s *sessionSummarizer) SetPrompt(prompt string) {
	if prompt == "" {
		return
	}
	if err := validatePrompt(prompt); err != nil {
		log.Warnf("invalid prompt: %v", err)
		return
	}
	if err := validateMaxSummaryWordsPrompt(prompt, s.systemPrompt, s.maxSummaryWords); err != nil {
		log.Warnf("invalid prompt: %v", err)
		return
	}
	s.prompt = prompt
}

// SetModel updates the summarizer's model dynamically.
// This allows switching to different models at runtime based on different
// scenarios or requirements. If nil is provided, it will be ignored and the
// current model will remain unchanged.
func (s *sessionSummarizer) SetModel(m model.Model) {
	if m != nil {
		s.model = m
	}
}

// Metadata returns metadata about the summarizer configuration.
func (s *sessionSummarizer) Metadata() map[string]any {
	var modelName string
	modelAvailable := false
	if s.model != nil {
		modelName = s.model.Info().Name
		modelAvailable = true
	}
	return map[string]any{
		metadataKeyModelName:         modelName,
		metadataKeySummarizerName:    s.name,
		metadataKeyMaxSummaryWords:   s.maxSummaryWords,
		metadataKeyModelAvailable:    modelAvailable,
		metadataKeyCheckFunctions:    len(s.checks),
		metadataKeySkipRecentEnabled: s.skipRecentFunc != nil,
		metadataKeyCacheSafeForking:  s.cacheSafeForking,
	}
}

// extractConversationText extracts conversation text from events.
// This includes regular messages, tool calls, and tool responses.
func (s *sessionSummarizer) extractConversationText(events []event.Event) string {
	return extractConversationText(
		events,
		s.toolCallFormatter,
		s.toolResultFormatter,
	)
}

// extractConversationText converts events into conversation text.
// When tool formatters are nil, default formatters are used.
func extractConversationText(
	events []event.Event,
	toolCallFmt ToolCallFormatter,
	toolResultFmt ToolResultFormatter,
) string {
	var parts []string

	if toolCallFmt == nil {
		toolCallFmt = defaultToolCallFormatter
	}
	if toolResultFmt == nil {
		toolResultFmt = defaultToolResultFormatter
	}

	for _, e := range events {
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		author := e.Author
		if author == "" {
			author = authorUnknown
		}

		// Iterate over all choices, not just the first one.
		// When model returns multiple tool call results, they may be distributed
		// across different choices (len(e.Response.Choices) > 1).
		for _, choice := range e.Response.Choices {
			msg := choice.Message

			// Handle tool calls from assistant.
			// Note: A message may contain both ToolCalls and Content (e.g., "Let me check
			// the weather" + tool call), so we process both without using continue.
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					toolCallText := toolCallFmt(tc)
					if toolCallText != "" {
						parts = append(parts, fmt.Sprintf("%s: %s", author, toolCallText))
					}
				}
			}

			// Handle tool response.
			if msg.ToolID != "" {
				toolRespText := toolResultFmt(msg)
				if toolRespText != "" {
					parts = append(parts, fmt.Sprintf("%s: %s", author, toolRespText))
				}
				continue // Tool responses don't have additional content.
			}

			// Handle regular message content.
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				parts = append(parts, fmt.Sprintf("%s: %s", author, trimmed))
			}
		}
	}

	return strings.Join(parts, "\n")
}

func extractTokenThresholdMessage(events []event.Event) model.Message {
	return model.Message{
		Content:          extractConversationText(events, nil, nil),
		ReasoningContent: extractReasoningContent(events),
	}
}

func extractReasoningContent(events []event.Event) string {
	var parts []string
	for _, e := range events {
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			if trimmed := strings.TrimSpace(choice.Message.ReasoningContent); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// generateSummary generates a summary using the LLM model.
func (s *sessionSummarizer) generateSummary(
	ctx context.Context,
	sess *session.Session,
	input summaryPromptInput,
) (context.Context, string, error) {
	// Telemetry trace + metrics tracking (aligned with toolsearch/llm_search.go).
	var err error
	modelName := ""
	if s.model != nil {
		modelName = s.model.Info().Name
	}
	_, span := trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(modelName))
	defer span.End()

	request, mode, err := s.buildSummaryRequest(ctx, input)
	if err != nil {
		err = fmt.Errorf("failed to build summary request: %w", err)
		s.emitReport(ctx, err)
		return ctx, "", err
	}

	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		invocation = agent.NewInvocation(
			agent.WithInvocationModel(s.model),
			agent.WithInvocationSession(sess),
		)
	} else {
		// Best-effort: ensure telemetry has model/session info.
		if invocation.Model == nil && s.model != nil {
			invocation.Model = s.model
		}
		if invocation.Session == nil && sess != nil {
			invocation.Session = sess
		}
	}

	// Get or create timing info from invocation (only record first LLM call).
	timingInfo := invocation.GetOrCreateTimingInfo()
	taskType := itelemetry.NewSummarizeTaskType(s.name)
	tracker := itelemetry.NewChatMetricsTracker(
		ctx,
		invocation,
		request,
		timingInfo,
		&taskType,
		&err,
	)
	defer tracker.RecordMetrics()()

	ensureTimingInfo := func(resp *model.Response) {
		if resp == nil {
			return
		}
		if resp.Usage == nil {
			resp.Usage = &model.Usage{}
		}
		resp.Usage.TimingInfo = timingInfo
	}

	trackResponse := func(resp *model.Response) {
		tracker.TrackResponse(resp)
		s.recordReportUsage(ctx, resp, nil)
		ensureTimingInfo(resp)
	}

	var finalResp *model.Response
	defer func() {
		s.recordReportUsage(ctx, finalResp, err)
		s.emitReport(ctx, err)
		if finalResp == nil {
			return
		}
		ensureTimingInfo(finalResp)

		itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{
			Invocation:       invocation,
			Request:          request,
			Response:         finalResp,
			TimeToFirstToken: tracker.FirstTokenTimeDuration(),
			TaskType:         taskType,
		})
	}()

	ctx, summaryText, finalResp, err := s.runSummaryAttempts(
		ctx,
		request,
		mode,
		input,
		trackResponse,
		ensureTimingInfo,
	)
	return ctx, summaryText, err
}

func (s *sessionSummarizer) runSummaryAttempts(
	ctx context.Context,
	request *model.Request,
	mode string,
	input summaryPromptInput,
	trackResponse func(*model.Response),
	ensureTimingInfo func(*model.Response),
) (context.Context, string, *model.Response, error) {
	result := s.runSummaryAttempt(
		ctx,
		request,
		mode,
		input,
		0,
		trackResponse,
		ensureTimingInfo,
	)
	if result.err == nil && result.summaryText != "" {
		return result.ctx, result.summaryText, result.response, nil
	}
	if result.custom || !shouldRetrySummary(
		result.summaryText,
		result.err,
		result.response,
	) {
		return result.ctx, "", result.response, summaryAttemptError(
			result.err,
			input,
		)
	}

	retryBudget := max(
		int(float64(result.budget)*summaryRequestRetryRatio),
		1,
	)
	retryRequest, buildErr := s.buildBoundedStandaloneSummaryRequest(
		result.ctx,
		input,
		retryBudget,
	)
	if buildErr != nil {
		return result.ctx, "", result.response, fmt.Errorf(
			"build summary retry request: %w",
			buildErr,
		)
	}
	log.DebugfContext(
		result.ctx,
		"retrying summary with standalone bounded input: budget=%d",
		retryBudget,
	)
	result = s.runSummaryAttempt(
		result.ctx,
		retryRequest,
		callModeStandalone,
		input,
		retryBudget,
		trackResponse,
		ensureTimingInfo,
	)
	if result.err != nil || result.summaryText == "" {
		return result.ctx, "", result.response, summaryAttemptError(
			result.err,
			input,
		)
	}
	return result.ctx, result.summaryText, result.response, nil
}

func shouldRetrySummary(
	summaryText string,
	err error,
	response *model.Response,
) bool {
	return isSummaryContextLengthError(err, response) ||
		(err == nil && summaryText == "")
}

func summaryAttemptError(err error, input summaryPromptInput) error {
	if err != nil {
		return err
	}
	return fmt.Errorf(
		"generated empty summary (input_chars=%d)",
		input.characterCount(),
	)
}

type summaryAttemptResult struct {
	ctx         context.Context
	summaryText string
	response    *model.Response
	custom      bool
	mode        string
	budget      int
	err         error
}

func (s *sessionSummarizer) runSummaryAttempt(
	ctx context.Context,
	request *model.Request,
	mode string,
	input summaryPromptInput,
	budgetLimit int,
	trackResponse func(*model.Response),
	ensureTimingInfo func(*model.Response),
) summaryAttemptResult {
	result := summaryAttemptResult{ctx: ctx, mode: mode}
	result.budget = s.summaryRequestInputBudget(ctx, request)
	if budgetLimit > 0 && budgetLimit < result.budget {
		result.budget = budgetLimit
	}
	prepared, preparedMode, err := s.prepareSummaryRequest(
		ctx,
		request,
		mode,
		input,
		result.budget,
	)
	if err != nil {
		result.err = fmt.Errorf("prepare summary request: %w", err)
		return result
	}
	if prepared != request {
		*request = *prepared
	}
	result.mode = preparedMode

	ctx, responseChan, err := s.runBeforeModelCallbacks(ctx, request)
	result.ctx = ctx
	if err != nil {
		result.err = err
		return result
	}

	result.custom = responseChan != nil
	if !result.custom {
		result.budget = s.summaryRequestInputBudget(ctx, request)
		if budgetLimit > 0 && budgetLimit < result.budget {
			result.budget = budgetLimit
		}
		if fitErr := s.ensureSummaryRequestFits(
			ctx,
			request,
			false,
			result.budget,
		); fitErr != nil {
			result.err = fmt.Errorf(
				"summary request no longer fits after before-model callbacks: %w",
				fitErr,
			)
			return result
		}
		s.recordReportCall(ctx, request, result.mode)
		responseChan, err = s.model.GenerateContent(ctx, request)
		if err != nil {
			result.err = fmt.Errorf(
				"failed to generate summary: %w",
				err,
			)
			return result
		}
	} else {
		s.recordReportCall(ctx, nil, callModeCustomResponse)
	}

	ctx, summaryText, finalResp, err := s.collectSummaryFromResponses(
		ctx,
		request,
		responseChan,
		trackResponse,
		ensureTimingInfo,
	)
	result.ctx = ctx
	result.summaryText = summaryText
	result.response = finalResp
	result.err = err
	return result
}

func (s *sessionSummarizer) buildSummaryPrompt(input summaryPromptInput) (string, error) {
	vars := prompt.Vars{
		conversationTextVar: input.conversationText,
		previousSummaryVar:  input.previousSummary,
		maxSummaryWordsVar:  "",
	}
	if s.maxSummaryWords > 0 {
		vars[maxSummaryWordsVar] = strconv.Itoa(s.maxSummaryWords)
	}
	return prompt.Text{Template: s.prompt}.Render(
		prompt.RenderEnv{Vars: vars},
		prompt.WithUnknownBehavior(prompt.ErrorOnUnknown),
	)
}

func (s *sessionSummarizer) buildSystemPrompt() (string, error) {
	if s.systemPrompt == "" {
		return "", nil
	}
	vars := prompt.Vars{
		maxSummaryWordsVar: "",
	}
	if s.maxSummaryWords > 0 {
		vars[maxSummaryWordsVar] = strconv.Itoa(s.maxSummaryWords)
	}
	return prompt.Text{Template: s.systemPrompt}.Render(
		prompt.RenderEnv{Vars: vars},
		prompt.WithUnknownBehavior(prompt.ErrorOnUnknown),
	)
}

func (s *sessionSummarizer) buildCacheSafeForkPrompt() (string, error) {
	vars := prompt.Vars{
		maxSummaryWordsVar: "",
	}
	if s.maxSummaryWords > 0 {
		vars[maxSummaryWordsVar] = strconv.Itoa(s.maxSummaryWords)
	}
	return prompt.Text{Template: s.cacheSafeForkPrompt}.Render(
		prompt.RenderEnv{Vars: vars},
		prompt.WithUnknownBehavior(prompt.ErrorOnUnknown),
	)
}

func (s *sessionSummarizer) buildSummaryRequest(
	ctx context.Context,
	input summaryPromptInput,
) (*model.Request, string, error) {
	if s.cacheSafeForking {
		if parent, ok := CacheSafeForkRequestFromContext(ctx); ok {
			request, err := s.buildCacheSafeForkRequest(parent)
			return request, callModeCacheSafeFork, err
		}
		log.DebugfContext(ctx, "cache-safe summary forking requested but no parent request is available; falling back to standalone summary request")
	}
	request, err := s.buildStandaloneSummaryRequest(input)
	return request, callModeStandalone, err
}

func (s *sessionSummarizer) buildStandaloneSummaryRequest(
	input summaryPromptInput,
) (*model.Request, error) {
	messages := make([]model.Message, 0, 2)
	systemPrompt, err := s.buildSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		messages = append(messages, model.NewSystemMessage(systemPrompt))
	}

	userPrompt, err := s.buildSummaryPrompt(input)
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}
	messages = append(messages, model.NewUserMessage(userPrompt))
	return newSummaryRequest(messages), nil
}

func (s *sessionSummarizer) buildCacheSafeForkRequest(
	parent *model.Request,
) (*model.Request, error) {
	request := cloneRequestForCacheSafeFork(parent)
	if request == nil {
		return nil, errors.New("parent request is nil")
	}
	if !hasSummarySourceContent(request.Messages) {
		return nil, errors.New("cache-safe summary request has no conversation content")
	}
	userPrompt, err := s.buildCacheSafeForkPrompt()
	if err != nil {
		return nil, fmt.Errorf("render cache-safe fork prompt: %w", err)
	}
	request.Messages = append(request.Messages, model.NewUserMessage(userPrompt))
	request.GenerationConfig.Stream = false
	request.StructuredOutput = nil
	return request, nil
}

type summaryPayloadCandidate struct {
	messageIndex int
	replacement  model.Message
	savedTokens  int
}

func (s *sessionSummarizer) prepareSummaryRequest(
	ctx context.Context,
	request *model.Request,
	mode string,
	input summaryPromptInput,
	budget int,
) (*model.Request, string, error) {
	if mode == callModeStandalone {
		bounded, err := s.buildBoundedStandaloneSummaryRequest(
			ctx,
			input,
			budget,
		)
		return bounded, mode, err
	}
	if err := s.ensureSummaryRequestFits(
		ctx,
		request,
		true,
		budget,
	); err == nil {
		return request, mode, nil
	}

	// Cache-safe forking is an optimization. When the parent prefix cannot be
	// made safe without dropping its last source round, fall back to a bounded
	// standalone prompt whose final user message contains the source itself.
	bounded, err := s.buildBoundedStandaloneSummaryRequest(
		ctx,
		input,
		budget,
	)
	if err != nil {
		return nil, "", err
	}
	return bounded, callModeStandalone, nil
}

func (s *sessionSummarizer) buildBoundedStandaloneSummaryRequest(
	ctx context.Context,
	input summaryPromptInput,
	budget int,
) (*model.Request, error) {
	request, err := s.buildStandaloneSummaryRequest(input)
	if err != nil {
		return nil, err
	}
	if fits, err := summaryRequestFits(ctx, request, budget); err != nil {
		return nil, err
	} else if fits {
		return request, nil
	}

	minimal, err := s.buildStandaloneSummaryRequest(summaryPromptInput{})
	if err != nil {
		return nil, err
	}
	minimalTokens, err := countSummaryRequestTokens(ctx, minimal)
	if err != nil {
		return nil, err
	}
	if minimalTokens >= budget {
		return nil, fmt.Errorf(
			"summary prompt requires %d tokens but input budget is %d",
			minimalTokens,
			budget,
		)
	}

	totalRunes := len([]rune(input.conversationText)) + len([]rune(input.previousSummary))
	best := (*model.Request)(nil)
	low, high := 1, totalRunes
	for low <= high {
		mid := low + (high-low)/2
		candidate, buildErr := s.buildStandaloneSummaryRequest(
			truncateSummaryPromptInput(input, mid),
		)
		if buildErr != nil {
			return nil, buildErr
		}
		fits, countErr := summaryRequestFits(ctx, candidate, budget)
		if countErr != nil {
			return nil, countErr
		}
		if fits {
			best = candidate
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	if best == nil {
		return nil, fmt.Errorf(
			"summary input cannot fit a non-empty source within budget %d",
			budget,
		)
	}
	return best, nil
}

func truncateSummaryConversation(runes []rune, retain int) string {
	return truncateSummaryText(runes, retain, summaryConversationOmitted)
}

func truncatePreviousSummary(runes []rune, retain int) string {
	return truncateSummaryText(runes, retain, summaryPreviousOmitted)
}

func truncateSummaryText(runes []rune, retain int, marker string) string {
	if retain >= len(runes) {
		return string(runes)
	}
	if retain <= 0 {
		return ""
	}
	markerRunes := []rune(marker)
	head := (retain + 1) / 2
	tail := retain / 2
	result := make([]rune, 0, retain+len(markerRunes))
	result = append(result, runes[:head]...)
	result = append(result, markerRunes...)
	result = append(result, runes[len(runes)-tail:]...)
	return string(result)
}

func truncateSummaryPromptInput(input summaryPromptInput, retain int) summaryPromptInput {
	conversationRunes := []rune(input.conversationText)
	previousRunes := []rune(input.previousSummary)
	total := len(conversationRunes) + len(previousRunes)
	if retain >= total {
		return input
	}
	if retain <= 0 {
		return summaryPromptInput{}
	}
	if len(previousRunes) == 0 {
		return summaryPromptInput{
			conversationText: truncateSummaryConversation(conversationRunes, retain),
		}
	}
	if len(conversationRunes) == 0 {
		return summaryPromptInput{
			previousSummary: truncatePreviousSummary(previousRunes, retain),
		}
	}

	previousRetain := 0
	conversationRetain := 1
	if retain > 1 {
		previousRetain = retain * len(previousRunes) / total
		if previousRetain < 1 {
			previousRetain = 1
		}
		conversationRetain = retain - previousRetain
	}

	return summaryPromptInput{
		conversationText: truncateSummaryConversation(conversationRunes, conversationRetain),
		previousSummary:  truncatePreviousSummary(previousRunes, previousRetain),
	}
}

func summaryRequestFits(
	ctx context.Context,
	request *model.Request,
	budget int,
) (bool, error) {
	tokens, err := countSummaryRequestTokens(ctx, request)
	if err != nil {
		return false, fmt.Errorf("count summary request tokens: %w", err)
	}
	return tokens <= budget, nil
}

func (s *sessionSummarizer) ensureSummaryRequestFits(
	ctx context.Context,
	request *model.Request,
	compactToolPayloads bool,
	budget int,
) error {
	tokens, err := countSummaryRequestTokens(ctx, request)
	if err != nil {
		return fmt.Errorf("count summary request tokens: %w", err)
	}
	if tokens <= budget {
		return nil
	}
	if !compactToolPayloads {
		return fmt.Errorf(
			"summary request input too large: estimated %d tokens exceeds budget %d",
			tokens,
			budget,
		)
	}

	// A summary never calls tools. Once the cache-safe request is already too
	// large, prefer correctness over preserving the tool schema cache key.
	request.Tools = nil
	tokens, err = countSummaryRequestTokens(ctx, request)
	if err != nil {
		return fmt.Errorf("count summary request without tools: %w", err)
	}
	if tokens <= budget {
		return nil
	}
	// Prefer dropping source rounds already represented by newer context before
	// erasing payloads from the latest complete round.
	for dropOldestSummarySourceRound(request) {
		tokens, err = countSummaryRequestTokens(ctx, request)
		if err != nil {
			return fmt.Errorf("count pruned summary request tokens: %w", err)
		}
		if tokens <= budget {
			return nil
		}
	}
	candidates, err := summaryToolPayloadCandidates(ctx, request.Messages)
	if err != nil {
		return fmt.Errorf("build summary payload candidates: %w", err)
	}
	for _, candidate := range candidates {
		request.Messages[candidate.messageIndex] = candidate.replacement
		tokens, err = countSummaryRequestTokens(ctx, request)
		if err != nil {
			return fmt.Errorf("count compacted summary request tokens: %w", err)
		}
		if tokens <= budget {
			return nil
		}
	}
	return fmt.Errorf(
		"cache-safe summary request input too large after semantic compaction: estimated %d tokens exceeds budget %d",
		tokens,
		budget,
	)
}

func (s *sessionSummarizer) summaryRequestInputBudget(
	ctx context.Context,
	request *model.Request,
) int {
	contextWindow := defaultContextThresholdFallbackWindow
	if resolved, ok := modelcontext.ResolveContextWindow(s.model); ok {
		contextWindow = resolved
	}
	budget := int(float64(contextWindow) * summaryRequestInputRatio)
	var requestWithoutTools *model.Request
	if request != nil {
		cloned := *request
		cloned.Tools = nil
		requestWithoutTools = &cloned
	}
	if providerBudget, ok := modelcontext.ResolveInputTokenBudget(
		ctx,
		s.model,
		requestWithoutTools,
	); ok && providerBudget < budget {
		budget = providerBudget
	}
	if budget < 1 {
		return 1
	}
	return budget
}

func dropOldestSummarySourceRound(request *model.Request) bool {
	if request == nil || len(request.Messages) < 3 {
		return false
	}
	sourceEnd := len(request.Messages) - 1
	rounds := summarySourceRounds(request.Messages[:sourceEnd])
	if len(rounds) <= 1 {
		return false
	}
	drop := make(map[int]struct{}, len(rounds[0]))
	for _, index := range rounds[0] {
		drop[index] = struct{}{}
	}
	messages := make([]model.Message, 0, len(request.Messages)-len(drop))
	for i, message := range request.Messages {
		if _, ok := drop[i]; ok {
			continue
		}
		messages = append(messages, message)
	}
	request.Messages = messages
	return true
}

func summarySourceRounds(messages []model.Message) [][]int {
	var rounds [][]int
	for i, message := range messages {
		if message.Role == model.RoleSystem {
			continue
		}
		if len(rounds) == 0 ||
			(message.Role == model.RoleUser && len(rounds[len(rounds)-1]) > 0) {
			rounds = append(rounds, nil)
		}
		rounds[len(rounds)-1] = append(rounds[len(rounds)-1], i)
	}
	return rounds
}

func isSummaryContextLengthError(err error, response *model.Response) bool {
	var parts []string
	if err != nil {
		parts = append(parts, err.Error())
	}
	if response != nil && response.Error != nil {
		parts = append(parts, response.Error.Type, response.Error.Message)
		if response.Error.Code != nil {
			parts = append(parts, *response.Error.Code)
		}
	}
	text := strings.ToLower(strings.Join(parts, " "))
	for _, marker := range []string{
		"context_length_exceeded",
		"context_window_exceeded",
		"context length exceeded",
		"context window exceeded",
		"maximum context length",
		"prompt is too long",
		"input is too long",
		"too many tokens",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func countSummaryRequestTokens(
	ctx context.Context,
	request *model.Request,
) (int, error) {
	if request == nil {
		return 0, nil
	}
	counter := getTokenCounter()
	tokens := 0
	if len(request.Messages) > 0 {
		var err error
		tokens, err = counter.CountTokensRange(
			ctx,
			request.Messages,
			0,
			len(request.Messages),
		)
		if err != nil {
			return 0, err
		}
	}

	toolNames := make([]string, 0, len(request.Tools))
	for name := range request.Tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	for _, name := range toolNames {
		declaration := any(name)
		if summaryTool := request.Tools[name]; summaryTool != nil {
			if declared := summaryTool.Declaration(); declared != nil {
				declaration = declared
			}
		}
		encoded, err := json.Marshal(declaration)
		if err != nil {
			return 0, fmt.Errorf("marshal tool declaration %q: %w", name, err)
		}
		toolTokens, err := counter.CountTokens(
			ctx,
			model.NewSystemMessage(string(encoded)),
		)
		if err != nil {
			return 0, fmt.Errorf("count tool declaration %q: %w", name, err)
		}
		tokens += toolTokens
	}
	return tokens, nil
}

func summaryToolPayloadCandidates(
	ctx context.Context,
	messages []model.Message,
) ([]summaryPayloadCandidate, error) {
	counter := getTokenCounter()
	candidates := make([]summaryPayloadCandidate, 0, len(messages))
	for i, message := range messages {
		replacement, ok := compactSummaryToolPayload(message)
		if !ok {
			continue
		}
		before, err := counter.CountTokens(ctx, message)
		if err != nil {
			return nil, err
		}
		after, err := counter.CountTokens(ctx, replacement)
		if err != nil {
			return nil, err
		}
		if before <= after {
			continue
		}
		candidates = append(candidates, summaryPayloadCandidate{
			messageIndex: i,
			replacement:  replacement,
			savedTokens:  before - after,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].savedTokens > candidates[j].savedTokens
	})
	return candidates, nil
}

func compactSummaryToolPayload(message model.Message) (model.Message, bool) {
	switch {
	case message.Role == model.RoleTool && message.ToolID != "":
		replacement := cloneMessageForCacheSafeFork(message)
		replacement.Content = fmt.Sprintf(
			summaryToolResultOmittedFmt,
			message.ToolName,
			message.ToolID,
		)
		replacement.ContentParts = nil
		replacement.ReasoningContent = ""
		replacement.ReasoningSignature = ""
		return replacement, true
	case len(message.ToolCalls) > 0:
		replacement := cloneMessageForCacheSafeFork(message)
		changed := false
		for i := range replacement.ToolCalls {
			if len(replacement.ToolCalls[i].Function.Arguments) == 0 {
				continue
			}
			replacement.ToolCalls[i].Function.Arguments = []byte(
				summaryToolArgumentsOmitted,
			)
			changed = true
		}
		return replacement, changed
	default:
		return model.Message{}, false
	}
}

func hasSummarySourceContent(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleSystem {
			continue
		}
		if strings.TrimSpace(message.Content) != "" ||
			len(message.ContentParts) > 0 ||
			len(message.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func newSummaryRequest(messages []model.Message) *model.Request {
	return &model.Request{
		Messages: messages,
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
}

func (s *sessionSummarizer) recordReportCall(
	ctx context.Context,
	request *model.Request,
	mode string,
) {
	report, ok := reportFromContext(ctx)
	if !ok {
		return
	}
	report.Call.Mode = mode
	report.Call.EstimatedPromptTokens = estimateRequestPromptTokens(ctx, request)
}

func (s *sessionSummarizer) recordReportUsage(
	ctx context.Context,
	response *model.Response,
	err error,
) {
	report, ok := reportFromContext(ctx)
	if !ok {
		return
	}
	report.Error = err
	if response == nil || response.Usage == nil {
		return
	}
	if !usageHasTokenCounts(response.Usage) {
		return
	}
	report.Call.PromptTokens = response.Usage.PromptTokens
	report.Call.CachedTokens = response.Usage.PromptTokensDetails.CachedTokens
}

func usageHasTokenCounts(usage *model.Usage) bool {
	if usage == nil {
		return false
	}
	return usage.PromptTokens != 0 ||
		usage.CompletionTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.PromptTokensDetails.CachedTokens != 0 ||
		usage.PromptTokensDetails.CacheReadTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationTokens != 0
}

func (s *sessionSummarizer) emitReport(ctx context.Context, err error) {
	if s.reportHook == nil {
		return
	}
	report, ok := reportFromContext(ctx)
	if !ok {
		return
	}
	report.Error = err
	cloned := cloneReport(*report)
	defer func() {
		if r := recover(); r != nil {
			log.WarnfContext(ctx, "summary report hook panic: %v", r)
		}
	}()
	s.reportHook(ctx, cloned)
}

func estimateRequestPromptTokens(ctx context.Context, request *model.Request) int {
	if request == nil || len(request.Messages) == 0 {
		return 0
	}
	counter := getTokenCounter()
	tokens, err := counter.CountTokensRange(ctx, request.Messages, 0, len(request.Messages))
	if err == nil {
		return tokens
	}
	var total int
	for _, message := range request.Messages {
		tokens, err := counter.CountTokens(ctx, message)
		if err != nil {
			return 0
		}
		total += tokens
	}
	return total
}

func (s *sessionSummarizer) runBeforeModelCallbacks(
	ctx context.Context,
	request *model.Request,
) (context.Context, <-chan *model.Response, error) {
	if s.modelCallbacks == nil {
		return ctx, nil, nil
	}

	result, err := s.modelCallbacks.RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("before model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = inheritReportContext(result.Context, ctx)
	}
	if result == nil || result.CustomResponse == nil {
		return ctx, nil, nil
	}

	customChan := make(chan *model.Response, 1)
	customChan <- result.CustomResponse
	close(customChan)
	return ctx, customChan, nil
}

func modelErrFromResponse(resp *model.Response) error {
	if resp == nil || resp.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", resp.Error.Type, resp.Error.Message)
}

func (s *sessionSummarizer) runAfterModelCallbacks(
	ctx context.Context,
	request *model.Request,
	response *model.Response,
) (context.Context, *model.Response, error) {
	if s.modelCallbacks == nil {
		return ctx, response, nil
	}

	result, err := s.modelCallbacks.RunAfterModel(
		ctx,
		&model.AfterModelArgs{
			Request:  request,
			Response: response,
			Error:    modelErrFromResponse(response),
		},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("after model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = inheritReportContext(result.Context, ctx)
	}
	if result != nil && result.CustomResponse != nil {
		response = result.CustomResponse
	}
	return ctx, response, nil
}

func inheritReportContext(next context.Context, current context.Context) context.Context {
	if next == nil {
		return current
	}
	report, ok := reportFromContext(current)
	if !ok {
		return next
	}
	if _, exists := reportFromContext(next); exists {
		return next
	}
	return ContextWithReport(next, report)
}

func (s *sessionSummarizer) collectSummaryFromResponses(
	ctx context.Context,
	request *model.Request,
	responseChan <-chan *model.Response,
	trackResponse func(resp *model.Response),
	ensureTimingInfo func(resp *model.Response),
) (context.Context, string, *model.Response, error) {
	if responseChan == nil {
		return ctx, "", nil, errors.New("model returned nil response channel")
	}

	var (
		summary   strings.Builder
		finalResp *model.Response
	)

	for {
		select {
		case <-ctx.Done():
			return ctx, "", finalResp, fmt.Errorf("summary response collection canceled: %w", ctx.Err())
		case response, ok := <-responseChan:
			if !ok {
				summaryText := strings.TrimSpace(summary.String())
				return ctx, summaryText, finalResp, nil
			}
			if trackResponse != nil {
				trackResponse(response)
			}

			var err error
			ctx, response, err = s.runAfterModelCallbacks(ctx, request, response)
			if err != nil {
				return ctx, "", finalResp, err
			}
			if ensureTimingInfo != nil {
				ensureTimingInfo(response)
			}
			if response == nil {
				continue
			}
			finalResp = response

			if response.Error != nil {
				return ctx, "", finalResp, formatResponseError(response.Error)
			}
			if len(response.Choices) > 0 {
				content := response.Choices[0].Message.Content
				if content != "" {
					summary.WriteString(content)
				}
			}
			if response.Done {
				summaryText := strings.TrimSpace(summary.String())
				return ctx, summaryText, finalResp, nil
			}
		}
	}
}
