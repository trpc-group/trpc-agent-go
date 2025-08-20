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
	"fmt"
	"sync"
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

	// DefaultCheckpointNamespace is the default namespace for checkpoints.
	DefaultCheckpointNamespace = ""
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
	// Channel is the channel being written to.
	Channel string `json:"channel"`
	// Value is the value being written.
	Value any `json:"value"`
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
	Put(ctx context.Context, config map[string]any, checkpoint *Checkpoint, metadata *CheckpointMetadata, newVersions map[string]any) (map[string]any, error)
	// PutWrites stores intermediate writes linked to a checkpoint.
	PutWrites(ctx context.Context, config map[string]any, writes []PendingWrite, taskID string, taskPath string) error
	// DeleteThread removes all checkpoints for a thread.
	DeleteThread(ctx context.Context, threadID string) error
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

// CheckpointConfig contains configuration for checkpoint operations.
type CheckpointConfig struct {
	// ThreadID is the unique identifier for the conversation thread.
	ThreadID string `json:"thread_id"`
	// CheckpointID is the specific checkpoint to retrieve.
	CheckpointID string `json:"checkpoint_id,omitempty"`
	// Namespace is the checkpoint namespace.
	Namespace string `json:"checkpoint_ns,omitempty"`
	// Additional configuration fields.
	Extra map[string]any `json:"extra,omitempty"`
}

// NewCheckpoint creates a new checkpoint with the given data.
func NewCheckpoint(channelValues map[string]any, channelVersions map[string]any, versionsSeen map[string]map[string]any) *Checkpoint {
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
		Parents: map[string]string{},
		Extra:   map[string]any{},
	}
}

// Copy creates a deep copy of the checkpoint.
func (c *Checkpoint) Copy() *Checkpoint {
	if c == nil {
		return nil
	}

	// Deep copy channel values.
	channelValues := make(map[string]any, len(c.ChannelValues))
	for k, v := range c.ChannelValues {
		channelValues[k] = v
	}

	// Deep copy channel versions.
	channelVersions := make(map[string]any, len(c.ChannelVersions))
	for k, v := range c.ChannelVersions {
		channelVersions[k] = v
	}

	// Deep copy versions seen.
	versionsSeen := make(map[string]map[string]any, len(c.VersionsSeen))
	for k, v := range c.VersionsSeen {
		versionsSeen[k] = make(map[string]any, len(v))
		for k2, v2 := range v {
			versionsSeen[k][k2] = v2
		}
	}

	// Deep copy updated channels.
	updatedChannels := make([]string, len(c.UpdatedChannels))
	copy(updatedChannels, c.UpdatedChannels)

	return &Checkpoint{
		Version:         c.Version,
		ID:              uuid.New().String(), // Generate new ID for copy
		Timestamp:       c.Timestamp,
		ChannelValues:   channelValues,
		ChannelVersions: channelVersions,
		VersionsSeen:    versionsSeen,
		UpdatedChannels: updatedChannels,
	}
}

// GetCheckpointID extracts checkpoint ID from configuration.
func GetCheckpointID(config map[string]any) string {
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if checkpointID, ok := configurable["checkpoint_id"].(string); ok {
			return checkpointID
		}
	}
	return ""
}

// GetThreadID extracts thread ID from configuration.
func GetThreadID(config map[string]any) string {
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if threadID, ok := configurable["thread_id"].(string); ok {
			return threadID
		}
	}
	return ""
}

// GetNamespace extracts namespace from configuration.
func GetNamespace(config map[string]any) string {
	if configurable, ok := config["configurable"].(map[string]any); ok {
		if namespace, ok := configurable["checkpoint_ns"].(string); ok {
			return namespace
		}
	}
	return DefaultCheckpointNamespace
}

