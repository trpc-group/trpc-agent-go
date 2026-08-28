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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stateEnvelopeFixture struct {
	ID    string           `json:"id"`
	State session.StateMap `json:"state"`
}

func TestStateEnvelopeRoundTrip(t *testing.T) {
	wantState := stateEnvelopeFixture{
		ID: "session",
		State: session.StateMap{
			"key":            []byte("value"),
			stateMetadataKey: []byte("user value"),
		},
	}
	wantRecord := &PersistedRecord{
		Generation: 3,
		Head:       7,
		Checkpoint: &PersistedCheckpoint{
			RequestID: "request",
			Boundary:  []byte("boundary"),
		},
	}
	raw, err := EncodeState(wantState, wantRecord)
	require.NoError(t, err)

	var gotState stateEnvelopeFixture
	gotRecord, err := DecodeState(raw, &gotState)
	require.NoError(t, err)
	assert.Equal(t, wantState, gotState)
	assert.Equal(t, wantRecord, gotRecord)
	assert.Equal(t, []byte("user value"), gotState.State[stateMetadataKey])
}

func TestStateEnvelopeLegacyAndVersionChecks(t *testing.T) {
	legacy := []byte(`{"id":"session","state":{"key":"dmFsdWU="}}`)
	var state stateEnvelopeFixture
	record, err := DecodeState(legacy, &state)
	require.NoError(t, err)
	assert.Equal(t, "session", state.ID)
	assert.Equal(t, []byte("value"), state.State["key"])
	assert.Equal(t, &PersistedRecord{}, record)

	raw, err := EncodeState(state, nil)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &envelope))
	assert.NotContains(t, envelope, stateMetadataKey)

	var rolledBackState stateEnvelopeFixture
	record, err = DecodeState(
		[]byte(`{"id":"session","state":{},"_trpcAgent":{"version":2}}`),
		&rolledBackState,
	)
	require.NoError(t, err)
	assert.Equal(t, "session", rolledBackState.ID)
	require.NotNil(t, record.Checkpoint)
	assert.True(t, record.Checkpoint.Hazard)
	_, err = RewindCheckpoint(record, "request", "request")
	assert.ErrorIs(t, err, ErrRewindUnavailable)
	_, err = EncodeState(rolledBackState, record)
	assert.ErrorContains(t, err, "unsupported persisted version 2")
	_, err = DecodeState([]byte("not-json"), &stateEnvelopeFixture{})
	assert.Error(t, err)
}
