// Package message provides the core message types for agent communication.
package message

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Role represents the role of a message sender.
type Role string

// Predefined message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleFunction  Role = "function"
)

// ContentType represents the type of content in a message part.
type ContentType string

// Predefined content types.
const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
	ContentTypeFile  ContentType = "file"
	ContentTypeJSON  ContentType = "json"
)

// Part represents a part of a message with specific content type.
type Part struct {
	Type     ContentType     `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
	FileURL  string          `json:"file_url,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`
}

// Message represents a message in the ADK system.
type Message struct {
	ID        string                 `json:"id"`
	Role      Role                   `json:"role"`
	Content   string                 `json:"content,omitempty"`
	Parts     []Part                 `json:"parts,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// NewMessage creates a new message with the given role and content.
func NewMessage(role Role, content string) *Message {
	return &Message{
		ID:        uuid.New().String(),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
	}
}

// NewMessageWithParts creates a new message with the given role and parts.
func NewMessageWithParts(role Role, parts []Part) *Message {
	return &Message{
		ID:        uuid.New().String(),
		Role:      role,
		Parts:     parts,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
	}
}

// NewUserMessage creates a new user message.
func NewUserMessage(content string) *Message {
	return NewMessage(RoleUser, content)
}

// NewAssistantMessage creates a new assistant message.
func NewAssistantMessage(content string) *Message {
	return NewMessage(RoleAssistant, content)
}

// NewSystemMessage creates a new system message.
func NewSystemMessage(content string) *Message {
	return NewMessage(RoleSystem, content)
}

// NewToolMessage creates a new tool message.
func NewToolMessage(content string) *Message {
	return NewMessage(RoleTool, content)
}

// NewFunctionMessage creates a new function message.
func NewFunctionMessage(content string) *Message {
	return NewMessage(RoleFunction, content)
}

// AddPart adds a part to the message.
func (m *Message) AddPart(part Part) {
	m.Parts = append(m.Parts, part)
}

// AddTextPart adds a text part to the message.
func (m *Message) AddTextPart(text string) {
	m.AddPart(Part{
		Type: ContentTypeText,
		Text: text,
	})
}

// AddImagePart adds an image part to the message.
func (m *Message) AddImagePart(imageURL string) {
	m.AddPart(Part{
		Type:     ContentTypeImage,
		ImageURL: imageURL,
	})
}

// AddFilePart adds a file part to the message.
func (m *Message) AddFilePart(fileURL string) {
	m.AddPart(Part{
		Type:    ContentTypeFile,
		FileURL: fileURL,
	})
}

// AddJSONPart adds a JSON part to the message.
func (m *Message) AddJSONPart(data json.RawMessage) {
	m.AddPart(Part{
		Type: ContentTypeJSON,
		JSON: data,
	})
}

// SetMetadata sets a metadata value.
func (m *Message) SetMetadata(key string, value interface{}) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]interface{})
	}
	m.Metadata[key] = value
}

// GetMetadata gets a metadata value.
func (m *Message) GetMetadata(key string) (interface{}, bool) {
	if m.Metadata == nil {
		return nil, false
	}
	val, ok := m.Metadata[key]
	return val, ok
}
