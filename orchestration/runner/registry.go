package runner

import (
	"fmt"
	"sync"
)

// Registry manages runner components.
type Registry struct {
	mu      sync.RWMutex
	runners map[string]Runner
	configs map[string]Config
}

// NewRegistry creates a new runner registry.
func NewRegistry() *Registry {
	return &Registry{
		runners: make(map[string]Runner),
		configs: make(map[string]Config),
	}
}

// RegisterRunner registers a runner with the registry.
func (r *Registry) RegisterRunner(runner Runner) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := runner.Name()
	if name == "" {
		return fmt.Errorf("runner name cannot be empty")
	}

	if _, exists := r.runners[name]; exists {
		return fmt.Errorf("runner with name %q already registered", name)
	}

	r.runners[name] = runner
	return nil
}

// GetRunner retrieves a runner by name.
func (r *Registry) GetRunner(name string) (Runner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	runner, exists := r.runners[name]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrRunnerNotFound, name)
	}

	return runner, nil
}

// ListRunners returns a list of all registered runner names.
func (r *Registry) ListRunners() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.runners))
	for name := range r.runners {
		names = append(names, name)
	}

	return names
}

// RegisterConfig registers a configuration for a runner.
func (r *Registry) RegisterConfig(name string, config Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		return fmt.Errorf("config name cannot be empty")
	}

	r.configs[name] = config
	return nil
}

// GetConfig retrieves a configuration by name.
func (r *Registry) GetConfig(name string) (Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	config, exists := r.configs[name]
	if !exists {
		return Config{}, fmt.Errorf("config %q not found", name)
	}

	return config, nil
}

// UnregisterRunner removes a runner from the registry.
func (r *Registry) UnregisterRunner(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.runners[name]; !exists {
		return fmt.Errorf("%w: %q", ErrRunnerNotFound, name)
	}

	delete(r.runners, name)
	return nil
}

// Reset clears all registered runners and configs.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runners = make(map[string]Runner)
	r.configs = make(map[string]Config)
}

// globalRegistry is the default registry for runners.
var globalRegistry = NewRegistry()

// RegisterRunner registers a runner with the global registry.
func RegisterRunner(runner Runner) error {
	return globalRegistry.RegisterRunner(runner)
}

// GetRunner retrieves a runner from the global registry.
func GetRunner(name string) (Runner, error) {
	return globalRegistry.GetRunner(name)
}

// ListRunners returns a list of all registered runner names from the global registry.
func ListRunners() []string {
	return globalRegistry.ListRunners()
}

// RegisterConfig registers a configuration with the global registry.
func RegisterConfig(name string, config Config) error {
	return globalRegistry.RegisterConfig(name, config)
}

// GetConfig retrieves a configuration from the global registry.
func GetConfig(name string) (Config, error) {
	return globalRegistry.GetConfig(name)
}

// UnregisterRunner removes a runner from the global registry.
func UnregisterRunner(name string) error {
	return globalRegistry.UnregisterRunner(name)
}

// Reset clears all registered runners and configs in the global registry.
func Reset() {
	globalRegistry.Reset()
}
