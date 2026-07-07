//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package deepsearch provides row-attached cue/tag indexes for memory search.
package deepsearch

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	// IndexVersion is the current row-attached DeepSearch index format.
	IndexVersion = 1

	// ToolSetName is the default activatable tool set name for DeepSearch tools.
	ToolSetName = "memory_deepsearch"

	// CueSearchToolName searches cue nodes.
	CueSearchToolName = "memory_deepsearch_cue_search"
	// TagExpandToolName expands cues into tag/content paths.
	TagExpandToolName = "memory_deepsearch_tag_expand"
	// ContentLoadToolName loads indexed memory content.
	ContentLoadToolName = "memory_deepsearch_content_load"
)

// Service defines the optional DeepSearch indexing and query capability.
type Service interface {
	// EnsureIndex ensures that a user's row-attached index matches current
	// memory entries.
	EnsureIndex(ctx context.Context, userKey memory.UserKey) error
	// SearchCues searches cue nodes.
	SearchCues(ctx context.Context, req CueSearchRequest) (*CueSearchResult, error)
	// ExpandTags expands cues into tag and content paths.
	ExpandTags(ctx context.Context, req TagExpandRequest) (*TagExpandResult, error)
	// LoadContents loads content by ID or source reference.
	LoadContents(ctx context.Context, req ContentLoadRequest) (*ContentLoadResult, error)
}

// ContentRefKind identifies the source object referenced by content.
type ContentRefKind string

const (
	// RefKindMemoryEntry indicates that content references a memory entry.
	RefKindMemoryEntry ContentRefKind = "memory_entry"
)

// ContentRef identifies the memory entry represented by DeepSearch content.
type ContentRef struct {
	Kind     ContentRefKind `json:"kind"`
	AppName  string         `json:"app_name,omitempty"`
	UserID   string         `json:"user_id,omitempty"`
	SourceID string         `json:"source_id,omitempty"`
}

// Metadata contains memory metadata used by the index.
type Metadata struct {
	SourceFingerprint string      `json:"source_fingerprint,omitempty"`
	EventTime         time.Time   `json:"event_time,omitempty"`
	Topics            []string    `json:"topics,omitempty"`
	Participants      []string    `json:"participants,omitempty"`
	Location          string      `json:"location,omitempty"`
	Kind              memory.Kind `json:"kind,omitempty"`
}

// Document represents a DeepSearch document generated from a memory entry.
type Document struct {
	ID       string     `json:"id,omitempty"`
	Text     string     `json:"text"`
	Cues     []string   `json:"cues"`
	Tags     []string   `json:"tags"`
	Ref      ContentRef `json:"ref"`
	Metadata Metadata   `json:"metadata,omitempty"`
	Created  time.Time  `json:"created,omitempty"`
	Updated  time.Time  `json:"updated,omitempty"`
}

// Index stores the hidden row-attached derived DeepSearch index.
type Index struct {
	Version           int       `json:"version"`
	Content           Content   `json:"content"`
	Cues              []string  `json:"cues"`
	Tags              []string  `json:"tags"`
	SourceFingerprint string    `json:"source_fingerprint"`
	IndexedAt         time.Time `json:"indexed_at"`
}

// CueSearchRequest describes a cue search.
type CueSearchRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	Query      string         `json:"query"`
	MaxResults int            `json:"max_results,omitempty"`
	MinScore   float64        `json:"min_score,omitempty"`
}

// CueSearchResult contains cue search results.
type CueSearchResult struct {
	Query string `json:"query"`
	Cues  []Cue  `json:"cues"`
}

// Cue represents a retrieval clue node.
type Cue struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

// TagExpandRequest describes expansion from cues to tag and content paths.
type TagExpandRequest struct {
	UserKey        memory.UserKey `json:"user_key"`
	CueIDs         []string       `json:"cue_ids,omitempty"`
	Cues           []string       `json:"cues,omitempty"`
	MaxTagsPerCue  int            `json:"max_tags_per_cue,omitempty"`
	MaxContents    int            `json:"max_contents,omitempty"`
	MinPathScore   float64        `json:"min_path_score,omitempty"`
	IncludeContent bool           `json:"include_content,omitempty"`
}

// TagExpandResult contains tags and traversal paths.
type TagExpandResult struct {
	Tags  []Tag  `json:"tags"`
	Paths []Path `json:"paths"`
}

// Tag represents a relation between a cue and content.
type Tag struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	CueID     string  `json:"cue_id,omitempty"`
	ContentID string  `json:"content_id,omitempty"`
	Weight    float64 `json:"weight,omitempty"`
}

// Path represents a cue-tag-content path.
type Path struct {
	Cue     Cue      `json:"cue"`
	Tag     Tag      `json:"tag"`
	Content *Content `json:"content,omitempty"`
	Score   float64  `json:"score,omitempty"`
}

// ContentLoadRequest describes a content load request.
type ContentLoadRequest struct {
	UserKey    memory.UserKey `json:"user_key"`
	ContentIDs []string       `json:"content_ids,omitempty"`
	Refs       []ContentRef   `json:"refs,omitempty"`
	MaxResults int            `json:"max_results,omitempty"`
}

// ContentLoadResult contains loaded content.
type ContentLoadResult struct {
	Contents []Content `json:"contents"`
}

// Content represents an indexed reference to an authoritative memory entry.
type Content struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Ref      ContentRef `json:"ref"`
	Metadata Metadata   `json:"metadata,omitempty"`
	Score    float64    `json:"score,omitempty"`
	Created  time.Time  `json:"created,omitempty"`
	Updated  time.Time  `json:"updated,omitempty"`
}
