package tool

import (
	"fmt"
	"sync"
)

// Registry is a registry for tool configurations and instances.
type Registry struct {
	mu        sync.RWMutex
	toolMap   map[string]Tool
	factories map[string]ToolFactory
}

// ToolFactory is a function that creates a tool.
type ToolFactory func() (Tool, error)

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		toolMap:   make(map[string]Tool),
		factories: make(map[string]ToolFactory),
	}
}

// Register registers a tool with the registry.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool cannot be nil")
	}

	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.toolMap[name]; exists {
		return fmt.Errorf("tool with name %s already exists", name)
	}

	r.toolMap[name] = tool
	return nil
}

// RegisterFactory registers a tool factory with the registry.
func (r *Registry) RegisterFactory(name string, factory ToolFactory) error {
	if name == "" {
		return fmt.Errorf("factory name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("factory cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("factory with name %s already exists", name)
	}

	r.factories[name] = factory
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	tool, exists := r.toolMap[name]
	r.mu.RUnlock()

	if exists {
		return tool, true
	}

	// If the tool doesn't exist, try to create it from a factory
	r.mu.Lock()
	defer r.mu.Unlock()

	factory, exists := r.factories[name]
	if !exists {
		return nil, false
	}

	// Create the tool from the factory
	tool, err := factory()
	if err != nil {
		return nil, false
	}

	// Cache the created tool
	r.toolMap[name] = tool
	return tool, true
}

// GetAll returns all registered tools.
func (r *Registry) GetAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.toolMap))
	for _, tool := range r.toolMap {
		tools = append(tools, tool)
	}
	return tools
}

// GetNames returns the names of all registered tools.
func (r *Registry) GetNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.toolMap)+len(r.factories))
	for name := range r.toolMap {
		names = append(names, name)
	}
	for name := range r.factories {
		// Only add factory names if they haven't been instantiated yet
		if _, exists := r.toolMap[name]; !exists {
			names = append(names, name)
		}
	}
	return names
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.toolMap, name)
	delete(r.factories, name)
}

// Clear removes all tools and factories from the registry.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolMap = make(map[string]Tool)
	r.factories = make(map[string]ToolFactory)
}

// CreateToolSet creates a ToolSet containing all tools in the registry.
func (r *Registry) CreateToolSet() *ToolSet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	toolSet := NewToolSet()
	for _, tool := range r.toolMap {
		_ = toolSet.Add(tool)
	}
	return toolSet
}

// DefaultRegistry is the global tool registry.
var DefaultRegistry = NewRegistry() 