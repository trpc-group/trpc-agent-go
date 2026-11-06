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

// ProviderData stores a provider-native completion with the identity of its
// producer. The provider owns the payload schema. The payload determines the
// next model request. Editing the payload in AfterModel rebuilds display fields
// and ToolCalls through the model's ProviderDataProjector before publication and
// tool execution. When the payload is unchanged, callbacks may edit display
// fields and ToolCalls independently. A message carrying ProviderData must
// remain an assistant message.
//
// Callbacks may edit Payload on their message while preserving the producer's
// Protocol and Model. A nil Message.ProviderData means the message carries no
// native provider data.
type ProviderData struct {
	// Protocol identifies the adapter's wire protocol.
	Protocol string `json:"provider"`
	// Model is the configured model name that produced the record.
	Model string `json:"model"`
	// Payload holds an object or array in the adapter's native JSON format.
	Payload json.RawMessage `json:"payload"`
}

// providerDataJSON has the same fields and tags without the custom JSON methods.
type providerDataJSON ProviderData

// MarshalJSON validates the provider data record before encoding it. Invalid
// or zero-valued records return an error.
func (r ProviderData) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(providerDataJSON(r))
}

// UnmarshalJSON restores a valid provider data record. On error, the receiver
// is unchanged.
func (r *ProviderData) UnmarshalJSON(data []byte) error {
	var stored providerDataJSON
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode provider data: %w", err)
	}
	decoded := ProviderData(stored)
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
