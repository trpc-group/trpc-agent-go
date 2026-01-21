//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// CheckpointMetaKeyBaseCheckpointID is stored in CheckpointMetadata.Extra
	// when a checkpoint is created via TimeTravel.EditState.
	CheckpointMetaKeyBaseCheckpointID = "base_checkpoint_id"
	// CheckpointMetaKeyUpdatedKeys is stored in CheckpointMetadata.Extra when a
	// checkpoint is created via TimeTravel.EditState.
	CheckpointMetaKeyUpdatedKeys = "updated_keys"
)

// CheckpointRef is a stable pointer to a checkpoint.
//
// It is intentionally small and "UI friendly":
//   - It can be stored outside the runtime (e.g. in DB / UI state).
//   - It can be converted to:
//   - saver config (for CheckpointSaver APIs)
//   - runtime_state (for GraphAgent / Runner resume)
type CheckpointRef struct {
	LineageID    string
	Namespace    string
	CheckpointID string
}

// Validate returns an error when the ref is incomplete.
func (r CheckpointRef) Validate() error {
	if r.LineageID == "" {
		return ErrLineageIDRequired
	}
	return nil
}

// ToSaverConfig converts the ref into a config map for CheckpointSaver.
//
// When CheckpointID is empty, most savers interpret it as
// "latest checkpoint".
func (r CheckpointRef) ToSaverConfig() (map[string]any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	cfg := NewCheckpointConfig(r.LineageID).WithNamespace(r.Namespace)
	if r.CheckpointID != "" {
		cfg.WithCheckpointID(r.CheckpointID)
	}
	return cfg.ToMap(), nil
}

// ToRuntimeState converts the ref into the runtime_state map expected by
// GraphAgent / Runner.
//
// Note: checkpoint_id is always present so callers can use an empty string to
// mean "resume from latest checkpoint" (GraphAgent uses key presence as the
// resume signal).
func (r CheckpointRef) ToRuntimeState() map[string]any {
	state := map[string]any{
		CfgKeyLineageID:    r.LineageID,
		CfgKeyCheckpointID: r.CheckpointID,
	}
	if r.Namespace != "" {
		state[CfgKeyCheckpointNS] = r.Namespace
	}
	return state
}

// CheckpointInfo is a lightweight checkpoint header for history views.
type CheckpointInfo struct {
	Ref              CheckpointRef
	ParentCheckpoint string
	Source           string
	Step             int
	Timestamp        time.Time
}

// StateSnapshot is a checkpoint state snapshot suitable for debugging and
// HITL.
type StateSnapshot struct {
	CheckpointInfo
	State        State
	NextNodes    []string
	NextChannels []string
}

// TimeTravel provides first-class "query / edit / resume" operations built on
// top of the checkpoint system.
//
// It is additive and does not change existing checkpoint/resume semantics
// unless explicitly called by the user.
type TimeTravel struct {
	executor *Executor
	saver    CheckpointSaver
}

// TimeTravel returns a helper bound to this executor.
func (e *Executor) TimeTravel() (*TimeTravel, error) {
	if e == nil || e.checkpointSaver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return &TimeTravel{
		executor: e,
		saver:    e.checkpointSaver,
	}, nil
}

// GetState returns the state snapshot at the referenced checkpoint.
//
// If ref.CheckpointID is empty, the latest checkpoint in the namespace is
// used.
func (t *TimeTravel) GetState(
	ctx context.Context,
	ref CheckpointRef,
) (*StateSnapshot, error) {
	if t == nil || t.saver == nil || t.executor == nil {
		return nil, fmt.Errorf("time travel is not configured")
	}
	cfg, err := ref.ToSaverConfig()
	if err != nil {
		return nil, err
	}
	tuple, err := t.saver.GetTuple(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}
	if tuple == nil || tuple.Checkpoint == nil {
		return nil, ErrCheckpointNotFound
	}
	return t.snapshotFromTuple(tuple), nil
}

// History returns checkpoint headers in descending timestamp order.
func (t *TimeTravel) History(
	ctx context.Context,
	lineageID string,
	namespace string,
	limit int,
) ([]CheckpointInfo, error) {
	if t == nil || t.saver == nil {
		return nil, fmt.Errorf("time travel is not configured")
	}
	if lineageID == "" {
		return nil, ErrLineageIDRequired
	}
	cfg := NewCheckpointConfig(lineageID).WithNamespace(namespace).ToMap()
	filter := &CheckpointFilter{Limit: limit}
	if limit <= 0 {
		filter = nil
	}
	tuples, err := t.saver.List(ctx, cfg, filter)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	out := make([]CheckpointInfo, 0, len(tuples))
	for _, tuple := range tuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		info := t.infoFromTuple(tuple)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out, nil
}

// EditStateOption configures EditState.
type EditStateOption func(*editStateOptions)

type editStateOptions struct {
	allowInternalKeys bool
}

// WithAllowInternalKeys allows editing internal runtime keys (keys that start
// with "__" or belong to checkpoint/runtime wiring).
//
// Most users should not enable this.
func WithAllowInternalKeys() EditStateOption {
	return func(o *editStateOptions) {
		o.allowInternalKeys = true
	}
}

