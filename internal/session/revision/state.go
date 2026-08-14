//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package revision

import (
	"encoding/json"
	"fmt"
)

const (
	stateMetadataKey     = "_trpcAgent"
	stateMetadataVersion = 1
)

type stateMetadata struct {
	Version  int             `json:"version"`
	Revision PersistedRecord `json:"latestTurnRevision"`
}

// DecodeState decodes a persisted session-state envelope and its private
// revision sidecar. Envelopes written before revision support decode as an
// empty revision record.
func DecodeState(raw []byte, state any) (*PersistedRecord, error) {
	if err := json.Unmarshal(raw, state); err != nil {
		return nil, fmt.Errorf("decode session state: %w", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode session state envelope: %w", err)
	}
	metadataRaw := envelope[stateMetadataKey]
	if len(metadataRaw) == 0 || string(metadataRaw) == "null" {
		return &PersistedRecord{}, nil
	}
	var metadata stateMetadata
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return nil, fmt.Errorf("decode session revision metadata: %w", err)
	}
	if metadata.Version != stateMetadataVersion {
		return nil, fmt.Errorf(
			"decode session revision metadata: unsupported version %d",
			metadata.Version,
		)
	}
	return &metadata.Revision, nil
}

// EncodeState encodes a session-state envelope with its private revision
// sidecar. The sidecar is outside the user-visible StateMap.
func EncodeState(state any, record *PersistedRecord) ([]byte, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode session state: %w", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("encode session state envelope: %w", err)
	}
	if record == nil {
		delete(envelope, stateMetadataKey)
		return json.Marshal(envelope)
	}
	metadataRaw, err := json.Marshal(stateMetadata{
		Version:  stateMetadataVersion,
		Revision: *record,
	})
	if err != nil {
		return nil, fmt.Errorf("encode session revision metadata: %w", err)
	}
	envelope[stateMetadataKey] = metadataRaw
	return json.Marshal(envelope)
}
