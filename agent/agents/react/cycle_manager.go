package react

import (
	"context"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// InMemoryCycleManager manages cycles in memory.
type InMemoryCycleManager struct {
	cycles       []*Cycle
	currentCycle *Cycle
	mu           sync.RWMutex
}

// NewInMemoryCycleManager creates a new in-memory cycle manager.
func NewInMemoryCycleManager() *InMemoryCycleManager {
	return &InMemoryCycleManager{
		cycles: make([]*Cycle, 0),
	}
}

// StartCycle starts a new cycle with the given thought.
func (m *InMemoryCycleManager) StartCycle(ctx context.Context, thought *Thought) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if there's already an active cycle
	if m.currentCycle != nil {
		return fmt.Errorf("cannot start a new cycle while another is in progress")
	}

	m.currentCycle = &Cycle{
		ID:        fmt.Sprintf("cycle-%d", time.Now().UnixNano()),
		Thought:   thought,
		StartTime: time.Now().Unix(),
	}

	return nil
}

// RecordActions records one or more actions for the current cycle.
func (m *InMemoryCycleManager) RecordActions(ctx context.Context, actions []*Action) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentCycle == nil {
		return fmt.Errorf("no active cycle to record actions for")
	}

	// Add all actions to the Actions array
	m.currentCycle.Actions = append(m.currentCycle.Actions, actions...)
	return nil
}

// RecordObservations records one or more observations for the current cycle.
func (m *InMemoryCycleManager) RecordObservations(ctx context.Context, observations []*CycleObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentCycle == nil {
		return fmt.Errorf("no active cycle to record observations for")
	}

	// Add all observations to the Observations array
	m.currentCycle.Observations = append(m.currentCycle.Observations, observations...)
	return nil
}

// EndCycle ends the current cycle and adds it to the history.
func (m *InMemoryCycleManager) EndCycle(ctx context.Context) (*Cycle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentCycle == nil {
		return nil, fmt.Errorf("no active cycle to end")
	}

	m.currentCycle.EndTime = time.Now().Unix()
	m.cycles = append(m.cycles, m.currentCycle)

	completedCycle := m.currentCycle
	m.currentCycle = nil

	return completedCycle, nil
}

// GetHistory returns all completed cycles.
func (m *InMemoryCycleManager) GetHistory(ctx context.Context) ([]*Cycle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to prevent external modification
	cycles := make([]*Cycle, len(m.cycles))
	copy(cycles, m.cycles)

	return cycles, nil
}

// CurrentCycle returns the current active cycle, if any.
func (m *InMemoryCycleManager) CurrentCycle(ctx context.Context) (*Cycle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.currentCycle == nil {
		return nil, nil // No error, just no current cycle
	}

	// Return a copy to prevent external modification
	cycle := *m.currentCycle
	return &cycle, nil
}

// PersistentCycleManager manages cycles with persistence.
type PersistentCycleManager struct {
	inMemory *InMemoryCycleManager
	storage  CycleStorage
}

// CycleStorage represents a storage backend for cycles.
type CycleStorage interface {
	// StoreCycle stores a cycle.
	StoreCycle(ctx context.Context, cycle *Cycle) error

	// RetrieveCycles retrieves all stored cycles.
	RetrieveCycles(ctx context.Context) ([]*Cycle, error)
}

// NewPersistentCycleManager creates a new persistent cycle manager.
func NewPersistentCycleManager(storage CycleStorage) *PersistentCycleManager {
	return &PersistentCycleManager{
		inMemory: NewInMemoryCycleManager(),
		storage:  storage,
	}
}

// StartCycle starts a new cycle with the given thought.
func (m *PersistentCycleManager) StartCycle(ctx context.Context, thought *Thought) error {
	return m.inMemory.StartCycle(ctx, thought)
}

// RecordActions records one or more actions for the current cycle.
func (m *PersistentCycleManager) RecordActions(ctx context.Context, actions []*Action) error {
	return m.inMemory.RecordActions(ctx, actions)
}

