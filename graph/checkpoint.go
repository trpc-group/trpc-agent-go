//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	// CheckpointVersion is the current version of the checkpoint format.
	CheckpointVersion = 1

	// CheckpointSourceInput indicates the checkpoint was created from input.
	CheckpointSourceInput = "input"
	// CheckpointSourceLoop indicates the checkpoint was created from inside the loop.
	CheckpointSourceLoop = "loop"
	// CheckpointSourceUpdate indicates the checkpoint was created from manual update.
	CheckpointSourceUpdate = "update"
	// CheckpointSourceFork indicates the checkpoint was created as a copy.
	CheckpointSourceFork = "fork"
	// CheckpointSourceInterrupt indicates the checkpoint was created from an interrupt.
	CheckpointSourceInterrupt = "interrupt"

	// DefaultCheckpointNamespace is the default namespace for checkpoints.
	DefaultCheckpointNamespace = ""
	// DefaultChannelVersion is the default version for channels.
	DefaultChannelVersion = 1
	// DefaultMaxCheckpointsPerLineage is the default maximum number of checkpoints per lineage.
	DefaultMaxCheckpointsPerLineage = 100
)

// Special channel names for interrupt and resume functionality.
const (
	InterruptChannel = "__interrupt__"
	ResumeChannel    = "__resume__"
	ErrorChannel     = "__error__"
	ScheduledChannel = "__scheduled__"
)

// Checkpoint represents a snapshot of graph state at a specific point in time.
type Checkpoint struct {
	// Version is the version of the checkpoint format.
	Version int `json:"v"`
	// ID is the unique identifier for this checkpoint.
	ID string `json:"id"`
	// Timestamp is when the checkpoint was created.
	Timestamp time.Time `json:"ts"`
	// ChannelValues contains the values of channels at checkpoint time.
	ChannelValues map[string]any `json:"channel_values"`
	// ChannelVersions contains the versions of channels at checkpoint time.
	ChannelVersions map[string]int64 `json:"channel_versions"`
	// VersionsSeen tracks which versions each node has seen.
	VersionsSeen map[string]map[string]int64 `json:"versions_seen"`
	// ParentCheckpointID is the ID of the parent checkpoint (for branching).
	ParentCheckpointID string `json:"parent_checkpoint_id,omitempty"`
	// UpdatedChannels lists channels updated in this checkpoint.
	UpdatedChannels []string `json:"updated_channels,omitempty"`
	// PendingSends contains messages that haven't been sent yet.
	PendingSends []PendingSend `json:"pending_sends,omitempty"`
	// InterruptState contains information about the current interrupt state.
	InterruptState *InterruptState `json:"interrupt_state,omitempty"`
	// NextNodes contains the next nodes to execute (alternative to pendingWrites).
	NextNodes []string `json:"next_nodes,omitempty"`
	// NextChannels contains the next channels to trigger (alternative to pendingWrites).
	NextChannels []string `json:"next_channels,omitempty"`
}

// InterruptState represents the state of an interrupted execution.
type InterruptState struct {
	// NodeID is the ID of the node where execution was interrupted.
	NodeID string `json:"node_id"`
	// TaskID is the ID of the task that was interrupted.
	TaskID string `json:"task_id"`
	// InterruptValue is the value that was passed to interrupt().
	InterruptValue any `json:"interrupt_value"`
	// ResumeValues contains values to resume execution with.
	ResumeValues []any `json:"resume_values,omitempty"`
	// Step is the step number when the interrupt occurred.
	Step int `json:"step"`
	// Path is the execution path to the interrupted node.
	Path []string `json:"path,omitempty"`
}

// PendingSend represents a message that hasn't been sent yet.
type PendingSend struct {
	// Channel is the channel to send to.
	Channel string `json:"channel"`
	// Value is the value to send.
	Value any `json:"value"`
	// TaskID is the ID of the task that created this send.
	TaskID string `json:"task_id,omitempty"`
}

// CheckpointMetadata contains metadata about a checkpoint.
type CheckpointMetadata struct {
	// Source indicates how the checkpoint was created.
	Source string `json:"source"`
	// Step is the step number (-1 for input, 0+ for loop steps).
	Step int `json:"step"`
	// Parents maps checkpoint namespaces to parent checkpoint IDs.
	Parents map[string]string `json:"parents"`
	// Additional metadata fields.
	Extra map[string]any `json:"extra,omitempty"`
	// IsResuming indicates if this checkpoint is being resumed from.
	IsResuming bool `json:"is_resuming,omitempty"`
}

