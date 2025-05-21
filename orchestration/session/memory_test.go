package session

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewMemoryManager(t *testing.T) {
	manager := NewMemoryManager()

	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	if manager.sessions == nil {
		t.Error("Expected non-nil sessions map")
	}

	if manager.expiration == nil {
		t.Error("Expected non-nil expiration map")
	}

	// Test with custom options
	customGenerator := func() string { return "custom-id" }
	customExpiration := 5 * time.Hour

	manager = NewMemoryManager(
		WithIDGenerator(customGenerator),
		WithExpiration(customExpiration),
	)

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

func TestMemoryManager_Create(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Test creating a session with default options
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Unexpected error creating session: %v", err)
	}

	if session == nil {
		t.Fatal("Expected non-nil session")
	}

	if session.ID() == "" {
		t.Error("Expected non-empty session ID")
	}

	// Verify session is stored in the manager
	if _, ok := manager.sessions[session.ID()]; !ok {
		t.Error("Session not found in manager")
	}

	// Test creating with custom ID generator
	customID := "test-session-id"
	customGenerator := func() string { return customID }

	session, err = manager.Create(ctx, WithIDGenerator(customGenerator))
	if err != nil {
		t.Fatalf("Unexpected error creating session: %v", err)
	}

	if session.ID() != customID {
		t.Errorf("Expected ID '%s', got '%s'", customID, session.ID())
	}

	// Verify expiration is set
	expTime, ok := manager.expiration[session.ID()]
	if !ok {
		t.Error("Expected expiration to be set")
	} else if expTime.IsZero() {
		t.Error("Expected non-zero expiration time")
	}
}

func TestMemoryManager_Get(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Test getting a non-existent session (should create new)
	session, err := manager.Get(ctx, "non-existent")
	if err != nil {
		t.Fatalf("Unexpected error getting session: %v", err)
	}

	if session == nil {
		t.Fatal("Expected non-nil session")
	}

	if session.ID() != "non-existent" {
		t.Errorf("Expected ID 'non-existent', got '%s'", session.ID())
	}

	// Test getting an existing session
	session, err = manager.Get(ctx, "non-existent")
	if err != nil {
		t.Fatalf("Unexpected error getting session: %v", err)
	}

	if session.ID() != "non-existent" {
		t.Errorf("Expected ID 'non-existent', got '%s'", session.ID())
	}

	// Test getting an expired session
	manager.mutex.Lock()
	manager.expiration["expired-id"] = time.Now().Add(-time.Hour)
	session = memory.NewBaseSession("expired-id", nil)
	manager.sessions["expired-id"] = session
	manager.mutex.Unlock()

	session, err = manager.Get(ctx, "expired-id")
	if err != nil {
		t.Fatalf("Unexpected error getting session: %v", err)
	}

	if session.ID() != "expired-id" {
		t.Errorf("Expected a new session with ID 'expired-id', got '%s'", session.ID())
	}

	// Check that the old session is gone
	manager.mutex.RLock()
	expiry, exists := manager.expiration["expired-id"]
	manager.mutex.RUnlock()

	if !exists {
		t.Error("Expected expiration to exist")
	} else if !expiry.After(time.Now()) {
		t.Error("Expected expiration to be in the future")
	}
}

func TestMemoryManager_Delete(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Create a session
	session, _ := manager.Create(ctx)
	id := session.ID()

	// Verify it exists
	manager.mutex.RLock()
	_, sessionExists := manager.sessions[id]
	_, expirationExists := manager.expiration[id]
	manager.mutex.RUnlock()

	if !sessionExists || !expirationExists {
		t.Error("Session should exist before deletion")
	}

	// Delete it
	err := manager.Delete(ctx, id)
	if err != nil {
		t.Fatalf("Unexpected error deleting session: %v", err)
	}

	// Verify it's gone
	manager.mutex.RLock()
	_, sessionExists = manager.sessions[id]
	_, expirationExists = manager.expiration[id]
	manager.mutex.RUnlock()

	if sessionExists || expirationExists {
		t.Error("Session should not exist after deletion")
	}

	// Test deleting non-existent session (should not error)
	err = manager.Delete(ctx, "non-existent")
	if err != nil {
		t.Fatalf("Unexpected error deleting non-existent session: %v", err)
	}
}

func TestMemoryManager_ListIDs(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Create some sessions
	session1, _ := manager.Create(ctx)
	session2, _ := manager.Create(ctx)
	session3, _ := manager.Create(ctx)

	// List IDs
	ids, err := manager.ListIDs(ctx)
	if err != nil {
		t.Fatalf("Unexpected error listing IDs: %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(ids))
	}

	// Check that all IDs are present
	idMap := make(map[string]bool)
	for _, id := range ids {
		idMap[id] = true
	}

	if !idMap[session1.ID()] || !idMap[session2.ID()] || !idMap[session3.ID()] {
		t.Error("Not all session IDs were returned")
	}
}

func TestMemoryManager_CleanExpired(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Create some sessions with immediate expiration
	manager.mutex.Lock()
	expiredTime := time.Now().Add(-time.Hour)
	unexpiredTime := time.Now().Add(time.Hour)

	// Create 3 expired sessions
	for i := 0; i < 3; i++ {
		id := generateID()
		manager.sessions[id] = memory.NewBaseSession(id, nil)
		manager.expiration[id] = expiredTime
	}

	// Create 2 unexpired sessions
	for i := 0; i < 2; i++ {
		id := generateID()
		manager.sessions[id] = memory.NewBaseSession(id, nil)
		manager.expiration[id] = unexpiredTime
	}
	manager.mutex.Unlock()

	// Clean expired sessions
	cleaned, err := manager.CleanExpired(ctx)
	if err != nil {
		t.Fatalf("Unexpected error cleaning expired sessions: %v", err)
	}

	if cleaned != 3 {
		t.Errorf("Expected 3 expired sessions to be cleaned, got %d", cleaned)
	}

	// Check that only unexpired sessions remain
	manager.mutex.RLock()
	if len(manager.sessions) != 2 {
		t.Errorf("Expected 2 sessions remaining, got %d", len(manager.sessions))
	}
	manager.mutex.RUnlock()
}

func TestSessionIntegration(t *testing.T) {
	ctx := context.Background()
	manager := NewMemoryManager()

	// Create a session
	session, _ := manager.Create(ctx)
	id := session.ID()

	// Add a message to the session
	err := session.AddMessage(ctx, message.NewUserMessage("Hello, world!"))
	if err != nil {
		t.Fatalf("Unexpected error adding message: %v", err)
	}

	// Get the session again
	retrievedSession, err := manager.Get(ctx, id)
	if err != nil {
		t.Fatalf("Unexpected error getting session: %v", err)
	}

	// Check that the message is there
	messages, err := retrievedSession.GetMessages(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting messages: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	} else if messages[0].Content != "Hello, world!" {
		t.Errorf("Expected message 'Hello, world!', got '%s'", messages[0].Content)
	}
}
