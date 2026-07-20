//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// OSSMemory contains a standard memory entry and optional fields returned by
// self-hosted Mem0 OSS. Metadata and ScoreDetails are detached snapshots that
// callers may modify without mutating the HTTP response retained by Service.
type OSSMemory struct {
	Entry          *memory.Entry  `json:"entry"`
	AgentID        string         `json:"agent_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	Hash           string         `json:"hash,omitempty"`
	ExpirationDate string         `json:"expiration_date,omitempty"`
	ActorID        string         `json:"actor_id,omitempty"`
	Role           string         `json:"role,omitempty"`
	AttributedTo   string         `json:"attributed_to,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ScoreDetails   map[string]any `json:"score_details,omitempty"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type createMemoryRequest struct {
	Messages  []apiMessage   `json:"messages"`
	UserID    string         `json:"user_id,omitempty"`
	AppID     string         `json:"app_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Infer     bool           `json:"infer"`
	Async     bool           `json:"async_mode"`
	Version   string         `json:"version,omitempty"`
	OrgID     string         `json:"org_id,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
}

type ossCreateMemoryRequest struct {
	Messages       []apiMessage   `json:"messages"`
	UserID         string         `json:"user_id,omitempty"`
	AgentID        string         `json:"agent_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ExpirationDate string         `json:"expiration_date,omitempty"`
	Infer          bool           `json:"infer"`
	MemoryType     string         `json:"memory_type,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
}

type createMemoryEvent struct {
	ID      string `json:"id"`
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type createMemoryEvents []createMemoryEvent

func (e *createMemoryEvents) UnmarshalJSON(data []byte) error {
	var direct []createMemoryEvent
	if err := json.Unmarshal(data, &direct); err == nil {
		*e = direct
		return nil
	}

	var wrapped struct {
		Results []createMemoryEvent `json:"results"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*e = wrapped.Results
	return nil
}

type eventStatusResponse struct {
	ID      string              `json:"id"`
	Status  string              `json:"status"`
	Results []createMemoryEvent `json:"results"`
}

type memoryRecord struct {
	ID             string         `json:"id"`
	Memory         string         `json:"memory"`
	Metadata       map[string]any `json:"metadata"`
	UserID         string         `json:"user_id"`
	AppID          string         `json:"app_id"`
	AgentID        string         `json:"agent_id"`
	RunID          string         `json:"run_id"`
	Hash           string         `json:"hash"`
	ExpirationDate string         `json:"expiration_date"`
	ActorID        string         `json:"actor_id"`
	Role           string         `json:"role"`
	AttributedTo   string         `json:"attributed_to"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	ScoreDetails   map[string]any `json:"score_details"`
}

type listMemoriesResponse struct {
	Count    int            `json:"count"`
	Next     *string        `json:"next"`
	Previous *string        `json:"previous"`
	Results  []memoryRecord `json:"results"`
}

func (r *listMemoriesResponse) UnmarshalJSON(data []byte) error {
	var direct []memoryRecord
	if err := json.Unmarshal(data, &direct); err == nil {
		r.Results = direct
		return nil
	}

	type rawListMemoriesResponse listMemoriesResponse
	var wrapped rawListMemoriesResponse
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = listMemoriesResponse(wrapped)
	return nil
}

type searchV2Request struct {
	Query       string         `json:"query"`
	Filters     map[string]any `json:"filters,omitempty"`
	TopK        int            `json:"top_k,omitempty"`
	Threshold   *float64       `json:"threshold,omitempty"`
	Explain     bool           `json:"explain,omitempty"`
	ShowExpired bool           `json:"show_expired,omitempty"`
}

type searchV2Response struct {
	Memories []searchMemoryRecord `json:"memories"`
}

type searchMemoryRecord struct {
	ID             string         `json:"id"`
	Memory         string         `json:"memory"`
	Metadata       map[string]any `json:"metadata"`
	Score          float64        `json:"score"`
	ScoreDetails   map[string]any `json:"score_details"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      *string        `json:"updated_at"`
	UserID         string         `json:"user_id"`
	AppID          string         `json:"app_id"`
	AgentID        string         `json:"agent_id"`
	RunID          string         `json:"run_id"`
	Hash           string         `json:"hash"`
	ExpirationDate string         `json:"expiration_date"`
	ActorID        string         `json:"actor_id"`
	Role           string         `json:"role"`
	AttributedTo   string         `json:"attributed_to"`
}

func (r searchMemoryRecord) toMemoryRecord() memoryRecord {
	record := memoryRecord{
		ID:             r.ID,
		Memory:         r.Memory,
		Metadata:       r.Metadata,
		UserID:         r.UserID,
		AppID:          r.AppID,
		AgentID:        r.AgentID,
		RunID:          r.RunID,
		Hash:           r.Hash,
		ExpirationDate: r.ExpirationDate,
		ActorID:        r.ActorID,
		Role:           r.Role,
		AttributedTo:   r.AttributedTo,
		CreatedAt:      r.CreatedAt,
		ScoreDetails:   r.ScoreDetails,
	}
	if r.UpdatedAt != nil {
		record.UpdatedAt = *r.UpdatedAt
	}
	return record
}

func (r *searchV2Response) UnmarshalJSON(data []byte) error {
	var direct []searchMemoryRecord
	if err := json.Unmarshal(data, &direct); err == nil {
		r.Memories = direct
		return nil
	}

	var wrapped struct {
		Memories []searchMemoryRecord `json:"memories"`
		Results  []searchMemoryRecord `json:"results"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	if wrapped.Memories != nil {
		r.Memories = wrapped.Memories
		return nil
	}
	r.Memories = wrapped.Results
	return nil
}

type parsedTimes struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}
