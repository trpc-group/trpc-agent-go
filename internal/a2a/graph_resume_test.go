//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestDecodeStateDeltaString(t *testing.T) {
	tests := []struct {
		name   string
		delta  map[string][]byte
		key    string
		want   string
		wantOK bool
	}{
		{
			name:   "valid string",
			delta:  map[string][]byte{"k": []byte(`"hello"`)},
			key:    "k",
			want:   "hello",
			wantOK: true,
		},
		{
			name:   "missing key",
			delta:  map[string][]byte{},
			key:    "k",
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty bytes",
			delta:  map[string][]byte{"k": {}},
			key:    "k",
			want:   "",
			wantOK: false,
		},
		{
			name:   "nil bytes",
			delta:  map[string][]byte{"k": nil},
			key:    "k",
			want:   "",
			wantOK: false,
		},
		{
			name:   "invalid json",
			delta:  map[string][]byte{"k": []byte(`not-json`)},
			key:    "k",
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty string value",
			delta:  map[string][]byte{"k": []byte(`""`)},
			key:    "k",
			want:   "",
			wantOK: false,
		},
		{
			name:   "number instead of string",
			delta:  map[string][]byte{"k": []byte(`42`)},
			key:    "k",
			want:   "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeStateDeltaString(tt.delta, tt.key)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDecodeStateDeltaAny(t *testing.T) {
	tests := []struct {
		name   string
		delta  map[string][]byte
		key    string
		want   any
		wantOK bool
	}{
		{
			name:   "string value",
			delta:  map[string][]byte{"k": []byte(`"approve"`)},
			key:    "k",
			want:   "approve",
			wantOK: true,
		},
		{
			name:   "number value",
			delta:  map[string][]byte{"k": []byte(`42`)},
			key:    "k",
			want:   float64(42),
			wantOK: true,
		},
		{
			name:   "bool value",
			delta:  map[string][]byte{"k": []byte(`true`)},
			key:    "k",
			want:   true,
			wantOK: true,
		},
		{
			name:   "null value",
			delta:  map[string][]byte{"k": []byte(`null`)},
			key:    "k",
			want:   nil,
			wantOK: true,
		},
		{
			name:   "missing key",
			delta:  map[string][]byte{},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "empty bytes",
			delta:  map[string][]byte{"k": {}},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "invalid json",
			delta:  map[string][]byte{"k": []byte(`{broken`)},
			key:    "k",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeStateDeltaAny(tt.delta, tt.key)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDecodeStateDeltaAnyMap(t *testing.T) {
	tests := []struct {
		name   string
		delta  map[string][]byte
		key    string
		want   map[string]any
		wantOK bool
	}{
		{
			name:   "valid map",
			delta:  map[string][]byte{"k": []byte(`{"a":1,"b":"two"}`)},
			key:    "k",
			want:   map[string]any{"a": float64(1), "b": "two"},
			wantOK: true,
		},
		{
			name:   "missing key",
			delta:  map[string][]byte{},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "empty bytes",
			delta:  map[string][]byte{"k": {}},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "empty object",
			delta:  map[string][]byte{"k": []byte(`{}`)},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "invalid json",
			delta:  map[string][]byte{"k": []byte(`not-json`)},
			key:    "k",
			wantOK: false,
		},
		{
			name:   "not a map",
			delta:  map[string][]byte{"k": []byte(`"string"`)},
			key:    "k",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeStateDeltaAnyMap(tt.delta, tt.key)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDecodePregelMetadata(t *testing.T) {
	t.Run("valid metadata", func(t *testing.T) {
		meta := graph.PregelStepMetadata{
			LineageID:    "ln-1",
			CheckpointID: "ck-1",
			CheckpointNS: "ns-1",
		}
		raw, err := json.Marshal(meta)
		require.NoError(t, err)

		got, ok := DecodePregelMetadata(map[string][]byte{
			graph.MetadataKeyPregel: raw,
		})
		assert.True(t, ok)
		assert.Equal(t, "ln-1", got.LineageID)
		assert.Equal(t, "ck-1", got.CheckpointID)
		assert.Equal(t, "ns-1", got.CheckpointNS)
	})

	t.Run("missing key", func(t *testing.T) {
		_, ok := DecodePregelMetadata(map[string][]byte{})
		assert.False(t, ok)
	})

	t.Run("empty bytes", func(t *testing.T) {
		_, ok := DecodePregelMetadata(map[string][]byte{
			graph.MetadataKeyPregel: {},
		})
		assert.False(t, ok)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, ok := DecodePregelMetadata(map[string][]byte{
			graph.MetadataKeyPregel: []byte(`{broken`),
		})
		assert.False(t, ok)
	})
}

func TestCloneAnyMap(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		assert.Nil(t, CloneAnyMap(nil))
	})

	t.Run("empty map", func(t *testing.T) {
		assert.Nil(t, CloneAnyMap(map[string]any{}))
	})

	t.Run("non-empty map", func(t *testing.T) {
		src := map[string]any{"a": 1, "b": "two"}
		dst := CloneAnyMap(src)
		assert.Equal(t, src, dst)
		// Verify it is a different map instance.
		dst["c"] = 3
		assert.NotContains(t, src, "c")
	})
}

func TestGraphResumeStateFromStateDelta(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		assert.Nil(t, GraphResumeStateFromStateDelta(nil))
	})

	t.Run("empty state delta", func(t *testing.T) {
		encoded := EncodeStateDeltaMetadata(map[string][]byte{})
		assert.Nil(t, GraphResumeStateFromStateDelta(encoded))
	})

	t.Run("lineage and checkpoint from flat keys", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-1"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
			graph.CfgKeyCheckpointNS: []byte(`"ns-1"`),
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		assert.Equal(t, "ln-1", state[graph.CfgKeyLineageID])
		assert.Equal(t, "ck-1", state[graph.CfgKeyCheckpointID])
		assert.Equal(t, "ns-1", state[graph.CfgKeyCheckpointNS])
	})

	t.Run("lineage and checkpoint from pregel metadata fallback", func(t *testing.T) {
		meta := graph.PregelStepMetadata{
			LineageID:    "ln-pregel",
			CheckpointID: "ck-pregel",
			CheckpointNS: "ns-pregel",
		}
		raw, _ := json.Marshal(meta)
		delta := map[string][]byte{
			graph.MetadataKeyPregel: raw,
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		assert.Equal(t, "ln-pregel", state[graph.CfgKeyLineageID])
		assert.Equal(t, "ck-pregel", state[graph.CfgKeyCheckpointID])
		assert.Equal(t, "ns-pregel", state[graph.CfgKeyCheckpointNS])
	})

	t.Run("flat keys take precedence over pregel metadata", func(t *testing.T) {
		meta := graph.PregelStepMetadata{
			LineageID:    "ln-pregel",
			CheckpointID: "ck-pregel",
		}
		raw, _ := json.Marshal(meta)
		delta := map[string][]byte{
			graph.CfgKeyLineageID:   []byte(`"ln-flat"`),
			graph.MetadataKeyPregel: raw,
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		assert.Equal(t, "ln-flat", state[graph.CfgKeyLineageID])
		// pregel metadata should NOT override when flat key exists
		_, hasCheckpoint := state[graph.CfgKeyCheckpointID]
		assert.False(t, hasCheckpoint)
	})

	t.Run("resume value only", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-1"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
			"resume":                 []byte(`"approve"`),
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "approve", cmd.Resume)
	})

	t.Run("resume map only", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-1"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
			graph.CfgKeyResumeMap:    []byte(`{"task1":true}`),
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, true, cmd.ResumeMap["task1"])
	})

	t.Run("resume and resume map together", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-1"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
			"resume":                 []byte(`"yes"`),
			graph.CfgKeyResumeMap:    []byte(`{"task1":"ok"}`),
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "yes", cmd.Resume)
		assert.Equal(t, "ok", cmd.ResumeMap["task1"])
	})

	t.Run("no resume produces no command", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-1"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-1"`),
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		require.NotNil(t, state)
		_, hasCmd := state[graph.StateKeyCommand]
		assert.False(t, hasCmd)
	})

	t.Run("pregel metadata with empty lineage ignored", func(t *testing.T) {
		meta := graph.PregelStepMetadata{}
		raw, _ := json.Marshal(meta)
		delta := map[string][]byte{
			graph.MetadataKeyPregel: raw,
		}
		encoded := EncodeStateDeltaMetadata(delta)
		state := GraphResumeStateFromStateDelta(encoded)
		assert.Nil(t, state)
	})
}

func TestGraphResumeStateFromMetadata(t *testing.T) {
	t.Run("nil metadata", func(t *testing.T) {
		assert.Nil(t, GraphResumeStateFromMetadata(nil))
	})

	t.Run("empty metadata", func(t *testing.T) {
		assert.Nil(t, GraphResumeStateFromMetadata(map[string]any{}))
	})

	t.Run("state_delta path takes precedence", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
		}
		metadata := map[string]any{
			MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(delta),
			graph.CfgKeyLineageID:        "ln-flat",
			graph.CfgKeyCheckpointID:     "ck-flat",
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		assert.Equal(t, "ln-sd", state[graph.CfgKeyLineageID])
		assert.Equal(t, "ck-sd", state[graph.CfgKeyCheckpointID])
	})

	t.Run("state_delta checkpoint merges serialized Command fallback", func(t *testing.T) {
		delta := map[string][]byte{
			graph.CfgKeyLineageID:    []byte(`"ln-sd"`),
			graph.CfgKeyCheckpointID: []byte(`"ck-sd"`),
		}
		metadata := map[string]any{
			MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(delta),
			graph.CfgKeyCheckpointID:     "ck-flat",
			graph.StateKeyCommand: map[string]any{
				"Resume":    "approve",
				"ResumeMap": map[string]any{"approval": true},
			},
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		assert.Equal(t, "ln-sd", state[graph.CfgKeyLineageID])
		assert.Equal(t, "ck-sd", state[graph.CfgKeyCheckpointID])
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "approve", cmd.Resume)
		assert.Equal(t, true, cmd.ResumeMap["approval"])
	})

	t.Run("flattened fields fallback", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyLineageID:    "ln-flat",
			graph.CfgKeyCheckpointID: "ck-flat",
			graph.CfgKeyCheckpointNS: "ns-flat",
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		assert.Equal(t, "ln-flat", state[graph.CfgKeyLineageID])
		assert.Equal(t, "ck-flat", state[graph.CfgKeyCheckpointID])
		assert.Equal(t, "ns-flat", state[graph.CfgKeyCheckpointNS])
	})

	t.Run("flattened with resume value", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			"resume":                 "approve",
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "approve", cmd.Resume)
	})

	t.Run("flattened with resume_map", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			graph.CfgKeyResumeMap:    map[string]any{"task1": true},
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, true, cmd.ResumeMap["task1"])
	})

	t.Run("no checkpoint returns partial state", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyLineageID: "ln-only",
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		assert.Equal(t, "ln-only", state[graph.CfgKeyLineageID])
		_, hasCmd := state[graph.StateKeyCommand]
		assert.False(t, hasCmd)
	})

	t.Run("no checkpoint and no lineage returns nil", func(t *testing.T) {
		metadata := map[string]any{
			"unrelated_key": "value",
		}
		state := GraphResumeStateFromMetadata(metadata)
		assert.Nil(t, state)
	})

	t.Run("serialized Command struct fallback with ResumeMap", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			graph.StateKeyCommand: map[string]any{
				"Resume":    nil,
				"ResumeMap": map[string]any{"approval": true},
			},
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, true, cmd.ResumeMap["approval"])
	})

	t.Run("serialized Command struct fallback with Resume value", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			graph.StateKeyCommand: map[string]any{
				"Resume":    "yes",
				"ResumeMap": nil,
			},
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "yes", cmd.Resume)
	})

	t.Run("direct resume takes precedence over Command fallback", func(t *testing.T) {
		metadata := map[string]any{
			graph.CfgKeyCheckpointID: "ck-1",
			"resume":                 "direct",
			graph.StateKeyCommand: map[string]any{
				"Resume": "from-command",
			},
		}
		state := GraphResumeStateFromMetadata(metadata)
		require.NotNil(t, state)
		cmd, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand)
		require.True(t, ok)
		assert.Equal(t, "direct", cmd.Resume)
	})
}

func TestExtractResumeFromCommandMetadata(t *testing.T) {
	t.Run("no command key", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{}, cmd)
		assert.False(t, ok)
	})

	t.Run("nil command value", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: nil,
		}, cmd)
		assert.False(t, ok)
	})

	t.Run("non-map command value", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: "not-a-map",
		}, cmd)
		assert.False(t, ok)
	})

	t.Run("empty map command value", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{},
		}, cmd)
		assert.False(t, ok)
	})

	t.Run("Resume only", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"Resume": "approve",
			},
		}, cmd)
		assert.True(t, ok)
		assert.Equal(t, "approve", cmd.Resume)
	})

	t.Run("ResumeMap only", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"ResumeMap": map[string]any{"task1": true},
			},
		}, cmd)
		assert.True(t, ok)
		assert.Equal(t, true, cmd.ResumeMap["task1"])
	})

	t.Run("Resume nil is ignored", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"Resume":    nil,
				"ResumeMap": map[string]any{"x": 1},
			},
		}, cmd)
		assert.True(t, ok)
		assert.Nil(t, cmd.Resume)
		assert.Equal(t, 1, cmd.ResumeMap["x"])
	})

	t.Run("both Resume and ResumeMap", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"Resume":    42,
				"ResumeMap": map[string]any{"k": "v"},
			},
		}, cmd)
		assert.True(t, ok)
		assert.Equal(t, 42, cmd.Resume)
		assert.Equal(t, "v", cmd.ResumeMap["k"])
	})

	t.Run("ResumeMap empty is ignored", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"ResumeMap": map[string]any{},
			},
		}, cmd)
		assert.False(t, ok)
	})

	t.Run("unrelated fields do not trigger resume", func(t *testing.T) {
		cmd := graph.NewResumeCommand()
		ok := extractResumeFromCommandMetadata(map[string]any{
			graph.StateKeyCommand: map[string]any{
				"GoTo":   "some-node",
				"Update": map[string]any{"counter": 1},
			},
		}, cmd)
		assert.False(t, ok)
	})
}
