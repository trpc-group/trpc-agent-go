package artifact

import (
	"fmt"
	"sync"
)

// Registry is a storage system for managing different artifact storage implementations.
type Registry struct {
	mu       sync.RWMutex
	storages map[string]Storage
	default_ string
}

// NewRegistry creates a new artifact storage registry.
func NewRegistry() *Registry {
	return &Registry{
		storages: make(map[string]Storage),
	}
}

// Register adds a new storage implementation to the registry with the given name.
// If default is true, this storage becomes the default for GetDefault().
func (r *Registry) Register(name string, storage Storage, default_ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.storages[name]; exists {
		return fmt.Errorf("storage with name %s already registered", name)
	}

	r.storages[name] = storage

	if default_ || r.default_ == "" {
		r.default_ = name
	}

	return nil
}

// Get retrieves a storage implementation by name.
func (r *Registry) Get(name string) (Storage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	storage, exists := r.storages[name]
	if !exists {
		return nil, fmt.Errorf("no storage registered with name %s", name)
	}

	return storage, nil
}

// GetDefault returns the default storage implementation.
func (r *Registry) GetDefault() (Storage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.default_ == "" {
		return nil, fmt.Errorf("no default storage registered")
	}

	return r.storages[r.default_], nil
}

// SetDefault sets the default storage implementation by name.
func (r *Registry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.storages[name]; !exists {
		return fmt.Errorf("no storage registered with name %s", name)
	}

	r.default_ = name
	return nil
}

// List returns the names of all registered storage implementations.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for name := range r.storages {
		names = append(names, name)
	}

	return names
}

// Unregister removes a storage implementation from the registry.
// If the storage was the default, a new default is chosen if available.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.storages[name]; !exists {
		return fmt.Errorf("no storage registered with name %s", name)
	}

	delete(r.storages, name)

	// If we removed the default, pick a new one if available
	if r.default_ == name {
		r.default_ = ""
		for name := range r.storages {
			r.default_ = name
			break
		}
	}

	return nil
}

// DefaultRegistry is the global artifact storage registry.
var DefaultRegistry = NewRegistry() 