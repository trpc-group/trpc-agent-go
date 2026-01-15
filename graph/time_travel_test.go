//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	timeTravelTestLineageID = "lineage-1"
	timeTravelTestNS        = "ns-1"
	timeTravelTestStep      = 3
	timeTravelTestNode      = "node-1"
	timeTravelTestTaskID    = "task-1"
	timeTravelTestChannel   = "channel-1"
	timeTravelTestFieldCnt  = "count"
	timeTravelTestBaseID    = "ck-base"
)

func newTimeTravelTestGraph(
	t *testing.T,
	schema *gr.StateSchema,
) *gr.Graph {
	t.Helper()
	sg := gr.NewStateGraph(schema)
	sg.AddNode(
		timeTravelTestNode,
		func(ctx context.Context, state gr.State) (any, error) {
			return nil, nil
		},
	)
	sg.SetEntryPoint(timeTravelTestNode)
	g, err := sg.Compile()
	require.NoError(t, err)
	return g
}

func TestCheckpointRef_ToSaverConfig(t *testing.T) {
	ref := gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: "ck-1",
	}
	cfg, err := ref.ToSaverConfig()
	require.NoError(t, err)
	require.Equal(t, timeTravelTestLineageID, gr.GetLineageID(cfg))
	require.Equal(t, timeTravelTestNS, gr.GetNamespace(cfg))
	require.Equal(t, "ck-1", gr.GetCheckpointID(cfg))
}

func TestCheckpointRef_ToRuntimeStateIncludesCheckpointID(t *testing.T) {
	ref := gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
		Namespace: timeTravelTestNS,
	}
	state := ref.ToRuntimeState()
	require.Equal(t, timeTravelTestLineageID, state[gr.CfgKeyLineageID])
	v, ok := state[gr.CfgKeyCheckpointID]
	require.True(t, ok)
	require.Equal(t, "", v)
	require.Equal(t, timeTravelTestNS, state[gr.CfgKeyCheckpointNS])
}

func TestExecutor_TimeTravelRequiresSaver(t *testing.T) {
	g := newTimeTravelTestGraph(t, gr.NewStateSchema())
	exec, err := gr.NewExecutor(g)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.Error(t, err)
	require.Nil(t, tt)
}

