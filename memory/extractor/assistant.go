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
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	metadataKeyConversationExtraction = "conversation_extraction"
	assistantEpisodeMetadataValue     = "assistant-episode"
	assistantEpisodeToolName          = "memory_assistant_episode"
	assistantEpisodeMaxBytes          = 4096
	assistantEpisodeSourceMaxBytes    = 8192
	assistantEpisodePrefix            = "Assistant-provided conversation episode: "
	assistantEpisodeTruncationMarker  = "\n...[truncated]...\n"
)

var (
	assistantEpisodeListItemPattern = regexp.MustCompile(
		`(?m)^\s*(?:[-*]\s+|\d{1,2}[.)]\s+)\S`,
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
	assistantEpisodeNumberPattern = regexp.MustCompile(`\b\d+(?:[.,]\d+)*(?:%|\b)`)
)

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
		req := &model.Request{
			Messages: ordinaryExtractor.buildMessages(ctx, ordinaryMessages, existing),
			Tools:    filterTools(backgroundTools, ordinaryExtractor.enabledTools),
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
	userMessage, assistantMessage, ok := selectAssistantEpisodePair(messages)
	if !ok || !strongAssistantEpisodeCandidate(
		assistantEpisodeMessageText(userMessage),
		assistantEpisodeMessageText(assistantMessage),
	) {
		return ordinaryOps, nil
	}

	assistantOp, err := e.extractAssistantEpisode(
		nextCtx,
		userMessage,
		assistantMessage,
	)
	if err != nil {
		if nextCtx.Err() != nil {
			return nil, err
		}
		log.WarnfContext(
			nextCtx,
			"extractor: optional assistant episode extraction failed: %v",
			err,
		)
		return ordinaryOps, nil
	}
	if assistantOp == nil {
		return ordinaryOps, nil
	}
	return append(ordinaryOps, assistantOp), nil
}

func (e *memoryExtractor) extractAssistantEpisode(
	ctx context.Context,
	userMessage model.Message,
	assistantMessage model.Message,
) (*Operation, error) {
	userText := assistantEpisodeSourceExcerpt(
		assistantEpisodeMessageText(userMessage),
	)
	assistantText := assistantEpisodeSourceExcerpt(
		assistantEpisodeMessageText(assistantMessage),
	)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(assistantEpisodeSystemPrompt),
			model.NewUserMessage(userText),
			model.NewAssistantMessage(assistantText),
			model.NewUserMessage(
				"Extract the reusable result from the assistant response.",
			),
		},
		Tools: map[string]tool.Tool{
			assistantEpisodeToolName: assistantEpisodeTool,
		},
	}
	var assistantOp *Operation
	var parseErr error
	_, err := e.runExtractionRequest(ctx, req, func(
		callCtx context.Context,
		call model.ToolCall,
	) {
		if assistantOp != nil || parseErr != nil ||
			call.Function.Name != assistantEpisodeToolName {
			return
		}
		var args map[string]any
		if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
			parseErr = fmt.Errorf("parse assistant episode arguments: %w", err)
			return
		}
		assistantOp, parseErr = e.parseAssistantEpisode(
			callCtx,
			args,
			userText+"\n"+assistantText,
		)
	})
	if err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}
	return assistantOp, nil
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
	if err := validateAssistantEpisodeNumbers(memoryText, source); err != nil {
		return nil, err
	}
	op := &Operation{
		Type:         OperationAdd,
		Memory:       assistantEpisodePrefix + memoryText,
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

func selectAssistantEpisodePair(
	messages []model.Message,
) (model.Message, model.Message, bool) {
	for assistantIndex := len(messages) - 1; assistantIndex >= 0; assistantIndex-- {
		assistant := messages[assistantIndex]
		if !eligibleAssistantEpisodeMessage(assistant, model.RoleAssistant) {
			continue
		}
		for userIndex := assistantIndex - 1; userIndex >= 0; userIndex-- {
			user := messages[userIndex]
			if eligibleAssistantEpisodeMessage(user, model.RoleUser) {
				return user, assistant, true
			}
		}
		return model.Message{}, model.Message{}, false
	}
	return model.Message{}, model.Message{}, false
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
	tailBytes := available - headBytes
	head := text[:headBytes]
	for !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := text[len(text)-tailBytes:]
	for !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return head + assistantEpisodeTruncationMarker + tail
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

func validateAssistantEpisodeNumbers(memoryText, source string) error {
	sourceNumbers := make(map[string]struct{})
	for _, value := range assistantEpisodeNumberPattern.FindAllString(source, -1) {
		sourceNumbers[value] = struct{}{}
	}
	for _, value := range assistantEpisodeNumberPattern.FindAllString(memoryText, -1) {
		if _, ok := sourceNumbers[value]; !ok {
			return fmt.Errorf("assistant episode number %q is not present in the source", value)
		}
	}
	return nil
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

var assistantEpisodeTool = &declarationOnlyTool{
	decl: &tool.Declaration{
		Name: assistantEpisodeToolName,
		Description: "Record a reusable result supplied by the assistant as " +
			"attributed conversation history.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
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
			},
			Required:             []string{argKeyMemory},
			AdditionalProperties: false,
		},
	},
}

const assistantEpisodeSystemPrompt = `You extract durable results previously
provided by the assistant as attributed conversation episodes.

- Call memory_assistant_episode at most once.
- Preserve the user's request and the assistant's material result, including
  exact names, quantities, dates, negation, and qualifications.
- Do not rewrite assistant output as verified truth, a user preference, or a
  user action.
- Do not record filler, acknowledgments, hidden reasoning, credentials, tool
  calls, or tool results.`
