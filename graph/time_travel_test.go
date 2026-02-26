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
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
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
	timeTravelTestUnknownK  = "unknown"
	timeTravelTestCk1       = "ck-1"
	timeTravelTestCk2       = "ck-2"
	timeTravelTestKeyEmpty  = ""
	timeTravelTestKeyIntK   = "__k"
	timeTravelTestValueX    = "x"
	timeTravelTestValueV    = "v"
	timeTravelTestErrMsg    = "time travel test error"
	timeTravelTestErrGetCk  = "get checkpoint"
	timeTravelTestErrListCk = "list checkpoints"
	timeTravelTestErrSaveCk = "save checkpoint"
	timeTravelTestErrTTNil  = "time travel is not configured"
)

var errTimeTravelTest = errors.New(timeTravelTestErrMsg)

type timeTravelStubSaver struct {
	getTupleFn func(
		ctx context.Context,
		config map[string]any,
	) (*gr.CheckpointTuple, error)

	listFn func(
		ctx context.Context,
		config map[string]any,
		filter *gr.CheckpointFilter,
	) ([]*gr.CheckpointTuple, error)

	putFullFn func(
		ctx context.Context,
		req gr.PutFullRequest,
	) (map[string]any, error)

	putFullCheckpointID string
	putFullStep         int
	putFullValues       map[string]any
}

func (s *timeTravelStubSaver) Get(
	ctx context.Context,
	config map[string]any,
) (*gr.Checkpoint, error) {
	tuple, err := s.GetTuple(ctx, config)
	if err != nil || tuple == nil {
		return nil, err
	}
	return tuple.Checkpoint, nil
}

func (s *timeTravelStubSaver) GetTuple(
	ctx context.Context,
	config map[string]any,
) (*gr.CheckpointTuple, error) {
	if s.getTupleFn == nil {
		return nil, nil
	}
	return s.getTupleFn(ctx, config)
}

func (s *timeTravelStubSaver) List(
	ctx context.Context,
	config map[string]any,
	filter *gr.CheckpointFilter,
) ([]*gr.CheckpointTuple, error) {
	if s.listFn == nil {
		return nil, nil
	}
	return s.listFn(ctx, config, filter)
}

func (s *timeTravelStubSaver) Put(
	ctx context.Context,
	req gr.PutRequest,
) (map[string]any, error) {
	return req.Config, nil
}

func (s *timeTravelStubSaver) PutWrites(
	ctx context.Context,
	req gr.PutWritesRequest,
) error {
	return nil
}

func (s *timeTravelStubSaver) PutFull(
	ctx context.Context,
	req gr.PutFullRequest,
) (map[string]any, error) {
	if s.putFullFn == nil {
		s.putFullCheckpointID = ""
		s.putFullStep = 0
		s.putFullValues = nil
		return req.Config, nil
	}
	s.putFullCheckpointID = ""
	if req.Checkpoint != nil {
		s.putFullCheckpointID = req.Checkpoint.ID
		s.putFullValues = req.Checkpoint.ChannelValues
	}
	s.putFullStep = 0
	if req.Metadata != nil {
		s.putFullStep = req.Metadata.Step
	}
	return s.putFullFn(ctx, req)
}

func (s *timeTravelStubSaver) DeleteLineage(
	ctx context.Context,
	lineageID string,
) error {
	return nil
}

func (s *timeTravelStubSaver) Close() error {
	return nil
}

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

func TestCheckpointRef_ValidateRequiresLineageID(t *testing.T) {
	err := gr.CheckpointRef{}.Validate()
	require.ErrorIs(t, err, gr.ErrLineageIDRequired)
}

func TestCheckpointRef_ToSaverConfig_LatestCheckpoint(t *testing.T) {
	ref := gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
		Namespace: timeTravelTestNS,
	}
	cfg, err := ref.ToSaverConfig()
	require.NoError(t, err)
	require.Equal(t, "", gr.GetCheckpointID(cfg))
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

	saver := inmemory.NewSaver()
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

