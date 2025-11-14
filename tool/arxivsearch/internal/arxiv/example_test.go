//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package arxiv_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch/internal/arxiv"
)

func newFakeArxivServer() *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		feed := arxiv.AtomFeed{
			TotalResults: "2",
			Entries: []arxiv.AtomEntry{
				{
					ID:         "http://arxiv.org/abs/2401.12345v1",
					Title:      "Test Paper 1",
					Summary:    "Summary 1",
					Authors:    []arxiv.AtomAuthor{{Name: "Author1"}},
					Categories: []arxiv.AtomCategory{{Term: "cs.AI"}},
					Links: []arxiv.AtomLink{
						{Href: "http://arxiv.org/pdf/2401.12345v1", Rel: "related", Type: "application/pdf"},
					},
				},
				{
					ID:         "http://arxiv.org/abs/2401.12345v2",
					Title:      "Test Paper 2",
					Summary:    "Summary 2",
					Authors:    []arxiv.AtomAuthor{{Name: "Author2"}},
					Categories: []arxiv.AtomCategory{{Term: "cs.LG"}},
					Links: []arxiv.AtomLink{
						{Href: "http://arxiv.org/pdf/2401.12345v2", Rel: "related", Type: "application/pdf"},
					},
				},
				{
					ID:         "http://arxiv.org/abs/2401.12345v3",
					Title:      "Test Paper 3",
					Summary:    "Summary 3",
					Authors:    []arxiv.AtomAuthor{{Name: "Author3"}},
					Categories: []arxiv.AtomCategory{{Term: "cs.LG"}},
					Links: []arxiv.AtomLink{
						{Href: "http://arxiv.org/pdf/2401.12345v3", Rel: "related", Type: "application/pdf"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/atom+xml")
		xml.NewEncoder(w).Encode(feed)
	}))
	return server
}

// Example_basicSearch
func Example_basicSearch() {
	srv := newFakeArxivServer()
	defer srv.Close()
	client := arxiv.DefaultClient()
	client.BaseURL = srv.URL

	search := arxiv.NewSearch(
		"machine learning",
		arxiv.WithMaxResults(3),
		arxiv.WithSortBy(arxiv.SortCriterionRelevance),
	)

	results, err := client.Search(search)
	if err != nil {
		log.Fatalf("search failed: %v", err)
	}

	fmt.Printf("found %d results\n", len(results))

	for i, result := range results {
		fmt.Printf("%d. entry id length: %d\n", i+1, len(result.EntryID))
	}

	// Output:
	// found 3 results
	// 1. entry id length: 12
	// 2. entry id length: 12
	// 3. entry id length: 12
}

// Example_advancedSearch search by advanced query
func Example_advancedSearch() {
	srv := newFakeArxivServer()
	defer srv.Close()
	client := arxiv.DefaultClient()
	client.BaseURL = srv.URL

	search := arxiv.NewSearch(
		"ti:transformer AND au:vaswani",
		arxiv.WithMaxResults(5),
		arxiv.WithSortBy(arxiv.SortCriterionSubmittedDate),
		arxiv.WithSortOrder(arxiv.SortOrderDescending),
	)

	results, err := client.Search(search)
	if err != nil {
		log.Fatalf("search failed: %v", err)
	}

	fmt.Printf("found %d results about transformer and vaswani\n", len(results))
}

// Example_searchByID search by id
func Example_searchByID() {
	srv := newFakeArxivServer()
	defer srv.Close()
	client := arxiv.DefaultClient()
	client.BaseURL = srv.URL

	search := arxiv.NewSearch(
		"",
		arxiv.WithIDList("1706.03762"), // Attention Is All You Need
	)

	results, err := client.Search(search)
	if err != nil {
		log.Fatalf("search failed: %v", err)
	}

	if len(results) > 0 {
		result := results[0]
		fmt.Printf("Title: %s\n", result.Title)
		fmt.Printf("Published: %s\n", result.Published.Format("2006-01-02"))
		fmt.Printf("Short ID: %s\n", result.GetShortID())
	}
}

// Example_customClient custom client
func Example_customClient() {
	srv := newFakeArxivServer()
	defer srv.Close()
	config := arxiv.ClientConfig{
		PageSize:     1,
		DelaySeconds: 5 * time.Second,
		NumRetries:   5,
	}

	client := arxiv.NewClient(config)
	client.BaseURL = srv.URL

	search := arxiv.NewSearch(
		"quantum computing",
		arxiv.WithMaxResults(20),
	)

	_, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := client.Search(search)
	if err != nil {
		log.Fatalf("search failed: %v", err)
	}

	fmt.Printf("found %d results about quantum computing\n", len(results))
}

// Example_resultMethods result methods
func Example_resultMethods() {
	srv := newFakeArxivServer()
	defer srv.Close()
	client := arxiv.DefaultClient()
	client.BaseURL = srv.URL

	search := arxiv.NewSearch(
		"deep learning",
		arxiv.WithMaxResults(1),
	)

	results, err := client.Search(search)
	if err != nil || len(results) == 0 {
		return
	}

	result := results[0]

	fmt.Printf("Short ID: %s\n", result.GetShortID())
	fmt.Printf("Default PDF filename: %s\n", result.GetDefaultFilename("pdf"))
	fmt.Printf("Source code URL: %s\n", result.GetSourceURL())

	if len(result.Categories) > 0 {
		fmt.Printf("Categories: %v\n", result.Categories)
	}
}
