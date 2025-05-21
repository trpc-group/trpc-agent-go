package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewPersistentManager(t *testing.T) {
	// Create a temporary directory for tests
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	// Create a file store provider
	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	// Test with default options
	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager with default options: %v", err)
	}

	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	// Test with custom options
	customGenerator := func() string { return "custom-id" }
	customExpiration := 5 * time.Hour

	manager, err = NewPersistentManager(
		fileStore,
		WithIDGenerator(customGenerator),
		WithExpiration(customExpiration),
	)

	if err != nil {
		t.Fatalf("Failed to create PersistentManager with custom options: %v", err)
	}

	if manager.options.IDGenerator == nil {
		t.Error("Expected non-nil ID generator")
	} else {
		id := manager.options.IDGenerator()
		if id != "custom-id" {
			t.Errorf("Expected ID 'custom-id', got '%s'", id)
		}
	}

	if manager.options.Expiration != customExpiration {
		t.Errorf("Expected expiration %v, got %v", customExpiration, manager.options.Expiration)
	}
}

func TestPersistentManager_Create(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Test basic creation
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Unexpected error creating session: %v", err)
	}

	if session == nil {
		t.Fatal("Expected non-nil session")
	}

	sessionID := session.ID()
	if sessionID == "" {
		t.Error("Expected non-empty session ID")
	}

	// Add a message to trigger saving
	msg := &message.Message{
		Role:    "user",
		Content: "Test message",
	}

	if err := session.AddMessage(ctx, msg); err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	// Check that the file was created
	filename := filepath.Join(tmpDir, "session_"+sessionID+".json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Errorf("Session file should exist at %s", filename)
	}

	// Test creation with custom ID
	customID := "custom-session-id"
	customGenerator := func() string { return customID }

	customSession, err := manager.Create(ctx, WithIDGenerator(customGenerator))
	if err != nil {
		t.Fatalf("Failed to create session with custom ID: %v", err)
	}

	if customSession.ID() != customID {
		t.Errorf("Expected ID %s, got %s", customID, customSession.ID())
	}
}

func TestPersistentManager_Get(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Create a session to get
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	sessionID := session.ID()

	// Add a message to trigger saving
	msg := &message.Message{
		Role:    "user",
		Content: "Test message for retrieval",
	}

	if err := session.AddMessage(ctx, msg); err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	// Get the session
	retrievedSession, err := manager.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if retrievedSession.ID() != sessionID {
		t.Errorf("Expected ID %s, got %s", sessionID, retrievedSession.ID())
	}

	// Check message was retrieved
	messages, err := retrievedSession.GetMessages(ctx)
	if err != nil {
		t.Fatalf("Failed to get messages: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	} else if messages[0].Content != "Test message for retrieval" {
		t.Errorf("Expected content 'Test message for retrieval', got '%s'", messages[0].Content)
	}

	// Test getting non-existent session (should create new one)
	nonExistentID := "non-existent-id"
	newSession, err := manager.Get(ctx, nonExistentID)
	if err != nil {
		t.Fatalf("Failed to get/create non-existent session: %v", err)
	}

	if newSession.ID() != nonExistentID {
		t.Errorf("Expected ID %s, got %s", nonExistentID, newSession.ID())
	}

	// New session should have no messages
	msgs, err := newSession.GetMessages(ctx)
	if err != nil {
		t.Fatalf("Failed to get messages from new session: %v", err)
	}

	if len(msgs) != 0 {
		t.Errorf("Expected 0 messages in new session, got %d", len(msgs))
	}
}

func TestPersistentManager_Delete(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Create a session to delete
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	sessionID := session.ID()

	// Add a message to trigger saving
	msg := &message.Message{
		Role:    "user",
		Content: "Test message",
	}

	if err := session.AddMessage(ctx, msg); err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	// Verify the file exists
	filename := filepath.Join(tmpDir, "session_"+sessionID+".json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Errorf("Session file should exist at %s", filename)
	}

	// Delete the session
	err = manager.Delete(ctx, sessionID)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify the file is gone
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Errorf("Session file should be deleted after Delete call")
	}

	// Test deleting non-existent session (should not error)
	err = manager.Delete(ctx, "non-existent-id")
	if err != nil {
		t.Errorf("Expected no error when deleting non-existent session, got: %v", err)
	}
}

