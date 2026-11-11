//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolorder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	responseProvider = "openai.responses"
	functionToolType = "function"

	finishReasonStop      = "stop"
	finishReasonToolCalls = "tool_calls"

	incompleteReasonContentFilter = "content_filter"

	outputTypeMessage          = "message"
	outputTypeFunctionCall     = "function_call"
	outputTypeReasoning        = "reasoning"
	outputTypeToolSearchCall   = "tool_search_call"
	outputTypeToolSearchOutput = "tool_search_output"
	contentTypeOutputText      = "output_text"
	contentTypeRefusal         = "refusal"

	executionServer = "server"
	executionClient = "client"

	// extraFieldInclude is the only extra-field key consumed as an additive
	// include list. Nested and indexed paths keep the managed-field rejection.
	extraFieldInclude = "include"
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

type nativeItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Role   string `json:"role"`
	Phase  string `json:"phase"`
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	// Namespace is set on a function call to a tool inside a namespace.
	Namespace string `json:"namespace"`
	// Arguments is a JSON string on function calls and a JSON value on tool
	// search calls.
	Arguments json.RawMessage `json:"arguments"`
	// Execution is "server" or "client" on tool search items.
	Execution string            `json:"execution"`
	Tools     []json.RawMessage `json:"tools"`
	Content   []nativeContent   `json:"content"`
	Summary   []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

// functionArguments returns the JSON string arguments of a function call.
func (item nativeItem) functionArguments() (string, error) {
	var arguments string
	if err := json.Unmarshal(item.Arguments, &arguments); err != nil {
		return "", err
	}
	return arguments, nil
}

type nativeContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

func validateRequest(request *model.Request) error {
	if request == nil {
		return fmt.Errorf("openai/responses: request cannot be nil")
	}
	if len(request.Stop) > 0 {
		return fmt.Errorf("openai/responses: stop is not supported by the Responses API")
	}
	if request.PresencePenalty != nil {
		return fmt.Errorf("openai/responses: presence_penalty is not supported by the Responses API")
	}
	if request.FrequencyPenalty != nil {
		return fmt.Errorf("openai/responses: frequency_penalty is not supported by the Responses API")
	}
	if request.Logprobs != nil || request.TopLogprobs != nil {
		return fmt.Errorf("openai/responses: logprobs is not supported by this adapter")
	}
	if request.ReasoningEffort != nil && strings.TrimSpace(*request.ReasoningEffort) != "" {
		if _, ok := officialReasoningEfforts[*request.ReasoningEffort]; !ok {
			return fmt.Errorf("openai/responses: unsupported reasoning_effort %q", *request.ReasoningEffort)
		}
	}
	if err := validateExtraFields(request.ExtraFields); err != nil {
		return err
	}
	return nil
}

// buildParams converts request into API parameters. The returned tool plan
// maps function calls in the response back to request tools.
func (m *Model) buildParams(ctx context.Context, request *model.Request) (responses.ResponseNewParams, *toolPlan, error) {
	if strings.TrimSpace(m.name) == "" {
		return responses.ResponseNewParams{}, nil, fmt.Errorf("openai/responses: model name cannot be empty")
	}
	if err := validateExtraFields(m.extraFields); err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	items, err := convertMessages(request.Messages, m.name)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	plan, err := m.planTools(ctx, request)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	params, err := m.buildRequestParams(request, items, plan)
	if err != nil {
		return responses.ResponseNewParams{}, nil, err
	}
	return params, plan, nil
}

func (m *Model) buildRequestParams(
	request *model.Request,
	items responses.ResponseInputParam,
	plan *toolPlan,
) (responses.ResponseNewParams, error) {
	include, err := mergeIncludes(m.extraFields, request.ExtraFields)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model:   shared.ResponsesModel(m.name),
		Store:   openai.Bool(m.store),
		Include: include,
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
	if len(plan.params) > 0 {
		params.Tools = plan.params
	}
	if request.StructuredOutput != nil {
		format, ok := convertStructuredOutput(request.StructuredOutput)
		if !ok {
			return responses.ResponseNewParams{}, fmt.Errorf("openai/responses: unsupported structured output type %q", request.StructuredOutput.Type)
		}
		params.Text.Format = format
	}
	if err := applyToolChoice(&params, m.extraFields); err != nil {
		return responses.ResponseNewParams{}, err
	}
	if err := applyToolChoice(&params, request.ExtraFields); err != nil {
		return responses.ResponseNewParams{}, err
	}
	return params, nil
}

