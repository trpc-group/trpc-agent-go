package document

import (
	"testing"
	"time"
)

func TestDocument_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		doc      *Document
		expected bool
	}{
		{
			name:     "nil document",
			doc:      nil,
			expected: true,
		},
		{
			name: "empty content",
			doc: &Document{
				ID:      "test",
				Name:    "Test",
				Content: "",
			},
			expected: true,
		},
		{
			name: "non-empty content",
			doc: &Document{
				ID:      "test",
				Name:    "Test",
				Content: "Hello, world!",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.doc.IsEmpty()
			if result != tt.expected {
				t.Errorf("IsEmpty() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestDocument_Clone(t *testing.T) {
	original := &Document{
		ID:        "test-id",
		Name:      "Test Document",
		Content:   "This is test content.",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"key1": "value1",
			"key2": 42,
		},
	}

	clone := original.Clone()

	// Check that clone is not the same pointer.
	if clone == original {
		t.Error("Clone() returned the same pointer")
	}

	// Check that all fields are copied correctly.
	if clone.ID != original.ID {
		t.Errorf("Clone ID = %v, expected %v", clone.ID, original.ID)
	}
	if clone.Name != original.Name {
		t.Errorf("Clone Name = %v, expected %v", clone.Name, original.Name)
	}
	if clone.Content != original.Content {
		t.Errorf("Clone Content = %v, expected %v", clone.Content, original.Content)
	}
	if !clone.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("Clone CreatedAt = %v, expected %v", clone.CreatedAt, original.CreatedAt)
	}
	if !clone.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("Clone UpdatedAt = %v, expected %v", clone.UpdatedAt, original.UpdatedAt)
	}

	// Check that metadata is deep copied.
	if len(clone.Metadata) != len(original.Metadata) {
		t.Errorf("Clone metadata length = %v, expected %v", len(clone.Metadata), len(original.Metadata))
	}
	for k, v := range original.Metadata {
		if clone.Metadata[k] != v {
			t.Errorf("Clone metadata[%s] = %v, expected %v", k, clone.Metadata[k], v)
		}
	}

	// Verify that modifying clone doesn't affect original.
	clone.Content = "Modified content"
	if original.Content == clone.Content {
		t.Error("Modifying clone affected original")
	}
}
