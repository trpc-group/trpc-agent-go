//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import "trpc.group/trpc-go/trpc-agent-go/model"

type offloadResponseEnvelope[T any] struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      *T     `json:"data,omitempty"`
}

type offloadToolPair struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Params     any    `json:"params"`
	Result     any    `json:"result"`
	Error      string `json:"error,omitempty"`
	Timestamp  string `json:"timestamp"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type offloadRecentMessage struct {
	Role    model.Role `json:"role"`
	Content string     `json:"content"`
}

type offloadIngestRequest struct {
	SessionID      string                 `json:"session_id"`
	ToolPairs      []offloadToolPair      `json:"tool_pairs"`
	Prompt         string                 `json:"prompt,omitempty"`
	RecentMessages []offloadRecentMessage `json:"recent_messages,omitempty"`
}

type offloadIngestData struct{}

type offloadCompactRequest struct {
	SessionID     string           `json:"session_id"`
	Messages      []offloadMessage `json:"messages"`
	Ratio         float64          `json:"ratio"`
	ContextWindow int              `json:"context_window"`
	TotalTokens   int              `json:"total_tokens"`
	MessageTokens []int            `json:"message_tokens,omitempty"`
}

type offloadCompactData struct {
	Messages []offloadMessage `json:"messages"`
}

// offloadMessage follows the OpenAI-compatible message shape accepted by the
// offload v2 compaction endpoint. TencentDB uses tool_call_id for tool result
// messages, while model.Message calls the same field ToolID.
type offloadMessage struct {
	Role               model.Role          `json:"role"`
	Content            string              `json:"content,omitempty"`
	ContentParts       []model.ContentPart `json:"content_parts,omitempty"`
	ToolCallID         string              `json:"tool_call_id,omitempty"`
	ToolName           string              `json:"tool_name,omitempty"`
	ToolCalls          []model.ToolCall    `json:"tool_calls,omitempty"`
	ReasoningContent   string              `json:"reasoning_content,omitempty"`
	ReasoningSignature string              `json:"reasoning_signature,omitempty"`
}

func newOffloadMessage(message model.Message) offloadMessage {
	return offloadMessage{
		Role:               message.Role,
		Content:            message.Content,
		ContentParts:       message.ContentParts,
		ToolCallID:         message.ToolID,
		ToolName:           message.ToolName,
		ToolCalls:          message.ToolCalls,
		ReasoningContent:   message.ReasoningContent,
		ReasoningSignature: message.ReasoningSignature,
	}
}

func (m offloadMessage) modelMessage() model.Message {
	return model.Message{
		Role:               m.Role,
		Content:            m.Content,
		ContentParts:       m.ContentParts,
		ToolID:             m.ToolCallID,
		ToolName:           m.ToolName,
		ToolCalls:          m.ToolCalls,
		ReasoningContent:   m.ReasoningContent,
		ReasoningSignature: m.ReasoningSignature,
	}
}

type offloadReadRefRequest struct {
	SessionID string `json:"session_id"`
	ResultRef string `json:"result_ref"`
	Query     string `json:"query,omitempty"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
	MaxTokens *int   `json:"max_tokens,omitempty"`
}

type offloadReadRefData struct {
	ResultRef  string `json:"result_ref"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	MatchFound *bool  `json:"match_found,omitempty"`
}
