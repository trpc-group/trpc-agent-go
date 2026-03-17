//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"encoding/base64"
)

const (
	stateDeltaEnvelopeEncodingKey   = "encoding"
	stateDeltaEnvelopePayloadKey    = "payload"
	stateDeltaEnvelopeEncodingBytes = "bytes"
	stateDeltaEnvelopeEncodingNil   = "nil"
)

// EncodeStateDeltaMetadata converts Event.StateDelta into A2A metadata using a
// single lossless envelope format for every entry.
func EncodeStateDeltaMetadata(stateDelta map[string][]byte) map[string]any {
	if len(stateDelta) == 0 {
		return nil
	}

	encoded := make(map[string]any, len(stateDelta))
	for key, raw := range stateDelta {
		if raw == nil {
			encoded[key] = map[string]any{
				stateDeltaEnvelopeEncodingKey: stateDeltaEnvelopeEncodingNil,
			}
			continue
		}
		encoded[key] = map[string]any{
			stateDeltaEnvelopeEncodingKey: stateDeltaEnvelopeEncodingBytes,
			stateDeltaEnvelopePayloadKey:  base64.StdEncoding.EncodeToString(raw),
		}
	}

	return encoded
}

// DecodeStateDeltaMetadata restores Event.StateDelta from A2A metadata encoded
// by EncodeStateDeltaMetadata.
func DecodeStateDeltaMetadata(raw any) map[string][]byte {
	stateDelta, ok := raw.(map[string]any)
	if !ok || len(stateDelta) == 0 {
		return nil
	}

	decoded := make(map[string][]byte, len(stateDelta))
	for key, value := range stateDelta {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		bytes, ok := decodeStateDeltaEnvelope(entry)
		if !ok {
			continue
		}
		decoded[key] = bytes
	}

	if len(decoded) == 0 {
		return nil
	}
	return decoded
}

func decodeStateDeltaEnvelope(entry map[string]any) ([]byte, bool) {
	encoding, ok := entry[stateDeltaEnvelopeEncodingKey].(string)
	if !ok || encoding == "" {
		return nil, false
	}

	switch encoding {
	case stateDeltaEnvelopeEncodingNil:
		return nil, true
	case stateDeltaEnvelopeEncodingBytes:
		payload, ok := entry[stateDeltaEnvelopePayloadKey].(string)
		if !ok {
			return nil, false
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, false
		}
		return raw, true
	default:
		return nil, false
	}
}
