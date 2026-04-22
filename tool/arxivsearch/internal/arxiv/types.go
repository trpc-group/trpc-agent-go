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
	"net/http"
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
	Query      string        `json:"query" jsonschema:"description=The search query string for arXiv articles"`
	IDList     []string      `json:"id_list,omitempty" jsonschema:"description=List of arXiv IDs to search for"`
	MaxResults *int          `json:"max_results,omitempty" jsonschema:"description=Maximum number of results to return"`
	SortBy     SortCriterion `json:"sort_by,omitempty" jsonschema:"description=Sort criterion: relevance or lastUpdatedDate or submittedDate,enum=relevance,enum=lastUpdatedDate,enum=submittedDate"`
	SortOrder  SortOrder     `json:"sort_order,omitempty" jsonschema:"description=Sort order: ascending or descending,enum=ascending,enum=descending"`
}

// ClientConfig contains the configuration for the arXiv client
type ClientConfig struct {
	BaseURL      string        `json:"base_url"`
	PageSize     int           `json:"page_size"`
	DelaySeconds time.Duration `json:"delay_seconds"`
	NumRetries   int           `json:"num_retries"`
	// HTTPClient is the optional underlying HTTP client. If nil, a default
	// client with a 30s timeout is used. The caller's *http.Client is never
	// mutated; a shallow copy is taken when Timeout overrides are applied.
	HTTPClient *http.Client `json:"-"`
	// Timeout, when non-nil, overrides the HTTP request timeout on the
	// underlying HTTPClient. A nil pointer means "not set" (use the
	// HTTPClient's own Timeout). A non-nil zero value explicitly disables
	// the timeout (Go http.Client treats Timeout==0 as "no deadline").
	Timeout *time.Duration `json:"-"`
}

// DefaultConfig returns the default configuration for the arXiv client
func DefaultConfig() ClientConfig {
	return ClientConfig{
		PageSize:     100,
		DelaySeconds: 3 * time.Second,
		NumRetries:   3,
	}
}