func validateExtraFields(fields map[string]any) error {
	for key := range fields {
		// The exact key is parsed into the typed include list. A trimmed,
		// nested, indexed, or escaped variant is not that key.
		if key == extraFieldInclude {
			continue
		}
		path := strings.TrimSpace(key)
		// sjson treats a leading colon as a path modifier and backslashes as
		// escape characters. Reject those forms instead of trying to infer the
		// root with ordinary string splitting; they can otherwise alias managed
		// fields such as "input".
		if strings.HasPrefix(path, ":") || strings.Contains(path, "\\") {
			return fmt.Errorf("openai/responses: extra field %q uses unsupported sjson path syntax", key)
		}
		root := path
		if i := strings.IndexAny(root, ".["); i >= 0 {
			root = root[:i]
		}
		switch root {
		case "model", "input", "stream", "store", "include", "tools",
			"previous_response_id", "conversation", "background":
			return fmt.Errorf("openai/responses: extra field %q cannot override managed request state", key)
		}
	}
	return nil
}

// mergeIncludes returns the include list sent on the wire. The required
// encrypted-reasoning item is first, then model-level names, then
// request-level names. Duplicates keep the earliest occurrence. Nil and empty
// additions contribute nothing. Caller-owned maps and arrays are not modified.
func mergeIncludes(modelFields, requestFields map[string]any) ([]responses.ResponseIncludable, error) {
	names := []string{string(responses.ResponseIncludableReasoningEncryptedContent)}
	seen := map[string]struct{}{names[0]: {}}
	for _, fields := range []map[string]any{modelFields, requestFields} {
		extra, err := extraIncludeNames(fields)
		if err != nil {
			return nil, err
		}
		for _, name := range extra {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	include := make([]responses.ResponseIncludable, len(names))
	for i, name := range names {
		include[i] = responses.ResponseIncludable(name)
	}
	return include, nil
}

func extraIncludeNames(fields map[string]any) ([]string, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	raw, ok := fields[extraFieldInclude]
	if !ok || raw == nil {
		return nil, nil
	}
	return parseIncludeNames(raw)
}

// parseIncludeNames accepts a JSON string array represented as []string,
// []any containing strings, or any other slice or array whose elements are
// strings. It copies the names and does not retain or modify raw.
func parseIncludeNames(raw any) ([]string, error) {
	value := reflect.ValueOf(raw)
	if !value.IsValid() {
		return nil, nil
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
	default:
		return nil, fmt.Errorf("openai/responses: include must be an array of nonempty strings")
	}
	if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
		return nil, fmt.Errorf("openai/responses: include must be an array of nonempty strings")
	}
	names := make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		elem := value.Index(i)
		if elem.Kind() == reflect.Interface {
			if elem.IsNil() {
				return nil, fmt.Errorf("openai/responses: include[%d] must be a nonempty string", i)
			}
			elem = elem.Elem()
		}
		if !elem.IsValid() || elem.Kind() != reflect.String || elem.String() == "" {
			return nil, fmt.Errorf("openai/responses: include[%d] must be a nonempty string", i)
		}
		names = append(names, elem.String())
	}
	return names, nil
}

func applyToolChoice(params *responses.ResponseNewParams, fields map[string]any) error {
	if params == nil || fields == nil {
		return nil
	}
	raw, ok := fields["tool_choice"]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case string:
		mode := responses.ToolChoiceOptions(strings.TrimSpace(value))
		if mode == "" {
			return fmt.Errorf("openai/responses: tool_choice cannot be empty")
		}
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(mode),
		}
	case map[string]any:
		typ, _ := value["type"].(string)
		name, _ := value["name"].(string)
		if (typ == "" || typ == "function") && strings.TrimSpace(name) != "" {
			params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
				OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: name},
			}
			return nil
		}
		return fmt.Errorf("openai/responses: unsupported tool_choice object")
	default:
		return fmt.Errorf("openai/responses: unsupported tool_choice value %T", raw)
	}
	return nil
}

