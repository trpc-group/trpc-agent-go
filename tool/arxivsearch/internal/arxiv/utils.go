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
	"fmt"
	"regexp"
	"strings"
)

// GetShortID get short id from entry id
func (r *Result) GetShortID() string {
	if strings.Contains(r.EntryID, "arxiv.org/abs/") {
		return strings.Split(r.EntryID, "arxiv.org/abs/")[1]
	}
	return r.EntryID
}

// GetDefaultFilename get default filename from result
func (r *Result) GetDefaultFilename(extension string) string {
	if extension == "" {
		extension = "pdf"
	}

	shortID := r.GetShortID()
	shortID = strings.ReplaceAll(shortID, "/", "_")

	title := r.Title
	if title == "" {
		title = "UNTITLED"
	}

	re := regexp.MustCompile(`[^\w]`)
	cleanTitle := re.ReplaceAllString(title, "_")

	return fmt.Sprintf("%s.%s.%s", shortID, cleanTitle, extension)
}

// GetSourceURL get source url from result
func (r *Result) GetSourceURL() string {
	if r.PdfURL == "" {
		return ""
	}
	return strings.Replace(r.PdfURL, "/pdf/", "/src/", 1)
}

// ArxivError arxiv api error
type ArxivError struct {
	URL     string
	Retry   int
	Message string
}

// Error error
func (e *ArxivError) Error() string {
	return fmt.Sprintf("%s (%s)", e.Message, e.URL)
}

// UnexpectedEmptyPageError unexpected empty page error
type UnexpectedEmptyPageError struct {
	ArxivError
}

// HTTPError http error
type HTTPError struct {
	ArxivError
	Status int
}

// NewSearch create new search instance
func NewSearch(query string, options ...SearchOption) Search {
	search := Search{
		Query:     query,
		IDList:    []string{},
		SortBy:    SortCriterionRelevance,
		SortOrder: SortOrderDescending,
	}

	for _, option := range options {
		option(&search)
	}

	return search
}

// SearchOption search option
type SearchOption func(*Search)

// WithIDList set id list
func WithIDList(ids ...string) SearchOption {
	return func(s *Search) {
		s.IDList = ids
	}
}

// WithMaxResults set max results
func WithMaxResults(max int) SearchOption {
	return func(s *Search) {
		s.MaxResults = &max
	}
}

// WithSortBy set sort by
func WithSortBy(sortBy SortCriterion) SearchOption {
	return func(s *Search) {
		s.SortBy = sortBy
	}
}

// WithSortOrder set sort order
func WithSortOrder(sortOrder SortOrder) SearchOption {
	return func(s *Search) {
		s.SortOrder = sortOrder
	}
}

// DefaultClient return default client
func DefaultClient() *Client {
	return NewClient(DefaultConfig())
}