// EditState creates a new checkpoint derived from base, with patched state.
//
// It writes a new checkpoint with:
//   - Source = "update"
//   - ParentCheckpointID = base checkpoint ID
//
// The new checkpoint is safe to resume from via:
//
//	agent.WithRuntimeState(newRef.ToRuntimeState())
func (t *TimeTravel) EditState(
	ctx context.Context,
	base CheckpointRef,
	patch State,
	opts ...EditStateOption,
) (CheckpointRef, error) {
	if t == nil || t.saver == nil || t.executor == nil {
		return CheckpointRef{}, fmt.Errorf("time travel is not configured")
	}
	if err := base.Validate(); err != nil {
		return CheckpointRef{}, err
	}

	options := editStateOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&options)
	}

	baseCfg, err := base.ToSaverConfig()
	if err != nil {
		return CheckpointRef{}, err
	}
	baseTuple, err := t.saver.GetTuple(ctx, baseCfg)
	if err != nil {
		return CheckpointRef{}, fmt.Errorf("get checkpoint: %w", err)
	}
	if baseTuple == nil || baseTuple.Checkpoint == nil {
		return CheckpointRef{}, ErrCheckpointNotFound
	}

	updated := baseTuple.Checkpoint.Fork()
	if updated == nil {
		return CheckpointRef{}, fmt.Errorf("fork checkpoint")
	}
	if updated.ChannelValues == nil {
		updated.ChannelValues = make(map[string]any)
	}

	updatedKeys := make([]string, 0, len(patch))
	for key, value := range patch {
		if !options.allowInternalKeys &&
			isProtectedTimeTravelKey(key) {
			return CheckpointRef{},
				fmt.Errorf("cannot edit key %q", key)
		}
		value = t.coerceValue(key, value)
		updated.ChannelValues[key] = deepCopyAny(value)
		updatedKeys = append(updatedKeys, key)
	}
	sort.Strings(updatedKeys)

	step := 0
	if baseTuple.Metadata != nil {
		step = baseTuple.Metadata.Step
	}
	metadata := NewCheckpointMetadata(CheckpointSourceUpdate, step)
	metadata.Parents = map[string]string{
		GetNamespace(baseCfg): baseTuple.Checkpoint.ID,
	}
	metadata.Extra[CheckpointMetaKeyBaseCheckpointID] =
		baseTuple.Checkpoint.ID
	metadata.Extra[CheckpointMetaKeyUpdatedKeys] = updatedKeys

	pendingWrites := make([]PendingWrite, len(baseTuple.PendingWrites))
	copy(pendingWrites, baseTuple.PendingWrites)

	lineageID := GetLineageID(baseCfg)
	namespace := GetNamespace(baseCfg)
	putCfg := NewCheckpointConfig(lineageID).WithNamespace(namespace).ToMap()

	updatedCfg, err := t.saver.PutFull(ctx, PutFullRequest{
		Config:        putCfg,
		Checkpoint:    updated,
		Metadata:      metadata,
		NewVersions:   updated.ChannelVersions,
		PendingWrites: pendingWrites,
	})
	if err != nil {
		return CheckpointRef{}, fmt.Errorf("save checkpoint: %w", err)
	}
	return checkpointRefFromConfig(updatedCfg, updated.ID), nil
}

func (t *TimeTravel) snapshotFromTuple(
	tuple *CheckpointTuple,
) *StateSnapshot {
	info := t.infoFromTuple(tuple)
	state := t.executor.restoreStateFromCheckpoint(tuple)
	return &StateSnapshot{
		CheckpointInfo: info,
		State:          state,
		NextNodes:      tuple.Checkpoint.NextNodes,
		NextChannels:   tuple.Checkpoint.NextChannels,
	}
}

func (t *TimeTravel) infoFromTuple(tuple *CheckpointTuple) CheckpointInfo {
	ref := checkpointRefFromConfig(tuple.Config, "")
	if tuple != nil && tuple.Checkpoint != nil {
		if ref.CheckpointID == "" {
			ref.CheckpointID = tuple.Checkpoint.ID
		}
	}

	var source string
	var step int
	if tuple != nil && tuple.Metadata != nil {
		source = tuple.Metadata.Source
		step = tuple.Metadata.Step
	}

	info := CheckpointInfo{
		Ref:              ref,
		ParentCheckpoint: tuple.Checkpoint.ParentCheckpointID,
		Source:           source,
		Step:             step,
		Timestamp:        tuple.Checkpoint.Timestamp,
	}
	return info
}

func (t *TimeTravel) coerceValue(key string, value any) any {
	if t.executor == nil || t.executor.graph == nil {
		return value
	}
	schema := t.executor.graph.Schema()
	if schema == nil {
		return value
	}
	field, exists := schema.Fields[key]
	if !exists {
		return value
	}
	return t.executor.restoreCheckpointValueWithSchema(value, field)
}

func checkpointRefFromConfig(
	config map[string]any,
	fallbackID string,
) CheckpointRef {
	ref := CheckpointRef{
		LineageID:    GetLineageID(config),
		Namespace:    GetNamespace(config),
		CheckpointID: GetCheckpointID(config),
	}
	if ref.CheckpointID == "" {
		ref.CheckpointID = fallbackID
	}
	return ref
}

func isProtectedTimeTravelKey(key string) bool {
	if key == "" {
		return true
	}
	if strings.HasPrefix(key, "__") {
		return true
	}
	if isUnsafeStateKey(key) {
		return true
	}
	switch key {
	case CfgKeyLineageID,
		CfgKeyCheckpointID,
		CfgKeyCheckpointNS:
		return true
	default:
		return false
	}
}
