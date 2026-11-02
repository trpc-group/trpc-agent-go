//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	replayVersionV1 = 1
	replayVersion   = 2
)

// ReplayData stores a provider-native completion with the identity of its
// producer. The provider owns the payload schema. Replay data determines the
// next model request. Content, content parts, and reasoning fields are editable
// display projections, and ToolCalls determine the current tool execution. A
// message carrying ReplayData must remain an assistant message.
//
// Use Message.SetReplayData to create a record and Message.ReplayDataFor to read
// it. Both methods copy the opaque payload at their boundaries. The zero value
// is invalid; a nil Message.Replay means no native replay data.
type ReplayData struct {
	provider string
	model    string
	payload  json.RawMessage
}

type replayJSON struct {
	Version  int             `json:"version"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Payload  json.RawMessage `json:"payload"`
}

// SetReplayData binds a copy of payload to the current assistant message.
// Provider identifies the wire protocol, and modelName is the configured model
// identity used for later requests, rather than a provider-resolved alias.
// Payload must be a JSON object or array understood by that provider.
//
// Content, content parts, and reasoning fields remain independently editable
// after binding. ReplayDataFor returns the native payload used for subsequent
// model requests, while the message's ToolCalls continue to determine current
// tool execution. The message must remain an assistant message. On error, the
// receiver and any existing replay record are unchanged.
func (m *Message) SetReplayData(provider, modelName string, payload json.RawMessage) error {
	if m == nil {
		return fmt.Errorf("cannot bind replay data to a nil message")
	}
	if m.Role != RoleAssistant {
		return fmt.Errorf("replay data requires an assistant message")
	}
	data := &ReplayData{
		provider: provider,
		model:    modelName,
		payload:  append(json.RawMessage(nil), payload...),
	}
	if err := data.validate(); err != nil {
		return err
	}
	m.Replay = data
	return nil
}

// ReplayDataFor returns a copy of the native payload for the named protocol and
// configured model. A message without Replay returns nil, nil. A producer
// mismatch or corrupt record returns an error.
func (m Message) ReplayDataFor(provider, modelName string) (json.RawMessage, error) {
	if m.Replay == nil {
		return nil, nil
	}
	if m.Role != RoleAssistant {
		return nil, fmt.Errorf("replay data requires an assistant message")
	}
	if err := m.Replay.validate(); err != nil {
		return nil, err
	}
	if m.Replay.provider != provider || m.Replay.model != modelName {
		return nil, fmt.Errorf("replay data belongs to protocol %q and model %q, not %q and %q",
			m.Replay.provider, m.Replay.model, provider, modelName)
	}
	return append(json.RawMessage(nil), m.Replay.payload...), nil
}

// MarshalJSON writes the current version of the native replay record. Invalid
// or zero-valued records return an error instead of persisting unusable state.
func (r ReplayData) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(replayJSON{
		Version:  replayVersion,
		Provider: r.provider,
		Model:    r.model,
		Payload:  r.payload,
	})
}

// UnmarshalJSON restores a versioned native replay record. It reads v1 records
// for persistence compatibility and writes them back as v2. Unsupported
// versions and invalid records fail rather than degrading silently to text-only
// history.
func (r *ReplayData) UnmarshalJSON(data []byte) error {
	var stored replayJSON
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode replay data: %w", err)
	}
	if stored.Version != replayVersionV1 && stored.Version != replayVersion {
		return fmt.Errorf("unsupported replay data version %d", stored.Version)
	}
	decoded := ReplayData{
		provider: stored.Provider,
		model:    stored.Model,
		payload:  append(json.RawMessage(nil), stored.Payload...),
	}
	if err := decoded.validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

func (r ReplayData) validate() error {
	if strings.TrimSpace(r.provider) == "" || strings.TrimSpace(r.model) == "" {
		return fmt.Errorf("replay data requires a protocol and model identity")
	}
	payload := bytes.TrimSpace(r.payload)
	if len(payload) == 0 || !json.Valid(payload) || (payload[0] != '{' && payload[0] != '[') {
		return fmt.Errorf("replay payload must be a valid JSON object or array")
	}
	return nil
}
