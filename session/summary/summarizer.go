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
	"fmt"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
)

const (
	// lastIncludedTsKey is the key for last included timestamp in summary.
	// This key is used to store the last included timestamp in the session state.
	lastIncludedTsKey = "summary:last_included_ts"

	// conversationTextVar is the prompt variable name for conversation text (without braces).
	conversationTextVar = "conversation_text"
	// conversationTextPlaceholder is the placeholder for conversation text in templates.
	conversationTextPlaceholder = "{" + conversationTextVar + "}"
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
	if textPrompt.ValidateRequired(conversationTextVar) == nil {
		return fmt.Errorf(
			"system prompt must not include %s placeholder",
			conversationTextPlaceholder,
		)
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
		"Do not make anything up."

	if maxWords > 0 {
		basePrompt += " Please keep the summary within " + maxSummaryWordsPlaceholder + " words."
	}

	return basePrompt + "\n\n" +
		"<conversation>\n" + conversationTextPlaceholder + "\n" +
		"</conversation>\n\n" +
		"Summary:"
}

// sessionSummarizer implements the SessionSummarizer interface.
type sessionSummarizer struct {
	model           model.Model
	name            string
	prompt          string
	systemPrompt    string
	checks          []ContextChecker
	maxSummaryWords int
	skipRecentFunc  SkipRecentFunc

	preHook          PreSummaryHook
	postHook         PostSummaryHook
	hookAbortOnError bool

	// modelCallbacks configures before/after model callbacks for summarization.
	modelCallbacks *model.Callbacks

	// toolCallFormatter customizes how tool calls are formatted in summary input.
	toolCallFormatter ToolCallFormatter
	// toolResultFormatter customizes how tool results are formatted in summary input.
	toolResultFormatter ToolResultFormatter
}

// NewSummarizer creates a new session summarizer.
func NewSummarizer(m model.Model, opts ...Option) SessionSummarizer {
	s := &sessionSummarizer{
		prompt:          "",                 // Will be set after processing options.
		checks:          []ContextChecker{}, // No default checks - summarization only when explicitly configured.
		maxSummaryWords: 0,                  // 0 means no word limit.
		skipRecentFunc:  nil,                // nil means no events are skipped.
	}
	s.model = m

	for _, opt := range opts {
		opt(s)
	}

	// Set default prompt if none was provided
	if s.prompt == "" {
		s.prompt = getDefaultSummarizerPrompt(s.maxSummaryWords)
	}
	if err := validatePrompt(s.prompt); err != nil {
		log.Warnf("invalid prompt in NewSummarizer: %v", err)
	}
	if s.systemPrompt != "" {
		if err := validateSystemPrompt(s.systemPrompt); err != nil {
			log.Warnf("invalid system prompt in NewSummarizer: %v", err)
		}
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
	if sess == nil || len(sess.Events) == 0 {
		return false
	}
	if len(s.filterEventsForSummary(sess.Events)) == 0 {
		return false
	}

	checkSess := s.buildCheckSession(sess)

	for _, check := range s.checks {
		if !check(ctx, checkSess) {
			return false
		}
	}
	return true
}

// Summarize generates a summary without modifying the session events.
func (s *sessionSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if s.model == nil {
		return "", fmt.Errorf("no model configured for summarization for session %s", sess.ID)
	}
	if len(sess.Events) == 0 {
		return "", fmt.Errorf("no events to summarize for session %s (events=0)", sess.ID)
	}

	// Extract conversation text from events. Use filtered events for summarization
	// to skip recent events while ensuring proper context.
	eventsToSummarize := s.filterEventsForSummary(sess.Events)

	conversationText := s.extractConversationText(eventsToSummarize)
	if s.preHook != nil {
		hookCtx := &PreSummaryHookContext{
			Ctx:     ctx,
			Session: sess,
			Events:  eventsToSummarize,
			Text:    conversationText,
		}
		hookErr := s.preHook(hookCtx)
		if hookErr != nil && s.hookAbortOnError {
			return "", fmt.Errorf("pre-summary hook failed: %w", hookErr)
		}
		if hookErr == nil {
			// Propagate context modifications from pre-hook to subsequent operations.
			if hookCtx.Ctx != nil {
				ctx = hookCtx.Ctx
			}
			if hookCtx.Text != "" {
				conversationText = hookCtx.Text
			} else if len(hookCtx.Events) > 0 {
				conversationText = s.extractConversationText(hookCtx.Events)
			}
		}
	}
	if conversationText == "" {
		return "", fmt.Errorf("no conversation text extracted for session %s (events=%d)", sess.ID, len(eventsToSummarize))
	}

	ctx, summaryText, err := s.generateSummary(ctx, sess, conversationText)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary for session %s: %w", sess.ID, err)
	}

	s.recordLastIncludedTimestamp(sess, eventsToSummarize)

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

// recordLastIncludedTimestamp records the last included timestamp in the session state.
func (s *sessionSummarizer) recordLastIncludedTimestamp(sess *session.Session, events []event.Event) {
	if sess == nil || len(events) == 0 {
		return
	}
	last := events[len(events)-1].Timestamp.UTC()
	sess.SetState(lastIncludedTsKey, []byte(last.Format(time.RFC3339Nano)))
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
	primary := filterPrimaryEvents(filtered, checkSess.AppName)
	checkSess.SetState(
		tokenThresholdConversationTextStateKey,
		[]byte(s.extractConversationText(primary)),
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
// replaced with the extracted conversation when generating the summary.
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

// generateSummary generates a summary using the LLM model.
func (s *sessionSummarizer) generateSummary(
	ctx context.Context,
	sess *session.Session,
	conversationText string,
) (context.Context, string, error) {
	// Telemetry trace + metrics tracking (aligned with toolsearch/llm_search.go).
	var err error
	modelName := ""
	if s.model != nil {
		modelName = s.model.Info().Name
	}
	_, span := trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(modelName))
	defer span.End()

	request, err := s.buildSummaryRequest(conversationText)
	if err != nil {
		return ctx, "", fmt.Errorf("failed to build summary request: %w", err)
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
		ensureTimingInfo(resp)
	}

	var finalResp *model.Response
	defer func() {
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

	ctx, responseChan, cbErr := s.runBeforeModelCallbacks(ctx, request)
	if cbErr != nil {
		err = cbErr
		return ctx, "", cbErr
	}

	if responseChan == nil {
		responseChan, cbErr = s.model.GenerateContent(ctx, request)
		if cbErr != nil {
			err = fmt.Errorf("failed to generate summary: %w", cbErr)
			return ctx, "", err
		}
	}

	var summaryText string
	ctx, summaryText, finalResp, cbErr = s.collectSummaryFromResponses(
		ctx,
		request,
		responseChan,
		trackResponse,
		ensureTimingInfo,
	)
	if cbErr != nil {
		err = cbErr
		return ctx, "", cbErr
	}
	if summaryText == "" {
		err = fmt.Errorf("generated empty summary (input_chars=%d)", len(conversationText))
		return ctx, "", err
	}
	return ctx, summaryText, nil
}

func (s *sessionSummarizer) buildSummaryPrompt(conversationText string) (string, error) {
	vars := prompt.Vars{
		conversationTextVar: conversationText,
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

func (s *sessionSummarizer) buildSummaryRequest(conversationText string) (*model.Request, error) {
	messages := make([]model.Message, 0, 2)
	systemPrompt, err := s.buildSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		messages = append(messages, model.NewSystemMessage(systemPrompt))
	}

	userPrompt, err := s.buildSummaryPrompt(conversationText)
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}
	messages = append(messages, model.NewUserMessage(userPrompt))
	return newSummaryRequest(messages), nil
}

func newSummaryRequest(messages []model.Message) *model.Request {
	return &model.Request{
		Messages: messages,
		GenerationConfig: model.GenerationConfig{
			Stream: false, // Non-streaming for summarization.
		},
	}
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
		ctx = result.Context
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
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		response = result.CustomResponse
	}
	return ctx, response, nil
}

func (s *sessionSummarizer) collectSummaryFromResponses(
	ctx context.Context,
	request *model.Request,
	responseChan <-chan *model.Response,
	trackResponse func(resp *model.Response),
	ensureTimingInfo func(resp *model.Response),
) (context.Context, string, *model.Response, error) {
	var (
		summary   strings.Builder
		finalResp *model.Response
	)

	for response := range responseChan {
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
			break
		}
	}

	summaryText := strings.TrimSpace(summary.String())
	return ctx, summaryText, finalResp, nil
}