// CheckpointTuple wraps a checkpoint with its configuration and metadata.
type CheckpointTuple struct {
	// Config contains the configuration used to create this checkpoint.
	Config map[string]any `json:"config"`
	// Checkpoint is the actual checkpoint data.
	Checkpoint *Checkpoint `json:"checkpoint"`
	// Metadata contains additional checkpoint information.
	Metadata *CheckpointMetadata `json:"metadata"`
	// ParentConfig is the configuration of the parent checkpoint.
	ParentConfig map[string]any `json:"parent_config,omitempty"`
	// PendingWrites contains writes that haven't been committed yet.
	PendingWrites []PendingWrite `json:"pending_writes,omitempty"`
}

// PendingWrite represents a write operation that hasn't been committed.
type PendingWrite struct {
	// TaskID is the ID of the task that created this write.
	TaskID string `json:"task_id"`
	// Channel is the channel being written to.
	Channel string `json:"channel"`
	// Value is the value being written.
	Value any `json:"value"`
	// Sequence is the global sequence number for deterministic replay.
	Sequence int64 `json:"sequence"`
}

// PutRequest contains all data needed to store a checkpoint.
type PutRequest struct {
	Config      map[string]any
	Checkpoint  *Checkpoint
	Metadata    *CheckpointMetadata
	NewVersions map[string]int64
}

// PutWritesRequest contains all data needed to store writes.
type PutWritesRequest struct {
	Config   map[string]any
	Writes   []PendingWrite
	TaskID   string
	TaskPath string
}

// PutFullRequest contains all data needed to atomically store a checkpoint with its writes.
type PutFullRequest struct {
	Config        map[string]any
	Checkpoint    *Checkpoint
	Metadata      *CheckpointMetadata
	NewVersions   map[string]int64
	PendingWrites []PendingWrite
}

// CheckpointSaver defines the interface for checkpoint storage implementations.
type CheckpointSaver interface {
	// Get retrieves a checkpoint by configuration.
	Get(ctx context.Context, config map[string]any) (*Checkpoint, error)
	// GetTuple retrieves a checkpoint tuple by configuration.
	GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error)
	// List retrieves checkpoints matching criteria.
	List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error)
	// Put stores a checkpoint.
	Put(ctx context.Context, req PutRequest) (map[string]any, error)
	// PutWrites stores intermediate writes linked to a checkpoint.
	PutWrites(ctx context.Context, req PutWritesRequest) error
	// PutFull atomically stores a checkpoint with its pending writes in a single transaction.
	PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error)
	// DeleteLineage removes all checkpoints for a lineage.
	DeleteLineage(ctx context.Context, lineageID string) error
	// Close releases resources held by the saver.
	Close() error
}

// CheckpointTree represents the tree structure of checkpoints in a lineage.
type CheckpointTree struct {
	// Root is the root node of the tree.
	Root *CheckpointNode `json:"root"`
	// Branches maps checkpoint IDs to their nodes for quick access.
	Branches map[string]*CheckpointNode `json:"branches"`
}

// CheckpointNode represents a node in the checkpoint tree.
type CheckpointNode struct {
	// Checkpoint is the checkpoint tuple at this node.
	Checkpoint *CheckpointTuple `json:"checkpoint"`
	// Children are the child nodes (forks from this checkpoint).
	Children []*CheckpointNode `json:"children"`
	// Parent is the parent node (null for root).
	Parent *CheckpointNode `json:"-"` // Avoid circular JSON.
}

