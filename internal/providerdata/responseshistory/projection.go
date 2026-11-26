//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responseshistory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Projector projects one request in order. Call Finish before each non-tool
// message and after the last message. The zero value is ready to use. Hosted
// searches are paired by order within one output and may omit call_id.
type Projector struct {
	client map[string]string
}

// ProjectMessage projects a completed Responses record using the current visible
// message, including callback edits. It leaves the original message unchanged.
// The boolean is false when the projected message has no transferable content.
func (h *Projector) ProjectMessage(message model.Message) (model.Message, bool, error) {
	data := message.ProviderData
	if data == nil {
		return model.Message{}, false, fmt.Errorf("expected Responses provider data")
	}
	if data.Protocol != Protocol {
		return model.Message{}, false, fmt.Errorf("unsupported provider data protocol %q", data.Protocol)
	}
	if err := providerdata.ValidateMessage(message, model.Info{Name: data.Model, ProviderDataProtocol: Protocol}); err != nil {
		return model.Message{}, false, err
	}
	items, err := ReadCompletedWithToolSearch(data.Payload)
	if err != nil {
		return model.Message{}, false, err
	}
	if message.Role == model.RoleTool && (len(items) != 1 || len(message.ToolCalls) != 0) {
		return model.Message{}, false, fmt.Errorf("tool message may only record the client tool_search_output for call_id %q", message.ToolID)
	}
	var hosted int
	for i, item := range items {
		if item.Type == ToolSearchCall || item.Type == ToolSearchOutput {
			if err := h.track(item, message, &hosted); err != nil {
				return model.Message{}, false, fmt.Errorf("item %d: %w", i, err)
			}
			continue
		}
		if message.Role == model.RoleTool {
			return model.Message{}, false, fmt.Errorf("tool message may only record the client tool_search_output for call_id %q", message.ToolID)
		}
	}
	if hosted != 0 {
		return model.Message{}, false, fmt.Errorf("hosted tool_search_call has no tool_search_output")
	}
	if message.Role == model.RoleTool {
		return model.Message{}, false, nil
	}
	return projectAssistantItems(message, items)
}

func (h *Projector) track(item Item, message model.Message, hosted *int) error {
	if message.Role == model.RoleTool {
		if item.Type != ToolSearchOutput || item.Execution != "client" || item.CallID != message.ToolID {
			return fmt.Errorf("tool message may only record the client tool_search_output for call_id %q", message.ToolID)
		}
		name, ok := h.client[item.CallID]
		if !ok {
			return fmt.Errorf("tool_search_output %q has no preceding tool_search_call", item.CallID)
		}
		if name != message.ProviderData.Model {
			return fmt.Errorf("tool_search_output %q belongs to a different model", item.CallID)
		}
		delete(h.client, item.CallID)
		return nil
	}
	if item.Execution == "server" {
		if item.Type == ToolSearchCall {
			*hosted++
		} else if *hosted == 0 {
			return fmt.Errorf("hosted tool_search_output has no preceding tool_search_call")
		} else {
			*hosted--
		}
		return nil
	}
	if item.Type != ToolSearchCall {
		return fmt.Errorf("client tool_search_output %q must be recorded on a tool message", item.CallID)
	}
	if _, exists := h.client[item.CallID]; exists {
		return fmt.Errorf("duplicate tool_search_call call_id %q", item.CallID)
	}
	if h.client == nil {
		h.client = make(map[string]string)
	}
	h.client[item.CallID] = message.ProviderData.Model
	return nil
}

// Finish checks that all client search calls in the current round have results.
func (h *Projector) Finish() error {
	for callID := range h.client {
		return fmt.Errorf("client tool_search_call %q has no tool_search_output", callID)
	}
	return nil
}