// CreateCheckpointConfig creates a checkpoint configuration.
func CreateCheckpointConfig(threadID string, checkpointID string, namespace string) map[string]any {
	config := map[string]any{
		"configurable": map[string]any{
			"thread_id": threadID,
		},
	}

	if checkpointID != "" {
		config["configurable"].(map[string]any)["checkpoint_id"] = checkpointID
	}

	if namespace != "" {
		config["configurable"].(map[string]any)["checkpoint_ns"] = namespace
	}

	return config
}

// InMemoryCheckpointSaver provides an in-memory implementation of CheckpointSaver.
// This is suitable for testing and debugging but not for production use.
type InMemoryCheckpointSaver struct {
	mu      sync.RWMutex
	storage map[string]map[string]map[string]*CheckpointTuple // threadID -> namespace -> checkpointID -> tuple
	writes  map[string]map[string]map[string][]PendingWrite   // threadID -> namespace -> checkpointID -> writes
}

// NewInMemoryCheckpointSaver creates a new in-memory checkpoint saver.
func NewInMemoryCheckpointSaver() *InMemoryCheckpointSaver {
	return &InMemoryCheckpointSaver{
		storage: make(map[string]map[string]map[string]*CheckpointTuple),
		writes:  make(map[string]map[string]map[string][]PendingWrite),
	}
}

// Get retrieves a checkpoint by configuration.
func (s *InMemoryCheckpointSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	tuple, err := s.GetTuple(ctx, config)
	if err != nil {
		return nil, err
	}
	if tuple == nil {
		return nil, nil
	}
	return tuple.Checkpoint, nil
}

// GetTuple retrieves a checkpoint tuple by configuration.
func (s *InMemoryCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	threadID := GetThreadID(config)
	namespace := GetNamespace(config)
	checkpointID := GetCheckpointID(config)

	if threadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	// Get the latest checkpoint if no specific ID is provided.
	if checkpointID == "" {
		namespaces, exists := s.storage[threadID]
		if !exists {
			return nil, nil
		}

		checkpoints, exists := namespaces[namespace]
		if !exists || len(checkpoints) == 0 {
			return nil, nil
		}

		// Find the latest checkpoint by ID (assuming UUIDs are sortable).
		var latestID string
		for id := range checkpoints {
			if id > latestID {
				latestID = id
			}
		}

		if latestID == "" {
			return nil, nil
		}

		checkpointID = latestID
		// Update config with the found checkpoint ID.
		if configurable, ok := config["configurable"].(map[string]any); ok {
			configurable["checkpoint_id"] = checkpointID
		}
	}

	// Retrieve the specific checkpoint.
	namespaces, exists := s.storage[threadID]
	if !exists {
		return nil, nil
	}

	checkpoints, exists := namespaces[namespace]
	if !exists {
		return nil, nil
	}

	tuple, exists := checkpoints[checkpointID]
	if !exists {
		return nil, nil
	}

	// Add pending writes if they exist.
	if writes, exists := s.writes[threadID][namespace][checkpointID]; exists {
		tuple.PendingWrites = writes
	}

	return tuple, nil
}

