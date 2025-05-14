package artifact

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestMetadata(t *testing.T) {
	now := time.Now()
	metadata := Metadata{
		ID:          "test-id",
		Name:        "test-artifact",
		Type:        TypeFile,
		ContentType: "text/plain",
		Size:        100,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        map[string]string{"key": "value"},
	}

	if metadata.ID != "test-id" {
		t.Errorf("Expected ID to be 'test-id', got '%s'", metadata.ID)
	}

	if metadata.Name != "test-artifact" {
		t.Errorf("Expected Name to be 'test-artifact', got '%s'", metadata.Name)
	}

	if metadata.Type != TypeFile {
		t.Errorf("Expected Type to be '%s', got '%s'", TypeFile, metadata.Type)
	}

	if metadata.ContentType != "text/plain" {
		t.Errorf("Expected ContentType to be 'text/plain', got '%s'", metadata.ContentType)
	}

	if metadata.Size != 100 {
		t.Errorf("Expected Size to be 100, got %d", metadata.Size)
	}

	if !metadata.CreatedAt.Equal(now) {
		t.Errorf("Expected CreatedAt to be %v, got %v", now, metadata.CreatedAt)
	}

	if !metadata.UpdatedAt.Equal(now) {
		t.Errorf("Expected UpdatedAt to be %v, got %v", now, metadata.UpdatedAt)
	}

	if value, exists := metadata.Tags["key"]; !exists || value != "value" {
		t.Errorf("Expected Tags to have key 'key' with value 'value', got '%s'", value)
	}
}

func TestBaseArtifact(t *testing.T) {
	now := time.Now()
	metadata := Metadata{
		ID:          "test-id",
		Name:        "test-artifact",
		Type:        TypeFile,
		ContentType: "text/plain",
		Size:        10,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        map[string]string{"key": "value"},
	}

	content := []byte("test data")
	artifact := NewBaseArtifact(metadata, content)

	// Test basic getters
	if artifact.ID() != "test-id" {
		t.Errorf("Expected ID to be 'test-id', got '%s'", artifact.ID())
	}

	if artifact.Name() != "test-artifact" {
		t.Errorf("Expected Name to be 'test-artifact', got '%s'", artifact.Name())
	}

	if artifact.Type() != TypeFile {
		t.Errorf("Expected Type to be '%s', got '%s'", TypeFile, artifact.Type())
	}

	if artifact.ContentType() != "text/plain" {
		t.Errorf("Expected ContentType to be 'text/plain', got '%s'", artifact.ContentType())
	}

	if artifact.Size() != 10 {
		t.Errorf("Expected Size to be 10, got %d", artifact.Size())
	}

	// Test content retrieval
	reader, err := artifact.Content()
	if err != nil {
		t.Errorf("Unexpected error getting content: %v", err)
	}

	retrievedContent, err := io.ReadAll(reader)
	if err != nil {
		t.Errorf("Unexpected error reading content: %v", err)
	}
	reader.Close()

	if !bytes.Equal(retrievedContent, content) {
		t.Errorf("Expected content to be '%s', got '%s'", content, retrievedContent)
	}

	// Test tag operations
	tags := artifact.Tags()
	if len(tags) != 1 || tags["key"] != "value" {
		t.Errorf("Expected Tags to have key 'key' with value 'value', got %v", tags)
	}

	// Add a new tag
	artifact.AddTag("new-key", "new-value")
	tags = artifact.Tags()
	if len(tags) != 2 || tags["new-key"] != "new-value" {
		t.Errorf("Expected Tags to have key 'new-key' with value 'new-value', got %v", tags)
	}

	// Remove a tag
	artifact.RemoveTag("key")
	tags = artifact.Tags()
	if len(tags) != 1 || tags["key"] == "value" {
		t.Errorf("Expected tag 'key' to be removed, got %v", tags)
	}

	// Test metadata
	metadataResult := artifact.Metadata()
	if metadataResult.ID != "test-id" || metadataResult.Name != "test-artifact" {
		t.Errorf("Expected metadata to match original, got %v", metadataResult)
	}

	// Test update content
	newContent := []byte("new test data")
	artifact.UpdateContent(newContent)
	if artifact.Size() != int64(len(newContent)) {
		t.Errorf("Expected Size to be %d after content update, got %d", len(newContent), artifact.Size())
	}

	reader, err = artifact.Content()
	if err != nil {
		t.Errorf("Unexpected error getting updated content: %v", err)
	}

	retrievedContent, err = io.ReadAll(reader)
	if err != nil {
		t.Errorf("Unexpected error reading updated content: %v", err)
	}
	reader.Close()

	if !bytes.Equal(retrievedContent, newContent) {
		t.Errorf("Expected updated content to be '%s', got '%s'", newContent, retrievedContent)
	}

	// Test update metadata
	newName := "updated-artifact"
	newContentType := "application/json"
	artifact.UpdateMetadata(UpdateOptions{
		Name:        &newName,
		ContentType: &newContentType,
		Tags:        map[string]string{"tag1": "value1"},
		RemoveTags:  []string{"new-key"},
	})

	if artifact.Name() != newName {
		t.Errorf("Expected Name to be '%s' after update, got '%s'", newName, artifact.Name())
	}

	if artifact.ContentType() != newContentType {
		t.Errorf("Expected ContentType to be '%s' after update, got '%s'", newContentType, artifact.ContentType())
	}

	tags = artifact.Tags()
	if len(tags) != 1 || tags["tag1"] != "value1" || tags["new-key"] == "new-value" {
		t.Errorf("Expected Tags to be updated correctly, got %v", tags)
	}
}
