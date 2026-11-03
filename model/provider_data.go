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

const providerDataVersion = 1

// ProviderData stores a provider-native completion with the identity of its
// producer. The provider owns the payload schema. The payload determines the
// next model request. Content, content parts, and reasoning fields are editable
// display projections, and ToolCalls determine the current tool execution. A
// message carrying ProviderData must remain an assistant message.
//
// Use Message.SetProviderData to create a record and Message.ProviderDataFor to read
// it. Both methods copy the opaque payload at their boundaries. The zero value
// is invalid; a nil Message.ProviderData means no native provider data.
type ProviderData struct {
	provider string
	model    string
	payload  json.RawMessage
}

type providerDataJSON struct {
	Version  int             `json:"version"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Payload  json.RawMessage `json:"payload"`
}

// SetProviderData binds a copy of payload to the current assistant message.
// Provider identifies the wire protocol, and modelName is the configured model
// identity used for later requests, rather than a provider-resolved alias.
// Payload must be a JSON object or array understood by that provider.
//
// Content, content parts, and reasoning fields remain independently editable
// after binding. ProviderDataFor returns the native payload used for subsequent
// model requests, while the message's ToolCalls continue to determine current
// tool execution. The message must remain an assistant message. On error, the
// receiver and any existing provider data are unchanged.
func (m *Message) SetProviderData(provider, modelName string, payload json.RawMessage) error {
	if m == nil {
		return fmt.Errorf("cannot bind provider data to a nil message")
	}
	if m.Role != RoleAssistant {
		return fmt.Errorf("provider data requires an assistant message")
	}
	data := &ProviderData{
		provider: provider,
		model:    modelName,
		payload:  append(json.RawMessage(nil), payload...),
	}
	if err := data.validate(); err != nil {
		return err
	}
	m.ProviderData = data
	return nil
}

// ProviderDataFor returns a copy of the native payload for the named protocol and
// configured model. A message without ProviderData returns nil, nil. A producer
// mismatch or corrupt record returns an error.
func (m Message) ProviderDataFor(provider, modelName string) (json.RawMessage, error) {
	if m.ProviderData == nil {
		return nil, nil
	}
	if m.Role != RoleAssistant {
		return nil, fmt.Errorf("provider data requires an assistant message")
	}
	if err := m.ProviderData.validate(); err != nil {
		return nil, err
	}
	if m.ProviderData.provider != provider || m.ProviderData.model != modelName {
		return nil, fmt.Errorf("provider data belongs to protocol %q and model %q, not %q and %q",
			m.ProviderData.provider, m.ProviderData.model, provider, modelName)
	}
	return append(json.RawMessage(nil), m.ProviderData.payload...), nil
}

// MarshalJSON writes the current version of the provider data record. Invalid
// or zero-valued records return an error instead of persisting unusable state.
func (r ProviderData) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(providerDataJSON{
		Version:  providerDataVersion,
		Provider: r.provider,
		Model:    r.model,
		Payload:  r.payload,
	})
}

// UnmarshalJSON restores a versioned provider data record. Unsupported versions
// and invalid records fail rather than degrading silently to text-only history.
func (r *ProviderData) UnmarshalJSON(data []byte) error {
	var stored providerDataJSON
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode provider data: %w", err)
	}
	if stored.Version != providerDataVersion {
		return fmt.Errorf("unsupported provider data version %d", stored.Version)
	}
	decoded := ProviderData{
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

func (r ProviderData) validate() error {
	if strings.TrimSpace(r.provider) == "" || strings.TrimSpace(r.model) == "" {
		return fmt.Errorf("provider data requires a protocol and model identity")
	}
	payload := bytes.TrimSpace(r.payload)
	if len(payload) == 0 || !json.Valid(payload) || (payload[0] != '{' && payload[0] != '[') {
		return fmt.Errorf("provider payload must be a valid JSON object or array")
	}
	return nil
}
