package memory

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewBaseMemory(t *testing.T) {
	memory := NewBaseMemory()
	if memory == nil {
		t.Fatal("Expected non-nil memory")
	}

	size, err := memory.Size(context.Background())
	if err != nil {
		t.Fatalf("Unexpected error getting size: %v", err)
	}
	if size != 0 {
		t.Errorf("Expected size 0, got %d", size)
	}
}

func TestBaseMemory_Store(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Store a message
	msg := message.NewUserMessage("Test message")
	err := memory.Store(ctx, msg)
	if err != nil {
		t.Fatalf("Unexpected error storing message: %v", err)
	}

	// Verify size
	size, err := memory.Size(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting size: %v", err)
	}
	if size != 1 {
		t.Errorf("Expected size 1, got %d", size)
	}

	// Store nil message (should be a no-op)
	err = memory.Store(ctx, nil)
	if err != nil {
		t.Fatalf("Unexpected error storing nil message: %v", err)
	}

	// Verify size didn't change
	size, err = memory.Size(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting size: %v", err)
	}
	if size != 1 {
		t.Errorf("Expected size 1, got %d", size)
	}
}

func TestBaseMemory_Retrieve(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Store messages
	msg1 := message.NewUserMessage("Message 1")
	msg2 := message.NewAssistantMessage("Message 2")

	_ = memory.Store(ctx, msg1)
	_ = memory.Store(ctx, msg2)

	// Retrieve messages
	messages, err := memory.Retrieve(ctx)
	if err != nil {
		t.Fatalf("Unexpected error retrieving messages: %v", err)
	}

	// Verify count
	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// Verify content
	if messages[0].Content != "Message 1" {
		t.Errorf("Expected 'Message 1', got '%s'", messages[0].Content)
	}
	if messages[1].Content != "Message 2" {
		t.Errorf("Expected 'Message 2', got '%s'", messages[1].Content)
	}

	// Verify we get a copy
	messages[0] = message.NewUserMessage("Modified")

	// Retrieve again to verify original is intact
	originalMessages, _ := memory.Retrieve(ctx)
	if originalMessages[0].Content != "Message 1" {
		t.Errorf("Original message was modified: '%s'", originalMessages[0].Content)
	}
}

