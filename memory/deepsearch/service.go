//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package deepsearch 提供基于 cue/tag 的长期记忆深度检索能力。
package deepsearch

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// DeepSearch 工具名。
const (
	CueSearchToolName   = "memory_deepsearch_cue_search"   // cue 检索工具。
	TagExpandToolName   = "memory_deepsearch_tag_expand"   // tag 扩展工具。
	ContentLoadToolName = "memory_deepsearch_content_load" // content 加载工具。
)

// Service 定义可选的 DeepSearch 索引和查询能力。
type Service interface {
	// EnsureIndex 确保指定用户的索引与当前 memory entries 一致。
	EnsureIndex(ctx context.Context, userKey memory.UserKey) error
	// IndexDocuments 写入 DeepSearch 文档。
	IndexDocuments(ctx context.Context, req IndexRequest) error
	// SearchCues 检索 cue 节点。
	SearchCues(ctx context.Context, req CueSearchRequest) (*CueSearchResult, error)
	// ExpandTags 将 cue 扩展为 tag/content 路径。
	ExpandTags(ctx context.Context, req TagExpandRequest) (*TagExpandResult, error)
	// LoadContents 按 ID 或引用加载 content。
	LoadContents(ctx context.Context, req ContentLoadRequest) (*ContentLoadResult, error)
	// DeleteDocuments 删除 DeepSearch 索引。
	DeleteDocuments(ctx context.Context, req DeleteRequest) error
}

// ContentRefKind 标识 content 指向的源对象类型。
type ContentRefKind string

const (
	// RefKindMemoryEntry 表示 content 指向 memory entry。
	RefKindMemoryEntry ContentRefKind = "memory_entry"
)

// ContentRef 标识 DeepSearch content 对应的 memory entry。
type ContentRef struct {
	Kind     ContentRefKind `json:"kind"`
	AppName  string         `json:"app_name,omitempty"`
	UserID   string         `json:"user_id,omitempty"`
	SourceID string         `json:"source_id,omitempty"`
}

// Metadata 保存索引使用的 memory 元数据。
type Metadata struct {
	SourceFingerprint string      `json:"source_fingerprint,omitempty"`
	EventTime         time.Time   `json:"event_time,omitempty"`
	Topics            []string    `json:"topics,omitempty"`
	Participants      []string    `json:"participants,omitempty"`
	Location          string      `json:"location,omitempty"`
	Kind              memory.Kind `json:"kind,omitempty"`
}

// Document 表示一条由 memory entry 生成的 DeepSearch 文档。
type Document struct {
	ID       string     `json:"id,omitempty"`
	Text     string     `json:"text"`
	Cues     []string   `json:"cues"`
	Tags     []string   `json:"tags"`
	Ref      ContentRef `json:"ref"`
	Metadata Metadata   `json:"metadata,omitempty"`
	Created  time.Time  `json:"created,omitempty"`
}

// IndexRequest 描述一个用户的索引写入请求。
type IndexRequest struct {
	UserKey   memory.UserKey `json:"user_key"`
	Documents []Document     `json:"documents"`
	Replace   bool           `json:"replace,omitempty"`
}

// CueSearchRequest 描述 cue 检索请求。
type CueSearchRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query"`
	MaxResults int            `json:"max_results,omitempty"`
	MinScore   float64        `json:"min_score,omitempty"`
}

// CueSearchResult 保存 cue 检索结果。
type CueSearchResult struct {
	Query string `json:"query"`
	Cues  []Cue  `json:"cues"`
}

// Cue 表示一个检索线索节点。
type Cue struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

// TagExpandRequest 描述 cue 到 tag/content 的扩展请求。
type TagExpandRequest struct {
	UserKey        memory.UserKey `json:"user_key"`
	CueIDs         []string       `json:"cue_ids,omitempty"`
	Cues           []string       `json:"cues,omitempty"`
	MaxTagsPerCue  int            `json:"max_tags_per_cue,omitempty"`
	MaxContents    int            `json:"max_contents,omitempty"`
	MinPathScore   float64        `json:"min_path_score,omitempty"`
	IncludeContent bool           `json:"include_content,omitempty"`
}

// TagExpandResult 保存 tag 和遍历路径。
type TagExpandResult struct {
	Tags  []Tag  `json:"tags"`
	Paths []Path `json:"paths"`
}

// Tag 表示 cue 与 content 之间的关系。
type Tag struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	CueID     string  `json:"cue_id,omitempty"`
	ContentID string  `json:"content_id,omitempty"`
	Weight    float64 `json:"weight,omitempty"`
}

// Path 表示一条 cue-tag-content 路径。
type Path struct {
	Cue     Cue      `json:"cue"`
	Tag     Tag      `json:"tag"`
	Content *Content `json:"content,omitempty"`
	Score   float64  `json:"score,omitempty"`
}

// ContentLoadRequest 描述 content 加载请求。
type ContentLoadRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	ContentIDs []string       `json:"content_ids,omitempty"`
	Refs       []ContentRef   `json:"refs,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// ContentLoadResult 保存加载出的 content。
type ContentLoadResult struct {
	Contents []Content `json:"contents"`
}

// Content 表示索引中的权威 memory entry 引用。
type Content struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Ref      ContentRef `json:"ref"`
	Metadata Metadata   `json:"metadata,omitempty"`
	Score    float64    `json:"score,omitempty"`
	Created  time.Time  `json:"created,omitempty"`
	Updated  time.Time  `json:"updated,omitempty"`
}

// DeleteRequest 描述索引删除请求。
type DeleteRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	ContentIDs []string       `json:"content_ids,omitempty"`
	Refs       []ContentRef   `json:"refs,omitempty"`
	ClearAll   bool           `json:"clear_all,omitempty"`
}
