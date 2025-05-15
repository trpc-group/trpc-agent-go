package prompt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileRepository(t *testing.T) {
	// Test with valid path
	tmpDir := filepath.Join(os.TempDir(), "prompt_test_"+t.Name())
	defer os.RemoveAll(tmpDir)

	repo, err := NewFileRepository(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create file repository: %v", err)
	}
	if repo == nil {
		t.Fatal("Expected non-nil repository")
	}
	if repo.basePath != tmpDir {
		t.Errorf("Expected basePath %s, got %s", tmpDir, repo.basePath)
	}

	// Test with empty path
	_, err = NewFileRepository("")
	if err == nil {
		t.Error("Expected error when creating repository with empty path, got nil")
	}

	// Test with invalid path (create a file and try to use it as a directory)
	invalidPath := filepath.Join(os.TempDir(), "prompt_test_invalid_"+t.Name())
	defer os.RemoveAll(invalidPath)

	// Create a file with the same name to cause an error
	if err := os.WriteFile(invalidPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_, err = NewFileRepository(invalidPath)
	if err == nil {
		t.Error("Expected error when creating repository with invalid path, got nil")
	}
}

func TestFileRepository_Save_Get_Delete(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "prompt_test_"+t.Name())
	defer os.RemoveAll(tmpDir)

	repo, err := NewFileRepository(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create file repository: %v", err)
	}

	// Create a test template
	template := &Template{
		ID:          "test-file-template",
		Name:        "Test File Template",
		Description: "A template for testing file repository",
		Version:     "1.0.0",
		Content:     "Hello, {{name}}! This is a {{type}} template.",
		Variables: []Variable{
			{
				Name:        "name",
				Description: "The name to greet",
				Required:    true,
			},
			{
				Name:         "type",
				Description:  "The type of template",
				Required:     false,
				DefaultValue: "test",
			},
		},
		Tags:               []string{"test", "file"},
		ModelCompatibility: []string{"gpt-3.5", "gemini"},
	}

	// Test Save
	err = repo.Save(ctx, template)
	if err != nil {
		t.Fatalf("Failed to save template: %v", err)
	}

	// Verify file was created
	expectedFilePath := filepath.Join(tmpDir, "test-file-template.json")
	if _, err := os.Stat(expectedFilePath); os.IsNotExist(err) {
		t.Errorf("Expected file to exist at %s", expectedFilePath)
	}

	// Test Get
	retrieved, err := repo.Get(ctx, "test-file-template")
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}
	if retrieved.ID != template.ID {
		t.Errorf("Retrieved template ID mismatch: got %s, want %s", retrieved.ID, template.ID)
	}
	if retrieved.Content != template.Content {
		t.Errorf("Retrieved template content mismatch: got %s, want %s", retrieved.Content, template.Content)
	}

	// Test Save with invalid template (nil)
	err = repo.Save(ctx, nil)
	if err == nil {
		t.Error("Expected error when saving nil template, got nil")
	}

	// Test Save with invalid template (empty ID)
	invalidTemplate := &Template{
		Name:    "Invalid Template",
		Content: "Empty ID",
	}
	err = repo.Save(ctx, invalidTemplate)
	if err == nil {
		t.Error("Expected error when saving template with empty ID, got nil")
	}

	// Test Get with non-existent ID
	_, err = repo.Get(ctx, "non-existent")
	if err == nil {
		t.Error("Expected error when getting non-existent template, got nil")
	}

	// Test Get with corrupted file
	corruptedID := "corrupted-template"
	corruptedFilePath := filepath.Join(tmpDir, corruptedID+".json")
	if err := os.WriteFile(corruptedFilePath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to create corrupted test file: %v", err)
	}
	_, err = repo.Get(ctx, corruptedID)
	if err == nil {
		t.Error("Expected error when getting corrupted template, got nil")
	}

	// Test Delete
	err = repo.Delete(ctx, "test-file-template")
	if err != nil {
		t.Fatalf("Failed to delete template: %v", err)
	}

	// Verify file was deleted
	if _, err := os.Stat(expectedFilePath); !os.IsNotExist(err) {
		t.Error("Expected file to be deleted")
	}

	// Test Delete with non-existent template
	err = repo.Delete(ctx, "non-existent")
	if err == nil {
		t.Error("Expected error when deleting non-existent template, got nil")
	}
}

