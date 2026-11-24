//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package responseshistory reads completed native Responses output without
// depending on an SDK or applying a target adapter's conversion rules.
package responseshistory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol identifies Responses records stored in model.ProviderData.
const Protocol = "openai.responses"

// Native output item and content types supported by the adapters.
const (
	Message          = "message"
	FunctionCall     = "function_call"
	Reasoning        = "reasoning"
	ToolSearchCall   = "tool_search_call"
	ToolSearchOutput = "tool_search_output"
	OutputText       = "output_text"
	Refusal          = "refusal"
)

// Item exposes the fields used to interpret an output item. Raw retains the
// original JSON, including fields that are only meaningful to the producer.
type Item struct {
	Raw       json.RawMessage   `json:"-"`
	Type      string            `json:"type"`
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	Role      string            `json:"role"`
	Phase     string            `json:"phase"`
	CallID    string            `json:"call_id"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Execution string            `json:"execution"`
	Tools     []json.RawMessage `json:"tools"`
	Arguments json.RawMessage   `json:"arguments"`
	Content   []Content         `json:"content"`
	Summary   []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

// Content contains a message's text or refusal and its original annotations.
type Content struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Refusal     string          `json:"refusal"`
	Annotations json.RawMessage `json:"annotations"`
}

// ReadCompleted reads message, reasoning, and function call items in order.
// Consumers must opt into tool search so they can handle paired search results.
func ReadCompleted(payload json.RawMessage) ([]Item, error) {
	return readCompleted(payload, false)
}

// ReadCompletedWithToolSearch also accepts native tool search calls and outputs.
// The consumer pairs items across messages according to its target protocol.
func ReadCompletedWithToolSearch(payload json.RawMessage) ([]Item, error) {
	return readCompleted(payload, true)
}

func readCompleted(payload json.RawMessage, toolSearch bool) ([]Item, error) {
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
	items := make([]Item, len(raw))
	for i, encoded := range raw {
		item, err := ReadCompletedItem(encoded)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		if !toolSearch && (item.Type == ToolSearchCall || item.Type == ToolSearchOutput) {
			return nil, fmt.Errorf("item %d: unsupported native item type %q", i, item.Type)
		}
		items[i] = item
	}
	return items, nil
}

// ReadCompletedItem decodes and validates one completed output item. Its Raw
// field refers to encoded, which the caller must treat as read-only.
func ReadCompletedItem(encoded json.RawMessage) (Item, error) {
	var item Item
	if err := json.Unmarshal(encoded, &item); err != nil {
		return Item{}, fmt.Errorf("decode: %w", err)
	}
	if err := item.validate(); err != nil {
		return Item{}, err
	}
	item.Raw = encoded
	return item, nil
}

func (item *Item) validate() error {
	switch item.Type {
	case Message, Reasoning, FunctionCall, ToolSearchCall, ToolSearchOutput:
	default:
		return fmt.Errorf("unsupported native item type %q", item.Type)
	}
	// Only reasoning and message items require a native item ID.
	if (item.Type == Message || item.Type == Reasoning) && strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("%s item has an empty id", item.Type)
	}
	// Reasoning and function calls may omit status. Messages must be completed.
	if item.Status != "completed" && (item.Type == Message || item.Status != "") {
		return fmt.Errorf("%s item %q has status %q, want completed", item.Type, item.ID, item.Status)
	}
	switch item.Type {
	case Message:
		if item.Role != "assistant" {
			return fmt.Errorf("message item %q has role %q, want assistant", item.ID, item.Role)
		}
		for _, content := range item.Content {
			if content.Type != OutputText && content.Type != Refusal {
				return fmt.Errorf("message item %q has unsupported content type %q", item.ID, content.Type)
			}
		}
	case FunctionCall:
		if strings.TrimSpace(item.CallID) == "" {
			return fmt.Errorf("function_call item %q has an empty call_id", item.ID)
		}
		if strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("function_call item %q has an empty name", item.ID)
		}
		args, err := item.FunctionArguments()
		if err != nil || !ValidArguments([]byte(args)) {
			return fmt.Errorf("function_call item %q has invalid JSON arguments", item.ID)
		}
	case ToolSearchCall, ToolSearchOutput:
		return item.validateToolSearch()
	}
	return nil
}

func (item *Item) validateToolSearch() error {
	switch item.Execution {
	case "server":
	case "client":
		if strings.TrimSpace(item.CallID) == "" {
			return fmt.Errorf("client %s item %q has an empty call_id", item.Type, item.ID)
		}
		if item.Type == ToolSearchCall && !ValidArguments(item.SearchArguments()) {
			return fmt.Errorf("client tool_search_call item %q has invalid JSON arguments", item.ID)
		}
	default:
		return fmt.Errorf("%s item %q has unsupported execution %q", item.Type, item.ID, item.Execution)
	}
	return nil
}

// FunctionArguments decodes the JSON string carried by a native function call.
func (item Item) FunctionArguments() (string, error) {
	var arguments string
	err := json.Unmarshal(item.Arguments, &arguments)
	return arguments, err
}

// SearchArguments returns a client search's JSON arguments. Some providers
// encode the object as a JSON string.
func (item Item) SearchArguments() []byte {
	raw := bytes.TrimSpace(item.Arguments)
	if len(raw) > 0 && raw[0] == '"' {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err == nil {
			return []byte(decoded)
		}
	}
	return raw
}

// ValidArguments reports whether a function call contains a JSON object.
func ValidArguments(arguments []byte) bool {
	arguments = bytes.TrimSpace(arguments)
	return len(arguments) > 0 && arguments[0] == '{' && json.Valid(arguments)
}
