//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolorder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	finishReasonStop          = "stop"
	finishReasonLength        = "length"
	finishReasonContentFilter = "content_filter"
	finishReasonToolCalls     = "tool_calls"

	functionToolType = "function"

	incompleteReasonMaxOutputTokens = "max_output_tokens"
	incompleteReasonContentFilter   = "content_filter"

	outputTypeMessage      = "message"
	outputTypeFunctionCall = "function_call"
	outputTypeReasoning    = "reasoning"
	contentTypeOutputText  = "output_text"
)

var officialReasoningEfforts = map[string]shared.ReasoningEffort{
	string(shared.ReasoningEffortNone):    shared.ReasoningEffortNone,
	string(shared.ReasoningEffortMinimal): shared.ReasoningEffortMinimal,
	string(shared.ReasoningEffortLow):     shared.ReasoningEffortLow,
	string(shared.ReasoningEffortMedium):  shared.ReasoningEffortMedium,
	string(shared.ReasoningEffortHigh):    shared.ReasoningEffortHigh,
	string(shared.ReasoningEffortXhigh):   shared.ReasoningEffortXhigh,
	string(shared.ReasoningEffortMax):     shared.ReasoningEffortMax,
}

func validateRequest(request *model.Request) error {
	if request == nil {
		return fmt.Errorf("openai/responses: request cannot be nil")
	}
	if len(request.Stop) > 0 {
		return fmt.Errorf("openai/responses: stop is not supported by the official Responses API")
	}
	if request.PresencePenalty != nil {
		return fmt.Errorf("openai/responses: presence_penalty is not supported by the official Responses API")
	}
	if request.FrequencyPenalty != nil {
		return fmt.Errorf("openai/responses: frequency_penalty is not supported by the official Responses API")
	}
	if request.Logprobs != nil || request.TopLogprobs != nil {
		return fmt.Errorf("openai/responses: logprobs is not supported by the official Responses API")
	}
	if request.ReasoningEffort != nil && strings.TrimSpace(*request.ReasoningEffort) != "" {
		if _, ok := officialReasoningEfforts[*request.ReasoningEffort]; !ok {
			return fmt.Errorf(
				"openai/responses: unsupported reasoning_effort %q; want one of none, minimal, low, medium, high, xhigh, max",
				*request.ReasoningEffort,
			)
		}
	}
	return nil
}

func (m *Model) buildParams(request *model.Request) (responses.ResponseNewParams, error) {
	items, err := convertMessages(request.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model: m.name,
		Store: openai.Bool(m.store),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: items,
		},
	}
	if request.MaxTokens != nil {
		params.MaxOutputTokens = openai.Int(int64(*request.MaxTokens))
	}
	if request.Temperature != nil {
		params.Temperature = openai.Float(*request.Temperature)
	}
	if request.TopP != nil {
		params.TopP = openai.Float(*request.TopP)
	}
	if request.ReasoningEffort != nil && strings.TrimSpace(*request.ReasoningEffort) != "" {
		params.Reasoning.Effort = officialReasoningEfforts[*request.ReasoningEffort]
	}
	if len(request.Tools) > 0 {
		params.Tools = convertTools(request.Tools)
	}
	if format, ok := convertStructuredOutput(request.StructuredOutput); ok {
		params.Text.Format = format
	}
	applyToolChoice(&params, m.extraFields)
	if request != nil {
		applyToolChoice(&params, request.ExtraFields)
	}
	return params, nil
}

func applyToolChoice(params *responses.ResponseNewParams, extra map[string]any) {
	if params == nil || extra == nil {
		return
	}
	raw, ok := extra["tool_choice"]
	if !ok {
		return
	}
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return
		}
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptions(v)),
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		name, _ := v["name"].(string)
		if (typ == "" || typ == "function") && name != "" {
			params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
				OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: name},
			}
		}
	}
}

