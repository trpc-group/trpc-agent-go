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
	"bytes"
	"encoding/json"
)

type tdaiMessage struct {
	ID        string `json:"id,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type serviceIdentity struct {
	serviceID string
	teamID    string
	agentID   string
}

type v3Version string

func (v *v3Version) UnmarshalJSON(data []byte) error {
	raw := bytes.TrimSpace(data)
	if bytes.Equal(raw, []byte("null")) {
		*v = ""
		return nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*v = v3Version(value)
		return nil
	}
	var value json.Number
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if _, err := value.Int64(); err != nil {
		return err
	}
	*v = v3Version(value.String())
	return nil
}

type captureRequest struct {
	UserContent      string        `json:"user_content"`
	AssistantContent string        `json:"assistant_content"`
	SessionKey       string        `json:"session_key"`
	SessionID        string        `json:"session_id,omitempty"`
	UserID           string        `json:"user_id,omitempty"`
	Messages         []tdaiMessage `json:"messages,omitempty"`
}

type captureResponse struct {
	L0Recorded        int  `json:"l0_recorded"`
	SchedulerNotified bool `json:"scheduler_notified"`
}

type recallRequest struct {
	Query      string `json:"query"`
	SessionKey string `json:"session_key"`
	UserID     string `json:"user_id,omitempty"`
}

type recallResponse struct {
	Context             string `json:"context"`
	PrependContext      string `json:"prepend_context,omitempty"`
	AppendSystemContext string `json:"append_system_context,omitempty"`
	Strategy            string `json:"strategy,omitempty"`
	MemoryCount         int    `json:"memory_count,omitempty"`
}

type searchMemoriesRequest struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
	Type   string `json:"type,omitempty"`
	Scene  string `json:"scene,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

type searchMemoriesResponse struct {
	Results  string `json:"results"`
	Total    int    `json:"total"`
	Strategy string `json:"strategy"`
}

type searchConversationsRequest struct {
	Query      string `json:"query"`
	Limit      int    `json:"limit,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	SessionID  string `json:"-"`
	UserID     string `json:"user_id,omitempty"`
}

type searchConversationsResponse struct {
	Results string `json:"results"`
	Total   int    `json:"total"`
}

type endSessionRequest struct {
	SessionKey string `json:"session_key"`
	UserID     string `json:"user_id,omitempty"`
}

type endSessionResponse struct {
	Flushed bool `json:"flushed"`
}

// HealthResponse describes gateway readiness.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  int64  `json:"uptime"`
	Stores  struct {
		VectorStore      bool `json:"vectorStore"`
		EmbeddingService bool `json:"embeddingService"`
	} `json:"stores"`
}

type v3ResponseEnvelope[T any] struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      *T     `json:"data,omitempty"`
}

type v3Isolation struct {
	TeamID    string `json:"team_id"`
	AgentID   string `json:"agent_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id,omitempty"`
}

type v3Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

type v3ConversationAddRequest struct {
	v3Isolation
	Messages []v3Message `json:"messages"`
}

type v3ConversationAddData struct {
	AcceptedIDs      []string `json:"accepted_ids"`
	AcceptedVersions []string `json:"accepted_versions"`
	TotalCount       int      `json:"total_count"`
}

type v3ConversationSearchRequest struct {
	v3Isolation
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type v3ConversationSearchData struct {
	Messages []v3ConversationSearchHit `json:"messages"`
}

type v3ConversationSearchHit struct {
	ID        string  `json:"id,omitempty"`
	Role      string  `json:"role"`
	Content   string  `json:"content"`
	Timestamp string  `json:"timestamp,omitempty"`
	Score     float64 `json:"score"`
}

type v3AtomicSearchRequest struct {
	v3Isolation
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Type  string `json:"type,omitempty"`
}

type v3AtomicSearchData struct {
	Items []v3AtomicSearchHit `json:"items"`
}

type v3AtomicSearchHit struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Content    string  `json:"content"`
	Background string  `json:"background,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	Score      float64 `json:"score"`
}

type v3ScenarioListRequest struct {
	v3Isolation
	PathPrefix string `json:"path_prefix,omitempty"`
}

type v3ScenarioListData struct {
	Entries []v3ScenarioEntry `json:"entries"`
	Total   int               `json:"total"`
}

type v3ScenarioReadRequest struct {
	v3Isolation
	Path string `json:"path"`
}

type v3ScenarioFile struct {
	Path      string    `json:"path"`
	Version   v3Version `json:"version"`
	Content   string    `json:"content"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type v3ScenarioEntry struct {
	Path      string    `json:"path"`
	Version   v3Version `json:"version"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type v3CoreReadRequest struct {
	v3Isolation
}

type v3CoreFile struct {
	Version   v3Version `json:"version"`
	Content   string    `json:"content"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}
