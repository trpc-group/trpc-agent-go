//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory checkpoint storage implementation
// for graph execution state persistence and recovery.
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
		storage:                  make(map[string]map[string]map[string]*graph.CheckpointTuple),
		writes:                   make(map[string]map[string]map[string][]graph.PendingWrite),
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

	if checkpointID == "" {
		return s.getLatestCheckpoint(lineageID, namespace, config)
	}
	return s.getSpecificCheckpoint(lineageID, namespace, checkpointID)
}

// getLatestCheckpoint retrieves the latest checkpoint.
func (s *Saver) getLatestCheckpoint(lineageID, namespace string,
	config map[string]any) (*graph.CheckpointTuple, error) {

	namespaces, exists := s.storage[lineageID]
	if !exists {
		return nil, nil
	}

	latestTuple := s.findLatestTuple(namespaces, namespace)
	if latestTuple == nil {
		return nil, nil
	}

	// Update config with the found checkpoint ID.
	checkpointID := latestTuple.Checkpoint.ID
	if configurable, ok := config[graph.CfgKeyConfigurable].(map[string]any); ok {
		configurable[graph.CfgKeyCheckpointID] = checkpointID
	}
	return s.createResultTuple(latestTuple, lineageID, namespace, checkpointID), nil
}

// findLatestTuple finds the latest checkpoint tuple.
func (s *Saver) findLatestTuple(namespaces map[string]map[string]*graph.CheckpointTuple,
	namespace string) *graph.CheckpointTuple {

	var latestTuple *graph.CheckpointTuple
	var latestTime time.Time
	if checkpoints, exists := namespaces[namespace]; exists {
		latestTuple = s.findLatestInMap(checkpoints, &latestTime)
	}
	return latestTuple
}

// findLatestInMap finds the latest tuple in a map of checkpoints.
func (s *Saver) findLatestInMap(checkpoints map[string]*graph.CheckpointTuple,
	latestTime *time.Time) *graph.CheckpointTuple {

	var latestTuple *graph.CheckpointTuple
	for _, tuple := range checkpoints {
		if tuple.Checkpoint != nil && tuple.Checkpoint.Timestamp.After(*latestTime) {
			*latestTime = tuple.Checkpoint.Timestamp
			latestTuple = tuple
		}
	}
	return latestTuple
}

// getSpecificCheckpoint retrieves a specific checkpoint by ID.
func (s *Saver) getSpecificCheckpoint(lineageID, namespace,
	checkpointID string) (*graph.CheckpointTuple, error) {

	namespaces, exists := s.storage[lineageID]
	if !exists {
		return nil, nil
	}

	tuple := s.findCheckpointByID(namespaces, namespace, checkpointID)
	if tuple == nil {
		return nil, nil
	}
	return s.createResultTuple(tuple, lineageID, namespace, checkpointID), nil
}

// findCheckpointByID finds a checkpoint by ID.
func (s *Saver) findCheckpointByID(namespaces map[string]map[string]*graph.CheckpointTuple,
	namespace, checkpointID string) *graph.CheckpointTuple {
	if checkpoints, exists := namespaces[namespace]; exists {
		return checkpoints[checkpointID]
	}

	return nil
}