func convertMessages(messages []model.Message) (responses.ResponseInputParam, error) {
	items := make(responses.ResponseInputParam, 0, len(messages)*2)
	reasoningIndex := 0
	for _, msg := range messages {
		switch msg.Role {
		case model.RoleTool:
			text, err := messageText(msg)
			if err != nil {
				return nil, err
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(
				msg.ToolID,
				text,
			))
		case model.RoleAssistant:
			if strings.TrimSpace(msg.ReasoningContent) != "" {
				items = append(items, responses.ResponseInputItemParamOfReasoning(
					fmt.Sprintf("rs_replay_%d", reasoningIndex),
					[]responses.ResponseReasoningItemSummaryParam{{
						Text: msg.ReasoningContent,
					}},
				))
				reasoningIndex++
			}
			for _, tc := range msg.ToolCalls {
				callID := tc.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d", len(items))
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(
					string(tc.Function.Arguments),
					callID,
					tc.Function.Name,
				))
			}
			item, hasContent, err := messageInputItem(msg, responses.EasyInputMessageRoleAssistant)
			if err != nil {
				return nil, err
			}
			if hasContent || len(msg.ToolCalls) == 0 {
				items = append(items, item)
			}
		default:
			item, _, err := messageInputItem(msg, roleToEasyInput(msg.Role))
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func roleToEasyInput(role model.Role) responses.EasyInputMessageRole {
	switch role {
	case model.RoleSystem:
		return responses.EasyInputMessageRoleSystem
	case model.RoleAssistant:
		return responses.EasyInputMessageRoleAssistant
	default:
		return responses.EasyInputMessageRoleUser
	}
}

func messageInputItem(
	msg model.Message,
	role responses.EasyInputMessageRole,
) (responses.ResponseInputItemUnionParam, bool, error) {
	if len(msg.ContentParts) == 0 {
		return responses.ResponseInputItemParamOfMessage(msg.Content, role), msg.Content != "", nil
	}
	parts, err := convertContentParts(msg)
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, false, err
	}
	return responses.ResponseInputItemParamOfMessage(parts, role), len(parts) > 0, nil
}

func convertContentParts(msg model.Message) (responses.ResponseInputMessageContentListParam, error) {
	parts := make(responses.ResponseInputMessageContentListParam, 0, len(msg.ContentParts)+1)
	if msg.Content != "" {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(msg.Content))
	}
	for _, part := range msg.ContentParts {
		converted, err := convertContentPart(part)
		if err != nil {
			return nil, err
		}
		if converted != nil {
			parts = append(parts, *converted)
		}
	}
	return parts, nil
}

func convertContentPart(part model.ContentPart) (*responses.ResponseInputContentUnionParam, error) {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text == nil {
			return nil, nil
		}
		p := responses.ResponseInputContentParamOfInputText(*part.Text)
		return &p, nil
	case model.ContentTypeImage:
		p, err := convertImagePart(part.Image)
		if err != nil {
			return nil, err
		}
		return &p, nil
	case model.ContentTypeFile:
		p, err := convertFilePart(part.File)
		if err != nil {
			return nil, err
		}
		return &p, nil
	case model.ContentTypeAudio, model.ContentTypeVideo:
		return nil, fmt.Errorf("openai/responses: %s content parts are not supported", part.Type)
	default:
		if part.Type == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("openai/responses: unsupported content part type %q", part.Type)
	}
}

func convertImagePart(image *model.Image) (responses.ResponseInputContentUnionParam, error) {
	if image == nil {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("openai/responses: image content part is empty")
	}
	url := imageToURLOrBase64(image)
	if url == "" {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("openai/responses: image content part has no url or data")
	}
	detail := responses.ResponseInputImageDetailAuto
	switch strings.ToLower(strings.TrimSpace(image.Detail)) {
	case "low":
		detail = responses.ResponseInputImageDetailLow
	case "high":
		detail = responses.ResponseInputImageDetailHigh
	case "original":
		detail = responses.ResponseInputImageDetailOriginal
	}
	p := responses.ResponseInputContentParamOfInputImage(detail)
	p.OfInputImage.ImageURL = openai.String(url)
	return p, nil
}