// projectAssistantItems projects validated message, reasoning and function call
// items onto the current assistant message, respecting edits to visible content.
func projectAssistantItems(message model.Message, items []Item) (model.Message, bool, error) {
	callIDs, nativeText, sources, err := readProjection(items)
	if err != nil {
		return model.Message{}, false, err
	}
	if len(callIDs) != len(message.ToolCalls) {
		return model.Message{}, false, fmt.Errorf("responses function calls do not match the message projection")
	}
	for i, id := range callIDs {
		if id != message.ToolCalls[i].ID {
			return model.Message{}, false, fmt.Errorf("responses function call %q does not match the message projection", id)
		}
	}
	converted := providerdata.CloneMessageProjections(message)
	converted.ProviderData = nil
	converted.ReasoningContent = ""
	converted.ReasoningSignature = ""
	// A callback can redact or otherwise replace the visible projection without
	// editing ProviderData. Never restore source URLs from that stale record.
	if visible, ok := visibleText(converted); ok && visible != "" && visible == nativeText && len(sources) > 0 {
		sources = slices.DeleteFunc(sources, func(source string) bool { return textContainsURL(visible, source) })
		if len(sources) > 0 {
			links := "\n\nSources:\n" + strings.Join(sources, "\n")
			if len(converted.ContentParts) == 0 {
				converted.Content += links
			} else {
				converted.ContentParts = append(converted.ContentParts, model.ContentPart{Type: model.ContentTypeText, Text: &links})
			}
		}
	}
	for i := range converted.ToolCalls {
		call := &converted.ToolCalls[i]
		if err := validateProjectedCall(call); err != nil {
			return model.Message{}, false, err
		}
		call.ExtraFields = nil
	}
	return converted, converted.Content != "" || len(converted.ContentParts) > 0 || len(converted.ToolCalls) > 0, nil
}

// readProjection validates the native items and collects their text
// and supported citations without changing the persisted record.
func readProjection(items []Item) ([]string, string, []string, error) {
	var callIDs []string
	var text strings.Builder
	var sources []string
	seen := make(map[string]struct{})
	for i, item := range items {
		switch item.Type {
		case FunctionCall:
			callIDs = append(callIDs, item.CallID)
		case Message:
			if len(callIDs) != 0 {
				return nil, "", nil, fmt.Errorf("responses message item %d follows a function call in the same output", i)
			}
			for _, part := range item.Content {
				switch part.Type {
				case OutputText:
					text.WriteString(part.Text)
				case Refusal:
					text.WriteString(part.Refusal)
				}
				links, err := citationURLs(part)
				if err != nil {
					return nil, "", nil, fmt.Errorf("responses message item %d has annotations that cannot be projected: %w", i, err)
				}
				for _, link := range links {
					if _, ok := seen[link]; !ok {
						seen[link] = struct{}{}
						sources = append(sources, link)
					}
				}
			}
		}
	}
	return callIDs, text.String(), sources, nil
}

func visibleText(message model.Message) (string, bool) {
	var text strings.Builder
	text.WriteString(message.Content)
	for _, part := range message.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil || part.ContentRef != nil {
			return "", false
		}
		text.WriteString(*part.Text)
	}
	return text.String(), true
}

// citationURLs accepts only URL citations with safe, absolute HTTP URLs.
// The title and offsets are not used when projecting plain source links.
func citationURLs(part Content) ([]string, error) {
	if part.Annotations == nil {
		return nil, nil
	}
	var annotations []json.RawMessage
	if err := json.Unmarshal(part.Annotations, &annotations); err != nil {
		return nil, fmt.Errorf("invalid annotations")
	}
	var links []string
	for _, raw := range annotations {
		var citation struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		if err := json.Unmarshal(raw, &citation); err != nil || part.Type != OutputText ||
			citation.Type != "url_citation" || !validCitationURL(citation.URL) {
			return nil, fmt.Errorf("unsupported or invalid annotation")
		}
		links = append(links, citation.URL)
	}
	return links, nil
}

// textContainsURL avoids treating one URL as present merely because it is
// a prefix or a substring of a longer URL in the visible text.
func textContainsURL(text, source string) bool {
	for start := 0; start < len(text); {
		i := strings.Index(text[start:], source)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(source)
		before, after := rune(0), rune(0)
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:i])
		}
		if end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		wrapped := before == '(' && after == ')'
		if wrapped && i > 1 {
			outer, _ := utf8.DecodeLastRuneInString(text[:i-1])
			wrapped = outer == ']' || !urlCharacter(outer)
		}
		if (!urlCharacter(before) && !urlCharacter(after)) || wrapped {
			return true
		}
		start = i + len(source)
	}
	return false
}

func urlCharacter(r rune) bool {
	return r != 0 && !unicode.IsSpace(r) && !unicode.IsControl(r) && !strings.ContainsRune(`<>"`, r)
}

func validCitationURL(raw string) bool {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == ""
}

// validateProjectedCall checks the call that will actually be sent. Its
// values may have been edited independently of the native payload.
func validateProjectedCall(call *model.ToolCall) error {
	if strings.TrimSpace(call.Function.Name) == "" {
		return fmt.Errorf("function call %q has an empty name", call.ID)
	}
	args := bytes.TrimSpace(call.Function.Arguments)
	if len(args) == 0 {
		call.Function.Arguments = []byte("{}")
		return nil
	}
	if !ValidArguments(args) {
		return fmt.Errorf("function call %q has invalid JSON object arguments", call.ID)
	}
	return nil
}