func convertMessages(messages []model.Message, modelName string) (responses.ResponseInputParam, error) {
	items := make(responses.ResponseInputParam, 0, len(messages)*2)
	calls := newCallTracker()
	for messageIndex, msg := range messages {
		if msg.ProviderData != nil {
			converted, err := convertProviderDataMessage(msg, modelName, messageIndex, calls)
			if err != nil {
				return nil, err
			}
			items = append(items, converted...)
			continue
		}

		switch msg.Role {
		case model.RoleTool:
			callID := msg.ToolID
			if strings.TrimSpace(callID) == "" {
				return nil, fmt.Errorf("openai/responses: tool result at message %d has an empty call_id", messageIndex)
			}
			if _, ok := calls.called[callID]; !ok {
				return nil, fmt.Errorf("openai/responses: tool result %q has no preceding function call", callID)
			}
			if _, ok := calls.completed[callID]; ok {
				return nil, fmt.Errorf("openai/responses: duplicate tool result for call_id %q", callID)
			}
			text, err := messageText(msg)
			if err != nil {
				return nil, fmt.Errorf("openai/responses: tool result %q: %w", callID, err)
			}
			item := responses.ResponseInputItemParamOfFunctionCallOutput(text)
			item.OfFunctionCallOutput.CallID = openai.String(callID)
			items = append(items, item)
			calls.completed[callID] = struct{}{}
		case model.RoleAssistant:
			converted, err := convertAssistantMessage(msg, calls)
			if err != nil {
				return nil, fmt.Errorf("openai/responses: assistant message %d: %w", messageIndex, err)
			}
			items = append(items, converted...)
		case model.RoleSystem, model.RoleUser:
			item, _, err := messageInputItem(msg, roleToEasyInput(msg.Role))
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		default:
			return nil, fmt.Errorf("openai/responses: unsupported message role %q at message %d", msg.Role, messageIndex)
		}
	}
	for callID := range calls.called {
		if _, ok := calls.completed[callID]; !ok {
			return nil, fmt.Errorf("openai/responses: function call %q has no tool result", callID)
		}
	}
	for _, callID := range calls.searchOrder {
		if !calls.searches[callID] {
			return nil, fmt.Errorf("openai/responses: client tool_search_call %q has no tool_search_output", callID)
		}
	}
	return items, nil
}

func convertProviderDataMessage(msg model.Message, modelName string, messageIndex int, calls *callTracker) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam
	info := model.Info{Name: modelName, ProviderDataProtocol: responseProvider}
	if err := providerdata.ValidateMessage(msg, info); err != nil {
		return nil, fmt.Errorf("openai/responses: invalid provider data at message %d: %w", messageIndex, err)
	}
	raw := msg.ProviderData.Payload
	native, err := parseNativeItems(raw, true)
	if err != nil {
		return nil, fmt.Errorf("openai/responses: invalid provider data items at message %d: %w", messageIndex, err)
	}
	for _, item := range native {
		if msg.Role == model.RoleTool &&
			(item.Type != outputTypeToolSearchOutput || item.Execution != executionClient || item.CallID != msg.ToolID) {
			return nil, fmt.Errorf("openai/responses: tool message %d may only record the client tool_search_output for call_id %q", messageIndex, msg.ToolID)
		}
		if err := calls.track(item, msg.Role); err != nil {
			return nil, fmt.Errorf("openai/responses: message %d: %w", messageIndex, err)
		}
	}
	for _, rawItem := range rawItems(raw) {
		var item responses.ResponseInputItemUnion
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return nil, fmt.Errorf("openai/responses: decode provider data item: %w", err)
		}
		items = append(items, item.ToParam())
	}
	return items, nil
}

func convertAssistantMessage(msg model.Message, calls *callTracker) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam
	if msg.ReasoningContent != "" || msg.ReasoningSignature != "" {
		return nil, fmt.Errorf("assistant reasoning requires provider data")
	}
	if len(msg.ContentParts) > 0 || msg.Content != "" {
		item, hasContent, err := messageInputItem(msg, responses.EasyInputMessageRoleAssistant)
		if err != nil {
			return nil, err
		}
		if hasContent || len(msg.ToolCalls) == 0 {
			items = append(items, item)
		}
	}
	for _, call := range msg.ToolCalls {
		if err := validateLegacyToolCall(call); err != nil {
			return nil, err
		}
		if err := calls.track(nativeItem{Type: outputTypeFunctionCall, CallID: call.ID}, model.RoleAssistant); err != nil {
			return nil, err
		}
		items = append(items, responses.ResponseInputItemParamOfFunctionCall(
			string(call.Function.Arguments), call.ID, call.Function.Name,
		))
	}
	return items, nil
}

func validateLegacyToolCall(call model.ToolCall) error {
	if strings.TrimSpace(call.ID) == "" {
		return fmt.Errorf("function call has an empty call_id")
	}
	if strings.TrimSpace(call.Function.Name) == "" {
		return fmt.Errorf("function call %q has an empty name", call.ID)
	}
	if !isJSONObject(call.Function.Arguments) {
		return fmt.Errorf("function call %q has invalid JSON arguments", call.ID)
	}
	return nil
}

