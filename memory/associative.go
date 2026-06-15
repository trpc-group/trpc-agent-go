//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"context"
	"time"
)

// Associative tool names for cue-tag-content memory traversal.
const (
	// CueSearchToolName is the tool name for cue search.
	CueSearchToolName = "memory_cue_search"
	// TagExpandToolName is the tool name for tag expansion.
	TagExpandToolName = "memory_tag_expand"
	// ContentLoadToolName is the tool name for content loading.
	ContentLoadToolName = "memory_content_load"
)

// AssociativeService defines optional cue-tag-content memory operations.
// It is intentionally separate from Service so existing memory backends and
// user implementations remain source-compatible.
type AssociativeService interface {
	// IndexAssociations writes cue-tag-content associations for a user.
	// Implementations should upsert by ContentRef and replace stale edges for
	// the same content.
	IndexAssociations(ctx context.Context, req IndexAssociationsRequest) error
	// SearchCues searches cue nodes for a user.
	SearchCues(ctx context.Context, req CueSearchRequest) (*CueSearchResult, error)
	// ExpandTags expands cue nodes into tag/content paths.
	ExpandTags(ctx context.Context, req TagExpandRequest) (*TagExpandResult, error)
	// LoadContents loads content nodes by id or content reference.
	LoadContents(ctx context.Context, req ContentLoadRequest) (*ContentLoadResult, error)
	// DeleteAssociations deletes cue-tag-content associations for a user.
	DeleteAssociations(ctx context.Context, req DeleteAssociationsRequest) error
}

// ContentRefKind identifies what a content node points to.
type ContentRefKind string

const (
	// RefKindSessionEvent points content to a session event.
	RefKindSessionEvent ContentRefKind = "session_event"
	// RefKindMemoryEntry points content to a memory entry.
	RefKindMemoryEntry ContentRefKind = "memory_entry"
)

// ContentRef identifies the source object behind an associative content node.
type ContentRef struct {
	Kind      ContentRefKind `json:"kind"`
	AppName   string         `json:"app_name,omitempty"`
	UserID    string         `json:"user_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	SourceID  string         `json:"source_id,omitempty"`
}

// AssociationMetadata stores optional metadata for associative indexing.
type AssociationMetadata struct {
	QuestionType string    `json:"question_type,omitempty"`
	CaseID       string    `json:"case_id,omitempty"`
	SessionDate  string    `json:"session_date,omitempty"`
	EventTime    time.Time `json:"event_time,omitempty"`
	Topics       []string  `json:"topics,omitempty"`
	Participants []string  `json:"participants,omitempty"`
	Location     string    `json:"location,omitempty"`
	Kind         Kind      `json:"kind,omitempty"`
}

// AssociationDocument is the normalized input used to build associations.
type AssociationDocument struct {
	ID       string              `json:"id,omitempty"`
	Text     string              `json:"text"`
	Cues     []string            `json:"cues,omitempty"`
	Tags     []string            `json:"tags,omitempty"`
	Ref      ContentRef          `json:"ref"`
	Metadata AssociationMetadata `json:"metadata,omitempty"`
	Created  time.Time           `json:"created,omitempty"`
}

// IndexAssociationsRequest describes associative indexing work for one user.
type IndexAssociationsRequest struct {
	UserKey   UserKey               `json:"user_key"`
	Documents []AssociationDocument `json:"documents"`
	Replace   bool                  `json:"replace,omitempty"`
}

// CueSearchRequest describes a cue search.
type CueSearchRequest struct {
	UserKey    UserKey `json:"user_key"`
	Query      string  `json:"query"`
	MaxResults int     `json:"max_results,omitempty"`
	MinScore   float64 `json:"min_score,omitempty"`
}

// CueSearchResult stores cue search output.
type CueSearchResult struct {
	Query string `json:"query"`
	Cues  []Cue  `json:"cues"`
}

// Cue represents a cue node.
type Cue struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

// TagExpandRequest describes cue-to-tag expansion.
type TagExpandRequest struct {
	UserKey        UserKey  `json:"user_key"`
	CueIDs         []string `json:"cue_ids,omitempty"`
	Cues           []string `json:"cues,omitempty"`
	MaxTagsPerCue  int      `json:"max_tags_per_cue,omitempty"`
	MaxContents    int      `json:"max_contents,omitempty"`
	MinPathScore   float64  `json:"min_path_score,omitempty"`
	IncludeContent bool     `json:"include_content,omitempty"`
}

// TagExpandResult stores expanded tags and traversal paths.
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

// Path represents one cue-tag-content traversal path.
type Path struct {
	Cue     Cue      `json:"cue"`
	Tag     Tag      `json:"tag"`
	Content *Content `json:"content,omitempty"`
	Score   float64  `json:"score,omitempty"`
}

// ContentLoadRequest describes content loading by ids or source references.
type ContentLoadRequest struct {
	UserKey    UserKey      `json:"user_key"`
	ContentIDs []string     `json:"content_ids,omitempty"`
	Refs       []ContentRef `json:"refs,omitempty"`
	MaxResults int          `json:"max_results,omitempty"`
}

// ContentLoadResult stores loaded content nodes.
type ContentLoadResult struct {
	Contents []Content `json:"contents"`
}

// Content represents an indexed content node.
type Content struct {
	ID       string              `json:"id"`
	Text     string              `json:"text"`
	Ref      ContentRef          `json:"ref"`
	Metadata AssociationMetadata `json:"metadata,omitempty"`
	Score    float64             `json:"score,omitempty"`
	Created  time.Time           `json:"created,omitempty"`
	Updated  time.Time           `json:"updated,omitempty"`
}

// DeleteAssociationsRequest describes association deletion.
type DeleteAssociationsRequest struct {
	UserKey    UserKey      `json:"user_key"`
	ContentIDs []string     `json:"content_ids,omitempty"`
	Refs       []ContentRef `json:"refs,omitempty"`
	ClearAll   bool         `json:"clear_all,omitempty"`
}