func TestFileRepository_List(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "prompt_test_"+t.Name())
	defer os.RemoveAll(tmpDir)

	repo, err := NewFileRepository(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create file repository: %v", err)
	}

	// Add several templates directly to the filesystem
	templates := []*Template{
		{
			ID:                 "template1",
			Name:               "Template One",
			Version:            "1.0",
			Content:            "Content 1",
			Tags:               []string{"test", "one"},
			ModelCompatibility: []string{"gpt-3.5"},
		},
		{
			ID:                 "template2",
			Name:               "Template Two",
			Version:            "2.0",
			Content:            "Content 2",
			Tags:               []string{"test", "two"},
			ModelCompatibility: []string{"gemini"},
		},
		{
			ID:                 "template3",
			Name:               "Special Template",
			Version:            "1.0",
			Content:            "Content 3",
			Tags:               []string{"special"},
			ModelCompatibility: []string{"gpt-3.5", "gemini"},
		},
	}

	// Save templates directly to files
	for _, template := range templates {
		data, err := json.MarshalIndent(template, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal template: %v", err)
		}
		filePath := filepath.Join(tmpDir, template.ID+".json")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatalf("Failed to write template file: %v", err)
		}
	}

	// Create an invalid file (directory)
	invalidDir := filepath.Join(tmpDir, "invalid-dir")
	if err := os.Mkdir(invalidDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create a non-JSON file
	nonJSONPath := filepath.Join(tmpDir, "not-a-template.txt")
	if err := os.WriteFile(nonJSONPath, []byte("This is not JSON"), 0644); err != nil {
		t.Fatalf("Failed to create non-JSON file: %v", err)
	}

	// Test List with empty filter
	result, err := repo.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("Failed to list templates: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("Expected 3 templates, got %d", len(result))
	}

	// Test List with tag filter
	result, err = repo.List(ctx, Filter{Tags: []string{"special"}})
	if err != nil {
		t.Fatalf("Failed to list templates with tag filter: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("Expected 1 template with tag 'special', got %d", len(result))
	}
	if len(result) > 0 && result[0].ID != "template3" {
		t.Errorf("Expected template3, got %s", result[0].ID)
	}

	// Test List with model filter
	result, err = repo.List(ctx, Filter{ModelNames: []string{"gemini"}})
	if err != nil {
		t.Fatalf("Failed to list templates with model filter: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 templates compatible with 'gemini', got %d", len(result))
	}

	// Test List with name filter
	result, err = repo.List(ctx, Filter{NameContains: "Special"})
	if err != nil {
		t.Fatalf("Failed to list templates with name filter: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("Expected 1 template with 'Special' in name, got %d", len(result))
	}

	// Test List with version filter
	result, err = repo.List(ctx, Filter{VersionExact: "1.0"})
	if err != nil {
		t.Fatalf("Failed to list templates with version filter: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 templates with version '1.0', got %d", len(result))
	}

	// Test List with invalid directory
	invalidRepo, err := NewFileRepository(filepath.Join(os.TempDir(), "nonexistent-dir"))
	if err != nil {
		t.Fatalf("Failed to create file repository with nonexistent directory: %v", err)
	}
	// Make sure the directory doesn't exist for this test
	os.RemoveAll(invalidRepo.basePath)
	_, err = invalidRepo.List(ctx, Filter{})
	if err == nil {
		t.Error("Expected error when listing templates from nonexistent directory, got nil")
	}

	// Create an invalid JSON file
	invalidJSONPath := filepath.Join(tmpDir, "invalid.json")
	if err := os.WriteFile(invalidJSONPath, []byte("{not valid json}"), 0644); err != nil {
		t.Fatalf("Failed to create invalid JSON file: %v", err)
	}

	// List should still work, skipping the invalid file
	result, err = repo.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("Failed to list templates with invalid JSON file: %v", err)
	}
	// Should still get the 3 valid templates
	if len(result) != 3 {
		t.Errorf("Expected 3 templates even with invalid JSON file, got %d", len(result))
	}
}

func TestPromptErrorMethods(t *testing.T) {
	// Test Error method without cause
	err := PromptError{
		Code:    "test_code",
		Message: "test message",
	}
	if err.Error() != "test message" {
		t.Errorf("Expected error message 'test message', got '%s'", err.Error())
	}

	// Test Error method with cause
	underlyingErr := ErrTemplateNotFound
	errWithCause := err.WithCause(underlyingErr)
	expected := "test message: template not found"
	if errWithCause.Error() != expected {
		t.Errorf("Expected error message '%s', got '%s'", expected, errWithCause.Error())
	}

	// Test WithCause returns a new error with the cause set
	if errWithCause.Cause != underlyingErr {
		t.Errorf("Expected cause to be set to the underlying error")
	}
}

func TestManagerListMethod(t *testing.T) {
	// Create a repository with some templates
	repo := NewMemoryRepository()
	renderer := NewSimpleRenderer()
	manager := NewManager(repo, renderer)
	ctx := context.Background()

	// Add templates
	templates := []*Template{
		{
			ID:      "template1",
			Name:    "Template One",
			Version: "1.0",
			Content: "Content 1",
			Tags:    []string{"test"},
		},
		{
			ID:      "template2",
			Name:    "Template Two",
			Version: "2.0",
			Content: "Content 2",
			Tags:    []string{"production"},
		},
	}

	for _, template := range templates {
		err := repo.Save(ctx, template)
		if err != nil {
			t.Fatalf("Failed to save template: %v", err)
		}
	}

	// Test List method
	result, err := manager.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("Failed to list templates: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 templates, got %d", len(result))
	}

	// Test with filter
	result, err = manager.List(ctx, Filter{Tags: []string{"production"}})
	if err != nil {
		t.Fatalf("Failed to list templates with filter: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("Expected 1 template with tag 'production', got %d", len(result))
	}
	if len(result) > 0 && result[0].ID != "template2" {
		t.Errorf("Expected template2, got %s", result[0].ID)
	}
}
