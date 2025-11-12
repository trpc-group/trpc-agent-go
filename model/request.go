//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Role represents the role of a message author.
type Role string

// Role constants for message authors.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Thinking parameter keys used in API requests.
const (
	// ThinkingEnabledKey is the key used for enabling thinking mode in API requests.
	ThinkingEnabledKey = "thinking_enabled"
	// ThinkingTokensKey is the key used for thinking tokens configuration in API requests.
	ThinkingTokensKey = "thinking_tokens"
	// ReasoningContentKey is the key used for reasoning content in API responses.
	ReasoningContentKey = "reasoning_content"
	// EnabledThinkingKey is the key used for enabling thinking mode in API requests e.g. Qwen model.
	EnabledThinkingKey = "enabled_thinking"
)

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// IsValid checks if the role is one of the defined constants.
func (r Role) IsValid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// Message represents a single message in a conversation.
type Message struct {
	// Role is the role of the message author.
	Role Role `json:"role"`
	// Content is the message content.
	Content string `json:"content,omitempty"`
	// ContentParts is the content parts for multimodal messages.
	ContentParts []ContentPart `json:"content_parts,omitempty"`
	// ToolID is the ID of the tool used by tool response.
	ToolID string `json:"tool_id,omitempty"`
	// ToolName is the name of the tool used by tool response.
	ToolName string `json:"tool_name,omitempty"`
	// ToolCalls is the optional tool calls for the message.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent is hunyuan or deepseek think content
	// - https://api-docs.deepseek.com/api/create-chat-completion#responses
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// AddFilePath adds a file path to the message.
func (m *Message) AddFilePath(filepath string) error {
	mimeType, err := inferMimeType(filepath)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}
	m.AddFileData(filepath, content, mimeType)
	return nil
}

// AddFileData adds a file data to the message.
// The argument of data is the raw file data without base64 encoding.
func (m *Message) AddFileData(name string, data []byte, mimetype string) {
	m.ContentParts = append(m.ContentParts, ContentPart{
		Type: ContentTypeFile,
		File: &File{
			Name:     name,
			Data:     data,
			MimeType: mimetype,
		},
	})
}

// AddFileID adds a file ID to the message.
// The file id can be obtained from the response of the upload file API.
func (m *Message) AddFileID(fileID string) {
	m.ContentParts = append(m.ContentParts, ContentPart{
		Type: ContentTypeFile,
		File: &File{
			FileID: fileID,
		},
	})
}

// AddImageURL adds an image URL to the message.
// The argument of detail is the detail level: "low", "high", "auto".
// If detail is empty, it will be set to "auto".
func (m *Message) AddImageURL(url, detail string) {
	m.ContentParts = append(m.ContentParts, ContentPart{
		Type: ContentTypeImage,
		Image: &Image{
			URL:    url,
			Detail: detail,
		},
	})
}

// AddImageFilePath adds an image file path to the message.
// The argument detail specifies the detail level: "low", "high", or "auto".
// If detail is empty, it will be set to "auto".
// Supported formats:
//
//   - PNG (.png)
//   - JPEG (.jpeg, .jpg)
//   - WEBP (.webp)
//   - Non-animated GIF (.gif)
//
// Reference: https://platform.openai.com/docs/guides/images-vision.
func (m *Message) AddImageFilePath(path string, detail string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Infer format from the file extension.
	ext := filepath.Ext(path)
	var format string
	switch ext {
	case ".png":
		format = "png"
	case ".jpg":
		format = "jpg"
	case ".jpeg":
		format = "jpeg"
	case ".webp":
		format = "webp"
	case ".gif":
		format = "gif"
	default:
		return fmt.Errorf("unsupported image format: %s", ext)
	}
	m.AddImageData(content, detail, format)
	return nil
}

// AddImageData adds an image data to the message.
// The argument of data is the raw image data without base64 encoding.
// The argument of detail is the detail level: "low", "high", "auto".
// If detail is empty, it will be set to "auto".
func (m *Message) AddImageData(data []byte, detail, format string) {
	m.ContentParts = append(m.ContentParts, ContentPart{
		Type: ContentTypeImage,
		Image: &Image{
			Data:   data,
			Detail: detail,
			Format: format,
		},
	})
}

