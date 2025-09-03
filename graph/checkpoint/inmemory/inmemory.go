//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Saver provides an in-memory implementation of CheckpointSaver.
// This is suitable for testing and debugging but not for production use.
type Saver struct {
	mu      sync.RWMutex
	storage map[string]map[string]map[string]*graph.CheckpointTuple // lineageID -> namespace -> checkpointID -> tuple
	writes  map[string]map[string]map[string][]graph.PendingWrite   // lineageID -> namespace -> checkpointID -> writes
	// maxCheckpointsPerLineage limits the number of checkpoints per lineage.
	maxCheckpointsPerLineage int
}

// NewSaver creates a new in-memory checkpoint saver.
func NewSaver() *Saver {
	return &Saver{
		storage:                 make(map[string]map[string]map[string]*graph.CheckpointTuple),
		writes:                  make(map[string]map[string]map[string][]graph.PendingWrite),
		maxCheckpointsPerLineage: graph.DefaultMaxCheckpointsPerLineage,
	}
}

// WithMaxCheckpointsPerLineage sets the maximum number of checkpoints per lineage.
func (s *Saver) WithMaxCheckpointsPerLineage(max int) *Saver {
	s.maxCheckpointsPerLineage = max
	return s
}

// Get retrieves a checkpoint by configuration.
func (s *Saver) Get(ctx context.Context, config map[string]any) (*graph.Checkpoint, error) {
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
func (s *Saver) GetTuple(ctx context.Context, config map[string]any) (*graph.CheckpointTuple, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lineageID := graph.GetLineageID(config)
	namespace := graph.GetNamespace(config)
	checkpointID := graph.GetCheckpointID(config)

	if lineageID == "" {
		return nil, fmt.Errorf("lineage_id is required")
	}

	// Get the latest checkpoint if no specific ID is provided.
	if checkpointID == "" {
		namespaces, exists := s.storage[lineageID]
		if !exists {
			return nil, nil
		}

		checkpoints, exists := namespaces[namespace]
		if !exists || len(checkpoints) == 0 {
			return nil, nil
		}

		// Find the latest checkpoint by timestamp (more reliable than UUID comparison).
		var latestTuple *graph.CheckpointTuple
		var latestTime time.Time
		for _, tuple := range checkpoints {
			if tuple.Checkpoint != nil && tuple.Checkpoint.Timestamp.After(latestTime) {
				latestTime = tuple.Checkpoint.Timestamp
				latestTuple = tuple
			}
		}

		if latestTuple == nil {
			return nil, nil
		}

		checkpointID = latestTuple.Checkpoint.ID
		// Update config with the found checkpoint ID.
		if configurable, ok := config[graph.CfgKeyConfigurable].(map[string]any); ok {
			configurable[graph.CfgKeyCheckpointID] = checkpointID
		}
	}

	// Retrieve the specific checkpoint.
	namespaces, exists := s.storage[lineageID]
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

	// Create a copy to avoid concurrent modification issues.
	result := &graph.CheckpointTuple{
		Config:       tuple.Config,
		Checkpoint:   tuple.Checkpoint.Copy(),
		Metadata:     tuple.Metadata,
		ParentConfig: tuple.ParentConfig,
	}

	// Add pending writes if they exist.
	if writes, exists := s.writes[lineageID][namespace][checkpointID]; exists {
		result.PendingWrites = make([]graph.PendingWrite, len(writes))
		copy(result.PendingWrites, writes)
	}

	return result, nil
}

// List retrieves checkpoints matching criteria.
func (s *Saver) List(ctx context.Context, config map[string]any, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lineageID := graph.GetLineageID(config)
	namespace := graph.GetNamespace(config)

	if lineageID == "" {
		return nil, fmt.Errorf("lineage_id is required")
	}

	var results []*graph.CheckpointTuple

	namespaces, exists := s.storage[lineageID]
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
			beforeID := graph.GetCheckpointID(filter.Before)
			if beforeID != "" {
				// Get the timestamp of the before checkpoint to compare
				if beforeTuple, exists := checkpoints[beforeID]; exists {
					if tuple.Checkpoint.Timestamp.After(beforeTuple.Checkpoint.Timestamp) ||
						tuple.Checkpoint.Timestamp.Equal(beforeTuple.Checkpoint.Timestamp) {
						continue
					}
				} else {
					// If before checkpoint doesn't exist, skip all
					continue
				}
			}
		}

		// Apply metadata filter.
		if filter != nil && filter.Metadata != nil {
			matches := true
			for key, value := range filter.Metadata {
				if tuple.Metadata == nil || tuple.Metadata.Extra == nil {
					matches = false
					break
				}
				if tuple.Metadata.Extra[key] != value {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}

		// Create a copy to avoid concurrent modification issues.
		result := &graph.CheckpointTuple{
			Config:       tuple.Config,
			Checkpoint:   tuple.Checkpoint.Copy(),
			Metadata:     tuple.Metadata,
			ParentConfig: tuple.ParentConfig,
		}

		// Add pending writes.
		if writes, exists := s.writes[lineageID][namespace][checkpointID]; exists {
			result.PendingWrites = make([]graph.PendingWrite, len(writes))
			copy(result.PendingWrites, writes)
		}

		results = append(results, result)

		// Apply limit.
		if filter != nil && filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	// Sort results by timestamp (newest first).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Checkpoint.Timestamp.After(results[j].Checkpoint.Timestamp)
	})

	return results, nil
}

