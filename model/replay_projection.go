//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import "encoding/json"

// These private wire types freeze the v1 projection's field order, JSON names,
// omission rules and nested encoding. Do not extend them when public message
// types change. New fields require an explicit binding policy; execution-relevant
// fields that v1 cannot protect require a new binding version before use.
type replayProjectionV1 struct {
	Role               string                `json:"role"`
	Content            string                `json:"content,omitempty"`
	ContentParts       []replayContentPartV1 `json:"content_parts,omitempty"`
	ToolID             string                `json:"tool_id,omitempty"`
	ToolName           string                `json:"tool_name,omitempty"`
	ToolCalls          []replayToolCallV1    `json:"tool_calls,omitempty"`
	ReasoningContent   string                `json:"reasoning_content,omitempty"`
	ReasoningSignature string                `json:"reasoning_signature,omitempty"`
}

type replayToolCallV1 struct {
	Type        string           `json:"type"`
	Function    replayFunctionV1 `json:"function,omitempty"`
	ID          string           `json:"id,omitempty"`
	Index       *int             `json:"index,omitempty"`
	ExtraFields map[string]any   `json:"extra_fields,omitempty"`
}

// Arguments precedes the other fields to match FunctionDefinitionParam's v1
// custom encoder. Its bytes are encoded as a JSON string, not base64 or an object.
type replayFunctionV1 struct {
	Arguments   string `json:"arguments,omitempty"`
	Name        string `json:"name"`
	Strict      bool   `json:"strict,omitempty"`
	Description string `json:"description,omitempty"`
}

type replayContentPartV1 struct {
	Type       string              `json:"type"`
	Text       *string             `json:"text,omitempty"`
	Image      *replayImageV1      `json:"image,omitempty"`
	Audio      *replayAudioV1      `json:"audio,omitempty"`
	Video      *replayVideoV1      `json:"video,omitempty"`
	File       *replayFileV1       `json:"file,omitempty"`
	ContentRef *replayContentRefV1 `json:"content_ref,omitempty"`
}

type replayImageV1 struct {
	URL    string `json:"url"`
	Data   []byte `json:"data"`
	Detail string `json:"detail,omitempty"`
	Format string `json:"format,omitempty"`
}

type replayAudioV1 struct {
	URL    string `json:"url,omitempty"`
	Data   []byte `json:"data"`
	Format string `json:"format"`
}

type replayVideoV1 struct {
	URL    string `json:"url,omitempty"`
	Data   []byte `json:"data"`
	Format string `json:"format,omitempty"`
}

type replayFileV1 struct {
	Name     string `json:"filename"`
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data"`
	FileID   string `json:"file_id"`
	MimeType string `json:"format,omitempty"`
}

type replayContentRefV1 struct {
	ArtifactRef     string `json:"artifact_ref"`
	ArtifactName    string `json:"artifact_name,omitempty"`
	ArtifactVersion int    `json:"artifact_version"`
	MimeType        string `json:"mime_type,omitempty"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	OriginalName    string `json:"original_name,omitempty"`
	EventID         string `json:"event_id,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
}

func marshalReplayProjectionV1(m *Message) ([]byte, error) {
	projection := replayProjectionV1{
		Role:               string(m.Role),
		Content:            m.Content,
		ToolID:             m.ToolID,
		ToolName:           m.ToolName,
		ReasoningContent:   m.ReasoningContent,
		ReasoningSignature: m.ReasoningSignature,
	}
	if len(m.ToolCalls) > 0 {
		projection.ToolCalls = make([]replayToolCallV1, len(m.ToolCalls))
		for i, call := range m.ToolCalls {
			projection.ToolCalls[i] = replayToolCallV1{
				Type: call.Type,
				Function: replayFunctionV1{
					Arguments:   string(call.Function.Arguments),
					Name:        call.Function.Name,
					Strict:      call.Function.Strict,
					Description: call.Function.Description,
				},
				ID:          call.ID,
				Index:       call.Index,
				ExtraFields: call.ExtraFields,
			}
		}
	}
	if len(m.ContentParts) > 0 {
		projection.ContentParts = make([]replayContentPartV1, len(m.ContentParts))
		for i, part := range m.ContentParts {
			// These conversions also force review when a public nested struct
			// changes shape, instead of silently changing the persisted hash.
			projection.ContentParts[i] = replayContentPartV1{
				Type:       string(part.Type),
				Text:       part.Text,
				Image:      (*replayImageV1)(part.Image),
				Audio:      (*replayAudioV1)(part.Audio),
				Video:      (*replayVideoV1)(part.Video),
				File:       (*replayFileV1)(part.File),
				ContentRef: (*replayContentRefV1)(part.ContentRef),
			}
		}
	}
	return json.Marshal(projection)
}