// createResultTuple creates a result tuple with pending writes.
func (s *Saver) createResultTuple(tuple *graph.CheckpointTuple, lineageID,
	namespace, checkpointID string) *graph.CheckpointTuple {

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
	return result
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

	// If namespace is empty, search across all namespaces (cross-namespace search like GetTuple)
	if namespace == "" {
		// Search across all namespaces. Do not apply limit until after sorting to avoid
		// bias from map iteration order.
		for ns, checkpoints := range namespaces {
			for checkpointID, tuple := range checkpoints {
				if !s.passesFilters(checkpointID, tuple, checkpoints, filter) {
					continue
				}
				result := s.createCheckpointResult(tuple, lineageID, ns, checkpointID)
				results = append(results, result)
			}
		}
	} else {
		// Search in specific namespace
		checkpoints, exists := namespaces[namespace]
		if !exists {
			return results, nil
		}
		// Apply filters and collect results.
		for checkpointID, tuple := range checkpoints {
			if !s.passesFilters(checkpointID, tuple, checkpoints, filter) {
				continue
			}
			result := s.createCheckpointResult(tuple, lineageID, namespace, checkpointID)
			results = append(results, result)
		}
	}
	// Sort results by timestamp (newest first).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Checkpoint.Timestamp.After(results[j].Checkpoint.Timestamp)
	})
	// Apply limit after sorting to ensure correct ordering.
	if filter != nil && filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}
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

	// Create updated config with THIS checkpoint's ID.
	// This ensures proper parent-child relationships when resuming.
	updatedConfig := graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, namespace)

	// Create checkpoint tuple with the updated config.
	tuple := &graph.CheckpointTuple{
		Config:     updatedConfig,
		Checkpoint: req.Checkpoint.Copy(), // Store a copy to avoid external modification
		Metadata:   req.Metadata,
	}

	// Set parent config if there's a parent checkpoint ID.
	// Determine the correct parent namespace by looking up the parent checkpoint.
	if parentID := req.Checkpoint.ParentCheckpointID; parentID != "" {
		parentNS := s.findParentNamespace(lineageID, parentID)
		tuple.ParentConfig = graph.CreateCheckpointConfig(lineageID, parentID, parentNS)
	}

	// Store the checkpoint.
	s.storage[lineageID][namespace][req.Checkpoint.ID] = tuple

	// Clean up old checkpoints if we exceed the limit.
	s.cleanupOldCheckpoints(lineageID, namespace)

	// Return the updated config.
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

	// Create updated config with THIS checkpoint's ID.
	// This ensures proper parent-child relationships when resuming.
	updatedConfig := graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, namespace)

	// Create checkpoint tuple with the updated config.
	tuple := &graph.CheckpointTuple{
		Config:     updatedConfig,
		Checkpoint: req.Checkpoint.Copy(), // Store a copy to avoid external modification
		Metadata:   req.Metadata,
	}

	// Set parent config if there's a parent checkpoint ID.
	// Determine the correct parent namespace by looking up the parent checkpoint.
	if parentID := req.Checkpoint.ParentCheckpointID; parentID != "" {
		parentNS := s.findParentNamespace(lineageID, parentID)
		tuple.ParentConfig = graph.CreateCheckpointConfig(lineageID, parentID, parentNS)
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

	// Return the updated config.
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

// passesFilters checks if a checkpoint passes all filter criteria.
func (s *Saver) passesFilters(checkpointID string, tuple *graph.CheckpointTuple, checkpoints map[string]*graph.CheckpointTuple, filter *graph.CheckpointFilter) bool {
	if filter == nil {
		return true
	}
	if !s.passesBeforeFilter(tuple, checkpoints, filter.Before) {
		return false
	}
	return s.passesMetadataFilter(tuple, filter.Metadata)
}

// passesBeforeFilter checks if checkpoint passes the before filter.
func (s *Saver) passesBeforeFilter(tuple *graph.CheckpointTuple, checkpoints map[string]*graph.CheckpointTuple, before map[string]any) bool {
	if before == nil {
		return true
	}
	beforeID := graph.GetCheckpointID(before)
	if beforeID == "" {
		return true
	}
	beforeTuple, exists := checkpoints[beforeID]
	if !exists {
		return false
	}
	return tuple.Checkpoint.Timestamp.Before(beforeTuple.Checkpoint.Timestamp)
}

// passesMetadataFilter checks if checkpoint passes the metadata filter.
func (s *Saver) passesMetadataFilter(tuple *graph.CheckpointTuple, metadata map[string]any) bool {
	// Treat nil or empty metadata map as "no metadata filter".
	if len(metadata) == 0 {
		return true
	}

	if tuple.Metadata == nil || tuple.Metadata.Extra == nil {
		return false
	}

	for key, value := range metadata {
		if tuple.Metadata.Extra[key] != value {
			return false
		}
	}
	return true
}

// createCheckpointResult creates a checkpoint result tuple.
func (s *Saver) createCheckpointResult(tuple *graph.CheckpointTuple, lineageID, namespace, checkpointID string) *graph.CheckpointTuple {
	result := &graph.CheckpointTuple{
		Config:       tuple.Config,
		Checkpoint:   tuple.Checkpoint.Copy(),
		Metadata:     tuple.Metadata,
		ParentConfig: tuple.ParentConfig,
	}
	if writes, exists := s.writes[lineageID][namespace][checkpointID]; exists {
		result.PendingWrites = make([]graph.PendingWrite, len(writes))
		copy(result.PendingWrites, writes)
	}
	return result
}

// findParentNamespace locates the namespace of a parent checkpoint ID within a lineage.
// If not found, returns an empty namespace to allow cross-namespace lookup by ID.
func (s *Saver) findParentNamespace(lineageID, parentID string) string {
	if lineageID == "" || parentID == "" {
		return ""
	}
	if namespaces, ok := s.storage[lineageID]; ok {
		for ns, checkpoints := range namespaces {
			if _, exists := checkpoints[parentID]; exists {
				return ns
			}
		}
	}
	// Unknown parent namespace; use empty to indicate cross-namespace search.
	return ""
}
