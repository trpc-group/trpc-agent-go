//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	// CheckpointVersion is the current version of the checkpoint format.
	CheckpointVersion = 2

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
	// DefaultMaxCheckpointsPerThread is the default maximum number of checkpoints per thread.
	DefaultMaxCheckpointsPerThread = 100
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
	ChannelVersions map[string]any `json:"channel_versions"`
	// VersionsSeen tracks which versions each node has seen.
	VersionsSeen map[string]map[string]any `json:"versions_seen"`
	// UpdatedChannels lists channels updated in this checkpoint.
	UpdatedChannels []string `json:"updated_channels,omitempty"`
	// PendingSends contains messages that haven't been sent yet.
	PendingSends []PendingSend `json:"pending_sends,omitempty"`
	// InterruptState contains information about the current interrupt state.
	InterruptState *InterruptState `json:"interrupt_state,omitempty"`
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
}

// PutRequest contains all data needed to store a checkpoint.
type PutRequest struct {
	Config      map[string]any
	Checkpoint  *Checkpoint
	Metadata    *CheckpointMetadata
	NewVersions map[string]any
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
	NewVersions   map[string]any
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
	// DeleteThread removes all checkpoints for a thread.
	DeleteThread(ctx context.Context, threadID string) error
	// Close releases resources held by the saver.
	Close() error
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
	// ThreadID is the unique identifier for the conversation thread.
	ThreadID string
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
func NewCheckpoint(channelValues map[string]any, channelVersions map[string]any, versionsSeen map[string]map[string]any) *Checkpoint {
	if channelValues == nil {
		channelValues = make(map[string]any)
	}
	if channelVersions == nil {
		channelVersions = make(map[string]any)
	}
	if versionsSeen == nil {
		versionsSeen = make(map[string]map[string]any)
	}

	return &Checkpoint{
		Version:         CheckpointVersion,
		ID:              uuid.New().String(),
		Timestamp:       time.Now().UTC(),
		ChannelValues:   channelValues,
		ChannelVersions: channelVersions,
		VersionsSeen:    versionsSeen,
		UpdatedChannels: make([]string, 0),
		PendingSends:    make([]PendingSend, 0),
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
func NewCheckpointConfig(threadID string) *CheckpointConfig {
	return &CheckpointConfig{
		ThreadID:  threadID,
		Namespace: DefaultCheckpointNamespace,
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
		"configurable": map[string]any{
			"thread_id": c.ThreadID,
		},
	}

	if c.CheckpointID != "" {
		config["configurable"].(map[string]any)["checkpoint_id"] = c.CheckpointID
	}

	if c.Namespace != "" {
		config["configurable"].(map[string]any)["checkpoint_ns"] = c.Namespace
	}

	if len(c.ResumeMap) > 0 {
		config["configurable"].(map[string]any)["resume_map"] = c.ResumeMap
	}

	// Add extra fields.
	for k, v := range c.Extra {
		config[k] = v
	}

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
	channelVersions := deepCopyMap(c.ChannelVersions)

	// Deep copy versions seen.
	versionsSeen := make(map[string]map[string]any, len(c.VersionsSeen))
	for k, v := range c.VersionsSeen {
		versionsSeen[k] = deepCopyMap(v)
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

	return &Checkpoint{
		Version:         c.Version,
		ID:              uuid.New().String(), // Generate new ID for copy
		Timestamp:       c.Timestamp,
		ChannelValues:   channelValues,
		ChannelVersions: channelVersions,
		VersionsSeen:    versionsSeen,
		UpdatedChannels: updatedChannels,
		PendingSends:    pendingSends,
		InterruptState:  interruptState,
	}
}

// GetCheckpointID extracts checkpoint ID from configuration.
func GetCheckpointID(config map[string]any) string {
	if config == nil {
		return ""
	}
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if checkpointID, ok := configurable["checkpoint_id"].(string); ok {
			return checkpointID
		}
	}
	return ""
}

// GetThreadID extracts thread ID from configuration.
func GetThreadID(config map[string]any) string {
	if config == nil {
		return ""
	}
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if threadID, ok := configurable["thread_id"].(string); ok {
			return threadID
		}
	}
	return ""
}

// GetNamespace extracts namespace from configuration.
func GetNamespace(config map[string]any) string {
	if config == nil {
		return DefaultCheckpointNamespace
	}
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if namespace, ok := configurable["checkpoint_ns"].(string); ok {
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
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if resumeMap, ok := configurable["resume_map"].(map[string]any); ok {
			return resumeMap
		}
	}
	return nil
}

// CreateCheckpointConfig creates a checkpoint configuration (legacy function).
func CreateCheckpointConfig(threadID string, checkpointID string, namespace string) map[string]any {
	config := NewCheckpointConfig(threadID)
	if checkpointID != "" {
		config.WithCheckpointID(checkpointID)
	}
	if namespace != "" {
		config.WithNamespace(namespace)
	}
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
func (cm *CheckpointManager) CreateCheckpoint(ctx context.Context, config map[string]any, state State, source string, step int) (*Checkpoint, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}

	// Convert state to channel values.
	channelValues := make(map[string]any)
	for k, v := range state {
		channelValues[k] = v
	}

	// Create channel versions (simple incrementing integers for now).
	channelVersions := make(map[string]any)
	for k := range state {
		channelVersions[k] = DefaultChannelVersion
	}

	// Create versions seen (simplified for now).
	versionsSeen := make(map[string]map[string]any)

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
func (cm *CheckpointManager) ResumeFromCheckpoint(ctx context.Context, config map[string]any) (State, error) {
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

// ListCheckpoints lists checkpoints for a thread.
func (cm *CheckpointManager) ListCheckpoints(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	if cm.saver == nil {
		return nil, fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.List(ctx, config, filter)
}

// DeleteThread removes all checkpoints for a thread.
func (cm *CheckpointManager) DeleteThread(ctx context.Context, threadID string) error {
	if cm.saver == nil {
		return fmt.Errorf("checkpoint saver is not configured")
	}
	return cm.saver.DeleteThread(ctx, threadID)
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

// deepCopy performs a deep copy using JSON marshaling/unmarshaling for safety.
func deepCopy(src any) any {
	if src == nil {
		return nil
	}

	// Marshal to JSON
	data, err := json.Marshal(src)
	if err != nil {
		// If marshaling fails, return the original value
		return src
	}

	// Unmarshal to a generic map
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		// If unmarshaling fails, return the original value
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

	// Fallback: create a new map and copy values
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopy(v)
	}
	return dst
}

// deepCopySlice performs a deep copy of a []any.
func deepCopySlice(src []any) []any {
	if src == nil {
		return nil
	}

	result := deepCopy(src)
	if sliceResult, ok := result.([]any); ok {
		return sliceResult
	}

	// Fallback: create a new slice and copy values
	dst := make([]any, len(src))
	for i, v := range src {
		dst[i] = deepCopy(v)
	}
	return dst
}

// deepCopyStringMap performs a deep copy of a map[string]string.
func deepCopyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
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