func TestTimeTravel_GetState_EditState(t *testing.T) {
	ctx := context.Background()

	schema := gr.NewStateSchema().AddField(timeTravelTestFieldCnt, gr.StateField{
		Type:    reflect.TypeOf(int(0)),
		Default: func() any { return 0 },
	})
	g := newTimeTravelTestGraph(t, schema)

	saver := checkpointinmemory.NewSaver()
	exec, err := gr.NewExecutor(g, gr.WithCheckpointSaver(saver))
	require.NoError(t, err)

	base := gr.NewCheckpoint(
		map[string]any{timeTravelTestFieldCnt: 1},
		nil,
		nil,
	)
	base.ID = timeTravelTestBaseID
	base.Timestamp = time.Unix(1, 0).UTC()
	base.NextNodes = []string{timeTravelTestNode}
	base.PendingSends = []gr.PendingSend{
		{Channel: timeTravelTestChannel, Value: "x", TaskID: timeTravelTestTaskID},
	}

	baseCfg := gr.CreateCheckpointConfig(
		timeTravelTestLineageID,
		"",
		timeTravelTestNS,
	)
	baseMeta := gr.NewCheckpointMetadata(
		gr.CheckpointSourceLoop,
		timeTravelTestStep,
	)
	baseWrites := []gr.PendingWrite{
		{
			TaskID:   timeTravelTestTaskID,
			Channel:  timeTravelTestChannel,
			Value:    1,
			Sequence: 1,
		},
	}
	_, err = saver.PutFull(ctx, gr.PutFullRequest{
		Config:        baseCfg,
		Checkpoint:    base,
		Metadata:      baseMeta,
		NewVersions:   base.ChannelVersions,
		PendingWrites: baseWrites,
	})
	require.NoError(t, err)

	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	_, err = tt.EditState(ctx, gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: base.ID,
	}, gr.State{
		gr.CfgKeyCheckpointID: "ck-should-fail",
	})
	require.Error(t, err)

	newRef, err := tt.EditState(ctx, gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: base.ID,
	}, gr.State{
		timeTravelTestFieldCnt: float64(2),
	})
	require.NoError(t, err)
	require.Equal(t, timeTravelTestLineageID, newRef.LineageID)
	require.Equal(t, timeTravelTestNS, newRef.Namespace)
	require.NotEmpty(t, newRef.CheckpointID)
	require.NotEqual(t, base.ID, newRef.CheckpointID)

	newCfg, err := newRef.ToSaverConfig()
	require.NoError(t, err)
	newTuple, err := saver.GetTuple(ctx, newCfg)
	require.NoError(t, err)
	require.NotNil(t, newTuple)
	require.NotNil(t, newTuple.Checkpoint)
	require.NotNil(t, newTuple.Metadata)
	require.Equal(t, gr.CheckpointSourceUpdate, newTuple.Metadata.Source)
	require.Equal(t, base.ID, newTuple.Checkpoint.ParentCheckpointID)

	require.Equal(
		t,
		base.ID,
		newTuple.Metadata.Extra[gr.CheckpointMetaKeyBaseCheckpointID],
	)
	require.Equal(
		t,
		[]string{timeTravelTestFieldCnt},
		newTuple.Metadata.Extra[gr.CheckpointMetaKeyUpdatedKeys],
	)
	require.Equal(t, baseWrites, newTuple.PendingWrites)
	require.Equal(t, base.NextNodes, newTuple.Checkpoint.NextNodes)

	snap, err := tt.GetState(ctx, newRef)
	require.NoError(t, err)
	require.NotNil(t, snap)
	require.Equal(t, newRef, snap.Ref)

	got, ok := snap.State[timeTravelTestFieldCnt].(int)
	require.True(t, ok)
	require.Equal(t, 2, got)

	latest, err := tt.GetState(ctx, gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
		Namespace: timeTravelTestNS,
	})
	require.NoError(t, err)
	got, ok = latest.State[timeTravelTestFieldCnt].(int)
	require.True(t, ok)
	require.Equal(t, 2, got)

	history, err := tt.History(
		ctx,
		timeTravelTestLineageID,
		timeTravelTestNS,
		10,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 2)
	require.Equal(t, newRef.CheckpointID, history[0].Ref.CheckpointID)
}

func TestTimeTravel_EditState_AllowInternalKeys(t *testing.T) {
	ctx := context.Background()

	saver := checkpointinmemory.NewSaver()
	exec, err := gr.NewExecutor(
		newTimeTravelTestGraph(t, gr.NewStateSchema()),
		gr.WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	base := gr.NewCheckpoint(map[string]any{}, nil, nil)
	base.ID = timeTravelTestBaseID

	baseCfg := gr.CreateCheckpointConfig(
		timeTravelTestLineageID,
		"",
		timeTravelTestNS,
	)
	_, err = saver.PutFull(ctx, gr.PutFullRequest{
		Config:      baseCfg,
		Checkpoint:  base,
		Metadata:    gr.NewCheckpointMetadata(gr.CheckpointSourceLoop, 0),
		NewVersions: base.ChannelVersions,
	})
	require.NoError(t, err)

	_, err = tt.EditState(ctx, gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: base.ID,
	}, gr.State{
		gr.StateKeyResumeMap: map[string]any{"k": "v"},
	})
	require.Error(t, err)

	ref, err := tt.EditState(ctx, gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: base.ID,
	}, gr.State{
		gr.StateKeyResumeMap: map[string]any{"k": "v"},
	}, gr.WithAllowInternalKeys())
	require.NoError(t, err)

	snap, err := tt.GetState(ctx, ref)
	require.NoError(t, err)
	require.Equal(
		t,
		map[string]any{"k": "v"},
		snap.State[gr.StateKeyResumeMap],
	)
}