// Put stores a checkpoint.
func (s *Saver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lineageID := graph.GetLineageID(req.Config)
	namespace := graph.GetNamespace(req.Config)

	if lineageID == "" {
		return nil, fmt.Errorf("lineage_id is required")
	}

	if req.Checkpoint == nil {
		return nil, fmt.Errorf("checkpoint cannot be nil")
	}

	// Initialize storage structure if needed.
	if s.storage[lineageID] == nil {
		s.storage[lineageID] = make(map[string]map[string]*graph.CheckpointTuple)
	}
	if s.storage[lineageID][namespace] == nil {
		s.storage[lineageID][namespace] = make(map[string]*graph.CheckpointTuple)
	}

	// Create checkpoint tuple.
	tuple := &graph.CheckpointTuple{
		Config:     req.Config,
		Checkpoint: req.Checkpoint.Copy(), // Store a copy to avoid external modification
		Metadata:   req.Metadata,
	}

	// Set parent config if there's a parent checkpoint ID.
	if parentID := graph.GetCheckpointID(req.Config); parentID != "" {
		tuple.ParentConfig = graph.CreateCheckpointConfig(lineageID, parentID, namespace)
	}

	// Store the checkpoint.
	s.storage[lineageID][namespace][req.Checkpoint.ID] = tuple

	// Clean up old checkpoints if we exceed the limit.
	s.cleanupOldCheckpoints(lineageID, namespace)

	// Return updated config with the new checkpoint ID.
	updatedConfig := graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, namespace)
	return updatedConfig, nil
}

// PutWrites stores intermediate writes linked to a checkpoint.
func (s *Saver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lineageID := graph.GetLineageID(req.Config)
	namespace := graph.GetNamespace(req.Config)
	checkpointID := graph.GetCheckpointID(req.Config)

	if lineageID == "" || checkpointID == "" {
		return fmt.Errorf("lineage_id and checkpoint_id are required")
	}

	// Initialize writes structure if needed.
	if s.writes[lineageID] == nil {
		s.writes[lineageID] = make(map[string]map[string][]graph.PendingWrite)
	}
	if s.writes[lineageID][namespace] == nil {
		s.writes[lineageID][namespace] = make(map[string][]graph.PendingWrite)
	}

	// Store the writes (make a copy to avoid external modification).
	writes := make([]graph.PendingWrite, len(req.Writes))
	copy(writes, req.Writes)
	s.writes[lineageID][namespace][checkpointID] = writes

	return nil
}

