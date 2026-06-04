//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

type tdaiMessage struct {
	ID        string `json:"id,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
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
