//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deepsearch

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// DeepSearch extended query tool names.
const (
	EdgesByTagToolName          = "memory_deepsearch_edges_by_tag"
	ConversationTimeToolName    = "memory_deepsearch_conversation_time"
	EventKeywordsToolName       = "memory_deepsearch_event_keywords"
	EventContextToolName        = "memory_deepsearch_event_context"
	PersonalInformationToolName = "memory_deepsearch_personal_information"
	PersonalAspectToolName      = "memory_deepsearch_personal_aspect"
	TopicEventsToolName         = "memory_deepsearch_topic_events"
)

// QueryService extends Service with the complete DeepSearch query surface.
type QueryService interface {
	Service
	// EdgesByTag traverses index edges by tag or text query.
	EdgesByTag(ctx context.Context, req EdgesByTagRequest) (*EdgesByTagResult, error)
	// QueryConversationTime queries memories within a time range.
	QueryConversationTime(ctx context.Context, req QueryConversationTimeRequest) (*QueryResult, error)
	// QueryEventKeywords queries events by keywords and time range.
	QueryEventKeywords(ctx context.Context, req QueryEventKeywordsRequest) (*QueryResult, error)
	// QueryEventContext loads memories related to matched content.
	QueryEventContext(ctx context.Context, req QueryEventContextRequest) (*QueryResult, error)
	// QueryPersonalInformation queries stable personal information.
	QueryPersonalInformation(ctx context.Context, req QueryPersonalInformationRequest) (*QueryResult, error)
	// QueryPersonalAspect queries memories for one personal aspect.
	QueryPersonalAspect(ctx context.Context, req QueryPersonalAspectRequest) (*QueryResult, error)
	// QueryTopicEvents queries events by topic and time range.
	QueryTopicEvents(ctx context.Context, req QueryTopicEventsRequest) (*QueryResult, error)
}

// EdgesByTagRequest describes an index-edge traversal request.
type EdgesByTagRequest struct {
	UserKey        memory.UserKey `json:"user_key"`
	Tags           []string       `json:"tags,omitempty"`
	Query          string         `json:"query,omitempty"`
	MaxResults     int            `json:"max_results,omitempty"`
	IncludeContent bool           `json:"include_content,omitempty"`
}

// EdgesByTagResult contains paths returned by tag traversal.
type EdgesByTagResult struct {
	Query string `json:"query,omitempty"`
	Tags  []Tag  `json:"tags"`
	Paths []Path `json:"paths"`
}

// QueryConversationTimeRequest describes a time-range query.
type QueryConversationTimeRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryEventKeywordsRequest describes a keyword event query.
type QueryEventKeywordsRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	Keywords   []string       `json:"keywords,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryEventContextRequest describes a related-memory context query.
type QueryEventContextRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	ContentIDs []string       `json:"content_ids,omitempty"`
	Refs       []ContentRef   `json:"refs,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryPersonalInformationRequest describes a personal-information query.
type QueryPersonalInformationRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	Aspects    []string       `json:"aspects,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryPersonalAspectRequest describes a personal-aspect query.
type QueryPersonalAspectRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Aspect     string         `json:"aspect"`
	Query      string         `json:"query,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryTopicEventsRequest describes a topic event query.
type QueryTopicEventsRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Topic      string         `json:"topic"`
	Query      string         `json:"query,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryResult contains content returned by an extended query.
type QueryResult struct {
	Query    string    `json:"query,omitempty"`
	Contents []Content `json:"contents"`
}
