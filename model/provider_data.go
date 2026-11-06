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
// next model request. Editing the payload in AfterModel rebuilds display fields
// and ToolCalls through the model's ProviderDataProjector before publication and
// tool execution. When the payload is unchanged, callbacks may edit display
// fields and ToolCalls independently. A message carrying ProviderData must
// remain an assistant message.
//
// Callbacks may edit the record on their message. A nil Message.ProviderData
// means the message carries no native provider data.
type ProviderData struct {
	// Protocol identifies the adapter's wire protocol.
	Protocol string
	// Model is the configured model name that produced the record.
	Model string
	// Payload holds an object or array in the adapter's native JSON format.
	Payload json.RawMessage
}

type providerDataJSON struct {
	Version  int             `json:"version"`
	Protocol string          `json:"provider"`
	Model    string          `json:"model"`
	Payload  json.RawMessage `json:"payload"`
}

// MarshalJSON writes the current version of the provider data record. Invalid
// or zero-valued records return an error instead of persisting unusable state.
func (r ProviderData) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(providerDataJSON{
		Version:  providerDataVersion,
		Protocol: r.Protocol,
		Model:    r.Model,
		Payload:  r.Payload,
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
		Protocol: stored.Protocol,
		Model:    stored.Model,
		Payload:  stored.Payload,
	}
	if err := decoded.validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

func (r ProviderData) validate() error {
	if strings.TrimSpace(r.Protocol) == "" || strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("provider data requires a protocol and model identity")
	}
	payload := bytes.TrimSpace(r.Payload)
	if len(payload) == 0 || !json.Valid(payload) || (payload[0] != '{' && payload[0] != '[') {
		return fmt.Errorf("provider payload must be a valid JSON object or array")
	}
	return nil
}

func (r *ProviderData) clone() *ProviderData {
	if r == nil {
		return nil
	}
	cloned := *r
	cloned.Payload = bytes.Clone(r.Payload)
	return &cloned
}
