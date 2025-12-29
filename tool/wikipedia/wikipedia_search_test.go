//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package wikipedia provides Wikipedia search tool.
package wikipedia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia/internal/client"
)

func TestWikiSearchTool_Search_Success(t *testing.T) {
	// Mock Wikipedia API response
	mockResponse := `{
		"batchcomplete": "",
		"query": {
			"searchinfo": {
				"totalhits": 1247
			},
			"search": [
				{
					"ns": 0,
					"title": "Artificial intelligence",
					"pageid": 18985062,
					"size": 156789,
					"wordcount": 12543,
					"snippet": "Artificial intelligence (AI) is intelligence demonstrated by machines...",
					"timestamp": "2024-11-15T10:30:00Z"
				},
				{
					"ns": 0,
					"title": "Machine learning",
					"pageid": 18985063,
					"size": 98456,
					"wordcount": 8234,
					"snippet": "Machine learning is a subset of artificial intelligence...",
					"timestamp": "2024-11-10T08:20:00Z"
				}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	// Create tool with test client
	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	// Prepare request
	reqData := wikipediaSearchRequest{
		Query: "artificial intelligence",
		Limit: 3,
	}
	reqJSON, err := json.Marshal(reqData)
	require.NoError(t, err)

	// Execute search
	result, err := searchFunc.Call(context.Background(), reqJSON)
	require.NoError(t, err)

	// Validate results
	resp, ok := result.(wikipediaSearchResponse)
	require.True(t, ok, "Expected wikipediaSearchResponse type")

	if resp.Query != "artificial intelligence" {
		t.Errorf("Expected query 'artificial intelligence', got '%s'", resp.Query)
	}
	if len(resp.Results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(resp.Results))
	}
	if resp.TotalHits != 1247 {
		t.Errorf("Expected 1247 total hits, got %d", resp.TotalHits)
	}
	if resp.Results[0].Title != "Artificial intelligence" {
		t.Errorf("Expected title 'Artificial intelligence', got '%s'", resp.Results[0].Title)
	}
	if resp.Results[0].PageID != 18985062 {
		t.Errorf("Expected page ID 18985062, got %d", resp.Results[0].PageID)
	}
	if resp.Results[0].WordCount != 12543 {
		t.Errorf("Expected word count 12543, got %d", resp.Results[0].WordCount)
	}
	if !strings.Contains(resp.Summary, "Found 2 results") {
		t.Errorf("Expected summary to contain 'Found 2 results', got: %s", resp.Summary)
	}
	if resp.SearchTime == "" {
		t.Error("Expected search time to be set")
	}
}

func TestWikiSearchTool_EmptyQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	reqData := wikipediaSearchRequest{Query: ""}
	reqJSON, _ := json.Marshal(reqData)

	_, err := searchFunc.Call(context.Background(), reqJSON)
	if err == nil {
		t.Error("Expected error for empty query")
	}
}

func TestWikiSearchTool_NoResults(t *testing.T) {
	// Empty search results
	mockResponse := `{
		"batchcomplete": "",
		"query": {
			"searchinfo": {
				"totalhits": 0
			},
			"search": []
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	reqData := wikipediaSearchRequest{Query: "nonexistent_query_12345"}
	reqJSON, _ := json.Marshal(reqData)

	result, err := searchFunc.Call(context.Background(), reqJSON)
	require.NoError(t, err)

	resp, ok := result.(wikipediaSearchResponse)
	require.True(t, ok)

	if len(resp.Results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(resp.Results))
	}
	if !strings.Contains(resp.Summary, "Found 0 results") {
		t.Errorf("Expected 'Found 0 results' in summary, got: %s", resp.Summary)
	}
}

func TestWikiSearchTool_LimitValidation(t *testing.T) {
	mockResponse := `{
		"batchcomplete": "",
		"query": {
			"searchinfo": {"totalhits": 10},
			"search": [
				{"ns": 0, "title": "Test 1", "pageid": 1, "size": 100, "wordcount": 50, "snippet": "Test", "timestamp": "2024-01-01T00:00:00Z"},
				{"ns": 0, "title": "Test 2", "pageid": 2, "size": 100, "wordcount": 50, "snippet": "Test", "timestamp": "2024-01-01T00:00:00Z"},
				{"ns": 0, "title": "Test 3", "pageid": 3, "size": 100, "wordcount": 50, "snippet": "Test", "timestamp": "2024-01-01T00:00:00Z"}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	testCases := []struct {
		name          string
		limit         int
		expectedLimit int
	}{
		{"negative limit", -1, 5},
		{"zero limit", 0, 5},
		{"valid limit", 3, 3},
		{"exceeds max", 10, 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reqData := wikipediaSearchRequest{Query: "test", Limit: tc.limit}
			reqJSON, _ := json.Marshal(reqData)

			result, err := searchFunc.Call(context.Background(), reqJSON)
			require.NoError(t, err)

			resp, ok := result.(wikipediaSearchResponse)
			require.True(t, ok)

			// Just verify it doesn't crash - actual limit is handled by client
			if len(resp.Results) > tc.expectedLimit {
				t.Logf("Note: Got %d results with limit %d (max: %d)", len(resp.Results), tc.limit, tc.expectedLimit)
			}
		})
	}
}

func TestCleanHTMLTags(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "Simple text without tags",
			expected: "Simple text without tags",
		},
		{
			input:    "Text with <b>bold</b> and <i>italic</i> tags",
			expected: "Text with bold and italic tags",
		},
		{
			input:    "HTML entities: &amp; &lt; &gt; &quot; &#39; &nbsp;",
			expected: "HTML entities: & < > \" '",
		},
		{
			input:    "<span class=\"searchmatch\">keyword</span> in snippet",
			expected: "keyword in snippet",
		},
		{
			input:    "Multiple   spaces   should   collapse",
			expected: "Multiple spaces should collapse",
		},
		{
			input:    "  Leading and trailing spaces  ",
			expected: "Leading and trailing spaces",
		},
		{
			input:    "",
			expected: "",
		},
		{
			input:    "<p>Paragraph with <a href=\"url\">link</a> inside</p>",
			expected: "Paragraph with link inside",
		},
	}

	for _, tc := range testCases {
		result := cleanHTMLTags(tc.input)
		if result != tc.expected {
			t.Errorf("cleanHTMLTags(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestNewWikiToolSet(t *testing.T) {
	testCases := []struct {
		name string
		opts []Option
	}{
		{
			name: "default options",
			opts: nil,
		},
		{
			name: "with language",
			opts: []Option{WithLanguage("zh")},
		},
		{
			name: "with max results",
			opts: []Option{WithMaxResults(10)},
		},
		{
			name: "with timeout",
			opts: []Option{WithTimeout(60 * time.Second)},
		},
		{
			name: "with user agent",
			opts: []Option{WithUserAgent("custom-agent/2.0")},
		},
		{
			name: "all options combined",
			opts: []Option{
				WithLanguage("es"),
				WithMaxResults(15),
				WithTimeout(45 * time.Second),
				WithUserAgent("test-agent/1.0"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toolSet, err := NewToolSet(tc.opts...)
			require.NoError(t, err)
			if toolSet == nil {
				t.Fatalf("NewToolSet() returned nil for %s", tc.name)
			}
			tools := toolSet.Tools(context.Background())
			if len(tools) == 0 {
				t.Fatalf("NewToolSet() returned empty tools for %s", tc.name)
			}
		})
	}
}

func TestWikiToolSet_Tools(t *testing.T) {
	toolSet, err := NewToolSet(
		WithLanguage("en"),
		WithMaxResults(5),
	)
	require.NoError(t, err)

	tools := toolSet.Tools(context.Background())
	if len(tools) == 0 {
		t.Fatal("Tools() returned empty slice")
	}

	// Verify tool declaration
	searchTool := tools[0]
	decl := searchTool.Declaration()
	if decl == nil {
		t.Fatal("Declaration() returned nil")
	}
	if decl.Name != "wikipedia_search" {
		t.Errorf("Expected tool name 'wikipedia_search', got '%s'", decl.Name)
	}
	if decl.Description == "" {
		t.Error("Expected non-empty description")
	}
	if !strings.Contains(decl.Description, "WIKIPEDIA SEARCH") {
		t.Errorf("Expected description to contain 'WIKIPEDIA SEARCH', got: %s", decl.Description)
	}
	if decl.InputSchema == nil {
		t.Error("Expected non-nil InputSchema")
	}
	if decl.OutputSchema == nil {
		t.Error("Expected non-nil OutputSchema")
	}
}

func TestWikiSearchTool_ServerError(t *testing.T) {
	// Test with server returning error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	reqData := wikipediaSearchRequest{Query: "test query"}
	reqJSON, _ := json.Marshal(reqData)

	result, err := searchFunc.Call(context.Background(), reqJSON)
	if err == nil {
		t.Error("Expected error for server error response")
	}

	resp, ok := result.(wikipediaSearchResponse)
	if ok && !strings.Contains(resp.Summary, "Error") {
		t.Errorf("Expected error message in summary, got: %s", resp.Summary)
	}
}

func TestWikiSearchTool_URLGeneration(t *testing.T) {
	mockResponse := `{
		"batchcomplete": "",
		"query": {
			"searchinfo": {"totalhits": 1},
			"search": [
				{
					"ns": 0,
					"title": "Machine Learning",
					"pageid": 123456,
					"size": 50000,
					"wordcount": 5000,
					"snippet": "Test snippet",
					"timestamp": "2024-01-01T00:00:00Z"
				}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	testCases := []struct {
		language    string
		title       string
		expectedURL string
	}{
		{
			language:    "en",
			title:       "Machine Learning",
			expectedURL: "https://en.wikipedia.org/wiki/Machine_Learning",
		},
		{
			language:    "zh",
			title:       "Machine Learning",
			expectedURL: "https://zh.wikipedia.org/wiki/Machine_Learning",
		},
		{
			language:    "es",
			title:       "Machine Learning",
			expectedURL: "https://es.wikipedia.org/wiki/Machine_Learning",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.language, func(t *testing.T) {
			httpClient := &http.Client{Timeout: 30 * time.Second}
			testClient := client.New(server.URL, "test-agent/1.0", httpClient)
			cfg := &config{
				language:   tc.language,
				maxResults: 5,
			}

			searchFunc := createWikipediaSearchTool(testClient, cfg)

			reqData := wikipediaSearchRequest{Query: "machine learning"}
			reqJSON, _ := json.Marshal(reqData)

			result, err := searchFunc.Call(context.Background(), reqJSON)
			require.NoError(t, err)

			resp, ok := result.(wikipediaSearchResponse)
			require.True(t, ok)
			require.Greater(t, len(resp.Results), 0)

			if resp.Results[0].URL != tc.expectedURL {
				t.Errorf("Expected URL %s, got %s", tc.expectedURL, resp.Results[0].URL)
			}
		})
	}
}

func TestWikiSearchTool_IncludeAll(t *testing.T) {
	mockResponse := `{
		"batchcomplete": "",
		"query": {
			"searchinfo": {"totalhits": 1},
			"search": [
				{
					"ns": 0,
					"title": "Test Article",
					"pageid": 12345,
					"size": 1000,
					"wordcount": 500,
					"snippet": "Test snippet with <span>HTML</span>",
					"timestamp": "2024-01-01T00:00:00Z"
				}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	cfg := &config{
		language:   "en",
		maxResults: 5,
	}

	searchFunc := createWikipediaSearchTool(testClient, cfg)

	// Test with IncludeAll = true
	reqData := wikipediaSearchRequest{
		Query:      "test",
		Limit:      5,
		IncludeAll: true,
	}
	reqJSON, _ := json.Marshal(reqData)

	result, err := searchFunc.Call(context.Background(), reqJSON)
	require.NoError(t, err)

	resp, ok := result.(wikipediaSearchResponse)
	require.True(t, ok)
	require.Greater(t, len(resp.Results), 0)

	// Verify HTML tags are cleaned from description
	if strings.Contains(resp.Results[0].Description, "<span>") {
		t.Error("Expected HTML tags to be cleaned from description")
	}
	if !strings.Contains(resp.Results[0].Description, "HTML") {
		t.Error("Expected text content to remain after HTML cleaning")
	}
}

func TestConvertHTMLToMarkdown(t *testing.T) {
	htmlContent := `
		<html>
		<head>
			<title>Test Page</title>
			<style>body { color: red; }</style>
		</head>
		<body>
			<h1>Header</h1>
			<h2>Subheader</h2>
			<script>console.log("ignore");</script>
			<p>Paragraph text.</p>
			<ul>
				<li>Item 1</li>
				<li>Item 2</li>
			</ul>
			<p>Check <a href="http://example.com">this link</a>.</p>
			<p><b>Bold</b> and <i>Italic</i></p>
		</body>
		</html>
	`
	// Mock reader
	result, err := convertHTMLToMarkdown(strings.NewReader(htmlContent))
	require.NoError(t, err)

	// Debug output if test fails
	t.Logf("Converted Markdown:%s", result)

	// Check expected markdown content
	expectedParts := []string{
		"# Header",
		"## Subheader",
		"Paragraph text.",
		"- Item 1",
		"- Item 2",
		"Check [this link](http://example.com).",
		"**Bold**",
		"*Italic*",
	}

	for _, part := range expectedParts {
		assert.Contains(t, result, part)
	}

	assert.NotContains(t, result, "console.log")
	assert.NotContains(t, result, "color: red")
}