// callTracker pairs calls with their results across replayed history and
// within one model output.
type callTracker struct {
	called    map[string]struct{}
	completed map[string]struct{}
	// searches records client tool search calls by call_id and whether a
	// tool_search_output answered them. searchOrder keeps call order.
	searches    map[string]bool
	searchOrder []string
}

func newCallTracker() *callTracker {
	return &callTracker{
		called:    make(map[string]struct{}),
		completed: make(map[string]struct{}),
		searches:  make(map[string]bool),
	}
}

// track records item from a message with role. Model output records are
// assistant messages; client tool search results are tool messages.
func (t *callTracker) track(item nativeItem, role model.Role) error {
	switch item.Type {
	case outputTypeFunctionCall:
		callID := item.CallID
		if strings.TrimSpace(callID) == "" {
			return fmt.Errorf("function call has an empty call_id")
		}
		if _, ok := t.called[callID]; ok {
			return fmt.Errorf("duplicate function call call_id %q", callID)
		}
		t.called[callID] = struct{}{}
	case "function_call_output":
		callID := item.CallID
		if strings.TrimSpace(callID) == "" {
			return fmt.Errorf("function_call_output has an empty call_id")
		}
		if _, ok := t.called[callID]; !ok {
			return fmt.Errorf("function_call_output %q has no preceding function call", callID)
		}
		if _, ok := t.completed[callID]; ok {
			return fmt.Errorf("duplicate function_call_output for call_id %q", callID)
		}
		t.completed[callID] = struct{}{}
	case outputTypeToolSearchCall:
		if role != model.RoleAssistant {
			return fmt.Errorf("tool_search_call %q must be recorded in model output", item.CallID)
		}
		if item.Execution != executionClient {
			return nil
		}
		if _, ok := t.searches[item.CallID]; ok {
			return fmt.Errorf("duplicate tool_search_call call_id %q", item.CallID)
		}
		t.searches[item.CallID] = false
		t.searchOrder = append(t.searchOrder, item.CallID)
	case outputTypeToolSearchOutput:
		if item.Execution != executionClient {
			if role != model.RoleAssistant {
				return fmt.Errorf("hosted tool_search_output must be recorded in model output")
			}
			return nil
		}
		if role != model.RoleTool {
			return fmt.Errorf("client tool_search_output %q must be recorded on a tool message", item.CallID)
		}
		answered, ok := t.searches[item.CallID]
		if !ok {
			return fmt.Errorf("tool_search_output %q has no preceding tool_search_call", item.CallID)
		}
		if answered {
			return fmt.Errorf("duplicate tool_search_output for call_id %q", item.CallID)
		}
		t.searches[item.CallID] = true
	}
	return nil
}

func parseNativeItems(payload json.RawMessage, requireCompleted bool) ([]nativeItem, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, fmt.Errorf("provider data payload is empty")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("provider data payload must be an item array: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("provider data payload must be an item array")
	}
	items := make([]nativeItem, 0, len(raw))
	for i, encoded := range raw {
		var item nativeItem
		if err := json.Unmarshal(encoded, &item); err != nil {
			return nil, fmt.Errorf("decode item %d: %w", i, err)
		}
		if err := validateNativeItem(item, requireCompleted); err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		items = append(items, item)
	}
	return items, nil
}

func validateNativeItem(item nativeItem, requireCompleted bool) error {
	switch item.Type {
	case outputTypeReasoning, outputTypeMessage, outputTypeFunctionCall,
		outputTypeToolSearchCall, outputTypeToolSearchOutput:
	default:
		return fmt.Errorf("unsupported native item type %q", item.Type)
	}
	// Function-call and tool-search item IDs are optional; call_id is the
	// correlation key, and an application-built search output has no ID.
	// Preserve an absent item ID instead of inventing one during replay.
	if (item.Type == outputTypeReasoning || item.Type == outputTypeMessage) &&
		strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("%s item has an empty id", item.Type)
	}
	// The API requires status on messages, but makes it optional on reasoning
	// and function-call items. A present non-completed status is never replayable.
	if requireCompleted && item.Status != "completed" &&
		(item.Type == outputTypeMessage || item.Status != "") {
		return fmt.Errorf("%s item %q has status %q, want completed", item.Type, item.ID, item.Status)
	}
	switch item.Type {
	case outputTypeMessage:
		if item.Role != "assistant" {
			return fmt.Errorf("message item %q has role %q, want assistant", item.ID, item.Role)
		}
		for _, content := range item.Content {
			if content.Type != contentTypeOutputText && content.Type != contentTypeRefusal {
				return fmt.Errorf("message item %q has unsupported content type %q", item.ID, content.Type)
			}
		}
	case outputTypeFunctionCall:
		if strings.TrimSpace(item.CallID) == "" {
			return fmt.Errorf("function_call item %q has an empty call_id", item.ID)
		}
		if strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("function_call item %q has an empty name", item.ID)
		}
		arguments, err := item.functionArguments()
		if err != nil || !isJSONObject([]byte(arguments)) {
			return fmt.Errorf("function_call item %q has invalid JSON arguments", item.ID)
		}
	case outputTypeToolSearchCall, outputTypeToolSearchOutput:
		return validateToolSearchItem(item)
	}
	return nil
}

