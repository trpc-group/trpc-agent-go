//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracksFromState(t *testing.T) {
	t.Run("nil state returns nil", func(t *testing.T) {
		tracks, err := TracksFromState(nil)
		require.NoError(t, err)
		assert.Nil(t, tracks)
	})

	t.Run("missing track key returns nil", func(t *testing.T) {
		state := StateMap{"other": []byte("value")}
		tracks, err := TracksFromState(state)
		require.NoError(t, err)
		assert.Nil(t, tracks)
	})

	t.Run("empty track data returns nil", func(t *testing.T) {
		state := StateMap{tracksStateKey: []byte{}}
		tracks, err := TracksFromState(state)
		require.NoError(t, err)
		assert.Nil(t, tracks)
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		state := StateMap{tracksStateKey: []byte("{")}
		_, err := TracksFromState(state)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode track index")
	})

	t.Run("valid track list decoded", func(t *testing.T) {
		state := StateMap{tracksStateKey: mustMarshalTracks(t, []Track{"alpha", "beta"})}
		tracks, err := TracksFromState(state)
		require.NoError(t, err)
		assert.Equal(t, []Track{"alpha", "beta"}, tracks)
	})
}

func TestEnsureTrackExists(t *testing.T) {
	tests := []struct {
		name         string
		stateFactory func(t *testing.T) StateMap
		track        Track
		wantErr      string
		wantTracks   []Track
	}{
		{
			name: "nil state returns error",
			stateFactory: func(t *testing.T) StateMap {
				return nil
			},
			track:   "alpha",
			wantErr: "state is nil",
		},
		{
			name: "invalid track index returns error",
			stateFactory: func(t *testing.T) StateMap {
				return StateMap{tracksStateKey: []byte("{")}
			},
			track:   "alpha",
			wantErr: "get tracks from state",
		},
		{
			name: "track already exists is no-op",
			stateFactory: func(t *testing.T) StateMap {
				return StateMap{tracksStateKey: mustMarshalTracks(t, []Track{"alpha"})}
			},
			track:      "alpha",
			wantTracks: []Track{"alpha"},
		},
		{
			name: "track added when index missing",
			stateFactory: func(t *testing.T) StateMap {
				return StateMap{}
			},
			track:      "alpha",
			wantTracks: []Track{"alpha"},
		},
		{
			name: "track appended to existing index",
			stateFactory: func(t *testing.T) StateMap {
				return StateMap{tracksStateKey: mustMarshalTracks(t, []Track{"alpha"})}
			},
			track:      "beta",
			wantTracks: []Track{"alpha", "beta"},
		},
		{
			name: "track added when stored bytes empty",
			stateFactory: func(t *testing.T) StateMap {
				return StateMap{tracksStateKey: []byte{}}
			},
			track:      "alpha",
			wantTracks: []Track{"alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.stateFactory(t)
			err := ensureTrackExists(state, tt.track)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTracks, decodeTrackIndex(t, state))
		})
	}
}

func mustMarshalTracks(t *testing.T, tracks []Track) []byte {
	t.Helper()
	encoded, err := json.Marshal(tracks)
	require.NoError(t, err)
	return encoded
}

func decodeTrackIndex(t *testing.T, state StateMap) []Track {
	t.Helper()
	raw, ok := state[tracksStateKey]
	require.True(t, ok, "tracks key should exist")
	if len(raw) == 0 {
		return nil
	}
	var tracks []Track
	require.NoError(t, json.Unmarshal(raw, &tracks))
	return tracks
}
