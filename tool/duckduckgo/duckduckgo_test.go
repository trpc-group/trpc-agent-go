//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package duckduckgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo/internal/client"
)

func TestDuckDuckGoTool_Search_Results(t *testing.T) {
	// Mock API response with related topics
	mockResponse := `{
		"Abstract": "Beijing is the capital of China",
		"AbstractText": "Beijing is the capital of China and one of the most populous cities in the world.",
		"AbstractSource": "Wikipedia",
		"Answer": "",
		"Definition": "",
		"RelatedTopics": [
			{
				"Text": "Beijing Capital International Airport - Beijing Capital International Airport is the main international airport serving Beijing.",
				"FirstURL": "https://duckduckgo.com/Beijing_Capital_International_Airport"
			},
			{
				"Text": "Weather in Beijing - Current weather conditions in Beijing, China.",
				"FirstURL": "https://duckduckgo.com/Weather_in_Beijing"
			}
		],
		"Results": []
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
	ddgTool := &ddgTool{client: testClient}

	// Test search
	req := searchRequest{Query: "Beijing weather"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	// Validate results
	if result.Query != "Beijing weather" {
		t.Errorf("Expected query 'Beijing weather', got '%s'", result.Query)
	}
	if len(result.Results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(result.Results))
	}
	if result.Results[0].Title == "" {
		t.Error("Expected first result to have a title")
	}
	if result.Results[0].URL == "" {
		t.Error("Expected first result to have a URL")
	}
	if result.Summary == "" {
		t.Error("Expected summary to be set")
	}
}

func TestDDGTool_InstantAnswer(t *testing.T) {
	mockResponse := `{
		"Answer": "25°C, Partly cloudy",
		"AnswerType": "weather",
		"Abstract": "",
		"Definition": "",
		"RelatedTopics": [],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "weather Beijing"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	// Should create a summary result when no RelatedTopics but has Answer
	if len(result.Results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(result.Results))
	}
	if !contains(result.Summary, "Answer: 25°C, Partly cloudy") {
		t.Errorf("Expected summary to contain answer, got: %s", result.Summary)
	}
}

