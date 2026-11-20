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
	Message      = "message"
	FunctionCall = "function_call"
	Reasoning    = "reasoning"
	OutputText   = "output_text"
	Refusal      = "refusal"
)

// Item exposes the fields used to interpret an output item. Raw retains the
// original JSON, including fields that are only meaningful to the producer.
type Item struct {
	Raw       json.RawMessage `json:"-"`
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Role      string          `json:"role"`
	Phase     string          `json:"phase"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Content   []Content       `json:"content"`
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

// ReadCompleted reads an array of completed output items in their original order.
func ReadCompleted(payload json.RawMessage) ([]Item, error) {
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
			return nil, fmt.Errorf("decode item %d: %w", i, err)
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
		return Item{}, err
	}
	if err := item.validate(); err != nil {
		return Item{}, err
	}
	item.Raw = encoded
	return item, nil
}

func (item *Item) validate() error {
	switch item.Type {
	case Message, Reasoning, FunctionCall:
	default:
		return fmt.Errorf("unsupported native item type %q", item.Type)
	}
	// Function calls correlate by call_id and may omit their native item ID.
	if item.Type != FunctionCall && strings.TrimSpace(item.ID) == "" {
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
		if !ValidArguments([]byte(item.Arguments)) {
			return fmt.Errorf("function_call item %q has invalid JSON arguments", item.ID)
		}
	}
	return nil
}

// ValidArguments reports whether a function call contains a JSON object.
func ValidArguments(arguments []byte) bool {
	arguments = bytes.TrimSpace(arguments)
	return len(arguments) > 0 && arguments[0] == '{' && json.Valid(arguments)
}
