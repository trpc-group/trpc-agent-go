//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "trpc.group/trpc-go/trpc-agent-go/memory"

// CueSearchRequest represents the input for memory_cue_search.
type CueSearchRequest struct {
	Query      string  `json:"query" description:"Question or keywords used to find active memory cues"`
	MaxResults int     `json:"max_results,omitempty" description:"Maximum number of cues to return"`
	MinScore   float64 `json:"min_score,omitempty" description:"Minimum cue score"`
}

// CueSearchResponse represents the response from memory_cue_search.
type CueSearchResponse struct {
	Query string       `json:"query"`
	Cues  []memory.Cue `json:"cues"`
	Count int          `json:"count"`
}

// TagExpandRequest represents the input for memory_tag_expand.
type TagExpandRequest struct {
	CueIDs         []string `json:"cue_ids,omitempty" description:"Cue IDs returned by memory_cue_search"`
	Cues           []string `json:"cues,omitempty" description:"Cue texts to expand when IDs are unavailable"`
	MaxTagsPerCue  int      `json:"max_tags_per_cue,omitempty" description:"Maximum tags to expand per cue"`
	MaxContents    int      `json:"max_contents,omitempty" description:"Maximum content paths to return"`
	MinPathScore   float64  `json:"min_path_score,omitempty" description:"Minimum path score"`
	IncludeContent bool     `json:"include_content,omitempty" description:"Whether to include content nodes in returned paths"`
}

// TagExpandResponse represents the response from memory_tag_expand.
type TagExpandResponse struct {
	Tags  []memory.Tag  `json:"tags"`
	Paths []memory.Path `json:"paths"`
	Count int           `json:"count"`
}

// ContentLoadRequest represents the input for memory_content_load.
type ContentLoadRequest struct {
	ContentIDs []string            `json:"content_ids,omitempty" description:"Content node IDs to load"`
	Refs       []memory.ContentRef `json:"refs,omitempty" description:"Typed content references to load"`
	MaxResults int                 `json:"max_results,omitempty" description:"Maximum content nodes to return"`
}

// ContentLoadResponse represents the response from memory_content_load.
type ContentLoadResponse struct {
	Contents []memory.Content `json:"contents"`
	Count    int              `json:"count"`
}
