package evaluation

import (
	"fmt"
	"sync"
)

// Registry manages evaluation components.
type Registry struct {
	mu         sync.RWMutex
	evaluators map[string]Evaluator
	configs    map[string]Config
}

// NewRegistry creates a new evaluation registry.
func NewRegistry() *Registry {
	return &Registry{
		evaluators: make(map[string]Evaluator),
		configs:    make(map[string]Config),
	}
}

// RegisterEvaluator registers an evaluator with the registry.
func (r *Registry) RegisterEvaluator(evaluator Evaluator) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := evaluator.Name()
	if name == "" {
		return fmt.Errorf("evaluator name cannot be empty")
	}

	if _, exists := r.evaluators[name]; exists {
		return fmt.Errorf("evaluator with name %q already registered", name)
	}

	r.evaluators[name] = evaluator
	return nil
}

// GetEvaluator retrieves an evaluator by name.
func (r *Registry) GetEvaluator(name string) (Evaluator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	evaluator, exists := r.evaluators[name]
	if !exists {
		return nil, fmt.Errorf("evaluator %q not found", name)
	}

	return evaluator, nil
}

// ListEvaluators returns a list of all registered evaluator names.
func (r *Registry) ListEvaluators() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.evaluators))
	for name := range r.evaluators {
		names = append(names, name)
	}

	return names
}

// RegisterConfig registers a configuration for an evaluator.
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

// globalRegistry is the default registry for evaluators.
var globalRegistry = NewRegistry()

// RegisterEvaluator registers an evaluator with the global registry.
func RegisterEvaluator(evaluator Evaluator) error {
	return globalRegistry.RegisterEvaluator(evaluator)
}

// GetEvaluator retrieves an evaluator from the global registry.
func GetEvaluator(name string) (Evaluator, error) {
	return globalRegistry.GetEvaluator(name)
}

// ListEvaluators returns a list of all registered evaluator names from the global registry.
func ListEvaluators() []string {
	return globalRegistry.ListEvaluators()
}

// RegisterConfig registers a configuration with the global registry.
func RegisterConfig(name string, config Config) error {
	return globalRegistry.RegisterConfig(name, config)
}

// GetConfig retrieves a configuration from the global registry.
func GetConfig(name string) (Config, error) {
	return globalRegistry.GetConfig(name)
}

// Reset clears all registered evaluators and configs.
// Primarily used for testing.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.evaluators = make(map[string]Evaluator)
	r.configs = make(map[string]Config)
}
