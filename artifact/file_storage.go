package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStorage implements the Storage interface using the local filesystem.
type FileStorage struct {
	baseDir     string
	metadataDir string
	contentDir  string
	mu          sync.RWMutex
	artifacts   map[string]*BaseArtifact
}

// NewFileStorage creates a new file-based storage system for artifacts.
// The baseDir parameter specifies the directory where artifacts will be stored.
func NewFileStorage(baseDir string) (*FileStorage, error) {
	// Create the base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create subdirectories for metadata and content
	metadataDir := filepath.Join(baseDir, "metadata")
	contentDir := filepath.Join(baseDir, "content")

	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create metadata directory: %w", err)
	}

	if err := os.MkdirAll(contentDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create content directory: %w", err)
	}

	storage := &FileStorage{
		baseDir:     baseDir,
		metadataDir: metadataDir,
		contentDir:  contentDir,
		artifacts:   make(map[string]*BaseArtifact),
	}

	// Load existing artifacts
	if err := storage.loadArtifacts(); err != nil {
		return nil, fmt.Errorf("failed to load existing artifacts: %w", err)
	}

	return storage, nil
}

// loadArtifacts loads all existing artifacts from the filesystem.
func (s *FileStorage) loadArtifacts() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := ioutil.ReadDir(s.metadataDir)
	if err != nil {
		return fmt.Errorf("failed to read metadata directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Load metadata
		metadataPath := filepath.Join(s.metadataDir, file.Name())
		metadataBytes, err := ioutil.ReadFile(metadataPath)
		if err != nil {
			return fmt.Errorf("failed to read metadata file %s: %w", file.Name(), err)
		}

		var metadata Metadata
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			return fmt.Errorf("failed to unmarshal metadata from %s: %w", file.Name(), err)
		}

		// We don't load content here to save memory
		// Content will be loaded on demand when Content() is called
		s.artifacts[metadata.ID] = NewBaseArtifact(metadata, nil)
	}

	return nil
}

// generateID generates a unique ID for an artifact based on its name and content.
func (s *FileStorage) generateID(name string, content io.Reader) (string, []byte, error) {
	// Read the content bytes
	contentBytes, err := io.ReadAll(content)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read content: %w", err)
	}

	// Generate a hash based on the content and name
	h := sha256.New()
	h.Write(contentBytes)
	h.Write([]byte(name))
	h.Write([]byte(time.Now().String()))

	return hex.EncodeToString(h.Sum(nil)), contentBytes, nil
}

// Create stores a new artifact with the specified content.
func (s *FileStorage) Create(ctx context.Context, name string, artifactType Type,
	contentType string, content io.Reader, tags map[string]string) (Artifact, error) {

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Generate ID and read content
		id, contentBytes, err := s.generateID(name, content)
		if err != nil {
			return nil, err
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		// Check if ID already exists
		if _, exists := s.artifacts[id]; exists {
			return nil, errors.New("artifact with generated ID already exists")
		}

		// Create metadata
		now := time.Now()
		metadata := Metadata{
			ID:          id,
			Name:        name,
			Type:        artifactType,
			ContentType: contentType,
			Size:        int64(len(contentBytes)),
			CreatedAt:   now,
			UpdatedAt:   now,
			Tags:        tags,
		}

		// Create base artifact
		artifact := NewBaseArtifact(metadata, contentBytes)

		// Save metadata to file
		metadataPath := filepath.Join(s.metadataDir, id+".json")
		metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal metadata: %w", err)
		}

		if err := ioutil.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
			return nil, fmt.Errorf("failed to write metadata file: %w", err)
		}

		// Save content to file
		contentPath := filepath.Join(s.contentDir, id)
		if err := ioutil.WriteFile(contentPath, contentBytes, 0644); err != nil {
			// Clean up metadata file if content save fails
			os.Remove(metadataPath)
			return nil, fmt.Errorf("failed to write content file: %w", err)
		}

		// Store in memory
		s.artifacts[id] = artifact

		return artifact, nil
	}
}

// Get retrieves an artifact by its ID.
func (s *FileStorage) Get(ctx context.Context, id string) (Artifact, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		s.mu.RLock()
		artifact, exists := s.artifacts[id]
		s.mu.RUnlock()

		if !exists {
			return nil, fmt.Errorf("artifact with ID %s not found", id)
		}

		// Always load the content from disk to ensure it's available
		contentPath := filepath.Join(s.contentDir, id)
		contentBytes, err := ioutil.ReadFile(contentPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read artifact content: %w", err)
		}

		// Create a new artifact with the content to avoid modifying the stored one
		metadata := artifact.Metadata()
		newArtifact := NewBaseArtifact(metadata, contentBytes)

		return newArtifact, nil
	}
}

