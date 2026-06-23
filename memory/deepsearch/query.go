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

// DeepSearch 扩展查询工具名。
const (
	EdgesByTagToolName          = "memory_deepsearch_edges_by_tag"
	ConversationTimeToolName    = "memory_deepsearch_conversation_time"
	EventKeywordsToolName       = "memory_deepsearch_event_keywords"
	EventContextToolName        = "memory_deepsearch_event_context"
	PersonalInformationToolName = "memory_deepsearch_personal_information"
	PersonalAspectToolName      = "memory_deepsearch_personal_aspect"
	TopicEventsToolName         = "memory_deepsearch_topic_events"
)

// QueryService 扩展 Service，提供完整的 DeepSearch 查询能力。
type QueryService interface {
	Service
	// EdgesByTag 按 tag 或文本查询遍历索引边。
	EdgesByTag(ctx context.Context, req EdgesByTagRequest) (*EdgesByTagResult, error)
	// QueryConversationTime 按时间范围查询记忆。
	QueryConversationTime(ctx context.Context, req QueryConversationTimeRequest) (*QueryResult, error)
	// QueryEventKeywords 按关键词和时间范围查询事件。
	QueryEventKeywords(ctx context.Context, req QueryEventKeywordsRequest) (*QueryResult, error)
	// QueryEventContext 加载命中 content 附近的相关记忆。
	QueryEventContext(ctx context.Context, req QueryEventContextRequest) (*QueryResult, error)
	// QueryPersonalInformation 查询稳定的个人信息。
	QueryPersonalInformation(ctx context.Context, req QueryPersonalInformationRequest) (*QueryResult, error)
	// QueryPersonalAspect 按个人信息方面查询记忆。
	QueryPersonalAspect(ctx context.Context, req QueryPersonalAspectRequest) (*QueryResult, error)
	// QueryTopicEvents 按主题和时间范围查询事件。
	QueryTopicEvents(ctx context.Context, req QueryTopicEventsRequest) (*QueryResult, error)
}

// EdgesByTagRequest 描述按 tag 遍历索引边的请求。
type EdgesByTagRequest struct {
	UserKey        memory.UserKey `json:"user_key"`
	Tags           []string       `json:"tags,omitempty"`
	Query          string         `json:"query,omitempty"`
	MaxResults     int            `json:"max_results,omitempty"`
	IncludeContent bool           `json:"include_content,omitempty"`
}

// EdgesByTagResult 保存按 tag 遍历出的路径。
type EdgesByTagResult struct {
	Query string `json:"query,omitempty"`
	Tags  []Tag  `json:"tags"`
	Paths []Path `json:"paths"`
}

// QueryConversationTimeRequest 描述时间范围查询。
type QueryConversationTimeRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryEventKeywordsRequest 描述关键词事件查询。
type QueryEventKeywordsRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	Keywords   []string       `json:"keywords,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryEventContextRequest 描述相关记忆上下文查询。
type QueryEventContextRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	ContentIDs []string       `json:"content_ids,omitempty"`
	Refs       []ContentRef   `json:"refs,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryPersonalInformationRequest 描述个人信息查询。
type QueryPersonalInformationRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query,omitempty"`
	Aspects    []string       `json:"aspects,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryPersonalAspectRequest 描述个人信息方面查询。
type QueryPersonalAspectRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Aspect     string         `json:"aspect"`
	Query      string         `json:"query,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryTopicEventsRequest 描述主题事件查询。
type QueryTopicEventsRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Topic      string         `json:"topic"`
	Query      string         `json:"query,omitempty"`
	TimeAfter  time.Time      `json:"time_after,omitempty"`
	TimeBefore time.Time      `json:"time_before,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// QueryResult 保存扩展查询返回的 content。
type QueryResult struct {
	Query    string    `json:"query,omitempty"`
	Contents []Content `json:"contents"`
}
