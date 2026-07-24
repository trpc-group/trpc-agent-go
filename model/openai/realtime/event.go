//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package realtime provides OpenAI Realtime WebSocket event and connection
// primitives.
package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Event is an OpenAI Realtime client or server event. It preserves the full
// wire representation so new event fields can pass through without requiring
// a framework release.
type Event struct {
	eventType string
	raw       json.RawMessage
}

// ParseEvent validates and parses a Realtime JSON event.
func ParseEvent(data []byte) (Event, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Event{}, fmt.Errorf("realtime: decode event: %w", err)
	}
	if envelope.Type == "" {
		return Event{}, errors.New("realtime: event type is required")
	}
	raw := bytes.Clone(data)
	return Event{eventType: envelope.Type, raw: raw}, nil
}

// NewEvent creates an event with the supplied type and fields.
func NewEvent(eventType string, fields map[string]any) (Event, error) {
	if eventType == "" {
		return Event{}, errors.New("realtime: event type is required")
	}
	payload := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		if key == "type" {
			continue
		}
		payload[key] = value
	}
	payload["type"] = eventType
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("realtime: encode event: %w", err)
	}
	return Event{eventType: eventType, raw: raw}, nil
}

// Type returns the event's protocol type.
func (e Event) Type() string {
	return e.eventType
}

// Bytes returns an independent copy of the event's JSON wire representation.
func (e Event) Bytes() []byte {
	return bytes.Clone(e.raw)
}

// MarshalJSON implements json.Marshaler.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.eventType == "" || len(e.raw) == 0 {
		return nil, errors.New("realtime: invalid empty event")
	}
	return e.Bytes(), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (e *Event) UnmarshalJSON(data []byte) error {
	parsed, err := ParseEvent(data)
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}
