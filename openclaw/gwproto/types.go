//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gwproto defines the JSON payloads used by the OpenClaw gateway.
package gwproto

import "encoding/json"

// MessageRequest matches the gateway /messages JSON payload.
//
// The request supports both:
//   - Text-only messages via the "text" field, and
//   - Multimodal messages via "content_parts".
//
// When both are present, "text" is treated as an additional text part.
type MessageRequest struct {
	Channel   string `json:"channel,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text,omitempty"`

	ContentParts []ContentPart `json:"content_parts,omitempty"`

	RequestSystemPrompt string `json:"request_system_prompt,omitempty"`

	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

// APIError matches gateway error payloads.
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Usage is a transport-safe subset of model token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// MessageResponse matches the gateway /messages response JSON.
type MessageResponse struct {
	SessionID string    `json:"session_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	Usage     *Usage    `json:"usage,omitempty"`
	Ignored   bool      `json:"ignored,omitempty"`
	Error     *APIError `json:"error,omitempty"`
}

// StreamEventType identifies one streaming gateway event.
type StreamEventType string

// StreamProgressStage identifies one high-level run progress stage.
type StreamProgressStage string

const (
	// MessagesStreamSuffix is the default suffix for stream routes.
	MessagesStreamSuffix = ":stream"
	// SSEContentType is the HTTP Content-Type for gateway event streams.
	SSEContentType = "text/event-stream"
	// SSEEventPrefix is the raw SSE prefix for event lines.
	SSEEventPrefix = "event:"
	// SSEDataPrefix is the raw SSE prefix for data lines.
	SSEDataPrefix = "data:"
	// SSEEventLinePrefix is the emitted SSE event line prefix.
	SSEEventLinePrefix = SSEEventPrefix + " "
	// SSEDataLinePrefix is the emitted SSE data line prefix.
	SSEDataLinePrefix = SSEDataPrefix + " "
	// SSELineEnding terminates one SSE event.
	SSELineEnding = "\n\n"

	// StreamEventTypeRunStarted marks the start of one gateway run.
	StreamEventTypeRunStarted StreamEventType = "run.started"
	// StreamEventTypeRunIgnored marks a request ignored by policy.
	StreamEventTypeRunIgnored StreamEventType = "run.ignored"
	// StreamEventTypeMessageDelta carries an incremental text delta.
	StreamEventTypeMessageDelta StreamEventType = "message.delta"
	// StreamEventTypePublicDelta carries an incremental public progress delta.
	StreamEventTypePublicDelta StreamEventType = "public.delta"
	// StreamEventTypeThoughtDelta carries an incremental thought delta.
	StreamEventTypeThoughtDelta StreamEventType = "thought.delta"
	// StreamEventTypePublicCompleted carries the latest public progress text.
	StreamEventTypePublicCompleted StreamEventType = "public.completed"
	// StreamEventTypeThoughtCompleted carries the final thought text.
	StreamEventTypeThoughtCompleted StreamEventType = "thought.completed"
	// StreamEventTypeMessageCompleted carries the final reply text.
	StreamEventTypeMessageCompleted StreamEventType = "message.completed"
	// StreamEventTypeRunProgress carries a high-level run status update.
	StreamEventTypeRunProgress StreamEventType = "run.progress"
	// StreamEventTypeRunCompleted marks successful stream completion.
	StreamEventTypeRunCompleted StreamEventType = "run.completed"
	// StreamEventTypeRunCanceled marks a request canceled by the client.
	StreamEventTypeRunCanceled StreamEventType = "run.canceled"
	// StreamEventTypeRunError marks a terminal stream error.
	StreamEventTypeRunError StreamEventType = "run.error"

	// StreamProgressStagePreparing marks pre-tool request setup.
	StreamProgressStagePreparing StreamProgressStage = "preparing"
	// StreamProgressStageReadingDocument marks document extraction.
	StreamProgressStageReadingDocument StreamProgressStage = "reading_document"
	// StreamProgressStageReadingSpreadsheet marks tabular extraction.
	StreamProgressStageReadingSpreadsheet StreamProgressStage = "reading_spreadsheet"
	// StreamProgressStageRunningTool marks a generic tool run.
	StreamProgressStageRunningTool StreamProgressStage = "running_tool"
	// StreamProgressStageSummarizing marks post-tool answer generation.
	StreamProgressStageSummarizing StreamProgressStage = "summarizing"
)

// StreamEvent is one gateway streaming event payload.
type StreamEvent struct {
	Type StreamEventType `json:"type"`

	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	Delta     string              `json:"delta,omitempty"`
	Reply     string              `json:"reply,omitempty"`
	Stage     StreamProgressStage `json:"stage,omitempty"`
	Summary   string              `json:"summary,omitempty"`
	ElapsedMS int64               `json:"elapsed_ms,omitempty"`
	Usage     *Usage              `json:"usage,omitempty"`
	Ignored   bool                `json:"ignored,omitempty"`
	Error     *APIError           `json:"error,omitempty"`
}

// ContentPartType is the type discriminator for ContentPart.
type ContentPartType string

const (
	PartTypeText     ContentPartType = "text"
	PartTypeImage    ContentPartType = "image"
	PartTypeAudio    ContentPartType = "audio"
	PartTypeVoice    ContentPartType = "voice"
	PartTypeFile     ContentPartType = "file"
	PartTypeVideo    ContentPartType = "video"
	PartTypeLocation ContentPartType = "location"
	PartTypeLink     ContentPartType = "link"
)

// ContentPart is one structured input item in a user message.
//
// Only one of the typed payload fields should be set based on Type.
type ContentPart struct {
	Type ContentPartType `json:"type"`

	Text     *string       `json:"text,omitempty"`
	Image    *ImagePart    `json:"image,omitempty"`
	Audio    *AudioPart    `json:"audio,omitempty"`
	File     *FilePart     `json:"file,omitempty"`
	Location *LocationPart `json:"location,omitempty"`
	Link     *LinkPart     `json:"link,omitempty"`
}

// ImagePart describes an image input.
//
// Use URL to reference an image by URL, or Data to inline bytes.
type ImagePart struct {
	URL    string `json:"url,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Detail string `json:"detail,omitempty"`
	Format string `json:"format,omitempty"`
}

// AudioPart describes an audio input.
//
// Use URL to reference audio by URL, or Data to inline bytes.
// Format is required when Data is used (e.g. "wav", "mp3").
type AudioPart struct {
	URL    string `json:"url,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Format string `json:"format,omitempty"`
}

// FilePart describes a file input.
//
// Use one of:
//   - FileID: reference a pre-uploaded file ID.
//   - URL: download file content from a URL.
//   - Data: inline file bytes.
//
// Filename is required for Data. Format is the MIME type for Data/URL.
type FilePart struct {
	Filename string `json:"filename,omitempty"`
	Data     []byte `json:"data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Format   string `json:"format,omitempty"`
	URL      string `json:"url,omitempty"`
}

// LocationPart describes a location input.
type LocationPart struct {
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Name      string  `json:"name,omitempty"`
}

// LinkPart describes a link input.
type LinkPart struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}