// validateToolSearchItem checks a hosted or client tool search item. Hosted
// items have a null call_id; client items are paired by call_id.
func validateToolSearchItem(item nativeItem) error {
	switch item.Execution {
	case executionServer:
	case executionClient:
		if strings.TrimSpace(item.CallID) == "" {
			return fmt.Errorf("client %s item %q has an empty call_id", item.Type, item.ID)
		}
	default:
		return fmt.Errorf("%s item %q has unsupported execution %q", item.Type, item.ID, item.Execution)
	}
	if item.Type == outputTypeToolSearchCall {
		if len(bytes.TrimSpace(item.Arguments)) > 0 && !json.Valid(item.Arguments) {
			return fmt.Errorf("tool_search_call item %q has invalid JSON arguments", item.ID)
		}
		if item.Execution == executionClient &&
			!isJSONObject(clientSearchArguments(item.Arguments)) {
			return fmt.Errorf("client tool_search_call item %q has invalid JSON arguments", item.ID)
		}
	}
	return nil
}

func isJSONObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Valid(data)
}

func rawItems(payload json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	_ = json.Unmarshal(payload, &items)
	return items
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
	var b strings.Builder
	b.WriteString(msg.Content)
	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil {
				b.WriteString(*part.Text)
			}
		case "":
		default:
			return "", fmt.Errorf("tool messages only support text content, got %s", part.Type)
		}
	}
	return b.String(), nil
}

func convertTools(tools map[string]tool.Tool) ([]responses.ToolUnionParam, error) {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range toolorder.SortedTools(tools) {
		decl, err := toolDeclaration(t)
		if err != nil {
			return nil, err
		}
		fn, err := functionToolParam(decl)
		if err != nil {
			return nil, err
		}
		result = append(result, responses.ToolUnionParam{OfFunction: &fn})
	}
	return result, nil
}

func toolDeclaration(t tool.Tool) (*tool.Declaration, error) {
	if t == nil {
		return nil, fmt.Errorf("openai/responses: tool cannot be nil")
	}
	decl := t.Declaration()
	if decl == nil {
		return nil, fmt.Errorf("openai/responses: tool has no declaration")
	}
	return decl, nil
}

func functionToolParam(decl *tool.Declaration) (responses.FunctionToolParam, error) {
	parameters, err := functionParameters(decl)
	if err != nil {
		return responses.FunctionToolParam{}, err
	}
	fn := responses.FunctionToolParam{
		Name:       decl.Name,
		Parameters: parameters,
		Strict:     openai.Bool(false),
	}
	if decl.Description != "" {
		fn.Description = openai.String(decl.Description)
	}
	return fn, nil
}