func TestDDGTool_Definition(t *testing.T) {
	mockResponse := `{
		"Definition": "Large Language Model (LLM) is a type of artificial intelligence model.",
		"DefinitionSource": "Wikipedia",
		"Answer": "",
		"Abstract": "",
		"RelatedTopics": [],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "LLM definition"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	if !contains(result.Summary, "Definition:") {
		t.Errorf("Expected summary to contain definition, got: %s", result.Summary)
	}
	if !contains(result.Summary, "Wikipedia") {
		t.Errorf("Expected summary to contain source, got: %s", result.Summary)
	}
}

func TestDDGTool_EmptyQuery(t *testing.T) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New("https://api.duckduckgo.com", "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: ""}
	result, _ := ddgTool.search(context.Background(), req)

	if !contains(result.Summary, "Error: Empty search query") {
		t.Errorf("Expected error message for empty query, got: %s", result.Summary)
	}
	if len(result.Results) != 0 {
		t.Errorf("Expected 0 results for empty query, got %d", len(result.Results))
	}
}

func TestDDGTool_NoResults(t *testing.T) {
	// Empty response
	mockResponse := `{
		"Answer": "",
		"Abstract": "",
		"Definition": "",
		"RelatedTopics": [],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "nonexistent query"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	if len(result.Results) != 0 {
		t.Errorf("Expected 0 results for empty response, got %d", len(result.Results))
	}
	if !contains(result.Summary, "Found 0 results") {
		t.Errorf("Expected 'Found 0 results' in summary, got: %s", result.Summary)
	}
}

func TestExtractTitleFromTopic(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "Beijing Capital International Airport - Beijing Capital International Airport is the main airport",
			expected: "Beijing Capital International Airport",
		},
		{
			input:    "Weather in Beijing",
			expected: "Weather in Beijing",
		},
		{
			input:    "This is a very long title that exceeds the maximum length limit and should be truncated properly",
			expected: "This is a very long title that exceeds the maxi...",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		result := extractTitleFromTopic(tc.input)
		if result != tc.expected {
			t.Errorf("extractTitleFromTopic(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test JSON marshaling/unmarshaling of our tool types
func TestToolTypesJSONMarshaling(t *testing.T) {
	req := searchRequest{Query: "test query"}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal searchRequest: %v", err)
	}

	var unmarshaledReq searchRequest
	err = json.Unmarshal(reqJSON, &unmarshaledReq)
	if err != nil {
		t.Fatalf("Failed to unmarshal searchRequest: %v", err)
	}

	if unmarshaledReq.Query != req.Query {
		t.Errorf("Query mismatch after JSON round-trip: got %q, want %q", unmarshaledReq.Query, req.Query)
	}

	resp := searchResponse{
		Query: "test",
		Results: []resultItem{
			{Title: "Test", URL: "http://example.com", Description: "Test description"},
		},
		Summary: "Test summary",
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal searchResponse: %v", err)
	}

	var unmarshaledResp searchResponse
	err = json.Unmarshal(respJSON, &unmarshaledResp)
	if err != nil {
		t.Fatalf("Failed to unmarshal searchResponse: %v", err)
	}

	if len(unmarshaledResp.Results) != 1 {
		t.Errorf("Results length mismatch: got %d, want 1", len(unmarshaledResp.Results))
	}
}

func TestNewTool(t *testing.T) {
	customURL := "https://custom.api.com"
	customAgent := "custom-agent/2.0"
	customClient := &http.Client{Timeout: 10 * time.Second}

	testCases := []struct {
		name string
		opts []Option
	}{
		{
			name: "default options",
			opts: nil,
		},
		{
			name: "custom base URL",
			opts: []Option{WithBaseURL(customURL)},
		},
		{
			name: "custom user agent",
			opts: []Option{WithUserAgent(customAgent)},
		},
		{
			name: "custom HTTP client",
			opts: []Option{WithHTTPClient(customClient)},
		},
		{
			name: "all options combined",
			opts: []Option{
				WithBaseURL(customURL),
				WithUserAgent(customAgent),
				WithHTTPClient(customClient),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewTool(tc.opts...)
			if tool == nil {
				t.Fatalf("NewTool() returned nil for %s", tc.name)
			}
		})
	}
}

func TestDDGTool_SearchError(t *testing.T) {
	// Test with server returning error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "test query"}
	result, err := ddgTool.search(context.Background(), req)

	if err == nil {
		t.Error("Expected error for server error response")
	}
	if !contains(result.Summary, "Error performing search") {
		t.Errorf("Expected error message in summary, got: %s", result.Summary)
	}
}

func TestDDGTool_MaxResults(t *testing.T) {
	// Create response with more than maxResults (5) related topics
	mockResponse := `{
		"Abstract": "Test abstract",
		"AbstractText": "Test abstract text",
		"Answer": "",
		"Definition": "",
		"RelatedTopics": [
			{"Text": "Topic 1", "FirstURL": "http://example.com/1"},
			{"Text": "Topic 2", "FirstURL": "http://example.com/2"},
			{"Text": "Topic 3", "FirstURL": "http://example.com/3"},
			{"Text": "Topic 4", "FirstURL": "http://example.com/4"},
			{"Text": "Topic 5", "FirstURL": "http://example.com/5"},
			{"Text": "Topic 6", "FirstURL": "http://example.com/6"},
			{"Text": "Topic 7", "FirstURL": "http://example.com/7"}
		],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "test"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	// Should only return maxResults (5) items
	if len(result.Results) != maxResults {
		t.Errorf("Expected %d results (maxResults), got %d", maxResults, len(result.Results))
	}
}

func TestDDGTool_WhitespaceQuery(t *testing.T) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New("https://api.duckduckgo.com", "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}

	// Test with whitespace-only query
	req := searchRequest{Query: "   "}
	result, err := ddgTool.search(context.Background(), req)

	if err == nil {
		t.Error("Expected error for whitespace-only query")
	}
	if !contains(result.Summary, "Error: Empty search query") {
		t.Errorf("Expected error message for whitespace query, got: %s", result.Summary)
	}
}

func TestDDGTool_RelatedTopicsWithoutURL(t *testing.T) {
	// Test related topics with missing FirstURL
	mockResponse := `{
		"Abstract": "",
		"Answer": "",
		"Definition": "",
		"RelatedTopics": [
			{"Text": "Topic with URL", "FirstURL": "http://example.com/1"},
			{"Text": "Topic without URL", "FirstURL": ""},
			{"Text": "", "FirstURL": "http://example.com/2"}
		],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "test"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	// Should only include topics with both text and URL
	if len(result.Results) != 1 {
		t.Errorf("Expected 1 result (only valid topic), got %d", len(result.Results))
	}
}

func TestDDGTool_AbstractWithSource(t *testing.T) {
	mockResponse := `{
		"Abstract": "Test abstract",
		"AbstractText": "Test abstract text",
		"AbstractSource": "Test Source",
		"Answer": "",
		"Definition": "",
		"RelatedTopics": [],
		"Results": []
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	testClient := client.New(server.URL, "test-agent/1.0", httpClient)
	ddgTool := &ddgTool{client: testClient}
	req := searchRequest{Query: "test"}
	result, err := ddgTool.search(context.Background(), req)
	require.NoError(t, err)

	if !contains(result.Summary, "Abstract: Test abstract text") {
		t.Errorf("Expected abstract in summary, got: %s", result.Summary)
	}
	if !contains(result.Summary, "Source: Test Source") {
		t.Errorf("Expected source in summary, got: %s", result.Summary)
	}
}
