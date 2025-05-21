// Package artifact provides functionality for managing artifacts created or used
// by agents, such as files, images, or other binary data.
package artifact

import (
	"context"
	"io"
	"time"
)

// Type represents the type of an artifact.
type Type string

// Common artifact types.
const (
	TypeUnknown Type = "unknown"
	TypeFile    Type = "file"
	TypeImage   Type = "image"
	TypeText    Type = "text"
	TypeAudio   Type = "audio"
	TypeVideo   Type = "video"
	TypeJSON    Type = "json"
)

// Metadata contains information about an artifact.
type Metadata struct {
	// ID is the unique identifier for the artifact.
	ID string

	// Name is the display name of the artifact.
	Name string

	// Type is the type of the artifact.
	Type Type

	// ContentType is the MIME type of the artifact.
	ContentType string

	// Size is the size of the artifact in bytes.
	Size int64

	// CreatedAt is when the artifact was created.
	CreatedAt time.Time

	// UpdatedAt is when the artifact was last modified.
	UpdatedAt time.Time

	// Tags are optional key-value pairs associated with the artifact.
	Tags map[string]string
}

// Artifact represents a piece of data that can be stored and retrieved.
type Artifact interface {
	// ID returns the unique identifier of the artifact.
	ID() string

	// Name returns the display name of the artifact.
	Name() string

	// Type returns the type of the artifact.
	Type() Type

	// Metadata returns the complete metadata of the artifact.
	Metadata() Metadata

	// Content returns a reader to access the artifact's content.
	Content() (io.ReadCloser, error)

	// Size returns the size of the artifact in bytes.
	Size() int64

	// ContentType returns the MIME type of the artifact.
	ContentType() string

	// CreatedAt returns when the artifact was created.
	CreatedAt() time.Time

	// UpdatedAt returns when the artifact was last modified.
	UpdatedAt() time.Time

	// Tags returns the tags associated with the artifact.
	Tags() map[string]string

	// AddTag adds or updates a tag on the artifact.
	AddTag(key, value string)

	// RemoveTag removes a tag from the artifact.
	RemoveTag(key string)
}

// Storage defines the interface for artifact persistence.
type Storage interface {
	// Create stores a new artifact with the specified content.
	Create(ctx context.Context, name string, artifactType Type, contentType string, 
		content io.Reader, tags map[string]string) (Artifact, error)

	// Get retrieves an artifact by its ID.
	Get(ctx context.Context, id string) (Artifact, error)

	// Delete removes an artifact by its ID.
	Delete(ctx context.Context, id string) error

	// List returns all artifacts matching the provided criteria.
	List(ctx context.Context, filter FilterOptions) ([]Artifact, error)

	// Update updates an existing artifact's metadata.
	Update(ctx context.Context, id string, options UpdateOptions) error

	// UpdateContent updates the content of an existing artifact.
	UpdateContent(ctx context.Context, id string, content io.Reader) error
}

// FilterOptions provides criteria for listing artifacts.
type FilterOptions struct {
	// Types filters artifacts by their types.
	Types []Type

	// Tags filters artifacts by their tags.
	Tags map[string]string

	// CreatedAfter filters artifacts created after this time.
	CreatedAfter time.Time

	// CreatedBefore filters artifacts created before this time.
	CreatedBefore time.Time

	// Limit sets the maximum number of artifacts to return.
	Limit int

	// Offset sets the starting point for pagination.
	Offset int
}

// UpdateOptions defines what metadata can be updated for an artifact.
type UpdateOptions struct {
	// Name updates the display name.
	Name *string

	// ContentType updates the MIME type.
	ContentType *string

	// Tags adds or updates the specified tags.
	Tags map[string]string

	// RemoveTags lists tags to be removed.
	RemoveTags []string
} 