func convertFilePart(file *model.File) (responses.ResponseInputContentUnionParam, error) {
	if file == nil {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("openai/responses: file content part is empty")
	}
	p := responses.ResponseInputFileParam{}
	if strings.TrimSpace(file.FileID) != "" {
		p.FileID = openai.String(file.FileID)
	}
	if strings.TrimSpace(file.URL) != "" {
		p.FileURL = openai.String(file.URL)
	}
	if strings.TrimSpace(file.Name) != "" {
		p.Filename = openai.String(file.Name)
	}
	if len(file.Data) > 0 {
		mime := strings.TrimSpace(file.MimeType)
		if mime == "" {
			mime = "application/octet-stream"
		}
		p.FileData = openai.String("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(file.Data))
	}
	if strings.TrimSpace(file.FileID) == "" && strings.TrimSpace(file.URL) == "" && len(file.Data) == 0 {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("openai/responses: file content part has no file_id, url, or data")
	}
	return responses.ResponseInputContentUnionParam{OfInputFile: &p}, nil
}

func imageToURLOrBase64(image *model.Image) string {
	if image == nil {
		return ""
	}
	if strings.TrimSpace(image.URL) != "" {
		return image.URL
	}
	if len(image.Data) == 0 {
		return ""
	}
	format := strings.TrimSpace(image.Format)
	if format == "" {
		format = "png"
	}
	if strings.HasPrefix(format, "image/") {
		return "data:" + format + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func messageText(msg model.Message) (string, error) {
	if msg.Content != "" {
		if err := rejectNonTextParts(msg.ContentParts); err != nil {
			return "", err
		}
		return msg.Content, nil
	}
	var b strings.Builder
	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil {
				b.WriteString(*part.Text)
			}
		case "":
		default:
			return "", fmt.Errorf("openai/responses: tool messages only support text content, got %s", part.Type)
		}
	}
	return b.String(), nil
}

func rejectNonTextParts(parts []model.ContentPart) error {
	for _, part := range parts {
		if part.Type != model.ContentTypeText && part.Type != "" {
			return fmt.Errorf("openai/responses: tool messages only support text content, got %s", part.Type)
		}
	}
	return nil
}

func convertTools(tools map[string]tool.Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range toolorder.SortedTools(tools) {
		decl := t.Declaration()
		if decl == nil {
			continue
		}
		parameters := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		if decl.InputSchema != nil {
			schemaBytes, err := json.Marshal(decl.InputSchema)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(schemaBytes, &parameters); err != nil {
				continue
			}
			if typ, ok := parameters["type"].(string); ok && typ == "object" {
				if props, exists := parameters["properties"]; !exists || props == nil {
					parameters["properties"] = map[string]any{}
				}
			}
		}
		fn := responses.FunctionToolParam{
			Name:       decl.Name,
			Parameters: parameters,
			Strict:     openai.Bool(false),
		}
		if decl.Description != "" {
			fn.Description = openai.String(decl.Description)
		}
		result = append(result, responses.ToolUnionParam{OfFunction: &fn})
	}
	return result
}

func convertStructuredOutput(out *model.StructuredOutput) (responses.ResponseFormatTextConfigUnionParam, bool) {
	if out == nil || out.Type != model.StructuredOutputJSONSchema || out.JSONSchema == nil {
		return responses.ResponseFormatTextConfigUnionParam{}, false
	}
	js := out.JSONSchema
	format := &responses.ResponseFormatTextJSONSchemaConfigParam{
		Name:   js.Name,
		Schema: js.Schema,
		Strict: openai.Bool(js.Strict),
	}
	if js.Description != "" {
		format.Description = openai.String(js.Description)
	}
	if format.Name == "" {
		format.Name = "structured_output"
	}
	if format.Schema == nil {
		format.Schema = map[string]any{}
	}
	return responses.ResponseFormatTextConfigUnionParam{OfJSONSchema: format}, true
}

func projectResponse(id, modelName string, created int64, resp *responses.Response, partial bool, done bool, delta string, deltaReasoning string) *model.Response {
	projected := projectOutput(resp)
	finish := finishReasonFrom(resp, len(projected.toolCalls) > 0)
	out := &model.Response{
		ID:        firstNonEmpty(id, responseID(resp)),
		Object:    objectType(partial),
		Created:   created,
		Model:     firstNonEmpty(modelName, responseModel(resp)),
		Timestamp: time.Now(),
		Done:      done,
		IsPartial: partial,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:             model.RoleAssistant,
				Content:          projected.text,
				ReasoningContent: projected.reasoning,
				ToolCalls:        projected.toolCalls,
			},
			Delta: model.Message{
				Role:             model.RoleAssistant,
				Content:          delta,
				ReasoningContent: deltaReasoning,
			},
			FinishReason: finish,
		}},
		Usage: mapUsage(resp),
	}
	if done && !partial {
		out.Choices[0].Delta = model.Message{}
	}
	return out
}

