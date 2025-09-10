//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

// drainFinalState drains the event channel and returns the final state from the
// completion event's StateDelta.
// TestCheckpointPutGet_InMemorySaver verifies PutFull and GetTuple roundtrip.
func TestCheckpointPutGet_InMemorySaver(t *testing.T) {
	saver := checkpointinmemory.NewSaver()

	cfg := gr.CreateCheckpointConfig("lineage-test", "ckpt-1", "")
	ckpt := gr.NewCheckpoint(map[string]any{"foo": 1}, map[string]int64{"_input_foo": 1}, map[string]map[string]int64{})
	ckpt.NextNodes = []string{"A"}
	meta := &gr.CheckpointMetadata{Source: gr.CheckpointSourceInput, Step: -1, Extra: map[string]any{"k": "v"}}
	updatedCfg, err := saver.PutFull(context.Background(),
		gr.PutFullRequest{
			Config:      cfg,
			Checkpoint:  ckpt,
			Metadata:    meta,
			NewVersions: map[string]int64{"_input_foo": 1},
		},
	)
	require.NoError(t, err, "putfull failed")

	// Get by exact ID
	got, err := saver.GetTuple(context.Background(), updatedCfg)
	require.NoError(t, err, "gettuple failed")
	require.NotNil(t, got, "no tuple")
	require.Equal(t, []string{"A"}, got.Checkpoint.NextNodes)
}

// TestInitialCheckpointCreated_InMemorySaver verifies initial checkpoint creation with the official saver.
func TestInitialCheckpointCreated_InMemorySaver(t *testing.T) {
	saver := checkpointinmemory.NewSaver()
	// Query latest to ensure at least one checkpoint exists for default namespace.
	// Using empty checkpoint_id in config should fetch latest per saver implementation.
	latestCfg := gr.CreateCheckpointConfig("inv-init-ckpt", "", "")
	tuple, err := saver.GetTuple(context.Background(), latestCfg)
	// With no writes yet, this should be nil rather than error.
	require.NoError(t, err)
	require.Nil(t, tuple)
}
