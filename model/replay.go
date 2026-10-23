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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const replayVersion = 1

// ReplayData stores a provider-native completion together with the identity of
// its producer and a fingerprint of its Message projection. The provider owns
// the payload schema. Framework code must retain or remove the whole record
// instead of editing its payload or merging its message.
//
// Use Message.SetReplayData to create a record and Message.ReplayDataFor to read
// it. Neither method exposes mutable storage, and SetReplayData binds a new
// record instead of changing the existing one. UnmarshalJSON overwrites its
// receiver as a whole, so messages sharing a *ReplayData observe a decode into
// it; copying the ReplayData value gives a message its own record. JSON
// serialization retains the binding so that persistence cannot make an edited
// projection replayable. The zero value is invalid; a nil Message.Replay means
// no native replay data.
type ReplayData struct {
	provider   string
	model      string
	payload    json.RawMessage
	projection string
}

type replayJSON struct {
	Version    int             `json:"version"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	Payload    json.RawMessage `json:"payload"`
	Projection string          `json:"projection_sha256"`
}

// SetReplayData binds a copy of payload to the current assistant message.
// Provider identifies the wire protocol, and modelName is the configured model
// identity used for later requests, rather than a provider-resolved alias.
// Payload must be a JSON object or array understood by that provider.
//
// On success, the native payload is authoritative and the message fields become
// read-only projections for display and tool execution. Subsequent edits cause
// ReplayDataFor to fail, including edits to arguments with unchanged call IDs.
// On error, the receiver and any existing replay record are unchanged.
func (m *Message) SetReplayData(provider, modelName string, payload json.RawMessage) error {
	if m == nil {
		return fmt.Errorf("cannot bind replay data to a nil message")
	}
	if m.Role != RoleAssistant {
		return fmt.Errorf("replay data requires an assistant message")
	}
	projection, err := messageProjection(m)
	if err != nil {
		return err
	}
	data := &ReplayData{
		provider:   provider,
		model:      modelName,
		payload:    append(json.RawMessage(nil), payload...),
		projection: projection,
	}
	if err := data.validate(); err != nil {
		return err
	}
	m.Replay = data
	return nil
}

// ReplayDataFor returns a copy of the native payload for the named protocol and
// configured model. A message without Replay returns nil, nil. A producer
// mismatch, corrupt record, or any change to the bound message projection
// returns an error; callers must not silently fall back to an outdated payload.
//
// To intentionally replace a completed native history unit with newly authored
// semantic context, construct a new message or explicitly clear Replay first.
// This is a context rebase, not a lossless continuation of a native tool turn.
func (m Message) ReplayDataFor(provider, modelName string) (json.RawMessage, error) {
	if m.Replay == nil {
		return nil, nil
	}
	if err := m.Replay.validate(); err != nil {
		return nil, err
	}
	if m.Replay.provider != provider || m.Replay.model != modelName {
		return nil, fmt.Errorf("replay data belongs to protocol %q and model %q, not %q and %q",
			m.Replay.provider, m.Replay.model, provider, modelName)
	}
	projection, err := messageProjection(&m)
	if err != nil {
		return nil, err
	}
	if projection != m.Replay.projection {
		return nil, fmt.Errorf("message projection changed after native replay data was bound")
	}
	return append(json.RawMessage(nil), m.Replay.payload...), nil
}

// MarshalJSON preserves the native record and its projection binding. Invalid
// or zero-valued records return an error instead of persisting unusable state.
func (r ReplayData) MarshalJSON() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(replayJSON{
		Version:    replayVersion,
		Provider:   r.provider,
		Model:      r.model,
		Payload:    r.payload,
		Projection: r.projection,
	})
}

// UnmarshalJSON restores a versioned native replay record. Unsupported versions
// and invalid records fail rather than degrading silently to text-only history.
func (r *ReplayData) UnmarshalJSON(data []byte) error {
	var stored replayJSON
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode replay data: %w", err)
	}
	if stored.Version != replayVersion {
		return fmt.Errorf("unsupported replay data version %d", stored.Version)
	}
	decoded := ReplayData{
		provider:   stored.Provider,
		model:      stored.Model,
		payload:    append(json.RawMessage(nil), stored.Payload...),
		projection: stored.Projection,
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
	decoded, err := hex.DecodeString(r.projection)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid replay message fingerprint")
	}
	return nil
}

func messageProjection(m *Message) (string, error) {
	projection := *m
	projection.Replay = nil
	data, err := json.Marshal(projection)
	if err != nil {
		return "", fmt.Errorf("encode replay message projection: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
