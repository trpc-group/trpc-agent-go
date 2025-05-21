package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileRepository implements Repository using the filesystem for storage.
type FileRepository struct {
	basePath string
	mu       sync.RWMutex
}

// NewFileRepository creates a new file-based repository.
func NewFileRepository(basePath string) (*FileRepository, error) {
	if basePath == "" {
		return nil, ErrRepositoryError.WithCause(fmt.Errorf("base path cannot be empty"))
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, ErrRepositoryError.WithCause(err)
	}

	return &FileRepository{
		basePath: basePath,
	}, nil
}

// Get retrieves a template by its ID.
func (r *FileRepository) Get(_ context.Context, id string) (*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	filePath := r.getFilePath(id)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrTemplateNotFound
		}
		return nil, ErrRepositoryError.WithCause(err)
	}

	var template Template
	if err := json.Unmarshal(data, &template); err != nil {
		return nil, ErrRepositoryError.WithCause(err)
	}

	return &template, nil
}

// List returns templates matching the specified filter criteria.
func (r *FileRepository) List(ctx context.Context, filter Filter) ([]*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	files, err := os.ReadDir(r.basePath)
	if err != nil {
		return nil, ErrRepositoryError.WithCause(err)
	}

	var results []*Template
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(file.Name(), ".json")
		template, err := r.Get(ctx, id)
		if err != nil {
			continue // Skip files that can't be read
		}

		if matchesFilter(template, filter) {
			results = append(results, template)
		}
	}

	return results, nil
}

// Save persists a template to storage.
func (r *FileRepository) Save(_ context.Context, template *Template) error {
	if template == nil || template.ID == "" {
		return ErrInvalidTemplate
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return ErrRepositoryError.WithCause(err)
	}

	filePath := r.getFilePath(template.ID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return ErrRepositoryError.WithCause(err)
	}

	return nil
}

// Delete removes a template from storage.
func (r *FileRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	filePath := r.getFilePath(id)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return ErrTemplateNotFound
	}

	if err := os.Remove(filePath); err != nil {
		return ErrRepositoryError.WithCause(err)
	}

	return nil
}

// getFilePath constructs the full file path for a template ID.
func (r *FileRepository) getFilePath(id string) string {
	return filepath.Join(r.basePath, fmt.Sprintf("%s.json", id))
}