func TestPersistentManager_ListIDs(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Test empty list
	ids, err := manager.ListIDs(ctx)
	if err != nil {
		t.Fatalf("Failed to list IDs: %v", err)
	}

	if len(ids) != 0 {
		t.Errorf("Expected empty ID list, got %v", ids)
	}

	// Create some sessions
	sessionIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		session, err := manager.Create(ctx)
		if err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}

		sessionIDs[i] = session.ID()

		// Add a message to trigger saving
		msg := &message.Message{
			Role:    "user",
			Content: "Test message",
		}

		if err := session.AddMessage(ctx, msg); err != nil {
			t.Fatalf("Failed to add message to session %d: %v", i, err)
		}
	}

	// List the IDs
	ids, err = manager.ListIDs(ctx)
	if err != nil {
		t.Fatalf("Failed to list IDs: %v", err)
	}

	// Verify we have the expected number of IDs
	if len(ids) != 3 {
		t.Errorf("Expected 3 session IDs, got %d", len(ids))
	}

	// Verify all created sessions are in the list
	idMap := make(map[string]bool)
	for _, id := range ids {
		idMap[id] = true
	}

	for i, id := range sessionIDs {
		if !idMap[id] {
			t.Errorf("Session ID %d (%s) not found in list", i, id)
		}
	}
}

// Add a more comprehensive test for the Get method to improve coverage
func TestPersistentManager_GetWithErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := filepath.Join(os.TempDir(), "persistent_manager_test_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(tmpDir)

	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}

	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Test with empty session ID - the implementation generates an ID
	emptySession, err := manager.Get(ctx, "")
	if err != nil {
		t.Logf("Get with empty ID returned error: %v", err)
	} else {
		// If no error, we should have a session with a generated ID
		if emptySession.ID() == "" {
			t.Error("Expected a generated ID for empty ID request, got empty ID")
		} else {
			t.Logf("Empty ID request generated ID: %s", emptySession.ID())
		}
	}

	// Create a corrupted session file
	sessionID := "corrupted-session"
	filename := filepath.Join(tmpDir, fmt.Sprintf("session_%s.json", sessionID))
	err = os.WriteFile(filename, []byte("not valid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to create corrupted session file: %v", err)
	}

	// Try to get the corrupted session - expect error
	_, err = manager.Get(ctx, sessionID)
	if err == nil {
		t.Error("Expected error when loading corrupted session file")
	} else {
		t.Logf("Got expected error for corrupted file: %v", err)
	}
}

