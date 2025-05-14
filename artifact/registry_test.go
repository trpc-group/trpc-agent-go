package artifact

import (
	"context"
	"io"
	"testing"
)

// mockStorage is a simple mock implementation for testing.
type mockStorage struct {
	name string
}

// Implement stubs to satisfy the Storage interface.
func (m *mockStorage) Create(ctx context.Context, name string, artifactType Type, contentType string, content io.Reader, tags map[string]string) (Artifact, error) {
	return nil, nil
}
func (m *mockStorage) Get(ctx context.Context, id string) (Artifact, error) {
	return nil, nil
}
func (m *mockStorage) Delete(ctx context.Context, id string) error {
	return nil
}
func (m *mockStorage) List(ctx context.Context, filter FilterOptions) ([]Artifact, error) {
	return nil, nil
}
func (m *mockStorage) Update(ctx context.Context, id string, options UpdateOptions) error {
	return nil
}
func (m *mockStorage) UpdateContent(ctx context.Context, id string, content io.Reader) error {
	return nil
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	storage1 := &mockStorage{name: "storage1"}
	storage2 := &mockStorage{name: "storage2"}

	if err := registry.Register("storage1", storage1, true); err != nil {
		t.Errorf("Failed to register storage1: %v", err)
	}

	if err := registry.Register("storage2", storage2, false); err != nil {
		t.Errorf("Failed to register storage2: %v", err)
	}

	retrievedStorage, err := registry.Get("storage1")
	if err != nil {
		t.Errorf("Failed to get storage1: %v", err)
	}
	if retrievedStorage != storage1 {
		t.Errorf("Got wrong storage for storage1")
	}

	defaultStorage, err := registry.GetDefault()
	if err != nil {
		t.Errorf("Failed to get default storage: %v", err)
	}
	if defaultStorage != storage1 {
		t.Errorf("Wrong default storage, expected storage1")
	}
}
