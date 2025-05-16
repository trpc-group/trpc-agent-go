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

// GeminiPartData represents the structure for Gemini/Python compatibility.
type GeminiPartData struct {
	Text     string          `json:"text,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
	FileURL  string          `json:"file_url,omitempty"`
	JSON     json.RawMessage `json:"json,omitempty"`
}

// GeminiPart is the Gemini-compatible format for message parts.
type GeminiPart struct {
	MimeType string          `json:"mime_type,omitempty"`
	Text     string          `json:"text,omitempty"`
	InlineData *GeminiPartData `json:"inline_data,omitempty"`
}

// GeminiContent is the Gemini-compatible format for entire messages.
type GeminiContent struct {
	Role  string        `json:"role"`
	Parts []GeminiPart  `json:"parts"`
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

// ToGeminiContent converts a Message to the Gemini-compatible GeminiContent format.
func (m *Message) ToGeminiContent() *GeminiContent {
	geminiContent := &GeminiContent{
		Role:  string(m.Role),
		Parts: []GeminiPart{},
	}

	// If we have parts, convert them to GeminiParts
	if len(m.Parts) > 0 {
		for _, part := range m.Parts {
			geminiPart := GeminiPart{}
			
			switch part.Type {
			case ContentTypeText:
				geminiPart.MimeType = "text/plain"
				geminiPart.Text = part.Text
			case ContentTypeImage:
				geminiPart.MimeType = "image/jpeg" // Assuming JPEG, this can be refined
				geminiPart.InlineData = &GeminiPartData{
					ImageURL: part.ImageURL,
				}
			case ContentTypeFile:
				geminiPart.MimeType = "application/octet-stream" // Generic binary
				geminiPart.InlineData = &GeminiPartData{
					FileURL: part.FileURL,
				}
			case ContentTypeJSON:
				geminiPart.MimeType = "application/json"
				geminiPart.InlineData = &GeminiPartData{
					JSON: part.JSON,
				}
			}
			
			geminiContent.Parts = append(geminiContent.Parts, geminiPart)
		}
	} else if m.Content != "" {
		// If we only have content, add it as a text part
		geminiContent.Parts = append(geminiContent.Parts, GeminiPart{
			MimeType: "text/plain",
			Text:     m.Content,
		})
	}

	return geminiContent
}

// FromGeminiContent converts a GeminiContent to a Message.
func FromGeminiContent(geminiContent *GeminiContent) *Message {
	role := Role(geminiContent.Role)
	
	// If the role is not valid, default to user
	if role != RoleUser && role != RoleAssistant && role != RoleSystem && 
	   role != RoleTool && role != RoleFunction {
		role = RoleUser
	}
	
	message := &Message{
		ID:        uuid.New().String(),
		Role:      role,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
	}
	
	// Convert parts
	if len(geminiContent.Parts) > 0 {
		// If there's only one part with text, use it as Content
		if len(geminiContent.Parts) == 1 && geminiContent.Parts[0].Text != "" {
			message.Content = geminiContent.Parts[0].Text
		} else {
			// Otherwise, convert all parts
			for _, gPart := range geminiContent.Parts {
				if gPart.Text != "" {
					message.AddTextPart(gPart.Text)
				} else if gPart.InlineData != nil {
					if gPart.InlineData.ImageURL != "" {
						message.AddImagePart(gPart.InlineData.ImageURL)
					} else if gPart.InlineData.FileURL != "" {
						message.AddFilePart(gPart.InlineData.FileURL)
					} else if gPart.InlineData.JSON != nil {
						message.AddJSONPart(gPart.InlineData.JSON)
					}
				}
			}
		}
	}
	
	return message
}