// Delete removes an artifact by its ID.
func (s *FileStorage) Delete(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		s.mu.Lock()
		defer s.mu.Unlock()

		if _, exists := s.artifacts[id]; !exists {
			return fmt.Errorf("artifact with ID %s not found", id)
		}

		// Remove metadata file
		metadataPath := filepath.Join(s.metadataDir, id+".json")
		if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove metadata file: %w", err)
		}

		// Remove content file
		contentPath := filepath.Join(s.contentDir, id)
		if err := os.Remove(contentPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove content file: %w", err)
		}

		// Remove from memory
		delete(s.artifacts, id)

		return nil
	}
}

// List returns all artifacts matching the provided criteria.
func (s *FileStorage) List(ctx context.Context, filter FilterOptions) ([]Artifact, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		s.mu.RLock()
		defer s.mu.RUnlock()

		var result []Artifact
		for _, artifact := range s.artifacts {
			if s.matchesFilter(artifact, filter) {
				result = append(result, artifact)
			}
		}

		// Apply pagination if specified
		if filter.Limit > 0 {
			offset := filter.Offset
			if offset >= len(result) {
				return []Artifact{}, nil
			}

			end := offset + filter.Limit
			if end > len(result) {
				end = len(result)
			}

			result = result[offset:end]
		}

		return result, nil
	}
}

// matchesFilter checks if an artifact matches the filter criteria.
func (s *FileStorage) matchesFilter(artifact *BaseArtifact, filter FilterOptions) bool {
	metadata := artifact.Metadata()

	// Check type filter
	if len(filter.Types) > 0 {
		typeMatch := false
		for _, t := range filter.Types {
			if metadata.Type == t {
				typeMatch = true
				break
			}
		}
		if !typeMatch {
			return false
		}
	}

	// Check creation time filters
	if !filter.CreatedAfter.IsZero() && metadata.CreatedAt.Before(filter.CreatedAfter) {
		return false
	}
	if !filter.CreatedBefore.IsZero() && metadata.CreatedAt.After(filter.CreatedBefore) {
		return false
	}

	// Check tag filters
	if len(filter.Tags) > 0 {
		for k, v := range filter.Tags {
			if val, exists := metadata.Tags[k]; !exists || val != v {
				return false
			}
		}
	}

	return true
}

// Update updates an existing artifact's metadata.
func (s *FileStorage) Update(ctx context.Context, id string, options UpdateOptions) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		s.mu.Lock()
		defer s.mu.Unlock()

		artifact, exists := s.artifacts[id]
		if !exists {
			return fmt.Errorf("artifact with ID %s not found", id)
		}

		// Update the artifact
		artifact.UpdateMetadata(options)

		// Save updated metadata to file
		metadataPath := filepath.Join(s.metadataDir, id+".json")
		metadataBytes, err := json.MarshalIndent(artifact.Metadata(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		if err := ioutil.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
			return fmt.Errorf("failed to write metadata file: %w", err)
		}

		return nil
	}
}

// UpdateContent updates the content of an existing artifact.
func (s *FileStorage) UpdateContent(ctx context.Context, id string, content io.Reader) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Read all content
		contentBytes, err := io.ReadAll(content)
		if err != nil {
			return fmt.Errorf("failed to read content: %w", err)
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		artifact, exists := s.artifacts[id]
		if !exists {
			return fmt.Errorf("artifact with ID %s not found", id)
		}

		// Update the artifact
		artifact.UpdateContent(contentBytes)

		// Save updated content to file
		contentPath := filepath.Join(s.contentDir, id)
		if err := ioutil.WriteFile(contentPath, contentBytes, 0644); err != nil {
			return fmt.Errorf("failed to write content file: %w", err)
		}

		// Update metadata (just for size and updated time)
		metadataPath := filepath.Join(s.metadataDir, id+".json")
		metadataBytes, err := json.MarshalIndent(artifact.Metadata(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		if err := ioutil.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
			return fmt.Errorf("failed to write metadata file: %w", err)
		}

		return nil
	}
}
