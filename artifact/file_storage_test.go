package artifact

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStorage(t *testing.T) {
	// Create a temporary directory for testing
	testDir, err := os.MkdirTemp("", "artifact-test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Create the storage
	storage, err := NewFileStorage(testDir)
	if err != nil {
		t.Fatalf("Failed to create FileStorage: %v", err)
	}

	// Create a context for testing
	ctx := context.Background()

	// Test creating an artifact
	content := strings.NewReader("test content")
	tags := map[string]string{"tag1": "value1", "tag2": "value2"}
	artifact, err := storage.Create(ctx, "test-artifact", TypeText, "text/plain", content, tags)
	if err != nil {
		t.Fatalf("Failed to create artifact: %v", err)
	}

	// Verify the artifact was created correctly
	if artifact.Name() != "test-artifact" {
		t.Errorf("Expected Name to be 'test-artifact', got '%s'", artifact.Name())
	}
	if artifact.Type() != TypeText {
		t.Errorf("Expected Type to be '%s', got '%s'", TypeText, artifact.Type())
	}
	if artifact.ContentType() != "text/plain" {
		t.Errorf("Expected ContentType to be 'text/plain', got '%s'", artifact.ContentType())
	}
	if artifact.Size() != 12 { // "test content" is 12 bytes
		t.Errorf("Expected Size to be 12, got %d", artifact.Size())
	}

	// Verify tags
	retrievedTags := artifact.Tags()
	if len(retrievedTags) != 2 || retrievedTags["tag1"] != "value1" || retrievedTags["tag2"] != "value2" {
		t.Errorf("Expected Tags to be %v, got %v", tags, retrievedTags)
	}

	// Get the artifact's ID for later use
	id := artifact.ID()

	// Test retrieving the artifact
	retrievedArtifact, err := storage.Get(ctx, id)
	if err != nil {
		t.Fatalf("Failed to retrieve artifact: %v", err)
	}

	// Verify the retrieved artifact
	if retrievedArtifact.ID() != id {
		t.Errorf("Expected ID to be '%s', got '%s'", id, retrievedArtifact.ID())
	}
	if retrievedArtifact.Name() != "test-artifact" {
		t.Errorf("Expected Name to be 'test-artifact', got '%s'", retrievedArtifact.Name())
	}

	// Verify content
	reader, err := retrievedArtifact.Content()
	if err != nil {
		t.Fatalf("Failed to get content: %v", err)
	}
	retrievedContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read content: %v", err)
	}
	reader.Close()
	
	if string(retrievedContent) != "test content" {
		t.Errorf("Expected content to be 'test content', got '%s'", string(retrievedContent))
	}

	// Test updating metadata
	newName := "updated-artifact"
	newContentType := "application/json"
	err = storage.Update(ctx, id, UpdateOptions{
		Name:        &newName,
		ContentType: &newContentType,
		Tags:        map[string]string{"tag3": "value3"},
		RemoveTags:  []string{"tag1"},
	})
	if err != nil {
		t.Fatalf("Failed to update artifact: %v", err)
	}

	// Verify the update
	retrievedArtifact, err = storage.Get(ctx, id)
	if err != nil {
		t.Fatalf("Failed to retrieve updated artifact: %v", err)
	}

	if retrievedArtifact.Name() != newName {
		t.Errorf("Expected Name to be '%s' after update, got '%s'", newName, retrievedArtifact.Name())
	}
	if retrievedArtifact.ContentType() != newContentType {
		t.Errorf("Expected ContentType to be '%s' after update, got '%s'", newContentType, retrievedArtifact.ContentType())
	}

	// Verify updated tags
	retrievedTags = retrievedArtifact.Tags()
	if len(retrievedTags) != 2 || retrievedTags["tag3"] != "value3" || retrievedTags["tag2"] != "value2" || retrievedTags["tag1"] == "value1" {
		t.Errorf("Expected Tags to be updated correctly, got %v", retrievedTags)
	}

	// Test updating content
	newContent := bytes.NewReader([]byte("updated content"))
	err = storage.UpdateContent(ctx, id, newContent)
	if err != nil {
		t.Fatalf("Failed to update content: %v", err)
	}

	// Verify the content update
	retrievedArtifact, err = storage.Get(ctx, id)
	if err != nil {
		t.Fatalf("Failed to retrieve artifact with updated content: %v", err)
	}

	reader, err = retrievedArtifact.Content()
	if err != nil {
		t.Fatalf("Failed to get updated content: %v", err)
	}
	retrievedContent, err = io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read updated content: %v", err)
	}
	reader.Close()
	
	if string(retrievedContent) != "updated content" {
		t.Errorf("Expected content to be 'updated content', got '%s'", string(retrievedContent))
	}

	// Create a second artifact for testing filtering and listing
	content2 := strings.NewReader("second artifact")
	tags2 := map[string]string{"tag2": "value2", "tag4": "value4"}
	artifact2, err := storage.Create(ctx, "second-artifact", TypeImage, "image/png", content2, tags2)
	if err != nil {
		t.Fatalf("Failed to create second artifact: %v", err)
	}

	// Test listing with no filter
	artifacts, err := storage.List(ctx, FilterOptions{})
	if err != nil {
		t.Fatalf("Failed to list artifacts: %v", err)
	}
	if len(artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(artifacts))
	}

	// Test listing with type filter
	artifacts, err = storage.List(ctx, FilterOptions{
		Types: []Type{TypeImage},
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with type filter: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID() != artifact2.ID() {
		t.Errorf("Expected 1 artifact of type TypeImage, got %d artifacts", len(artifacts))
	}

	// Test listing with tag filter
	artifacts, err = storage.List(ctx, FilterOptions{
		Tags: map[string]string{"tag2": "value2"},
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with tag filter: %v", err)
	}
	if len(artifacts) != 2 {
		t.Errorf("Expected 2 artifacts with tag 'tag2', got %d", len(artifacts))
	}

	// Test listing with combined filter
	artifacts, err = storage.List(ctx, FilterOptions{
		Types: []Type{TypeImage},
		Tags:  map[string]string{"tag4": "value4"},
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with combined filter: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID() != artifact2.ID() {
		t.Errorf("Expected 1 artifact matching combined filter, got %d", len(artifacts))
	}

	// Test listing with time filter
	// Create a time filter that should include all artifacts
	timeNow := time.Now()
	timePast := timeNow.Add(-1 * time.Hour)
	artifacts, err = storage.List(ctx, FilterOptions{
		CreatedAfter: timePast,
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with time filter: %v", err)
	}
	if len(artifacts) != 2 {
		t.Errorf("Expected 2 artifacts created after %v, got %d", timePast, len(artifacts))
	}

	// Test listing with pagination
	artifacts, err = storage.List(ctx, FilterOptions{
		Limit:  1,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with pagination: %v", err)
	}
	if len(artifacts) != 1 {
		t.Errorf("Expected 1 artifact with pagination, got %d", len(artifacts))
	}

	// Test listing with pagination (second page)
	artifacts, err = storage.List(ctx, FilterOptions{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("Failed to list artifacts with pagination (second page): %v", err)
	}
	if len(artifacts) != 1 {
		t.Errorf("Expected 1 artifact with pagination (second page), got %d", len(artifacts))
	}

	// Test deletion
	err = storage.Delete(ctx, id)
	if err != nil {
		t.Fatalf("Failed to delete artifact: %v", err)
	}

	// Verify the artifact was deleted
	_, err = storage.Get(ctx, id)
	if err == nil {
		t.Errorf("Expected error when retrieving deleted artifact, got nil")
	}

	// Verify only one artifact remains
	artifacts, err = storage.List(ctx, FilterOptions{})
	if err != nil {
		t.Fatalf("Failed to list artifacts after deletion: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID() != artifact2.ID() {
		t.Errorf("Expected 1 artifact after deletion, got %d", len(artifacts))
	}

	// Test deleting a non-existent artifact
	err = storage.Delete(ctx, "non-existent-id")
	if err == nil {
		t.Errorf("Expected error when deleting non-existent artifact, got nil")
	}
}

func TestFileStoragePersistence(t *testing.T) {
	// Create a temporary directory for testing
	testDir, err := os.MkdirTemp("", "artifact-persistence-test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Create an artifact in the first storage instance
	storage1, err := NewFileStorage(testDir)
	if err != nil {
		t.Fatalf("Failed to create first FileStorage: %v", err)
	}

	ctx := context.Background()
	content := strings.NewReader("persistence test")
	artifact, err := storage1.Create(ctx, "persistence-test", TypeText, "text/plain", content, nil)
	if err != nil {
		t.Fatalf("Failed to create artifact: %v", err)
	}
	id := artifact.ID()

	// Explicitly load content to verify it was stored correctly in the first instance
	retrievedArtifact, err := storage1.Get(ctx, id)
	if err != nil {
		t.Fatalf("Failed to retrieve artifact from first storage instance: %v", err)
	}
	reader, err := retrievedArtifact.Content()
	if err != nil {
		t.Fatalf("Failed to get content from first instance: %v", err)
	}
	firstContent, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("Failed to read content from first instance: %v", err)
	}
	if string(firstContent) != "persistence test" {
		t.Errorf("Expected content in first instance to be 'persistence test', got '%s'", string(firstContent))
	}

	// Create a second storage instance pointing to the same directory
	storage2, err := NewFileStorage(testDir)
	if err != nil {
		t.Fatalf("Failed to create second FileStorage: %v", err)
	}

	// Verify the artifact can be retrieved from the second instance
	retrievedArtifact, err = storage2.Get(ctx, id)
	if err != nil {
		t.Fatalf("Failed to retrieve artifact from second storage instance: %v", err)
	}

	if retrievedArtifact.ID() != id {
		t.Errorf("Expected ID to be '%s', got '%s'", id, retrievedArtifact.ID())
	}
	if retrievedArtifact.Name() != "persistence-test" {
		t.Errorf("Expected Name to be 'persistence-test', got '%s'", retrievedArtifact.Name())
	}

	// Explicitly load content from the second instance
	reader, err = retrievedArtifact.Content()
	if err != nil {
		t.Fatalf("Failed to get content from second instance: %v", err)
	}
	secondContent, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("Failed to read content from second instance: %v", err)
	}
	
	// Verify that the content in the second instance matches what we expect
	if string(secondContent) != "persistence test" {
		// Check if the content file exists and read it directly
		contentPath := filepath.Join(testDir, "content", id)
		directContent, err := os.ReadFile(contentPath)
		if err != nil {
			t.Fatalf("Failed to read content file directly: %v", err)
		}
		t.Errorf("Expected content to be 'persistence test', got '%s'. Direct file content: '%s'", 
			string(secondContent), string(directContent))
	}

	// Verify the storage subdirectories were created
	metadataDir := filepath.Join(testDir, "metadata")
	contentDir := filepath.Join(testDir, "content")

	if _, err := os.Stat(metadataDir); os.IsNotExist(err) {
		t.Errorf("Metadata directory was not created")
	}
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		t.Errorf("Content directory was not created")
	}

	// Verify the files were created
	metadataFile := filepath.Join(metadataDir, id+".json")
	contentFile := filepath.Join(contentDir, id)

	if _, err := os.Stat(metadataFile); os.IsNotExist(err) {
		t.Errorf("Metadata file was not created")
	}
	if _, err := os.Stat(contentFile); os.IsNotExist(err) {
		t.Errorf("Content file was not created")
	}
} 