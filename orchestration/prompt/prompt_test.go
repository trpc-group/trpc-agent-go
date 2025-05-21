package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryRepository(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Create template
	template := &Template{
		ID:          "test-template-1",
		Name:        "Test Template",
		Description: "A template for testing",
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
		Tags:               []string{"test", "example"},
		ModelCompatibility: []string{"gpt-3.5", "gemini"},
	}

	// Test Save
	err := repo.Save(ctx, template)
	if err != nil {
		t.Fatalf("Failed to save template: %v", err)
	}

	// Test Get
	retrieved, err := repo.Get(ctx, "test-template-1")
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}
	if retrieved.ID != template.ID {
		t.Errorf("Retrieved template ID mismatch: got %s, want %s", retrieved.ID, template.ID)
	}

	// Test List with empty filter
	templates, err := repo.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("Failed to list templates: %v", err)
	}
	if len(templates) != 1 {
		t.Errorf("Expected 1 template, got %d", len(templates))
	}

	// Test List with tag filter
	templates, err = repo.List(ctx, Filter{Tags: []string{"example"}})
	if err != nil {
		t.Fatalf("Failed to list templates with tag filter: %v", err)
	}
	if len(templates) != 1 {
		t.Errorf("Expected 1 template with tag 'example', got %d", len(templates))
	}

	// Test List with non-matching tag filter
	templates, err = repo.List(ctx, Filter{Tags: []string{"nonexistent"}})
	if err != nil {
		t.Fatalf("Failed to list templates with non-matching tag filter: %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("Expected 0 templates with tag 'nonexistent', got %d", len(templates))
	}

	// Test Delete
	err = repo.Delete(ctx, "test-template-1")
	if err != nil {
		t.Fatalf("Failed to delete template: %v", err)
	}

	// Verify template is gone
	_, err = repo.Get(ctx, "test-template-1")
	if err == nil {
		t.Error("Expected error when getting deleted template, got nil")
	}
}

func TestSimpleRenderer(t *testing.T) {
	renderer := NewSimpleRenderer()
	ctx := context.Background()

	template := &Template{
		ID:      "test-renderer",
		Content: "Hello, {{name}}! Welcome to {{platform}}.",
		Variables: []Variable{
			{
				Name:     "name",
				Required: true,
			},
			{
				Name:         "platform",
				Required:     false,
				DefaultValue: "trpc-agent-go",
			},
		},
	}

	// Test with all variables
	result, err := renderer.Render(ctx, template, map[string]string{
		"name":     "User",
		"platform": "Custom Platform",
	})
	if err != nil {
		t.Fatalf("Failed to render template: %v", err)
	}
	expected := "Hello, User! Welcome to Custom Platform."
	if result != expected {
		t.Errorf("Rendered content mismatch: got %q, want %q", result, expected)
	}

	// Test with default value
	result, err = renderer.Render(ctx, template, map[string]string{
		"name": "User",
	})
	if err != nil {
		t.Fatalf("Failed to render template with default value: %v", err)
	}
	expected = "Hello, User! Welcome to trpc-agent-go."
	if result != expected {
		t.Errorf("Rendered content with default mismatch: got %q, want %q", result, expected)
	}

	// Test with nil template
	_, err = renderer.Render(ctx, nil, map[string]string{})
	if err == nil {
		t.Error("Expected error when rendering nil template, got nil")
	}

	// Test with unrecognized variable
	result, err = renderer.Render(ctx, template, map[string]string{
		"name":    "User",
		"unknown": "Value",
	})
	if err != nil {
		t.Fatalf("Failed to render template with unrecognized variable: %v", err)
	}
	if !strings.Contains(result, "trpc-agent-go") {
		t.Errorf("Template should ignore unrecognized variables, got: %s", result)
	}
}

func TestDefaultManager(t *testing.T) {
	repo := NewMemoryRepository()
	renderer := NewSimpleRenderer()
	manager := NewManager(repo, renderer)
	ctx := context.Background()

	// Create a template
	template := &Template{
		ID:      "greeting",
		Name:    "Greeting Template",
		Version: "1.0",
		Content: "Hello, {{name}}! Your favorite color is {{color}}.",
		Variables: []Variable{
			{
				Name:     "name",
				Required: true,
			},
			{
				Name:         "color",
				Required:     false,
				DefaultValue: "blue",
			},
		},
	}

	// Save template
	err := manager.Save(ctx, template)
	if err != nil {
		t.Fatalf("Failed to save template through manager: %v", err)
	}

	// Get template
	retrieved, err := manager.Get(ctx, "greeting")
	if err != nil {
		t.Fatalf("Failed to get template through manager: %v", err)
	}
	if retrieved.ID != "greeting" {
		t.Errorf("Retrieved template ID mismatch: got %s, want greeting", retrieved.ID)
	}

	// Render template
	result, err := manager.Render(ctx, "greeting", map[string]string{
		"name": "Alice",
	})
	if err != nil {
		t.Fatalf("Failed to render template through manager: %v", err)
	}
	expected := "Hello, Alice! Your favorite color is blue."
	if result != expected {
		t.Errorf("Rendered content mismatch: got %q, want %q", result, expected)
	}

	// Test GetRenderer
	if manager.GetRenderer() != renderer {
		t.Error("GetRenderer returned incorrect renderer")
	}

	// Test rendering non-existent template
	_, err = manager.Render(ctx, "nonexistent", map[string]string{})
	if err == nil {
		t.Error("Expected error when rendering non-existent template, got nil")
	}

	// Test Delete
	err = manager.Delete(ctx, "greeting")
	if err != nil {
		t.Fatalf("Failed to delete template through manager: %v", err)
	}

	// Verify template is gone
	_, err = manager.Get(ctx, "greeting")
	if err == nil {
		t.Error("Expected error when getting deleted template, got nil")
	}
}