func TestBaseMemory_Search(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Store messages
	_ = memory.Store(ctx, message.NewUserMessage("Hello world"))
	_ = memory.Store(ctx, message.NewAssistantMessage("Testing search"))
	_ = memory.Store(ctx, message.NewUserMessage("Another message"))

	// Test search
	results, err := memory.Search(ctx, "world")
	if err != nil {
		t.Fatalf("Unexpected error during search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Content != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", results[0].Content)
	}

	// Test search with no matches
	results, err = memory.Search(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Unexpected error during search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

func TestBaseMemory_FilterByMetadata(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Store messages with metadata
	msg1 := message.NewUserMessage("Message with meta")
	msg1.SetMetadata("category", "important")
	msg1.SetMetadata("priority", 1)

	msg2 := message.NewAssistantMessage("Another message")
	msg2.SetMetadata("category", "normal")
	msg2.SetMetadata("priority", 2)

	_ = memory.Store(ctx, msg1)
	_ = memory.Store(ctx, msg2)

	// Filter by single metadata field
	results, err := memory.FilterByMetadata(ctx, map[string]interface{}{
		"category": "important",
	})
	if err != nil {
		t.Fatalf("Unexpected error during filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Content != "Message with meta" {
		t.Errorf("Expected 'Message with meta', got '%s'", results[0].Content)
	}

	// Filter by multiple metadata fields
	results, err = memory.FilterByMetadata(ctx, map[string]interface{}{
		"category": "normal",
		"priority": 2,
	})
	if err != nil {
		t.Fatalf("Unexpected error during filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Content != "Another message" {
		t.Errorf("Expected 'Another message', got '%s'", results[0].Content)
	}

	// Filter with no matches
	results, err = memory.FilterByMetadata(ctx, map[string]interface{}{
		"category": "nonexistent",
	})
	if err != nil {
		t.Fatalf("Unexpected error during filter: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

func TestBaseMemory_Clear(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Store messages
	_ = memory.Store(ctx, message.NewUserMessage("Message 1"))
	_ = memory.Store(ctx, message.NewAssistantMessage("Message 2"))

	// Verify initial size
	size, _ := memory.Size(ctx)
	if size != 2 {
		t.Fatalf("Expected size 2, got %d", size)
	}

	// Clear memory
	err := memory.Clear(ctx)
	if err != nil {
		t.Fatalf("Unexpected error clearing memory: %v", err)
	}

	// Verify size after clear
	size, _ = memory.Size(ctx)
	if size != 0 {
		t.Errorf("Expected size 0 after clear, got %d", size)
	}

	// Verify retrieve returns empty after clear
	messages, _ := memory.Retrieve(ctx)
	if len(messages) != 0 {
		t.Errorf("Expected empty messages after clear, got %d", len(messages))
	}
}

func TestBaseMemory_Size(t *testing.T) {
	memory := NewBaseMemory()
	ctx := context.Background()

	// Initially empty
	size, err := memory.Size(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting size: %v", err)
	}
	if size != 0 {
		t.Errorf("Expected size 0, got %d", size)
	}

	// Add messages
	_ = memory.Store(ctx, message.NewUserMessage("Message 1"))
	size, _ = memory.Size(ctx)
	if size != 1 {
		t.Errorf("Expected size 1, got %d", size)
	}

	_ = memory.Store(ctx, message.NewAssistantMessage("Message 2"))
	size, _ = memory.Size(ctx)
	if size != 2 {
		t.Errorf("Expected size 2, got %d", size)
	}

	// Clear and check size
	_ = memory.Clear(ctx)
	size, _ = memory.Size(ctx)
	if size != 0 {
		t.Errorf("Expected size 0 after clear, got %d", size)
	}
}

func TestNewBaseSession(t *testing.T) {
	// Test with provided memory
	memory := NewBaseMemory()
	session := NewBaseSession("test-id", memory)

	if session.ID() != "test-id" {
		t.Errorf("Expected ID 'test-id', got '%s'", session.ID())
	}

	if session.GetMemory() != memory {
		t.Error("Session memory doesn't match provided memory")
	}

	// Test with nil memory (should create a new one)
	session = NewBaseSession("test-id-2", nil)

	if session.ID() != "test-id-2" {
		t.Errorf("Expected ID 'test-id-2', got '%s'", session.ID())
	}

	if session.GetMemory() == nil {
		t.Error("Session should have created a memory when nil was provided")
	}
}

func TestBaseSession_AddMessage(t *testing.T) {
	session := NewBaseSession("test-id", nil)
	ctx := context.Background()

	// Add a message
	msg := message.NewUserMessage("Test message")
	err := session.AddMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Unexpected error adding message: %v", err)
	}

	// Verify message was added
	messages, err := session.GetMessages(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "Test message" {
		t.Errorf("Expected 'Test message', got '%s'", messages[0].Content)
	}

	// Add nil message (should be a no-op)
	beforeTime := session.LastUpdated()
	time.Sleep(1 * time.Millisecond) // Ensure time would be different

	err = session.AddMessage(ctx, nil)
	if err != nil {
		t.Fatalf("Unexpected error adding nil message: %v", err)
	}

	// Verify last updated time didn't change with nil message
	if session.LastUpdated() != beforeTime {
		t.Error("LastUpdated time changed with nil message")
	}

	// Verify message count didn't change
	messages, _ = session.GetMessages(ctx)
	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}
}

func TestBaseSession_GetRecentMessages(t *testing.T) {
	session := NewBaseSession("test-id", nil)
	ctx := context.Background()

	// Add multiple messages
	for i := 0; i < 5; i++ {
		_ = session.AddMessage(ctx, message.NewUserMessage(string(rune('A'+i))))
	}

	// Get all messages
	messages, err := session.GetMessages(ctx)
	if err != nil {
		t.Fatalf("Unexpected error getting messages: %v", err)
	}
	if len(messages) != 5 {
		t.Fatalf("Expected 5 messages, got %d", len(messages))
	}

	// Get 2 most recent messages
	recent, err := session.GetRecentMessages(ctx, 2)
	if err != nil {
		t.Fatalf("Unexpected error getting recent messages: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("Expected 2 recent messages, got %d", len(recent))
	}
	if recent[0].Content != "D" {
		t.Errorf("Expected 'D', got '%s'", recent[0].Content)
	}
	if recent[1].Content != "E" {
		t.Errorf("Expected 'E', got '%s'", recent[1].Content)
	}

	// Test with n > message count (should return all messages)
	all, err := session.GetRecentMessages(ctx, 10)
	if err != nil {
		t.Fatalf("Unexpected error getting recent messages: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(all))
	}

	// Test with n <= 0 (should return all messages)
	all, err = session.GetRecentMessages(ctx, 0)
	if err != nil {
		t.Fatalf("Unexpected error getting recent messages: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(all))
	}
}

func TestBaseSession_Metadata(t *testing.T) {
	session := NewBaseSession("test-id", nil)

	// Test set and get
	session.SetMetadata("key1", "value1")
	session.SetMetadata("key2", 2)

	value1, exists := session.GetMetadata("key1")
	if !exists {
		t.Fatal("Expected metadata key1 to exist")
	}
	if value1 != "value1" {
		t.Errorf("Expected value 'value1', got '%v'", value1)
	}

	value2, exists := session.GetMetadata("key2")
	if !exists {
		t.Fatal("Expected metadata key2 to exist")
	}
	if value2 != 2 {
		t.Errorf("Expected value 2, got '%v'", value2)
	}

	// Test get nonexistent key
	_, exists = session.GetMetadata("nonexistent")
	if exists {
		t.Error("Expected nonexistent key to not exist")
	}

	// Test overwrite
	session.SetMetadata("key1", "new-value")
	value1, _ = session.GetMetadata("key1")
	if value1 != "new-value" {
		t.Errorf("Expected 'new-value', got '%v'", value1)
	}
}

func TestBaseSession_Clear(t *testing.T) {
	session := NewBaseSession("test-id", nil)
	ctx := context.Background()

	// Add messages and metadata
	_ = session.AddMessage(ctx, message.NewUserMessage("Test message"))
	session.SetMetadata("key", "value")

	// Clear the session
	err := session.Clear(ctx)
	if err != nil {
		t.Fatalf("Unexpected error clearing session: %v", err)
	}

	// Verify messages cleared
	messages, _ := session.GetMessages(ctx)
	if len(messages) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(messages))
	}

	// Verify metadata cleared
	_, exists := session.GetMetadata("key")
	if exists {
		t.Error("Expected metadata to be cleared")
	}
}

func TestBaseSession_LastUpdated(t *testing.T) {
	session := NewBaseSession("test-id", nil)
	ctx := context.Background()

	// Initial time
	initialTime := session.LastUpdated()
	if initialTime.IsZero() {
		t.Error("Expected LastUpdated to be initialized")
	}

	// Wait to ensure time would change
	time.Sleep(1 * time.Millisecond)

	// Update via AddMessage
	_ = session.AddMessage(ctx, message.NewUserMessage("Test"))
	afterMessageTime := session.LastUpdated()
	if !afterMessageTime.After(initialTime) {
		t.Error("Expected LastUpdated to increase after AddMessage")
	}

	// Wait again
	time.Sleep(1 * time.Millisecond)

	// Update via SetMetadata
	session.SetMetadata("key", "value")
	afterMetadataTime := session.LastUpdated()
	if !afterMetadataTime.After(afterMessageTime) {
		t.Error("Expected LastUpdated to increase after SetMetadata")
	}
}