// List retrieves checkpoints matching criteria.
func (s *InMemoryCheckpointSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	threadID := GetThreadID(config)
	namespace := GetNamespace(config)

	if threadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	var results []*CheckpointTuple

	namespaces, exists := s.storage[threadID]
	if !exists {
		return results, nil
	}

	checkpoints, exists := namespaces[namespace]
	if !exists {
		return results, nil
	}

	// Apply filters and collect results.
	for checkpointID, tuple := range checkpoints {
		// Apply before filter.
		if filter != nil && filter.Before != nil {
			beforeID := GetCheckpointID(filter.Before)
			if beforeID != "" && checkpointID >= beforeID {
				continue
			}
		}

		// Apply metadata filter.
		if filter != nil && filter.Metadata != nil {
			matches := true
			for key, value := range filter.Metadata {
				if tuple.Metadata.Extra[key] != value {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}

		// Add pending writes.
		if writes, exists := s.writes[threadID][namespace][checkpointID]; exists {
			tuple.PendingWrites = writes
		}

		results = append(results, tuple)

		// Apply limit.
		if filter != nil && filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	return results, nil
}

// Put stores a checkpoint.
func (s *InMemoryCheckpointSaver) Put(ctx context.Context, config map[string]any, checkpoint *Checkpoint, metadata *CheckpointMetadata, newVersions map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	threadID := GetThreadID(config)
	namespace := GetNamespace(config)

	if threadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	if checkpoint == nil {
		return nil, fmt.Errorf("checkpoint cannot be nil")
	}

	// Initialize storage structure if needed.
	if s.storage[threadID] == nil {
		s.storage[threadID] = make(map[string]map[string]*CheckpointTuple)
	}
	if s.storage[threadID][namespace] == nil {
		s.storage[threadID][namespace] = make(map[string]*CheckpointTuple)
	}

	// Create checkpoint tuple.
	tuple := &CheckpointTuple{
		Config:     config,
		Checkpoint: checkpoint,
		Metadata:   metadata,
	}

	// Set parent config if there's a parent checkpoint ID.
	if parentID := GetCheckpointID(config); parentID != "" {
		tuple.ParentConfig = CreateCheckpointConfig(threadID, parentID, namespace)
	}

	// Store the checkpoint.
	s.storage[threadID][namespace][checkpoint.ID] = tuple

	// Return updated config with the new checkpoint ID.
	updatedConfig := CreateCheckpointConfig(threadID, checkpoint.ID, namespace)
	return updatedConfig, nil
}

// PutWrites stores intermediate writes linked to a checkpoint.
func (s *InMemoryCheckpointSaver) PutWrites(ctx context.Context, config map[string]any, writes []PendingWrite, taskID string, taskPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	threadID := GetThreadID(config)
	namespace := GetNamespace(config)
	checkpointID := GetCheckpointID(config)

	if threadID == "" || checkpointID == "" {
		return fmt.Errorf("thread_id and checkpoint_id are required")
	}

	// Initialize writes structure if needed.
	if s.writes[threadID] == nil {
		s.writes[threadID] = make(map[string]map[string][]PendingWrite)
	}
	if s.writes[threadID][namespace] == nil {
		s.writes[threadID][namespace] = make(map[string][]PendingWrite)
	}

	// Store the writes.
	s.writes[threadID][namespace][checkpointID] = writes

	return nil
}

// DeleteThread removes all checkpoints for a thread.
func (s *InMemoryCheckpointSaver) DeleteThread(ctx context.Context, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.storage, threadID)
	delete(s.writes, threadID)

	return nil
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
	// Convert state to channel values.
	channelValues := make(map[string]any)
	for k, v := range state {
		channelValues[k] = v
	}

	// Create channel versions (simple incrementing integers for now).
	channelVersions := make(map[string]any)
	for k := range state {
		channelVersions[k] = 1 // This should be managed by the graph execution.
	}

	// Create versions seen (simplified for now).
	versionsSeen := make(map[string]map[string]any)

	// Create checkpoint.
	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	// Create metadata.
	metadata := NewCheckpointMetadata(source, step)

	// Store checkpoint.
	_, err := cm.saver.Put(ctx, config, checkpoint, metadata, channelVersions)
	if err != nil {
		return nil, fmt.Errorf("failed to store checkpoint: %w", err)
	}

	return checkpoint, nil
}

// ResumeFromCheckpoint resumes execution from a specific checkpoint.
func (cm *CheckpointManager) ResumeFromCheckpoint(ctx context.Context, config map[string]any) (State, error) {
	tuple, err := cm.saver.GetTuple(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve checkpoint: %w", err)
	}

	if tuple == nil {
		return nil, fmt.Errorf("checkpoint not found")
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
	return cm.saver.List(ctx, config, filter)
}

// DeleteThread removes all checkpoints for a thread.
func (cm *CheckpointManager) DeleteThread(ctx context.Context, threadID string) error {
	return cm.saver.DeleteThread(ctx, threadID)
}
