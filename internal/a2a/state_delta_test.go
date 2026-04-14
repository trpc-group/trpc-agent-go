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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeStateDeltaMetadata_RoundTrip(t *testing.T) {
	original := map[string][]byte{
		"json_object": []byte(`{"nodeId":"planner","phase":"start"}`),
		"json_string": []byte(`"hello"`),
		"json_number": []byte(`1`),
		"json_null":   []byte(`null`),
		"plain_text":  []byte("raw-text"),
		"empty":       []byte{},
		"deleted":     nil,
	}

	encoded := EncodeStateDeltaMetadata(original)
	decoded := DecodeStateDeltaMetadata(encoded)

	require.NotNil(t, decoded)
	require.Equal(t, original["json_object"], decoded["json_object"])
	require.Equal(t, original["json_string"], decoded["json_string"])
	require.Equal(t, original["json_number"], decoded["json_number"])
	require.Equal(t, original["json_null"], decoded["json_null"])
	require.Equal(t, original["plain_text"], decoded["plain_text"])
	require.NotNil(t, decoded["empty"])
	require.Len(t, decoded["empty"], 0)
	require.Nil(t, decoded["deleted"])
}

func TestEncodeStateDeltaMetadata_UsesSingleEnvelopeFormat(t *testing.T) {
	encoded := EncodeStateDeltaMetadata(map[string][]byte{
		"plain_text": []byte("raw-text"),
		"empty":      []byte{},
		"deleted":    nil,
	})

	require.Equal(t, map[string]any{
		"plain_text": map[string]any{
			stateDeltaEnvelopeEncodingKey: stateDeltaEnvelopeEncodingBytes,
			stateDeltaEnvelopePayloadKey:  base64.StdEncoding.EncodeToString([]byte("raw-text")),
		},
		"empty": map[string]any{
			stateDeltaEnvelopeEncodingKey: stateDeltaEnvelopeEncodingBytes,
			stateDeltaEnvelopePayloadKey:  "",
		},
		"deleted": map[string]any{
			stateDeltaEnvelopeEncodingKey: stateDeltaEnvelopeEncodingNil,
		},
	}, encoded)
}

func TestDecodeStateDeltaMetadata_InvalidShape(t *testing.T) {
	decoded := DecodeStateDeltaMetadata(map[string]any{
		"legacy": "raw-text",
		"bad": map[string]any{
			stateDeltaEnvelopeEncodingKey: "unknown",
		},
	})

	require.Nil(t, decoded)
}