// CheckpointFilter defines filtering criteria for listing checkpoints.
type CheckpointFilter struct {
	// Before limits results to checkpoints created before this config.
	Before map[string]any `json:"before,omitempty"`
	// Limit is the maximum number of checkpoints to return.
	Limit int `json:"limit,omitempty"`
	// Metadata filters checkpoints by metadata fields.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// CheckpointConfig provides a structured way to handle checkpoint configuration.
type CheckpointConfig struct {
	// LineageID is the unique identifier for the conversation lineage.
	LineageID string
	// CheckpointID is the specific checkpoint to retrieve.
	CheckpointID string
	// Namespace is the checkpoint namespace.
	Namespace string
	// ResumeMap maps task namespaces to resume values.
	ResumeMap map[string]any
	// Extra contains additional configuration fields.
	Extra map[string]any
}

// NewCheckpoint creates a new checkpoint with the given data.
func NewCheckpoint(
	channelValues map[string]any,
	channelVersions map[string]int64,
	versionsSeen map[string]map[string]int64,
) *Checkpoint {
	if channelValues == nil {
		channelValues = make(map[string]any)
	}
	if channelVersions == nil {
		channelVersions = make(map[string]int64)
	}
	if versionsSeen == nil {
		versionsSeen = make(map[string]map[string]int64)
	}
	return &Checkpoint{
		Version:         CheckpointVersion,
		ID:              uuid.New().String(),
		Timestamp:       time.Now().UTC(),
		ChannelValues:   channelValues,
		ChannelVersions: channelVersions,
		VersionsSeen:    versionsSeen,
	}
}

// NewCheckpointMetadata creates new checkpoint metadata.
func NewCheckpointMetadata(source string, step int) *CheckpointMetadata {
	return &CheckpointMetadata{
		Source:  source,
		Step:    step,
		Parents: make(map[string]string),
		Extra:   make(map[string]any),
	}
}

// NewCheckpointConfig creates a new checkpoint configuration.
func NewCheckpointConfig(lineageID string) *CheckpointConfig {
	if lineageID == "" {
		panic("lineage_id cannot be empty")
	}

	// Use default empty namespace to align with LangGraph's design.
	namespace := DefaultCheckpointNamespace

	return &CheckpointConfig{
		LineageID: lineageID,
		Namespace: namespace,
		ResumeMap: make(map[string]any),
		Extra:     make(map[string]any),
	}
}

// WithCheckpointID sets the checkpoint ID.
func (c *CheckpointConfig) WithCheckpointID(checkpointID string) *CheckpointConfig {
	c.CheckpointID = checkpointID
	return c
}

// WithNamespace sets the namespace.
func (c *CheckpointConfig) WithNamespace(namespace string) *CheckpointConfig {
	c.Namespace = namespace
	return c
}

// WithResumeMap sets the resume map.
func (c *CheckpointConfig) WithResumeMap(resumeMap map[string]any) *CheckpointConfig {
	c.ResumeMap = resumeMap
	return c
}

// WithExtra sets additional configuration.
func (c *CheckpointConfig) WithExtra(key string, value any) *CheckpointConfig {
	if c.Extra == nil {
		c.Extra = make(map[string]any)
	}
	c.Extra[key] = value
	return c
}

// ToMap converts the config to a map for backward compatibility.
func (c *CheckpointConfig) ToMap() map[string]any {
	config := map[string]any{
		CfgKeyConfigurable: map[string]any{
			CfgKeyLineageID: c.LineageID,
		},
	}

	if c.CheckpointID != "" {
		config[CfgKeyConfigurable].(map[string]any)[CfgKeyCheckpointID] = c.CheckpointID
	}

	// Always include namespace to ensure consistency (even if empty).
	config[CfgKeyConfigurable].(map[string]any)[CfgKeyCheckpointNS] = c.Namespace

	if len(c.ResumeMap) > 0 {
		config[CfgKeyConfigurable].(map[string]any)[CfgKeyResumeMap] = c.ResumeMap
	}

	// Add extra fields.
	maps.Copy(config, c.Extra)

	return config
}

// NewCheckpointFilter creates a new checkpoint filter.
func NewCheckpointFilter() *CheckpointFilter {
	return &CheckpointFilter{
		Metadata: make(map[string]any),
	}
}

// WithBefore sets the before filter.
func (f *CheckpointFilter) WithBefore(before map[string]any) *CheckpointFilter {
	f.Before = before
	return f
}

// WithLimit sets the limit.
func (f *CheckpointFilter) WithLimit(limit int) *CheckpointFilter {
	f.Limit = limit
	return f
}

// WithMetadata sets metadata filter.
func (f *CheckpointFilter) WithMetadata(key string, value any) *CheckpointFilter {
	if f.Metadata == nil {
		f.Metadata = make(map[string]any)
	}
	f.Metadata[key] = value
	return f
}

// Copy creates a deep copy of the checkpoint.
func (c *Checkpoint) Copy() *Checkpoint {
	if c == nil {
		return nil
	}

	// Deep copy channel values.
	channelValues := deepCopyMap(c.ChannelValues)

	// Deep copy channel versions.
	channelVersions := make(map[string]int64, len(c.ChannelVersions))
	for k, v := range c.ChannelVersions {
		channelVersions[k] = v
	}

	// Deep copy versions seen.
	versionsSeen := make(map[string]map[string]int64, len(c.VersionsSeen))
	for k, v := range c.VersionsSeen {
		inner := make(map[string]int64, len(v))
		for kk, vv := range v {
			inner[kk] = vv
		}
		versionsSeen[k] = inner
	}

	// Deep copy updated channels.
	updatedChannels := deepCopyStringSlice(c.UpdatedChannels)

	// Deep copy pending sends.
	pendingSends := make([]PendingSend, len(c.PendingSends))
	for i, send := range c.PendingSends {
		pendingSends[i] = PendingSend{
			Channel: send.Channel,
			Value:   deepCopy(send.Value),
			TaskID:  send.TaskID,
		}
	}

	// Deep copy interrupt state.
	var interruptState *InterruptState
	if c.InterruptState != nil {
		interruptState = &InterruptState{
			NodeID:         c.InterruptState.NodeID,
			TaskID:         c.InterruptState.TaskID,
			InterruptValue: c.InterruptState.InterruptValue,
			Step:           c.InterruptState.Step,
			Path:           make([]string, len(c.InterruptState.Path)),
		}
		copy(interruptState.Path, c.InterruptState.Path)
		if c.InterruptState.ResumeValues != nil {
			interruptState.ResumeValues = make([]any, len(c.InterruptState.ResumeValues))
			copy(interruptState.ResumeValues, c.InterruptState.ResumeValues)
		}
	}

	// Deep copy next nodes and channels.
	nextNodes := deepCopyStringSlice(c.NextNodes)
	nextChannels := deepCopyStringSlice(c.NextChannels)

	return &Checkpoint{
		Version:            c.Version,
		ID:                 c.ID, // Preserve original ID for true copy.
		Timestamp:          c.Timestamp,
		ChannelValues:      channelValues,
		ChannelVersions:    channelVersions,
		VersionsSeen:       versionsSeen,
		ParentCheckpointID: c.ParentCheckpointID,
		UpdatedChannels:    updatedChannels,
		PendingSends:       pendingSends,
		InterruptState:     interruptState,
		NextNodes:          nextNodes,
		NextChannels:       nextChannels,
	}
}

// Fork creates a copy of the checkpoint with a new ID and sets parent relationship.
// This is used for branching and creating new checkpoints based on existing ones.
func (c *Checkpoint) Fork() *Checkpoint {
	if c == nil {
		return nil
	}
	// Create a true copy first.
	forked := c.Copy()
	// Set the parent to the current checkpoint's ID.
	forked.ParentCheckpointID = c.ID
	// Generate a new ID for the forked checkpoint.
	forked.ID = uuid.New().String()
	// Update timestamp to current time.
	forked.Timestamp = time.Now().UTC()
	return forked
}

// GetCheckpointID extracts checkpoint ID from configuration.
func GetCheckpointID(config map[string]any) string {
	if config == nil {
		return ""
	}
	if configurable, ok := config[CfgKeyConfigurable].(map[string]any); ok {
		if checkpointID, ok := configurable[CfgKeyCheckpointID].(string); ok {
			return checkpointID
		}
	}
	return ""
}

// GetLineageID extracts lineage ID from configuration.
func GetLineageID(config map[string]any) string {
	if config == nil {
		return ""
	}
	if configurable, ok := config[CfgKeyConfigurable].(map[string]any); ok {
		if lineageID, ok := configurable[CfgKeyLineageID].(string); ok {
			return lineageID
		}
	}
	return ""
}

// GetNamespace extracts namespace from configuration.
func GetNamespace(config map[string]any) string {
	if config == nil {
		return DefaultCheckpointNamespace
	}
	if configurable, ok := config[CfgKeyConfigurable].(map[string]any); ok {
		if namespace, ok := configurable[CfgKeyCheckpointNS].(string); ok {
			return namespace
		}
	}
	return DefaultCheckpointNamespace
}

// GetResumeMap extracts resume map from configuration.
func GetResumeMap(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	if configurable, ok := config[CfgKeyConfigurable].(map[string]any); ok {
		if resumeMap, ok := configurable[CfgKeyResumeMap].(map[string]any); ok {
			return resumeMap
		}
	}
	return nil
}

// CreateCheckpointConfig creates a checkpoint configuration (legacy function).
func CreateCheckpointConfig(lineageID string, checkpointID string, namespace string) map[string]any {
	if lineageID == "" {
		panic("lineage_id cannot be empty")
	}
	// Use default empty namespace to align with LangGraph's design.
	if namespace == "" {
		namespace = DefaultCheckpointNamespace
	}

	config := NewCheckpointConfig(lineageID)
	if checkpointID != "" {
		config.WithCheckpointID(checkpointID)
	}
	config.WithNamespace(namespace)
	return config.ToMap()
}

// CheckpointManager provides high-level checkpoint management functionality.
type CheckpointManager struct {
	saver CheckpointSaver
}

// NewCheckpointManager creates a new checkpoint manager.
func NewCheckpointManager(saver CheckpointSaver) *CheckpointManager {
	return &CheckpointManager{
		saver: saver,
	}
}

// CreateCheckpoint creates a new checkpoint from the current state.
func (cm *CheckpointManager) CreateCheckpoint(
	ctx context.Context, config map[string]any, state State, source string, step int,
) (*Checkpoint, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Convert state to channel values with deep copy to prevent races when
	// the saver serializes the checkpoint concurrently with node execution.
	channelValues := make(map[string]any)
	for k, v := range state {
		// Skip non-serializable or volatile keys (callbacks, exec context,
		// session, etc.) to avoid losing function pointers and to prevent
		// JSON marshaling errors in savers.
		if isUnsafeStateKey(k) {
			continue
		}
		channelValues[k] = deepCopyAny(v)
	}

	// Create channel versions (simple incrementing integers for now).
	channelVersions := make(map[string]int64)
	for k := range state {
		channelVersions[k] = int64(DefaultChannelVersion)
	}

	// Create versions seen (simplified for now).
	versionsSeen := make(map[string]map[string]int64)

	// Create checkpoint.
	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	// Create metadata.
	metadata := NewCheckpointMetadata(source, step)

	// Store checkpoint.
	req := PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: channelVersions,
	}
	_, err := cm.saver.Put(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to store checkpoint: %w", err)
	}

	return checkpoint, nil
}