// RecordObservations records one or more observations for the current cycle.
func (m *PersistentCycleManager) RecordObservations(ctx context.Context, observations []*CycleObservation) error {
	return m.inMemory.RecordObservations(ctx, observations)
}

// EndCycle ends the current cycle, adds it to the history, and persists it.
func (m *PersistentCycleManager) EndCycle(ctx context.Context) (*Cycle, error) {
	cycle, err := m.inMemory.EndCycle(ctx)
	if err != nil {
		return nil, err
	}

	// Persist the cycle
	if m.storage != nil {
		if err := m.storage.StoreCycle(ctx, cycle); err != nil {
			// Log the error but don't fail the operation
			log.Infof("Warning: failed to persist cycle: %v\n", err)
		}
	}

	return cycle, nil
}

// GetHistory returns all completed cycles, both in-memory and persisted.
func (m *PersistentCycleManager) GetHistory(ctx context.Context) ([]*Cycle, error) {
	// Get in-memory cycles
	inMemoryCycles, err := m.inMemory.GetHistory(ctx)
	if err != nil {
		return nil, err
	}

	// If no storage, return just in-memory cycles
	if m.storage == nil {
		return inMemoryCycles, nil
	}

	// Get persisted cycles
	persistedCycles, err := m.storage.RetrieveCycles(ctx)
	if err != nil {
		// Log the error but return the in-memory cycles
		log.Infof("Warning: failed to retrieve persisted cycles: %v\n", err)
		return inMemoryCycles, nil
	}

	// Combine and deduplicate cycles
	// This is a simple approach - in a real implementation you'd need more sophisticated deduplication
	allCycles := make([]*Cycle, 0, len(inMemoryCycles)+len(persistedCycles))
	allCycles = append(allCycles, inMemoryCycles...)

	// Only add persisted cycles if they're not already in memory
	// This assumes cycles have unique IDs
	inMemoryIDs := make(map[string]bool)
	for _, cycle := range inMemoryCycles {
		inMemoryIDs[cycle.ID] = true
	}

	for _, cycle := range persistedCycles {
		if !inMemoryIDs[cycle.ID] {
			allCycles = append(allCycles, cycle)
		}
	}

	return allCycles, nil
}

// CurrentCycle returns the current active cycle, if any.
func (m *PersistentCycleManager) CurrentCycle(ctx context.Context) (*Cycle, error) {
	return m.inMemory.CurrentCycle(ctx)
}

// FileCycleStorage is an example implementation of CycleStorage using files.
// In a real implementation, this would likely use a database or other more robust storage.
type FileCycleStorage struct {
	filePath string
}

// NewFileCycleStorage creates a new file-based cycle storage.
func NewFileCycleStorage(filePath string) *FileCycleStorage {
	return &FileCycleStorage{
		filePath: filePath,
	}
}

// StoreCycle stores a cycle to file.
func (s *FileCycleStorage) StoreCycle(ctx context.Context, cycle *Cycle) error {
	// In a real implementation, this would write to a file or database
	// This is just a placeholder
	return nil
}

// RetrieveCycles retrieves all stored cycles from file.
func (s *FileCycleStorage) RetrieveCycles(ctx context.Context) ([]*Cycle, error) {
	// In a real implementation, this would read from a file or database
	// This is just a placeholder
	return []*Cycle{}, nil
}

// BaseCycleStorage is a base implementation of CycleStorage.
type BaseCycleStorage struct {
	cycles []*Cycle
	mu     sync.RWMutex
}

// NewBaseCycleStorage creates a new base cycle storage.
func NewBaseCycleStorage() *BaseCycleStorage {
	return &BaseCycleStorage{
		cycles: make([]*Cycle, 0),
	}
}

// StoreCycle stores a cycle.
func (s *BaseCycleStorage) StoreCycle(ctx context.Context, cycle *Cycle) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cycles = append(s.cycles, cycle)
	return nil
}

// RetrieveCycles retrieves all stored cycles.
func (s *BaseCycleStorage) RetrieveCycles(ctx context.Context) ([]*Cycle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modification
	cycles := make([]*Cycle, len(s.cycles))
	copy(cycles, s.cycles)

	return cycles, nil
}
