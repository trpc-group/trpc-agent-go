package prompt

import (
	"context"
)

// DefaultManager implements the Manager interface.
type DefaultManager struct {
	repo     Repository
	renderer Renderer
}

// NewManager creates a new prompt manager with the specified repository and renderer.
func NewManager(repo Repository, renderer Renderer) *DefaultManager {
	return &DefaultManager{
		repo:     repo,
		renderer: renderer,
	}
}

// Get retrieves a template by ID.
func (m *DefaultManager) Get(ctx context.Context, id string) (*Template, error) {
	return m.repo.Get(ctx, id)
}

// List returns templates matching the filter criteria.
func (m *DefaultManager) List(ctx context.Context, filter Filter) ([]*Template, error) {
	return m.repo.List(ctx, filter)
}

// Save persists a template.
func (m *DefaultManager) Save(ctx context.Context, template *Template) error {
	return m.repo.Save(ctx, template)
}

// Delete removes a template.
func (m *DefaultManager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
}

// Render processes a template by ID and returns the rendered content.
func (m *DefaultManager) Render(ctx context.Context, id string, variables map[string]string) (string, error) {
	template, err := m.repo.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return m.renderer.Render(ctx, template, variables)
}

// GetRenderer returns the renderer used by this manager.
func (m *DefaultManager) GetRenderer() Renderer {
	return m.renderer
}
