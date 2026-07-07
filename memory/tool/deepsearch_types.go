//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// CueSearchRequest is the input for memory_deepsearch_cue_search.
type CueSearchRequest struct {
	Query      string  `json:"query" description:"Cue search query"`
	MaxResults int     `json:"max_results,omitempty" description:"Maximum cue results"`
	MinScore   float64 `json:"min_score,omitempty" description:"Minimum cue score"`
}

// CueSearchResponse is the output from memory_deepsearch_cue_search.
type CueSearchResponse struct {
	Query string           `json:"query"`
	Cues  []deepsearch.Cue `json:"cues"`
	Count int              `json:"count"`
}

// TagExpandRequest is the input for memory_deepsearch_tag_expand.
type TagExpandRequest struct {
	CueIDs         []string `json:"cue_ids,omitempty" description:"Cue IDs returned by cue search"`
	Cues           []string `json:"cues,omitempty" description:"Cue texts to expand"`
	MaxTagsPerCue  int      `json:"max_tags_per_cue,omitempty" description:"Maximum tags per cue"`
	MaxContents    int      `json:"max_contents,omitempty" description:"Maximum content paths"`
	MinPathScore   float64  `json:"min_path_score,omitempty" description:"Minimum path score"`
	IncludeContent bool     `json:"include_content,omitempty" description:"Whether to include memory content in paths"`
}

// TagExpandResponse is the output from memory_deepsearch_tag_expand.
type TagExpandResponse struct {
	Tags  []deepsearch.Tag  `json:"tags"`
	Paths []deepsearch.Path `json:"paths"`
	Count int               `json:"count"`
}

// ContentLoadRequest is the input for memory_deepsearch_content_load.
type ContentLoadRequest struct {
	ContentIDs []string                `json:"content_ids,omitempty" description:"Content IDs to load"`
	Refs       []deepsearch.ContentRef `json:"refs,omitempty" description:"Original memory entry references to load"`
	MaxResults int                     `json:"max_results,omitempty" description:"Maximum content results"`
}

// ContentLoadResponse is the output from memory_deepsearch_content_load.
type ContentLoadResponse struct {
	Contents []deepsearch.Content `json:"contents"`
	Count    int                  `json:"count"`
}

func cueSearchInputSchema() *agenttool.Schema {
	return objectSchema(map[string]*agenttool.Schema{
		"query":       stringSchema("Cue search query."),
		"max_results": integerSchema("Maximum cue results."),
		"min_score":   numberSchema("Minimum cue score."),
	}, "query")
}

func tagExpandInputSchema() *agenttool.Schema {
	return objectSchema(map[string]*agenttool.Schema{
		"cue_ids":          stringArraySchema("Cue IDs returned by cue search."),
		"cues":             stringArraySchema("Cue texts to expand."),
		"max_tags_per_cue": integerSchema("Maximum tags per cue."),
		"max_contents":     integerSchema("Maximum content paths."),
		"min_path_score":   numberSchema("Minimum path score."),
		"include_content":  boolSchema("Whether to include memory content in paths."),
	})
}

func contentLoadInputSchema() *agenttool.Schema {
	return objectSchema(map[string]*agenttool.Schema{
		"content_ids": stringArraySchema("Content IDs to load."),
		"refs":        refArraySchema("Original memory entry references to load."),
		"max_results": integerSchema("Maximum content results."),
	})
}

func numberSchema(description string) *agenttool.Schema {
	return &agenttool.Schema{
		Type:        "number",
		Description: description,
	}
}

func refArraySchema(description string) *agenttool.Schema {
	return &agenttool.Schema{
		Type:        "array",
		Description: description,
		Items: objectSchema(map[string]*agenttool.Schema{
			"kind":      stringSchema("Reference kind."),
			"app_name":  stringSchema("App name."),
			"user_id":   stringSchema("User ID."),
			"source_id": stringSchema("Memory entry ID."),
		}),
	}
}