// functionParameters returns the JSON schema sent for a function tool.
func functionParameters(decl *tool.Declaration) (map[string]any, error) {
	parameters := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	if decl.InputSchema == nil {
		return parameters, nil
	}
	schemaBytes, err := json.Marshal(decl.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("openai/responses: marshal schema for tool %q: %w", decl.Name, err)
	}
	if err := json.Unmarshal(schemaBytes, &parameters); err != nil {
		return nil, fmt.Errorf("openai/responses: decode schema for tool %q: %w", decl.Name, err)
	}
	if typ, ok := parameters["type"].(string); ok && typ == "object" {
		if props, exists := parameters["properties"]; !exists || props == nil {
			parameters["properties"] = map[string]any{}
		}
	}
	return parameters, nil
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

func projectResponse(resp *responses.Response, modelName string) (*model.Response, error) {
	return projectRequestResponse(resp, modelName, nil)
}

// projectRequestResponse projects a completed response to the request that
// tools describes. A response that delegates client tool searches is not
// Done: the caller completes them through CompleteProviderActions.
func projectRequestResponse(resp *responses.Response, modelName string, tools *toolPlan) (*model.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("openai/responses: response is nil")
	}
	if resp.Status != responses.ResponseStatusCompleted {
		return nil, fmt.Errorf("openai/responses: response status %q is not completed", resp.Status)
	}
	projected, payload, err := projectOutput(resp.Output, tools)
	if err != nil {
		return nil, err
	}
	message := model.Message{
		Role:             model.RoleAssistant,
		Content:          projected.text,
		ReasoningContent: projected.reasoning,
		ToolCalls:        projected.toolCalls,
	}
	if len(resp.Output) > 0 {
		message.ProviderData = &model.ProviderData{
			Protocol: responseProvider,
			Model:    modelName,
			Payload:  payload,
		}
	}
	finish := finishReasonStop
	if len(projected.toolCalls) > 0 {
		finish = finishReasonToolCalls
	}
	return &model.Response{
		ID:        resp.ID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   createdUnix(resp),
		Model:     firstNonEmpty(modelName, string(resp.Model)),
		Timestamp: time.Now(),
		Done:      !projected.pendingSearch,
		Choices: []model.Choice{{
			Index:        0,
			Message:      message,
			FinishReason: &finish,
		}},
		Usage: mapUsage(resp),
	}, nil
}

type projectedOutput struct {
	text      string
	reasoning string
	toolCalls []model.ToolCall
	// pendingSearch reports a client tool search call that the caller must
	// answer before the next model step.
	pendingSearch bool
}

// projectOutput projects native output items. tools maps namespaced function
// calls back to request tools; without it, such calls cannot be projected.
func projectOutput(output []responses.ResponseOutputItemUnion, tools *toolPlan) (projectedOutput, json.RawMessage, error) {
	var out projectedOutput
	items := make([]json.RawMessage, 0, len(output))
	var text strings.Builder
	var reasoning strings.Builder
	calls := newCallTracker()
	for index, item := range output {
		raw := item.RawJSON()
		if raw == "" {
			encoded, err := json.Marshal(item)
			if err != nil {
				return out, nil, fmt.Errorf("openai/responses: marshal output item %d: %w", index, err)
			}
			raw = string(encoded)
		}
		encoded := json.RawMessage(raw)
		var native nativeItem
		if err := json.Unmarshal(encoded, &native); err != nil {
			return out, nil, fmt.Errorf("openai/responses: decode output item %d: %w", index, err)
		}
		if err := validateNativeItem(native, true); err != nil {
			return out, nil, fmt.Errorf("openai/responses: output item %d: %w", index, err)
		}
		if err := calls.track(native, model.RoleAssistant); err != nil {
			return out, nil, fmt.Errorf("openai/responses: output item %d: %w", index, err)
		}
		items = append(items, encoded)
		switch native.Type {
		case outputTypeMessage:
			for _, content := range native.Content {
				switch content.Type {
				case contentTypeOutputText:
					text.WriteString(content.Text)
				case contentTypeRefusal:
					text.WriteString(content.Refusal)
				}
			}
		case outputTypeFunctionCall:
			name, err := tools.functionName(native.Namespace, native.Name)
			if err != nil {
				return out, nil, fmt.Errorf("openai/responses: output item %d: %w", index, err)
			}
			arguments, _ := native.functionArguments()
			idx := len(out.toolCalls)
			out.toolCalls = append(out.toolCalls, model.ToolCall{
				Type:  functionToolType,
				ID:    native.CallID,
				Index: intPtr(idx),
				Function: model.FunctionDefinitionParam{
					Name:      name,
					Arguments: []byte(arguments),
				},
			})
		case outputTypeToolSearchCall:
			if native.Execution == executionClient {
				out.pendingSearch = true
			}
		case outputTypeReasoning:
			for _, part := range native.Summary {
				reasoning.WriteString(part.Text)
			}
		}
	}
	out.text = text.String()
	out.reasoning = reasoning.String()
	payload, err := json.Marshal(items)
	if err != nil {
		return out, nil, fmt.Errorf("openai/responses: marshal native output items: %w", err)
	}
	return out, payload, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intPtr(i int) *int { return &i }

func createdUnix(resp *responses.Response) int64 {
	if resp == nil {
		return time.Now().Unix()
	}
	return int64(resp.CreatedAt)
}
