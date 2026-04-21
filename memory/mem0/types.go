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
)

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
	ID        string         `json:"id"`
	Memory    string         `json:"memory"`
	Metadata  map[string]any `json:"metadata"`
	UserID    string         `json:"user_id"`
	AppID     string         `json:"app_id"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
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
	Query   string         `json:"query"`
	Filters map[string]any `json:"filters,omitempty"`
	TopK    int            `json:"top_k,omitempty"`
}

type searchV2Response struct {
	Memories []searchMemoryRecord `json:"memories"`
}

type searchMemoryRecord struct {
	ID        string         `json:"id"`
	Memory    string         `json:"memory"`
	Metadata  map[string]any `json:"metadata"`
	Score     float64        `json:"score"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt *string        `json:"updated_at"`
	UserID    string         `json:"user_id"`
	AppID     string         `json:"app_id"`
}

func (r *searchV2Response) UnmarshalJSON(data []byte) error {
	var direct []searchMemoryRecord
	if err := json.Unmarshal(data, &direct); err == nil {
		r.Memories = direct
		return nil
	}

	type rawSearchV2Response searchV2Response
	var wrapped rawSearchV2Response
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*r = searchV2Response(wrapped)
	return nil
}

type parsedTimes struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}