type projectedOutput struct {
	text      string
	reasoning string
	toolCalls []model.ToolCall
}

func projectOutput(resp *responses.Response) projectedOutput {
	var out projectedOutput
	if resp == nil {
		return out
	}
	var text strings.Builder
	var reasoning strings.Builder
	for _, item := range resp.Output {
		switch item.Type {
		case outputTypeMessage:
			for _, content := range item.Content {
				if content.Type == contentTypeOutputText {
					text.WriteString(content.Text)
				}
			}
		case outputTypeFunctionCall:
			idx := len(out.toolCalls)
			args := item.Arguments.OfString
			if args == "" {
				args = item.AsFunctionCall().Arguments
			}
			name := item.Name
			callID := item.CallID
			if name == "" || callID == "" {
				call := item.AsFunctionCall()
				if name == "" {
					name = call.Name
				}
				if callID == "" {
					callID = call.CallID
				}
				if args == "" {
					args = call.Arguments
				}
			}
			out.toolCalls = append(out.toolCalls, model.ToolCall{
				Type:  functionToolType,
				ID:    callID,
				Index: intPtr(idx),
				Function: model.FunctionDefinitionParam{
					Name:      name,
					Arguments: []byte(args),
				},
			})
		case outputTypeReasoning:
			rs := item.AsReasoning()
			for _, part := range rs.Summary {
				reasoning.WriteString(part.Text)
			}
			if reasoning.Len() == 0 {
				for _, part := range rs.Content {
					reasoning.WriteString(part.Text)
				}
			}
		}
	}
	out.text = text.String()
	out.reasoning = reasoning.String()
	return out
}

func finishReasonFrom(resp *responses.Response, hasToolCalls bool) *string {
	if resp == nil {
		return nil
	}
	switch resp.Status {
	case "incomplete":
		reason := finishReasonLength
		if resp.IncompleteDetails.Reason == incompleteReasonContentFilter {
			reason = finishReasonContentFilter
		}
		return &reason
	case "completed":
		reason := finishReasonStop
		if hasToolCalls {
			reason = finishReasonToolCalls
		}
		return &reason
	default:
		if hasToolCalls {
			reason := finishReasonToolCalls
			return &reason
		}
		return nil
	}
}

func mapUsage(resp *responses.Response) *model.Usage {
	if resp == nil {
		return nil
	}
	u := resp.Usage
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	return &model.Usage{
		PromptTokens:     int(u.InputTokens),
		CompletionTokens: int(u.OutputTokens),
		TotalTokens:      int(u.TotalTokens),
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: int(u.InputTokensDetails.CachedTokens),
		},
		CompletionTokensDetails: model.CompletionTokensDetails{
			ReasoningTokens: int(u.OutputTokensDetails.ReasoningTokens),
		},
	}
}

func responseID(resp *responses.Response) string {
	if resp == nil {
		return ""
	}
	return resp.ID
}

func responseModel(resp *responses.Response) string {
	if resp == nil {
		return ""
	}
	return string(resp.Model)
}

func objectType(partial bool) string {
	if partial {
		return model.ObjectTypeChatCompletionChunk
	}
	return model.ObjectTypeChatCompletion
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func intPtr(i int) *int {
	return &i
}

func createdUnix(resp *responses.Response) int64 {
	if resp == nil {
		return time.Now().Unix()
	}
	return int64(resp.CreatedAt)
}
