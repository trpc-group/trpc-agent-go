//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type offloadScope struct {
	AppName    string `json:"app_name"`
	UserID     string `json:"user_id"`
	SessionID  string `json:"session_id"`
	SessionKey string `json:"session_key"`
	AgentName  string `json:"agent_name"`
}

type offloadAfterToolMessagesRequest struct {
	Scope              offloadScope     `json:"scope"`
	Messages           []model.Message  `json:"messages,omitempty"`
	ToolCalls          []model.ToolCall `json:"tool_calls,omitempty"`
	ToolResultMessages []model.Message  `json:"tool_result_messages"`
}

type offloadAfterToolMessagesResponse struct {
	ToolResultMessages []model.Message `json:"tool_result_messages"`
}

func (r *offloadAfterToolMessagesResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		ToolResultMessages      []model.Message `json:"tool_result_messages"`
		ToolResultMessagesCamel []model.Message `json:"toolResultMessages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.ToolResultMessages = raw.ToolResultMessages
	if len(r.ToolResultMessages) == 0 {
		r.ToolResultMessages = raw.ToolResultMessagesCamel
	}
	return nil
}

type offloadBeforeModelRequest struct {
	Scope   offloadScope   `json:"scope"`
	Request *model.Request `json:"request"`
}

type offloadBeforeModelResponse struct {
	Request  *model.Request  `json:"request,omitempty"`
	Messages []model.Message `json:"messages,omitempty"`
}

type offloadReadRefRequest struct {
	Scope     offloadScope `json:"scope"`
	ResultRef string       `json:"result_ref"`
}

type offloadReadRefResponse struct {
	ResultRef string `json:"result_ref"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type offloadReadNodeRequest struct {
	Scope  offloadScope `json:"scope"`
	NodeID string       `json:"node_id"`
}

type offloadReadNodeResponse struct {
	NodeID  string              `json:"node_id"`
	Entries []offloadIndexEntry `json:"entries,omitempty"`
}

type offloadSearchIndexRequest struct {
	Scope offloadScope `json:"scope"`
	Query string       `json:"query"`
	Limit int          `json:"limit,omitempty"`
}

type offloadSearchIndexResponse struct {
	Query   string              `json:"query"`
	Entries []offloadIndexEntry `json:"entries,omitempty"`
	Total   int                 `json:"total"`
}

type offloadIndexEntry struct {
	Timestamp  string   `json:"timestamp,omitempty"`
	NodeID     *string  `json:"node_id,omitempty"`
	ToolCall   string   `json:"tool_call,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	ResultRef  string   `json:"result_ref,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	SessionKey string   `json:"session_key,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Offloaded  any      `json:"offloaded,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
}

func newOffloadScope(opts Options, sess *session.Session, agentName string) offloadScope {
	scope := offloadScope{
		SessionKey: defaultSessionKeyWithFunc(opts, sess),
		AgentName:  strings.TrimSpace(agentName),
	}
	if sess != nil {
		scope.AppName = strings.TrimSpace(sess.AppName)
		scope.UserID = strings.TrimSpace(sess.UserID)
		scope.SessionID = strings.TrimSpace(sess.ID)
	}
	return scope
}

func defaultSessionKeyWithFunc(opts Options, sess *session.Session) string {
	if opts.SessionKeyFunc != nil {
		return opts.SessionKeyFunc(sess)
	}
	return defaultSessionKey(sess)
}
