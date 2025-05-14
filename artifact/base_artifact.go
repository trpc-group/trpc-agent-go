package artifact

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"time"
)

// BaseArtifact provides a basic implementation of the Artifact interface.
type BaseArtifact struct {
	metadata Metadata
	content  []byte
	mu       sync.RWMutex
}

// NewBaseArtifact creates a new BaseArtifact with the provided metadata and content.
func NewBaseArtifact(metadata Metadata, content []byte) *BaseArtifact {
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now()
	}
	if metadata.UpdatedAt.IsZero() {
		metadata.UpdatedAt = metadata.CreatedAt
	}
	if metadata.Tags == nil {
		metadata.Tags = make(map[string]string)
	}
	if metadata.Size == 0 && content != nil {
		metadata.Size = int64(len(content))
	}

	return &BaseArtifact{
		metadata: metadata,
		content:  content,
	}
}

// ID returns the unique identifier of the artifact.
func (a *BaseArtifact) ID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.ID
}

// Name returns the display name of the artifact.
func (a *BaseArtifact) Name() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.Name
}

// Type returns the type of the artifact.
func (a *BaseArtifact) Type() Type {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.Type
}

// Metadata returns the complete metadata of the artifact.
func (a *BaseArtifact) Metadata() Metadata {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy of the metadata to prevent modification
	metadata := a.metadata

	// Copy the tags map
	if a.metadata.Tags != nil {
		metadata.Tags = make(map[string]string, len(a.metadata.Tags))
		for k, v := range a.metadata.Tags {
			metadata.Tags[k] = v
		}
	}

	return metadata
}

// Content returns a reader to access the artifact's content.
func (a *BaseArtifact) Content() (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Check if content is loaded
	if a.content == nil || len(a.content) == 0 {
		return nil, errors.New("content not loaded")
	}

	// Create a copy of the content to prevent external modification
	contentCopy := make([]byte, len(a.content))
	copy(contentCopy, a.content)

	return io.NopCloser(bytes.NewReader(contentCopy)), nil
}

// Size returns the size of the artifact in bytes.
func (a *BaseArtifact) Size() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.Size
}

// ContentType returns the MIME type of the artifact.
func (a *BaseArtifact) ContentType() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.ContentType
}

// CreatedAt returns when the artifact was created.
func (a *BaseArtifact) CreatedAt() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.CreatedAt
}

// UpdatedAt returns when the artifact was last modified.
func (a *BaseArtifact) UpdatedAt() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.metadata.UpdatedAt
}

// Tags returns the tags associated with the artifact.
func (a *BaseArtifact) Tags() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy of the tags to prevent modification
	tags := make(map[string]string, len(a.metadata.Tags))
	for k, v := range a.metadata.Tags {
		tags[k] = v
	}

	return tags
}

// AddTag adds or updates a tag on the artifact.
func (a *BaseArtifact) AddTag(key, value string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.metadata.Tags == nil {
		a.metadata.Tags = make(map[string]string)
	}

	a.metadata.Tags[key] = value
	a.metadata.UpdatedAt = time.Now()
}

// RemoveTag removes a tag from the artifact.
func (a *BaseArtifact) RemoveTag(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.metadata.Tags != nil {
		if _, exists := a.metadata.Tags[key]; exists {
			delete(a.metadata.Tags, key)
			a.metadata.UpdatedAt = time.Now()
		}
	}
}

// UpdateContent updates the content of the artifact.
func (a *BaseArtifact) UpdateContent(content []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.content = content
	a.metadata.Size = int64(len(content))
	a.metadata.UpdatedAt = time.Now()
}

// UpdateMetadata updates the metadata of the artifact.
func (a *BaseArtifact) UpdateMetadata(opts UpdateOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if opts.Name != nil {
		a.metadata.Name = *opts.Name
	}

	if opts.ContentType != nil {
		a.metadata.ContentType = *opts.ContentType
	}

	if opts.Tags != nil {
		if a.metadata.Tags == nil {
			a.metadata.Tags = make(map[string]string)
		}

		for k, v := range opts.Tags {
			a.metadata.Tags[k] = v
		}
	}

	if opts.RemoveTags != nil {
		for _, tag := range opts.RemoveTags {
			delete(a.metadata.Tags, tag)
		}
	}

	a.metadata.UpdatedAt = time.Now()
}