func TestTimeTravel_GetState_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	var nilTT *gr.TimeTravel
	_, err := nilTT.GetState(ctx, gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
	})
	require.ErrorContains(t, err, timeTravelTestErrTTNil)

	saver := &timeTravelStubSaver{
		getTupleFn: func(
			ctx context.Context,
			config map[string]any,
		) (*gr.CheckpointTuple, error) {
			return nil, errTimeTravelTest
		},
	}
	exec, err := gr.NewExecutor(
		newTimeTravelTestGraph(t, gr.NewStateSchema()),
		gr.WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	_, err = tt.GetState(ctx, gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
	})
	require.ErrorIs(t, err, errTimeTravelTest)
	require.ErrorContains(t, err, timeTravelTestErrGetCk)

	_, err = tt.GetState(ctx, gr.CheckpointRef{})
	require.ErrorIs(t, err, gr.ErrLineageIDRequired)

	saver.getTupleFn = func(
		ctx context.Context,
		config map[string]any,
	) (*gr.CheckpointTuple, error) {
		return nil, nil
	}
	_, err = tt.GetState(ctx, gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
	})
	require.ErrorIs(t, err, gr.ErrCheckpointNotFound)

	saver.getTupleFn = func(
		ctx context.Context,
		config map[string]any,
	) (*gr.CheckpointTuple, error) {
		return &gr.CheckpointTuple{}, nil
	}
	_, err = tt.GetState(ctx, gr.CheckpointRef{
		LineageID: timeTravelTestLineageID,
	})
	require.ErrorIs(t, err, gr.ErrCheckpointNotFound)
}

func TestTimeTravel_History_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	var nilTT *gr.TimeTravel
	_, err := nilTT.History(ctx, timeTravelTestLineageID, "", 1)
	require.ErrorContains(t, err, timeTravelTestErrTTNil)

	saver := &timeTravelStubSaver{
		listFn: func(
			ctx context.Context,
			config map[string]any,
			filter *gr.CheckpointFilter,
		) ([]*gr.CheckpointTuple, error) {
			return nil, errTimeTravelTest
		},
	}
	exec, err := gr.NewExecutor(
		newTimeTravelTestGraph(t, gr.NewStateSchema()),
		gr.WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	_, err = tt.History(ctx, "", timeTravelTestNS, 1)
	require.ErrorIs(t, err, gr.ErrLineageIDRequired)

	_, err = tt.History(ctx, timeTravelTestLineageID, timeTravelTestNS, 1)
	require.ErrorIs(t, err, errTimeTravelTest)
	require.ErrorContains(t, err, timeTravelTestErrListCk)
}

