//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package model

import "trpc.group/trpc-go/trpc-agent-go/tool"

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
	// Only one of Content or ContentParts should be provided.
	// If both are provided, ContentParts will be used.
	Content string `json:"content,omitempty"`
	// ContentParts is the content parts for multimodal messages.
	// Only one of Content or ContentParts should be provided.
	// If both are provided, ContentParts will be used.
	ContentParts []ContentPart `json:"content_parts,omitempty"`
	// ToolID is the ID of the tool used by tool response.
	ToolID string `json:"tool_id,omitempty"`
	// ToolName is the name of the tool used by tool response.
	ToolName string `json:"tool_name,omitempty"`
	// ToolCalls is the optional tool calls for the message.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
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

// Image represents an image data for vision models.
type Image struct {
	// URL is the URL of the image.
	URL string `json:"url"`
	// Detail is the detail level: "low", "high", "auto".
	Detail string `json:"detail,omitempty"`
}

// Audio represents audio input for audio models.
type Audio struct {
	// Data is the base64 encoded audio data.
	Data string `json:"data"`
	// Format is the format of the encoded audio data. Currently supports "wav" and "mp3".
	Format string `json:"format"`
}

// File represents file content for file input models.
type File struct {
	// Filename is the name of the file, used when passing the file to the model as a string.
	Filename string `json:"filename"`
	// FileData is the base64 encoded file data, used when passing the file to the model as a string.
	// Pick one from FileData or FileID.
	FileData string `json:"file_data"`
	// FileID is the ID of an uploaded file to use as input.
	// Pick one from FileData or FileID.
	FileID string `json:"file_id"`
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

// NewUserMessageWithContentParts creates a new user message with content parts.
func NewUserMessageWithContentParts(contentParts []ContentPart) Message {
	return Message{
		Role:         RoleUser,
		ContentParts: contentParts,
	}
}

// NewSystemMessageWithContentParts creates a new system message with content parts.
func NewSystemMessageWithContentParts(contentParts []ContentPart) Message {
	return Message{
		Role:         RoleSystem,
		ContentParts: contentParts,
	}
}

// NewAssistantMessageWithContentParts creates a new assistant message with content parts.
func NewAssistantMessageWithContentParts(contentParts []ContentPart) Message {
	return Message{
		Role:         RoleAssistant,
		ContentParts: contentParts,
	}
}

// NewTextContentPart creates a new text content part.
func NewTextContentPart(text string) ContentPart {
	return ContentPart{
		Type: ContentTypeText,
		Text: &text,
	}
}

// NewImageContentPart creates a new image content part.
func NewImageContentPart(url string, detail string) ContentPart {
	return ContentPart{
		Type: ContentTypeImage,
		Image: &Image{
			URL:    url,
			Detail: detail,
		},
	}
}

// NewAudioContentPart creates a new audio content part.
func NewAudioContentPart(data string, format string) ContentPart {
	return ContentPart{
		Type: ContentTypeAudio,
		Audio: &Audio{
			Data:   data,
			Format: format,
		},
	}
}

// NewFileContentPart creates a new file content part using file ID.
func NewFileContentPart(fileID string) ContentPart {
	return ContentPart{
		Type: ContentTypeFile,
		File: &File{
			FileID: fileID,
		},
	}
}

// NewFileContentPartWithData creates a new file content part using file data.
func NewFileContentPartWithData(filename, data string) ContentPart {
	return ContentPart{
		Type: ContentTypeFile,
		File: &File{
			FileData: data,
			Filename: filename,
		},
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
	GenerationConfig `json:",inline"`

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
