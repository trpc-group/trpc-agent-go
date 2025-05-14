package artifact

import (
	"context"
	"mime"
	"os"
	"path/filepath"
)

// CreateFromFile creates a new artifact from a file.
func CreateFromFile(ctx context.Context, storage Storage, filePath string, artifactType Type, tags map[string]string) (Artifact, error) {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Get file info
	_, err = file.Stat()
	if err != nil {
		return nil, err
	}

	// Determine the content type
	contentType := mime.TypeByExtension(filepath.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Create the artifact
	return storage.Create(ctx, filepath.Base(filePath), artifactType, contentType, file, tags)
}

// SaveToFile saves an artifact's content to a file.
func SaveToFile(ctx context.Context, artifact Artifact, filePath string) error {
	// Get the content
	reader, err := artifact.Content()
	if err != nil {
		return err
	}
	defer reader.Close()

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Copy the content to the file
	_, err = file.ReadFrom(reader)
	return err
}