func TestTimeTravel_History_LimitZeroAndSkipNil(t *testing.T) {
	ctx := context.Background()

	var gotFilter *gr.CheckpointFilter
	ck1 := &gr.Checkpoint{
		ID:        timeTravelTestCk1,
		Timestamp: time.Unix(1, 0).UTC(),
	}
	ck2 := &gr.Checkpoint{
		ID:        timeTravelTestCk2,
		Timestamp: time.Unix(2, 0).UTC(),
	}
	cfg := gr.NewCheckpointConfig(timeTravelTestLineageID).
		WithNamespace(timeTravelTestNS).
		ToMap()
	tuple1 := &gr.CheckpointTuple{
		Config:     cfg,
		Checkpoint: ck1,
	}
	tuple2 := &gr.CheckpointTuple{
		Config:     cfg,
		Checkpoint: ck2,
	}

	saver := &timeTravelStubSaver{
		listFn: func(
			ctx context.Context,
			config map[string]any,
			filter *gr.CheckpointFilter,
		) ([]*gr.CheckpointTuple, error) {
			gotFilter = filter
			return []*gr.CheckpointTuple{
				nil,
				&gr.CheckpointTuple{},
				tuple1,
				tuple2,
			}, nil
		},
	}
	exec, err := gr.NewExecutor(
		newTimeTravelTestGraph(t, gr.NewStateSchema()),
		gr.WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	history, err := tt.History(ctx, timeTravelTestLineageID, timeTravelTestNS, 0)
	require.NoError(t, err)
	require.Nil(t, gotFilter)
	require.Len(t, history, 2)
	require.Equal(t, timeTravelTestCk2, history[0].Ref.CheckpointID)
	require.Equal(t, timeTravelTestCk1, history[1].Ref.CheckpointID)
}

func TestTimeTravel_EditState_AllowInternalKeys(t *testing.T) {
	ctx := context.Background()

	saver := inmemory.NewSaver()
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

func TestTimeTravel_EditState_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	var nilTT *gr.TimeTravel
	_, err := nilTT.EditState(ctx, gr.CheckpointRef{}, nil)
	require.ErrorContains(t, err, timeTravelTestErrTTNil)

	base := &gr.Checkpoint{
		Version:         gr.CheckpointVersion,
		ID:              timeTravelTestBaseID,
		Timestamp:       time.Unix(1, 0).UTC(),
		ChannelValues:   nil,
		ChannelVersions: make(map[string]int64),
		VersionsSeen:    make(map[string]map[string]int64),
	}

	baseRef := gr.CheckpointRef{
		LineageID:    timeTravelTestLineageID,
		Namespace:    timeTravelTestNS,
		CheckpointID: timeTravelTestBaseID,
	}
	baseCfg, err := baseRef.ToSaverConfig()
	require.NoError(t, err)

	saver := &timeTravelStubSaver{
		getTupleFn: func(
			ctx context.Context,
			config map[string]any,
		) (*gr.CheckpointTuple, error) {
			return &gr.CheckpointTuple{
				Config:        baseCfg,
				Checkpoint:    base,
				Metadata:      nil,
				PendingWrites: nil,
			}, nil
		},
		putFullFn: func(
			ctx context.Context,
			req gr.PutFullRequest,
		) (map[string]any, error) {
			return req.Config, nil
		},
	}

	schema := gr.NewStateSchema().AddField(timeTravelTestFieldCnt, gr.StateField{
		Type: reflect.TypeOf(int(0)),
	})
	exec, err := gr.NewExecutor(
		newTimeTravelTestGraph(t, schema),
		gr.WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	tt, err := exec.TimeTravel()
	require.NoError(t, err)

	_, err = tt.EditState(ctx, gr.CheckpointRef{}, nil)
	require.ErrorIs(t, err, gr.ErrLineageIDRequired)

	_, err = tt.EditState(ctx, baseRef, gr.State{timeTravelTestKeyEmpty: 1})
	require.Error(t, err)

	_, err = tt.EditState(ctx, baseRef, gr.State{timeTravelTestKeyIntK: 1})
	require.Error(t, err)

	_, err = tt.EditState(
		ctx,
		baseRef,
		gr.State{gr.CfgKeyLineageID: timeTravelTestValueX},
	)
	require.Error(t, err)

	ref, err := tt.EditState(
		ctx,
		baseRef,
		gr.State{
			timeTravelTestUnknownK: timeTravelTestValueV,
			timeTravelTestFieldCnt: float64(2),
		},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, saver.putFullCheckpointID, ref.CheckpointID)
	require.Equal(t, 0, saver.putFullStep)
	require.NotNil(t, saver.putFullValues)
	require.Equal(
		t,
		timeTravelTestValueV,
		saver.putFullValues[timeTravelTestUnknownK],
	)

	saver.getTupleFn = func(
		ctx context.Context,
		config map[string]any,
	) (*gr.CheckpointTuple, error) {
		return nil, nil
	}
	_, err = tt.EditState(ctx, baseRef, gr.State{timeTravelTestFieldCnt: 1})
	require.ErrorIs(t, err, gr.ErrCheckpointNotFound)

	saver.getTupleFn = func(
		ctx context.Context,
		config map[string]any,
	) (*gr.CheckpointTuple, error) {
		return &gr.CheckpointTuple{
			Config:        baseCfg,
			Checkpoint:    base,
			Metadata:      gr.NewCheckpointMetadata(gr.CheckpointSourceLoop, 0),
			PendingWrites: nil,
		}, nil
	}
	saver.putFullFn = func(
		ctx context.Context,
		req gr.PutFullRequest,
	) (map[string]any, error) {
		return nil, errTimeTravelTest
	}
	_, err = tt.EditState(ctx, baseRef, gr.State{timeTravelTestFieldCnt: 1})
	require.ErrorIs(t, err, errTimeTravelTest)
	require.ErrorContains(t, err, timeTravelTestErrSaveCk)
}