// PutFull atomically stores a checkpoint with its pending writes in a single transaction.
func (s *Saver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lineageID := graph.GetLineageID(req.Config)
	namespace := graph.GetNamespace(req.Config)

	if lineageID == "" {
		return nil, fmt.Errorf("lineage_id is required")
	}

	if req.Checkpoint == nil {
		return nil, fmt.Errorf("checkpoint cannot be nil")
	}

	// Initialize storage structure if needed.
	if s.storage[lineageID] == nil {
		s.storage[lineageID] = make(map[string]map[string]*graph.CheckpointTuple)
	}
	if s.storage[lineageID][namespace] == nil {
		s.storage[lineageID][namespace] = make(map[string]*graph.CheckpointTuple)
	}

	// Initialize writes structure if needed.
	if s.writes[lineageID] == nil {
		s.writes[lineageID] = make(map[string]map[string][]graph.PendingWrite)
	}
	if s.writes[lineageID][namespace] == nil {
		s.writes[lineageID][namespace] = make(map[string][]graph.PendingWrite)
	}

	// Create checkpoint tuple.
	tuple := &graph.CheckpointTuple{
		Config:     req.Config,
		Checkpoint: req.Checkpoint.Copy(), // Store a copy to avoid external modification
		Metadata:   req.Metadata,
	}

	// Set parent config if there's a parent checkpoint ID.
	if parentID := graph.GetCheckpointID(req.Config); parentID != "" {
		tuple.ParentConfig = graph.CreateCheckpointConfig(lineageID, parentID, namespace)
	}

	// Store the checkpoint.
	s.storage[lineageID][namespace][req.Checkpoint.ID] = tuple

	// Store the writes atomically (make a copy to avoid external modification).
	if len(req.PendingWrites) > 0 {
		writes := make([]graph.PendingWrite, len(req.PendingWrites))
		copy(writes, req.PendingWrites)
		s.writes[lineageID][namespace][req.Checkpoint.ID] = writes
	}

	// Clean up old checkpoints if we exceed the limit.
	s.cleanupOldCheckpoints(lineageID, namespace)

	// Return updated config with the new checkpoint ID.
	updatedConfig := graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, namespace)
	return updatedConfig, nil
}

// DeleteLineage removes all checkpoints for a lineage.
func (s *Saver) DeleteLineage(ctx context.Context, lineageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.storage, lineageID)
	delete(s.writes, lineageID)

	return nil
}

// Close releases resources held by the saver.
func (s *Saver) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all data.
	s.storage = make(map[string]map[string]map[string]*graph.CheckpointTuple)
	s.writes = make(map[string]map[string]map[string][]graph.PendingWrite)

	return nil
}

// cleanupOldCheckpoints removes old checkpoints to stay within the limit.
func (s *Saver) cleanupOldCheckpoints(lineageID, namespace string) {
	checkpoints := s.storage[lineageID][namespace]
	if len(checkpoints) <= s.maxCheckpointsPerLineage {
		return
	}

	// Find checkpoints to remove (keep the most recent ones).
	type checkpointInfo struct {
		id        string
		timestamp time.Time
	}

	var checkpointInfos []checkpointInfo
	for id, tuple := range checkpoints {
		if tuple.Checkpoint != nil {
			checkpointInfos = append(checkpointInfos, checkpointInfo{
				id:        id,
				timestamp: tuple.Checkpoint.Timestamp,
			})
		}
	}

	// Sort by timestamp (oldest first).
	for i := 0; i < len(checkpointInfos)-1; i++ {
		for j := i + 1; j < len(checkpointInfos); j++ {
			if checkpointInfos[i].timestamp.After(checkpointInfos[j].timestamp) {
				checkpointInfos[i], checkpointInfos[j] = checkpointInfos[j], checkpointInfos[i]
			}
		}
	}

	// Remove oldest checkpoints.
	toRemove := len(checkpointInfos) - s.maxCheckpointsPerLineage
	for i := 0; i < toRemove; i++ {
		delete(checkpoints, checkpointInfos[i].id)
		// Also remove associated writes.
		delete(s.writes[lineageID][namespace], checkpointInfos[i].id)
	}
}
