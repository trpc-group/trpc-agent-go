package message

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMessage(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello, world!")

	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, RoleUser, msg.Role)
	assert.Equal(t, "Hello, world!", msg.Content)
	assert.NotZero(t, msg.CreatedAt)
	assert.NotNil(t, msg.Metadata)
}

func TestNewMessageWithParts(t *testing.T) {
	parts := []Part{
		{Type: ContentTypeText, Text: "Hello"},
		{Type: ContentTypeImage, ImageURL: "http://example.com/image.jpg"},
	}

	msg := NewMessageWithParts(RoleAssistant, parts)

	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, RoleAssistant, msg.Role)
	assert.Len(t, msg.Parts, 2)
	assert.Equal(t, "Hello", msg.Parts[0].Text)
	assert.Equal(t, "http://example.com/image.jpg", msg.Parts[1].ImageURL)
	assert.NotZero(t, msg.CreatedAt)
	assert.NotNil(t, msg.Metadata)
}

func TestRoleSpecificMessages(t *testing.T) {
	t.Run("User Message", func(t *testing.T) {
		msg := NewUserMessage("Hello")
		assert.Equal(t, RoleUser, msg.Role)
		assert.Equal(t, "Hello", msg.Content)
	})

	t.Run("Assistant Message", func(t *testing.T) {
		msg := NewAssistantMessage("Hello back")
		assert.Equal(t, RoleAssistant, msg.Role)
		assert.Equal(t, "Hello back", msg.Content)
	})

	t.Run("System Message", func(t *testing.T) {
		msg := NewSystemMessage("Instructions")
		assert.Equal(t, RoleSystem, msg.Role)
		assert.Equal(t, "Instructions", msg.Content)
	})

	t.Run("Tool Message", func(t *testing.T) {
		msg := NewToolMessage("Tool results")
		assert.Equal(t, RoleTool, msg.Role)
		assert.Equal(t, "Tool results", msg.Content)
	})

	t.Run("Function Message", func(t *testing.T) {
		msg := NewFunctionMessage("Function results")
		assert.Equal(t, RoleFunction, msg.Role)
		assert.Equal(t, "Function results", msg.Content)
	})
}

func TestAddPart(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	// Add a part directly
	part := Part{Type: ContentTypeImage, ImageURL: "http://example.com/image.jpg"}
	msg.AddPart(part)

	assert.Len(t, msg.Parts, 1)
	assert.Equal(t, ContentTypeImage, msg.Parts[0].Type)
	assert.Equal(t, "http://example.com/image.jpg", msg.Parts[0].ImageURL)
}

func TestAddTextPart(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	msg.AddTextPart("Additional text")

	assert.Len(t, msg.Parts, 1)
	assert.Equal(t, ContentTypeText, msg.Parts[0].Type)
	assert.Equal(t, "Additional text", msg.Parts[0].Text)
}

func TestAddImagePart(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	msg.AddImagePart("http://example.com/image.jpg")

	assert.Len(t, msg.Parts, 1)
	assert.Equal(t, ContentTypeImage, msg.Parts[0].Type)
	assert.Equal(t, "http://example.com/image.jpg", msg.Parts[0].ImageURL)
}

func TestAddFilePart(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	msg.AddFilePart("http://example.com/file.pdf")

	assert.Len(t, msg.Parts, 1)
	assert.Equal(t, ContentTypeFile, msg.Parts[0].Type)
	assert.Equal(t, "http://example.com/file.pdf", msg.Parts[0].FileURL)
}

func TestAddJSONPart(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	jsonData := json.RawMessage(`{"key":"value"}`)
	msg.AddJSONPart(jsonData)

	assert.Len(t, msg.Parts, 1)
	assert.Equal(t, ContentTypeJSON, msg.Parts[0].Type)
	assert.Equal(t, jsonData, msg.Parts[0].JSON)
}