// AddAudioFilePath adds an audio file path to the message.
func (m *Message) AddAudioFilePath(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Infer format from the file extension.
	format := filepath.Ext(path)
	if format == ".wav" {
		format = "wav"
	} else if format == ".mp3" {
		format = "mp3"
	} else {
		return fmt.Errorf("unsupported audio format: %s", format)
	}
	m.AddAudioData(content, format)
	return nil
}

// AddAudioData adds an audio data to the message.
// The argument of data is the raw audio data without base64 encoding.
// The argument of format is the format of the audio data.
// Currently supports "wav" and "mp3".
func (m *Message) AddAudioData(data []byte, format string) {
	m.ContentParts = append(m.ContentParts, ContentPart{
		Type: ContentTypeAudio,
		Audio: &Audio{
			Data:   data,
			Format: format,
		},
	})
}

// ContentType represents the type of content.
type ContentType string

// ContentType constants for content types.
const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
	ContentTypeAudio ContentType = "audio"
	ContentTypeFile  ContentType = "file"
)

// ContentPart represents a single content part in a multimodal message.
type ContentPart struct {
	// Type is the type of content: "text", "image", "audio", "file"
	Type ContentType `json:"type"`
	// Text is the text content.
	Text *string `json:"text,omitempty"`
	// Image is the image data.
	Image *Image `json:"image,omitempty"`
	// Audio is the audio data.
	Audio *Audio `json:"audio,omitempty"`
	// File is the file data.
	File *File `json:"file,omitempty"`
}

// File represents file content for file input models.
type File struct {
	// Name is the name of the file, used when passing the file to the model as a string.
	Name string `json:"filename"`
	// Data is the raw file data, used when passing the file to the model as a string.
	Data []byte `json:"data"`
	// FileID is the ID of an uploaded file to use as input.
	FileID string `json:"file_id"`
	// MimeType is the format of the file data.
	MimeType string `json:"format,omitempty"`
}

// Image represents an image data for vision models.
type Image struct {
	// URL is the URL of the image.
	URL string `json:"url"`
	// Data is the raw image data.
	Data []byte `json:"data"`
	// Detail is the detail level: "low", "high", "auto".
	Detail string `json:"detail,omitempty"`
	// Format is the format of the image data.
	Format string `json:"format,omitempty"`
}

// Audio represents audio input for audio models.
type Audio struct {
	// Data is the raw audio data.
	Data []byte `json:"data"`
	// Format is the format of the encoded audio data. Currently supports "wav" and "mp3".
	Format string `json:"format"`
}

// NewSystemMessage creates a new system message.
func NewSystemMessage(content string) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// NewUserMessage creates a new user message.
func NewUserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// NewToolMessage creates a new tool message.
func NewToolMessage(toolID, toolName, content string) Message {
	return Message{
		Role:     RoleTool,
		ToolID:   toolID,
		ToolName: toolName,
		Content:  content,
	}
}

// NewAssistantMessage creates a new assistant message.
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// GenerationConfig contains configuration for text generation.
type GenerationConfig struct {
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0 to 2.0).
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling (0.0 to 1.0).
	TopP *float64 `json:"top_p,omitempty"`

	// Stream indicates whether to stream the response.
	Stream bool `json:"stream"`

	// Stop sequences where the API will stop generating further tokens.
	Stop []string `json:"stop,omitempty"`

	// PresencePenalty penalizes new tokens based on their existing frequency.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// FrequencyPenalty penalizes new tokens based on their frequency in the text so far.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// ReasoningEffort limits the reasoning effort for reasoning models.
	// Supported values: "low", "medium", "high".
	// Only effective for OpenAI o-series models.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`

	// ThinkingEnabled enables thinking mode for Claude and Gemini models via OpenAI API.
	ThinkingEnabled *bool `json:"thinking_enabled,omitempty"`

	// ThinkingTokens controls the length of thinking for Claude and Gemini models via OpenAI API.
	ThinkingTokens *int `json:"thinking_tokens,omitempty"`
}

// Request is the request to the model.
type Request struct {
	// Messages is the conversation history.
	Messages []Message `json:"messages"`

	// GenerationConfig contains the generation parameters.
	GenerationConfig `json:"generation_config,omitempty"`

	// StructuredOutput defines how the model should produce structured output.
	// When set, the underlying model adapter may use native structured output
	// capabilities (e.g. OpenAI response_format with json_schema) to enforce
	// JSON formatting. This field is optional and provider-agnostic.
	StructuredOutput *StructuredOutput `json:"structured_output,omitempty"`

	Tools map[string]tool.Tool `json:"-"` // Tools are not serialized, handled separately
}

