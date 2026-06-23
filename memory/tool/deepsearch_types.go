//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"

// CueSearchRequest represents the input for memory_cue_search.
type CueSearchRequest struct {
	Query      string  `json:"query" description:"Question or keywords used to find active memory cues"`
	MaxResults int     `json:"max_results,omitempty" description:"Maximum number of cues to return"`
	MinScore   float64 `json:"min_score,omitempty" description:"Minimum cue score"`
}

// CueSearchResponse represents the response from memory_cue_search.
type CueSearchResponse struct {
	Query string           `json:"query"`
	Cues  []deepsearch.Cue `json:"cues"`
	Count int              `json:"count"`
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
	Tags  []deepsearch.Tag  `json:"tags"`
	Paths []deepsearch.Path `json:"paths"`
	Count int               `json:"count"`
}

// ContentLoadRequest represents the input for memory_content_load.
type ContentLoadRequest struct {
	ContentIDs []string                `json:"content_ids,omitempty" description:"Content node IDs to load"`
	Refs       []deepsearch.ContentRef `json:"refs,omitempty" description:"Typed content references to load"`
	MaxResults int                     `json:"max_results,omitempty" description:"Maximum content nodes to return"`
}

// ContentLoadResponse represents the response from memory_content_load.
type ContentLoadResponse struct {
	Contents []deepsearch.Content `json:"contents"`
	Count    int                  `json:"count"`
}

// EdgesByTagRequest represents the input for memory_cue_tag_edges_by_tag.
type EdgesByTagRequest struct {
	Tags           []string `json:"tags,omitempty" description:"Tag names or relation labels to traverse"`
	Query          string   `json:"query,omitempty" description:"Optional question or keywords used to rank tag edges"`
	MaxResults     int      `json:"max_results,omitempty" description:"Maximum number of paths to return"`
	IncludeContent bool     `json:"include_content,omitempty" description:"Whether to include content nodes in returned paths"`
}

// EdgesByTagResponse represents the response from memory_cue_tag_edges_by_tag.
type EdgesByTagResponse struct {
	Query string            `json:"query,omitempty"`
	Tags  []deepsearch.Tag  `json:"tags"`
	Paths []deepsearch.Path `json:"paths"`
	Count int               `json:"count"`
}

// QueryConversationTimeRequest represents the input for memory_cue_tag_conversation_time.
type QueryConversationTimeRequest struct {
	Query      string `json:"query,omitempty" description:"Optional question or keywords to rank events in the time window"`
	TimeAfter  string `json:"time_after,omitempty" description:"Start time or date in RFC3339 or YYYY-MM-DD format"`
	TimeBefore string `json:"time_before,omitempty" description:"End time or date in RFC3339 or YYYY-MM-DD format"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of events to return"`
}

// QueryEventKeywordsRequest represents the input for memory_cue_tag_event_keywords.
type QueryEventKeywordsRequest struct {
	Query      string   `json:"query,omitempty" description:"Question or keyword query used to retrieve events"`
	Keywords   []string `json:"keywords,omitempty" description:"Additional exact keywords, entities, or phrases"`
	TimeAfter  string   `json:"time_after,omitempty" description:"Optional start time or date in RFC3339 or YYYY-MM-DD format"`
	TimeBefore string   `json:"time_before,omitempty" description:"Optional end time or date in RFC3339 or YYYY-MM-DD format"`
	MaxResults int      `json:"max_results,omitempty" description:"Maximum number of events to return"`
}

// QueryEventContextRequest represents the input for memory_cue_tag_event_context.
type QueryEventContextRequest struct {
	Query      string                  `json:"query,omitempty" description:"Optional question or keywords used to rank related memories"`
	ContentIDs []string                `json:"content_ids,omitempty" description:"Content node IDs to anchor related memory lookup"`
	Refs       []deepsearch.ContentRef `json:"refs,omitempty" description:"Memory entry references to anchor related memory lookup"`
	MaxResults int                     `json:"max_results,omitempty" description:"Maximum number of context items to return"`
}

// QueryPersonalInformationRequest represents the input for memory_cue_tag_personal_information.
type QueryPersonalInformationRequest struct {
	Query      string   `json:"query,omitempty" description:"Question or keywords for stable personal facts"`
	Aspects    []string `json:"aspects,omitempty" description:"Personal aspects such as preference, profile, family, work, health, travel, or education"`
	MaxResults int      `json:"max_results,omitempty" description:"Maximum number of facts to return"`
}

// QueryPersonalAspectRequest represents the input for memory_cue_tag_personal_aspect.
type QueryPersonalAspectRequest struct {
	Aspect     string `json:"aspect" description:"Personal aspect to inspect, such as preference, family, work, health, travel, education, or routine"`
	Query      string `json:"query,omitempty" description:"Optional question or keywords used to rank results"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of memories to return"`
}

// QueryTopicEventsRequest represents the input for memory_cue_tag_topic_events.
type QueryTopicEventsRequest struct {
	Topic      string `json:"topic" description:"Topic, entity, or relation label whose events should be retrieved"`
	Query      string `json:"query,omitempty" description:"Optional question or keywords used to rank topic events"`
	TimeAfter  string `json:"time_after,omitempty" description:"Optional start time or date in RFC3339 or YYYY-MM-DD format"`
	TimeBefore string `json:"time_before,omitempty" description:"Optional end time or date in RFC3339 or YYYY-MM-DD format"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of events to return"`
}

// DeepSearchQueryResponse represents a content-list DeepSearch response.
type DeepSearchQueryResponse struct {
	Query    string               `json:"query,omitempty"`
	Contents []deepsearch.Content `json:"contents"`
	Count    int                  `json:"count"`
}