// TestPersistentManager_CleanExpired tests the expiration cleanup functionality
func TestPersistentManager_CleanExpired(t *testing.T) {
	ctx := context.Background()

	// Create a mock store with controlled behavior
	mockStore := &mockStore{
		sessions: make(map[string]memory.Session),
		idList:   []string{},
	}

	// Create a manager with our mock store
	manager, err := NewPersistentManager(mockStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Create several sessions directly in the mock store
	// 3 expired sessions
	expiredIDs := []string{"expired-1", "expired-2", "expired-3"}
	for _, id := range expiredIDs {
		// Create a session with expired time
		session := memory.NewBaseSession(id, nil)
		session.SetMetadata("expiration", time.Now().Add(-1*time.Hour))
		mockStore.sessions[id] = session
		mockStore.idList = append(mockStore.idList, id)
	}

	// 2 active sessions
	activeIDs := []string{"active-1", "active-2"}
	for _, id := range activeIDs {
		// Create a session with future expiration time
		session := memory.NewBaseSession(id, nil)
		session.SetMetadata("expiration", time.Now().Add(24*time.Hour))
		mockStore.sessions[id] = session
		mockStore.idList = append(mockStore.idList, id)
	}

	// Run the cleanup
	count, err := manager.CleanExpired(ctx)
	if err != nil {
		t.Fatalf("CleanExpired failed: %v", err)
	}

	// Verify the count of expired sessions
	if count != len(expiredIDs) {
		t.Errorf("Expected %d expired sessions cleaned, got %d", len(expiredIDs), count)
	}

	// Verify expired sessions are gone
	for _, id := range expiredIDs {
		if _, exists := mockStore.sessions[id]; exists {
			t.Errorf("Expired session %s should be deleted", id)
		}
	}

	// Verify active sessions still exist
	for _, id := range activeIDs {
		if _, exists := mockStore.sessions[id]; !exists {
			t.Errorf("Active session %s should still exist", id)
		}
	}

	// List remaining IDs (should only be active ones)
	if len(mockStore.idList) != len(activeIDs) {
		t.Errorf("Expected %d remaining sessions, got %d", len(activeIDs), len(mockStore.idList))
	}
}

// mockStore is a controlled implementation of StoreProvider for testing
type mockStore struct {
	sessions  map[string]memory.Session
	idList    []string
	deleteErr error
	loadErr   error
	listErr   error
}

func (s *mockStore) Save(ctx context.Context, session memory.Session) error {
	s.sessions[session.ID()] = session
	return nil
}

func (s *mockStore) Load(ctx context.Context, id string) (memory.Session, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	session, exists := s.sessions[id]
	if !exists {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (s *mockStore) Delete(ctx context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.sessions, id)

	// Also remove from idList - create a new slice to avoid index issues during deletion
	newList := make([]string, 0, len(s.idList))
	for _, sessionID := range s.idList {
		if sessionID != id {
			newList = append(newList, sessionID)
		}
	}
	s.idList = newList
	return nil
}

func (s *mockStore) ListIDs(ctx context.Context) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.idList, nil
}

// Test CleanExpired with additional error conditions
func TestPersistentManager_CleanExpiredLoadError(t *testing.T) {
	ctx := context.Background()

	// Create a mock store with load error
	mockStore := &mockStore{
		sessions: make(map[string]memory.Session),
		idList:   []string{"session-1", "session-2"},
		loadErr:  fmt.Errorf("simulated load error"),
	}

	manager, err := NewPersistentManager(mockStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Run cleanup - should skip sessions that fail to load
	count, err := manager.CleanExpired(ctx)
	if err != nil {
		t.Fatalf("CleanExpired should not return error on load failure: %v", err)
	}

	// No sessions should be expired since they couldn't be loaded
	if count != 0 {
		t.Errorf("Expected 0 expired sessions, got %d", count)
	}

	// All sessions should still exist
	if len(mockStore.idList) != 2 {
		t.Errorf("Expected 2 sessions, got %d", len(mockStore.idList))
	}
}

// Test CleanExpired when Delete returns an error
func TestPersistentManager_CleanExpiredDeleteError(t *testing.T) {
	ctx := context.Background()

	// Create a mock store with delete error
	mockStore := &mockStore{
		sessions:  make(map[string]memory.Session),
		idList:    []string{},
		deleteErr: fmt.Errorf("simulated delete error"),
	}

	manager, err := NewPersistentManager(mockStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Add an expired session
	expiredID := "expired-session"
	session := memory.NewBaseSession(expiredID, nil)
	session.SetMetadata("expiration", time.Now().Add(-1*time.Hour))
	mockStore.sessions[expiredID] = session
	mockStore.idList = append(mockStore.idList, expiredID)

	// Run cleanup - should not count sessions that fail to delete
	count, err := manager.CleanExpired(ctx)
	if err != nil {
		t.Fatalf("CleanExpired should not return error on delete failure: %v", err)
	}

	// No sessions should be counted as expired since deletion failed
	if count != 0 {
		t.Errorf("Expected 0 successful deletions, got %d", count)
	}

	// The session should still exist
	if _, exists := mockStore.sessions[expiredID]; !exists {
		t.Errorf("Session should still exist despite delete error")
	}
}

// Test CleanExpired when ListIDs returns an error
func TestPersistentManager_CleanExpiredListError(t *testing.T) {
	ctx := context.Background()

	// Create a mock store with list error
	mockStore := &mockStore{
		sessions: make(map[string]memory.Session),
		idList:   []string{},
		listErr:  fmt.Errorf("simulated list error"),
	}

	manager, err := NewPersistentManager(mockStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Run cleanup - should return the list error
	count, err := manager.CleanExpired(ctx)
	if err == nil || err.Error() != "simulated list error" {
		t.Errorf("Expected list error, got: %v", err)
	}

	// Count should be 0 since the operation failed
	if count != 0 {
		t.Errorf("Expected 0 expired sessions, got %d", count)
	}
}

// Test CleanExpired with sessions that have no expiration metadata
func TestPersistentManager_CleanExpiredNoExpiry(t *testing.T) {
	ctx := context.Background()

	// Create a mock store
	mockStore := &mockStore{
		sessions: make(map[string]memory.Session),
		idList:   []string{},
	}

	manager, err := NewPersistentManager(mockStore)
	if err != nil {
		t.Fatalf("Failed to create PersistentManager: %v", err)
	}

	// Add a session with no expiration metadata
	sessionID := "no-expiry-session"
	session := memory.NewBaseSession(sessionID, nil)
	// Deliberately not setting expiration metadata
	mockStore.sessions[sessionID] = session
	mockStore.idList = append(mockStore.idList, sessionID)

	// Run cleanup - should not delete the session without expiry
	count, err := manager.CleanExpired(ctx)
	if err != nil {
		t.Fatalf("CleanExpired failed: %v", err)
	}

	// No sessions should be expired
	if count != 0 {
		t.Errorf("Expected 0 expired sessions, got %d", count)
	}

	// The session should still exist
	if _, exists := mockStore.sessions[sessionID]; !exists {
		t.Errorf("Session without expiry should not be deleted")
	}
}