// ResumeFromCheckpoint resumes execution from a specific checkpoint.
func (cm *CheckpointManager) ResumeFromCheckpoint(
	ctx context.Context, config map[string]any,
) (State, error) {
	if cm.saver == nil {
		return nil, nil
	}

	tuple, err := cm.saver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve checkpoint: %w", err)
	}

	if tuple == nil {
		return nil, fmt.Errorf("checkpoint not found")
	}

	if tuple.Checkpoint == nil {
		return nil, fmt.Errorf("checkpoint data is nil")
	}

	// Convert channel values back to state.
	state := make(State)
	for k, v := range tuple.Checkpoint.ChannelValues {
		state[k] = v
	}

	return state, nil
}

// ListCheckpoints lists checkpoints for a lineage.
func (cm *CheckpointManager) ListCheckpoints(
	ctx context.Context, config map[string]any, filter *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.List(ctx, config, filter)
}

// DeleteLineage removes all checkpoints for a lineage.
func (cm *CheckpointManager) DeleteLineage(ctx context.Context, lineageID string) error {
	if cm.saver == nil {
		return fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.DeleteLineage(ctx, lineageID)
}

// Latest returns the most recent checkpoint for a lineage and namespace.
func (cm *CheckpointManager) Latest(
	ctx context.Context, lineageID, namespace string,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	config := CreateCheckpointConfig(lineageID, "", namespace)
	checkpoints, err := cm.saver.List(ctx, config, &CheckpointFilter{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		return nil, nil
	}

	return checkpoints[0], nil
}

// Get retrieves a checkpoint by configuration.
func (cm *CheckpointManager) Get(
	ctx context.Context, config map[string]any,
) (*Checkpoint, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.Get(ctx, config)
}

// GetTuple retrieves a checkpoint tuple by configuration.
func (cm *CheckpointManager) GetTuple(
	ctx context.Context, config map[string]any,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.GetTuple(ctx, config)
}

// Goto jumps to a specific checkpoint by ID.
func (cm *CheckpointManager) Goto(
	ctx context.Context, lineageID, namespace, checkpointID string,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	config := CreateCheckpointConfig(lineageID, checkpointID, namespace)
	return cm.saver.GetTuple(ctx, config)
}

// Put stores a checkpoint.
func (cm *CheckpointManager) Put(
	ctx context.Context, req PutRequest,
) (map[string]any, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.Put(ctx, req)
}

// BranchFrom creates a new checkpoint branch from an existing one within the same lineage.
func (cm *CheckpointManager) BranchFrom(
	ctx context.Context,
	lineageID, namespace, checkpointID, newNamespace string,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the source checkpoint.
	sourceConfig := CreateCheckpointConfig(lineageID, checkpointID, namespace)
	sourceTuple, err := cm.saver.GetTuple(ctx, sourceConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get source checkpoint: %w", err)
	}

	if sourceTuple == nil {
		return nil, fmt.Errorf("source checkpoint not found")
	}

	// Create a new checkpoint in the new namespace.
	newConfig := CreateCheckpointConfig(lineageID, "", newNamespace)
	newCheckpoint := sourceTuple.Checkpoint.Fork() // Fork creates a new ID.
	newCheckpoint.Timestamp = time.Now().UTC()

	// Determine step from source if available.
	var step int
	if sourceTuple.Metadata != nil {
		step = sourceTuple.Metadata.Step
	}

	// Store the new checkpoint.
	req := PutFullRequest{
		Config:        newConfig,
		Checkpoint:    newCheckpoint,
		Metadata:      NewCheckpointMetadata(CheckpointSourceFork, step),
		NewVersions:   newCheckpoint.ChannelVersions,
		PendingWrites: []PendingWrite{},
	}

	updatedConfig, err := cm.saver.PutFull(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch checkpoint: %w", err)
	}

	// Return the new checkpoint tuple.
	return &CheckpointTuple{
		Config:     updatedConfig,
		Checkpoint: newCheckpoint,
		Metadata:   NewCheckpointMetadata(CheckpointSourceFork, step),
	}, nil
}

// BranchToNewLineage creates a new checkpoint in a different lineage from an existing checkpoint.
func (cm *CheckpointManager) BranchToNewLineage(
	ctx context.Context,
	sourceLineageID, sourceNamespace, sourceCheckpointID string,
	newLineageID, newNamespace string,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the source checkpoint.
	// If namespace is empty and we're getting latest, we need to search across all namespaces.
	var sourceCheckpoint *Checkpoint
	var sourceStep int
	var err error
	if sourceCheckpointID == "" {
		// When getting latest without a specific checkpoint ID, search all namespaces if namespace is empty.
		searchConfig := map[string]any{
			CfgKeyConfigurable: map[string]any{
				CfgKeyLineageID: sourceLineageID,
			},
		}
		if sourceNamespace != "" {
			searchConfig[CfgKeyConfigurable].(map[string]any)[CfgKeyCheckpointNS] = sourceNamespace
		}

		tuple, err := cm.saver.GetTuple(ctx, searchConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest checkpoint: %w", err)
		}
		if tuple == nil || tuple.Checkpoint == nil {
			return nil, fmt.Errorf("no checkpoints found in source lineage")
		}
		sourceCheckpoint = tuple.Checkpoint
		if tuple.Metadata != nil {
			sourceStep = tuple.Metadata.Step
		}
	} else {
		// Fetch full tuple to get step as well.
		searchConfig := map[string]any{
			CfgKeyConfigurable: map[string]any{
				CfgKeyLineageID:    sourceLineageID,
				CfgKeyCheckpointID: sourceCheckpointID,
			},
		}
		if sourceNamespace != "" {
			searchConfig[CfgKeyConfigurable].(map[string]any)[CfgKeyCheckpointNS] = sourceNamespace
		}
		tuple, err := cm.saver.GetTuple(ctx, searchConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get source checkpoint: %w", err)
		}
		if tuple == nil || tuple.Checkpoint == nil {
			return nil, fmt.Errorf("source checkpoint not found")
		}
		sourceCheckpoint = tuple.Checkpoint
		if tuple.Metadata != nil {
			sourceStep = tuple.Metadata.Step
		}
	}

	// Create a new checkpoint in the new lineage.
	newConfig := CreateCheckpointConfig(newLineageID, "", newNamespace)
	newCheckpoint := sourceCheckpoint.Fork() // Fork creates a new ID
	newCheckpoint.Timestamp = time.Now().UTC()

	// Create metadata with source information.
	metadata := NewCheckpointMetadata(CheckpointSourceFork, sourceStep)
	metadata.Extra["source_lineage"] = sourceLineageID
	metadata.Extra["source_checkpoint"] = sourceCheckpointID
	metadata.Extra["source_namespace"] = sourceNamespace

	// Store the new checkpoint.
	req := PutRequest{
		Config:      newConfig,
		Checkpoint:  newCheckpoint,
		Metadata:    metadata,
		NewVersions: newCheckpoint.ChannelVersions,
	}

	updatedConfig, err := cm.saver.Put(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch checkpoint in new lineage: %w", err)
	}

	// Return the new checkpoint tuple.
	return &CheckpointTuple{
		Config:     updatedConfig,
		Checkpoint: newCheckpoint,
		Metadata:   metadata,
	}, nil
}

// ResumeFromLatest resumes execution from the latest checkpoint with a resume command.
func (cm *CheckpointManager) ResumeFromLatest(ctx context.Context, lineageID, namespace string, cmd *Command) (State, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the latest checkpoint.
	latest, err := cm.Latest(ctx, lineageID, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest checkpoint: %w", err)
	}

	if latest == nil {
		return nil, fmt.Errorf("no checkpoint found for lineage %s in namespace %s", lineageID, namespace)
	}

	// Convert channel values back to state.
	state := make(State)
	maps.Copy(state, latest.Checkpoint.ChannelValues)
	// Add the resume command.
	if cmd != nil {
		state[StateKeyCommand] = cmd
	}
	return state, nil
}

// IsInterrupted checks if a checkpoint represents an interrupted execution.
func (c *Checkpoint) IsInterrupted() bool {
	return c.InterruptState != nil && c.InterruptState.NodeID != ""
}

// GetInterruptValue returns the interrupt value if the checkpoint is interrupted.
func (c *Checkpoint) GetInterruptValue() any {
	if c.IsInterrupted() {
		return c.InterruptState.InterruptValue
	}
	return nil
}

// GetResumeValues returns the resume values for the interrupted execution.
func (c *Checkpoint) GetResumeValues() []any {
	if c.IsInterrupted() && c.InterruptState.ResumeValues != nil {
		return c.InterruptState.ResumeValues
	}
	return nil
}

// AddResumeValue adds a resume value to the checkpoint.
func (c *Checkpoint) AddResumeValue(value any) {
	if c.InterruptState == nil {
		c.InterruptState = &InterruptState{}
	}
	c.InterruptState.ResumeValues = append(c.InterruptState.ResumeValues, value)
}

// SetInterruptState sets the interrupt state for the checkpoint.
func (c *Checkpoint) SetInterruptState(nodeID, taskID string, interruptValue any, step int, path []string) {
	c.InterruptState = &InterruptState{
		NodeID:         nodeID,
		TaskID:         taskID,
		InterruptValue: interruptValue,
		Step:           step,
		Path:           make([]string, len(path)),
		ResumeValues:   make([]any, 0),
	}
	copy(c.InterruptState.Path, path)
}

// ClearInterruptState clears the interrupt state.
func (c *Checkpoint) ClearInterruptState() {
	c.InterruptState = nil
}

// GetCheckpointTree builds the tree structure of checkpoints in a lineage.
func (cm *CheckpointManager) GetCheckpointTree(
	ctx context.Context, lineageID string,
) (*CheckpointTree, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get all checkpoints for the lineage.
	config := CreateCheckpointConfig(lineageID, "", "")
	allCheckpoints, err := cm.saver.List(ctx, config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(allCheckpoints) == 0 {
		return &CheckpointTree{
			Root:     nil,
			Branches: make(map[string]*CheckpointNode),
		}, nil
	}

	// Build the tree structure.
	nodes := make(map[string]*CheckpointNode)
	var rootNodes []*CheckpointNode

	// First pass: create all nodes.
	for _, tuple := range allCheckpoints {
		node := &CheckpointNode{
			Checkpoint: tuple,
			Children:   []*CheckpointNode{},
		}
		nodes[tuple.Checkpoint.ID] = node
	}

	// Second pass: establish parent-child relationships.
	for _, tuple := range allCheckpoints {
		node := nodes[tuple.Checkpoint.ID]
		parentID := tuple.Checkpoint.ParentCheckpointID

		if parentID != "" {
			if parent, exists := nodes[parentID]; exists {
				parent.Children = append(parent.Children, node)
				node.Parent = parent
			} else {
				// Parent not found, treat as root.
				rootNodes = append(rootNodes, node)
			}
		} else {
			// No parent, this is a root node.
			rootNodes = append(rootNodes, node)
		}
	}

	// Sort children by timestamp for consistent ordering.
	for _, node := range nodes {
		sort.Slice(node.Children, func(i, j int) bool {
			return node.Children[i].Checkpoint.Checkpoint.Timestamp.Before(
				node.Children[j].Checkpoint.Checkpoint.Timestamp,
			)
		})
	}

	// Find the primary root (oldest without parent).
	var root *CheckpointNode
	if len(rootNodes) > 0 {
		root = rootNodes[0]
		for _, node := range rootNodes[1:] {
			if node.Checkpoint.Checkpoint.Timestamp.Before(root.Checkpoint.Checkpoint.Timestamp) {
				root = node
			}
		}
	}

	return &CheckpointTree{
		Root:     root,
		Branches: nodes,
	}, nil
}

// ListChildren returns the direct children of a checkpoint.
func (cm *CheckpointManager) ListChildren(
	ctx context.Context, config map[string]any,
) ([]*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the parent checkpoint to find its ID.
	parentTuple, err := cm.saver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent checkpoint: %w", err)
	}
	if parentTuple == nil {
		return nil, fmt.Errorf("parent checkpoint not found")
	}

	// Get all checkpoints in the lineage.
	lineageID := GetLineageID(config)
	allConfig := CreateCheckpointConfig(lineageID, "", "")
	allCheckpoints, err := cm.saver.List(ctx, allConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	// Filter to find children.
	var children []*CheckpointTuple
	parentID := parentTuple.Checkpoint.ID
	for _, tuple := range allCheckpoints {
		if tuple.Checkpoint.ParentCheckpointID == parentID {
			children = append(children, tuple)
		}
	}

	// Sort by timestamp.
	sort.Slice(children, func(i, j int) bool {
		return children[i].Checkpoint.Timestamp.Before(children[j].Checkpoint.Timestamp)
	})

	return children, nil
}

// GetParent returns the parent checkpoint of the given checkpoint.
func (cm *CheckpointManager) GetParent(
	ctx context.Context, config map[string]any,
) (*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Get the current checkpoint.
	currentTuple, err := cm.saver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get checkpoint: %w", err)
	}
	if currentTuple == nil {
		return nil, fmt.Errorf("checkpoint not found")
	}

	// Check if it has a parent.
	parentID := currentTuple.Checkpoint.ParentCheckpointID
	if parentID == "" {
		return nil, nil // No parent.
	}

	// Prefer using explicit ParentConfig if available (handles cross-namespace parents).
	if currentTuple.ParentConfig != nil {
		// Try using provided parent config first.
		if tuple, err := cm.saver.GetTuple(ctx, currentTuple.ParentConfig); err != nil {
			return nil, fmt.Errorf("failed to get parent checkpoint: %w", err)
		} else if tuple != nil {
			return tuple, nil
		}
		// If not found (possibly due to namespace mismatch), try cross-namespace search.
		lineageID := GetLineageID(currentTuple.ParentConfig)
		crossNSCfg := CreateCheckpointConfig(lineageID, parentID, "")
		if tuple, err := cm.saver.GetTuple(ctx, crossNSCfg); err == nil && tuple != nil {
			return tuple, nil
		}
	}

	// Fallback: use same-namespace lookup.
	lineageID := GetLineageID(config)
	namespace := GetNamespace(config)
	parentConfig := CreateCheckpointConfig(lineageID, parentID, namespace)
	return cm.saver.GetTuple(ctx, parentConfig)
}

// deepCopy performs a deep copy using JSON marshaling/unmarshaling for safety.
func deepCopy(src any) any {
	if src == nil {
		return nil
	}

	// Marshal to JSON.
	data, err := json.Marshal(src)
	if err != nil {
		// If marshaling fails, return the original value.
		return src
	}

	// Unmarshal to a generic map with number preservation.
	var result any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Preserve number types as json.Number.
	if err := decoder.Decode(&result); err != nil {
		// If unmarshaling fails, return the original value.
		return src
	}
	return result
}

// deepCopyMap performs a deep copy of a map[string]any.
func deepCopyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}

	result := deepCopy(src)
	if mapResult, ok := result.(map[string]any); ok {
		return mapResult
	}

	// Fallback: create a new map and copy values.
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopy(v)
	}
	return dst
}

// deepCopyStringSlice performs a deep copy of a []string.
func deepCopyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}

	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}
