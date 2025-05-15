package prompt

import (
	"context"
	"strings"
	"sync"
)

// MemoryRepository implements Repository using in-memory storage.
type MemoryRepository struct {
	templates map[string]*Template
	mu        sync.RWMutex
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		templates: make(map[string]*Template),
	}
}

// Get retrieves a template by its ID.
func (r *MemoryRepository) Get(_ context.Context, id string) (*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	template, ok := r.templates[id]
	if !ok {
		return nil, ErrTemplateNotFound
	}
	return template, nil
}

// List returns templates matching the specified filter criteria.
func (r *MemoryRepository) List(_ context.Context, filter Filter) ([]*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []*Template
	for _, template := range r.templates {
		if matchesFilter(template, filter) {
			results = append(results, template)
		}
	}
	return results, nil
}

// Save persists a template to storage.
func (r *MemoryRepository) Save(_ context.Context, template *Template) error {
	if template == nil || template.ID == "" {
		return ErrInvalidTemplate
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.templates[template.ID] = template
	return nil
}

// Delete removes a template from storage.
func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.templates[id]; !ok {
		return ErrTemplateNotFound
	}

	delete(r.templates, id)
	return nil
}

// matchesFilter checks if a template matches the given filter criteria.
func matchesFilter(template *Template, filter Filter) bool {
	// Check name contains
	if filter.NameContains != "" && !strings.Contains(template.Name, filter.NameContains) {
		return false
	}

	// Check version exact match
	if filter.VersionExact != "" && template.Version != filter.VersionExact {
		return false
	}

	// Check tags
	if len(filter.Tags) > 0 {
		tagMatch := false
		templateTags := make(map[string]bool)
		for _, tag := range template.Tags {
			templateTags[tag] = true
		}

		for _, tag := range filter.Tags {
			if templateTags[tag] {
				tagMatch = true
				break
			}
		}

		if !tagMatch {
			return false
		}
	}

	// Check model compatibility
	if len(filter.ModelNames) > 0 {
		modelMatch := false
		templateModels := make(map[string]bool)
		for _, model := range template.ModelCompatibility {
			templateModels[model] = true
		}

		for _, model := range filter.ModelNames {
			if templateModels[model] {
				modelMatch = true
				break
			}
		}

		if !modelMatch {
			return false
		}
	}

	return true
}