// ToolCall represents a call to a tool (function) in the model response.
type ToolCall struct {
	// Type of the tool. Currently, only `function` is supported.
	Type string `json:"type"`
	// Function definition for the tool
	Function FunctionDefinitionParam `json:"function,omitempty"`
	// The ID of the tool call returned by the model.
	ID string `json:"id,omitempty"`

	// Index is the index of the tool call in the message for streaming responses.
	Index *int `json:"index,omitempty"`
}

// FunctionDefinitionParam represents the parameters for a function definition in tool calls.
type FunctionDefinitionParam struct {
	// The name of the function to be called. Must be a-z, A-Z, 0-9, or contain
	// underscores and dashes, with a maximum length of 64.
	Name string `json:"name"`
	// Whether to enable strict schema adherence when generating the function call. If
	// set to true, the model will follow the exact schema defined in the `parameters`
	// field. Only a subset of JSON Schema is supported when `strict` is `true`. Learn
	// more about Structured Outputs in the
	// [function calling guide](docs/guides/function-calling).
	Strict bool `json:"strict,omitempty"`
	// A description of what the function does, used by the model to choose when and
	// how to call the function.
	Description string `json:"description,omitempty"`

	// Optional arguments to pass to the function, json-encoded.
	Arguments []byte `json:"arguments,omitempty"`
}

// MarshalJSON customizes JSON marshaling for FunctionDefinitionParam.
// This prevents double-encoding of the Arguments field by treating it as a string.
func (f FunctionDefinitionParam) MarshalJSON() ([]byte, error) {
	type Alias FunctionDefinitionParam
	return json.Marshal(&struct {
		Arguments string `json:"arguments,omitempty"`
		*Alias
	}{
		Arguments: string(f.Arguments),
		Alias:     (*Alias)(&f),
	})
}

// UnmarshalJSON customizes JSON unmarshaling for FunctionDefinitionParam.
// This ensures the Arguments field is properly decoded from JSON string to []byte.
func (f *FunctionDefinitionParam) UnmarshalJSON(data []byte) error {
	type Alias FunctionDefinitionParam
	aux := &struct {
		Arguments string `json:"arguments,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(f),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	f.Arguments = []byte(aux.Arguments)
	return nil
}

// toMIME maps file extensions to their corresponding MIME types.
var toMIME = map[string]string{
	".txt":  "text/plain",
	".md":   "text/markdown",
	".html": "text/html",
	".json": "application/json",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".pdf":  "application/pdf",
	".c":    "text/x-c",
	".cpp":  "text/x-c++",
	".cs":   "text/x-csharp",
	".java": "text/x-java",
	".js":   "text/javascript",
	".ts":   "application/typescript",
	".py":   "text/x-python",
	".rb":   "text/x-ruby",
	".css":  "text/css",
	".sh":   "application/x-sh",
	".php":  "text/x-php",
	".tex":  "text/x-tex",
}

// inferMimeType infers the MIME type from the file extension of the given path.
// Returns the MIME type string, or an error if the extension is unknown.
func inferMimeType(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if mime, ok := toMIME[ext]; ok {
		return mime, nil
	}
	return "", fmt.Errorf("unknown file extension: %s", ext)
}

// StructuredOutputType defines the type of structured output.
type StructuredOutputType string

const (
	// StructuredOutputJSONSchema enables structured JSON output.
	StructuredOutputJSONSchema StructuredOutputType = "json_schema"
)

// JSONSchemaConfig defines the configuration for JSON schema structured output.
type JSONSchemaConfig struct {
	// Name is the name of the structured output format.
	Name string `json:"name,omitempty"`
	// Schema is the JSON schema definition.
	Schema map[string]any `json:"schema"`
	// Strict controls whether to enforce strict schema adherence.
	Strict bool `json:"strict,omitempty"`
	// Description provides context for the model about the structured output.
	Description string `json:"description,omitempty"`
}

// StructuredOutput defines how the model should produce structured output.
type StructuredOutput struct {
	// Type specifies the structured output type.
	Type StructuredOutputType `json:"type"`
	// JSONSchema is used when Type is StructuredOutputJSONSchema.
	JSONSchema *JSONSchemaConfig `json:"json_schema,omitempty"`
}