func TestMetadata(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	// Test setting and getting metadata
	msg.SetMetadata("key1", "value1")
	msg.SetMetadata("key2", 42)

	// Get existing metadata
	val1, ok1 := msg.GetMetadata("key1")
	assert.True(t, ok1)
	assert.Equal(t, "value1", val1)

	val2, ok2 := msg.GetMetadata("key2")
	assert.True(t, ok2)
	assert.Equal(t, 42, val2)

	// Get non-existent metadata
	val3, ok3 := msg.GetMetadata("nonexistent")
	assert.False(t, ok3)
	assert.Nil(t, val3)

	// Test with nil metadata map
	msgWithNilMetadata := &Message{
		ID:       "test",
		Role:     RoleUser,
		Content:  "Hello",
		Metadata: nil,
	}

	val4, ok4 := msgWithNilMetadata.GetMetadata("key")
	assert.False(t, ok4)
	assert.Nil(t, val4)

	// Setting metadata should initialize the map
	msgWithNilMetadata.SetMetadata("key", "value")
	val5, ok5 := msgWithNilMetadata.GetMetadata("key")
	assert.True(t, ok5)
	assert.Equal(t, "value", val5)
}

func TestMessageWithMultipleParts(t *testing.T) {
	msg := NewMessage(RoleUser, "Hello")

	// Add multiple parts of different types
	msg.AddTextPart("Text part")
	msg.AddImagePart("http://example.com/image.jpg")
	msg.AddFilePart("http://example.com/file.pdf")
	jsonData := json.RawMessage(`{"key":"value"}`)
	msg.AddJSONPart(jsonData)

	// Verify all parts were added correctly
	assert.Len(t, msg.Parts, 4)
	assert.Equal(t, ContentTypeText, msg.Parts[0].Type)
	assert.Equal(t, "Text part", msg.Parts[0].Text)
	assert.Equal(t, ContentTypeImage, msg.Parts[1].Type)
	assert.Equal(t, "http://example.com/image.jpg", msg.Parts[1].ImageURL)
	assert.Equal(t, ContentTypeFile, msg.Parts[2].Type)
	assert.Equal(t, "http://example.com/file.pdf", msg.Parts[2].FileURL)
	assert.Equal(t, ContentTypeJSON, msg.Parts[3].Type)
	assert.Equal(t, jsonData, msg.Parts[3].JSON)
}

func TestMessageRoleConstants(t *testing.T) {
	// Test that role constants have the expected values
	assert.Equal(t, Role("user"), RoleUser)
	assert.Equal(t, Role("assistant"), RoleAssistant)
	assert.Equal(t, Role("system"), RoleSystem)
	assert.Equal(t, Role("tool"), RoleTool)
	assert.Equal(t, Role("function"), RoleFunction)
}

func TestContentTypeConstants(t *testing.T) {
	// Test that content type constants have the expected values
	assert.Equal(t, ContentType("text"), ContentTypeText)
	assert.Equal(t, ContentType("image"), ContentTypeImage)
	assert.Equal(t, ContentType("file"), ContentTypeFile)
	assert.Equal(t, ContentType("json"), ContentTypeJSON)
}

func TestPartStructure(t *testing.T) {
	// Test creating a Part with each content type
	textPart := Part{
		Type: ContentTypeText,
		Text: "Text content",
	}
	assert.Equal(t, ContentTypeText, textPart.Type)
	assert.Equal(t, "Text content", textPart.Text)

	imagePart := Part{
		Type:     ContentTypeImage,
		ImageURL: "http://example.com/image.jpg",
	}
	assert.Equal(t, ContentTypeImage, imagePart.Type)
	assert.Equal(t, "http://example.com/image.jpg", imagePart.ImageURL)

	filePart := Part{
		Type:    ContentTypeFile,
		FileURL: "http://example.com/file.pdf",
	}
	assert.Equal(t, ContentTypeFile, filePart.Type)
	assert.Equal(t, "http://example.com/file.pdf", filePart.FileURL)

	jsonData := json.RawMessage(`{"key":"value"}`)
	jsonPart := Part{
		Type: ContentTypeJSON,
		JSON: jsonData,
	}
	assert.Equal(t, ContentTypeJSON, jsonPart.Type)
	assert.Equal(t, jsonData, jsonPart.JSON)
}
