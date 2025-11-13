//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package arxiv

import (
	"time"
)

// SortCriterion defines the sort criterion
type SortCriterion string

const (
	// SortCriterionRelevance sorts by relevance
	SortCriterionRelevance SortCriterion = "relevance"
	// SortCriterionLastUpdatedDate sorts by last updated date
	SortCriterionLastUpdatedDate SortCriterion = "lastUpdatedDate"
	// SortCriterionSubmittedDate sorts by submitted date
	SortCriterionSubmittedDate SortCriterion = "submittedDate"
)

// SortOrder defines the sort order
type SortOrder string

const (
	// SortOrderAscending sorts in ascending order
	SortOrderAscending SortOrder = "ascending"
	// SortOrderDescending sorts in descending order
	SortOrderDescending SortOrder = "descending"
)

// Author represents an author of a paper
type Author struct {
	Name string `json:"name"`
}

// Link represents a link to a paper
type Link struct {
	Href        string `json:"href"`
	Title       string `json:"title,omitempty"`
	Rel         string `json:"rel,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// Result represents a search result
type Result struct {
	EntryID         string    `json:"entry_id"`
	Updated         time.Time `json:"updated"`
	Published       time.Time `json:"published"`
	Title           string    `json:"title"`
	Authors         []Author  `json:"authors"`
	Summary         string    `json:"summary"`
	Comment         string    `json:"comment,omitempty"`
	JournalRef      string    `json:"journal_ref,omitempty"`
	DOI             string    `json:"doi,omitempty"`
	PrimaryCategory string    `json:"primary_category"`
	Categories      []string  `json:"categories"`
	Links           []Link    `json:"links"`
	PdfURL          string    `json:"pdf_url,omitempty"`
}

// Search defines the search parameters
type Search struct {
	Query      string        `json:"query" jsonschema:"title=query,description=query,type=string"`
	IDList     []string      `json:"id_list" jsonschema:"title=id_list,description=id_list,type=array,items=string"`
	MaxResults *int          `json:"max_results,omitempty" jsonschema:"title=max_results,description=max_results,type=integer"`
	SortBy     SortCriterion `json:"sort_by" jsonschema:"title=sort_by,description=sort_by,type=string,enum=[\"relevance\",\"lastUpdatedDate\",\"submittedDate\"]"`
	SortOrder  SortOrder     `json:"sort_order" jsonschema:"title=sort_order,description=sort_order,type=string,enum=[\"ascending\",\"descending\"]"`
}

// ClientConfig contains the configuration for the arXiv client
type ClientConfig struct {
	BaseURL      string        `json:"base_url"`
	PageSize     int           `json:"page_size"`
	DelaySeconds time.Duration `json:"delay_seconds"`
	NumRetries   int           `json:"num_retries"`
}

// DefaultConfig returns the default configuration for the arXiv client
func DefaultConfig() ClientConfig {
	return ClientConfig{
		PageSize:     100,
		DelaySeconds: 3 * time.Second,
		NumRetries:   3,
	}
}
