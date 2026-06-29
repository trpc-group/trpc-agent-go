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
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo/internal/client"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var errReadFailed = errors.New("read failed")

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) {
	return 0, errReadFailed
}

func (errReadCloser) Close() error {
	return nil
}

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

func TestDDGTool_SERPBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend string
		body    string
	}{
		{
			name:    "html",
			backend: backendHTML,
			body: `
<html><body>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fgaia">GAIA benchmark</a>
  <a class="result__snippet">A benchmark for general AI assistants.</a>
</body></html>`,
		},
		{
			name:    "lite",
			backend: backendLite,
			body: `
<html><body>
  <a class="result-link" href="/l/?uddg=https%3A%2F%2Fexample.org%2Flite">Lite GAIA</a>
  <div class="result-snippet">Lite result snippet.</div>
</body></html>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(tc.body))
				},
			))
			defer server.Close()

			ddgTool := &ddgTool{
				httpClient: server.Client(),
				baseURL:    server.URL,
				backend:    tc.backend,
				userAgent:  "test-agent/1.0",
			}
			result, err := ddgTool.search(
				context.Background(),
				searchRequest{Query: "GAIA benchmark"},
			)
			require.NoError(t, err)
			require.Len(t, result.Results, 1)
			require.Contains(t, result.Results[0].Title, "GAIA")
			require.Contains(t, result.Results[0].URL, "example.")
			require.NotEmpty(t, result.Results[0].Description)
			require.Contains(t, result.Summary, tc.backend)
		})
	}
}

func TestDDGTool_SERPChallenge(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`
<html><body>
  <div class="anomaly-modal__title">
    Unfortunately, bots use DuckDuckGo too.
  </div>
</body></html>`))
		},
	))
	defer server.Close()

	ddgTool := &ddgTool{
		httpClient: server.Client(),
		baseURL:    server.URL,
		backend:    backendHTML,
		userAgent:  "test-agent/1.0",
	}
	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA benchmark"},
	)
	require.Error(t, err)
	require.Empty(t, result.Results)
	require.Contains(t, result.Summary, "anti-bot challenge")
	require.Contains(t, result.Summary, "another configured search provider")
}

func TestDDGTool_SERPFallbackOnChallenge(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
			switch r.URL.Path {
			case "/html/":
				_, _ = w.Write([]byte(`
<html><body>
  <div class="anomaly-modal__title">
    Unfortunately, bots use DuckDuckGo too.
  </div>
</body></html>`))
			case "/lite/":
				_, _ = w.Write([]byte(`
<html><body>
  <a class="result-link"
     href="/l/?uddg=https%3A%2F%2Fexample.org%2Fgaia">GAIA fallback</a>
  <div class="result-snippet">Lite fallback snippet.</div>
</body></html>`))
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
		},
	))
	defer server.Close()

	ddgTool := &ddgTool{
		httpClient: server.Client(),
		baseURL:    server.URL + "/html/",
		backend:    backendHTML,
		userAgent:  "test-agent/1.0",
	}
	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA benchmark"},
	)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "GAIA fallback", result.Results[0].Title)
	require.Equal(t, "https://example.org/gaia", result.Results[0].URL)
	require.Contains(t, result.Summary, "lite")
	require.Contains(t, result.Summary, "fallback from html")
}

func TestDDGTool_SERPHTTPFallbackForPlainHTTPOnHTTPS(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
			require.Equal(t, "/html/", r.URL.Path)
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a"
     href="/l/?uddg=https%3A%2F%2Fexample.org%2Fplain-http">Plain HTTP fallback</a>
  <a class="result__snippet">Recovered from an HTTPS transport mismatch.</a>
</body></html>`))
		},
	))
	defer server.Close()

	ddgTool := &ddgTool{
		httpClient: server.Client(),
		baseURL: strings.Replace(
			server.URL+"/html/",
			"http://",
			"https://",
			1,
		),
		backend:   backendHTML,
		userAgent: "test-agent/1.0",
	}
	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA benchmark"},
	)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "Plain HTTP fallback", result.Results[0].Title)
	require.Equal(t, "https://example.org/plain-http", result.Results[0].URL)
	require.Contains(t, result.Summary, "http fallback from https")
}

func TestDDGTool_APIFallsBackToSERPOnPlainHTTPMismatch(t *testing.T) {
	t.Parallel()

	apiServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"RelatedTopics":[]}`))
		},
	))
	defer apiServer.Close()

	userAgent := make(chan string, 1)
	serpServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA paper authors", r.URL.Query().Get("q"))
			userAgent <- r.UserAgent()
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a"
     href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpaper">Paper result</a>
  <a class="result__snippet">Author information snippet.</a>
</body></html>`))
		},
	))
	defer serpServer.Close()

	apiURL := "https://" + strings.TrimPrefix(apiServer.URL, "http://")
	ddgTool := &ddgTool{
		client: client.New(
			apiURL,
			defaultUserAgent,
			apiServer.Client(),
		),
		httpClient: serpServer.Client(),
		baseURL:    serpServer.URL,
		backend:    backendAPI,
		userAgent:  defaultUserAgent,
	}

	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA paper authors"},
	)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Contains(t, result.Results[0].Title, "Paper result")
	require.Contains(t, result.Summary, "fallback from api")
	require.Equal(t, defaultSERPUserAgent, <-userAgent)
}

func TestDDGTool_APIFallsBackToSERPOnAcceptedStatus(t *testing.T) {
	t.Parallel()

	apiServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		},
	))
	defer apiServer.Close()

	serpServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA paper authors", r.URL.Query().Get("q"))
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a"
     href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpaper">Paper result</a>
  <a class="result__snippet">Author information snippet.</a>
</body></html>`))
		},
	))
	defer serpServer.Close()

	ddgTool := &ddgTool{
		client: client.New(
			apiServer.URL,
			defaultUserAgent,
			apiServer.Client(),
		),
		httpClient: serpServer.Client(),
		baseURL:    serpServer.URL,
		backend:    backendAPI,
		userAgent:  defaultUserAgent,
	}

	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA paper authors"},
	)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Contains(t, result.Results[0].Title, "Paper result")
	require.Equal(t, "https://example.com/paper", result.Results[0].URL)
	require.Contains(t, result.Summary, "fallback from api")
}

func TestDDGTool_APIFallsBackToSERPOnMalformedJSON(t *testing.T) {
	t.Parallel()

	apiServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(``))
		},
	))
	defer apiServer.Close()

	serpServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA paper authors", r.URL.Query().Get("q"))
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a"
     href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpaper">Paper result</a>
  <a class="result__snippet">Author information snippet.</a>
</body></html>`))
		},
	))
	defer serpServer.Close()

	ddgTool := &ddgTool{
		client: client.New(
			apiServer.URL,
			defaultUserAgent,
			apiServer.Client(),
		),
		httpClient: serpServer.Client(),
		baseURL:    serpServer.URL,
		backend:    backendAPI,
		userAgent:  defaultUserAgent,
	}

	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA paper authors"},
	)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Contains(t, result.Results[0].Title, "Paper result")
	require.Equal(t, "https://example.com/paper", result.Results[0].URL)
	require.Contains(t, result.Summary, "fallback from api")
}

func TestDDGTool_SERPFallbackFailureAddsContext(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
			switch r.URL.Path {
			case "/html/":
				_, _ = w.Write([]byte(`
<html><body>
  <div class="anomaly-modal__title">
    Unfortunately, bots use DuckDuckGo too.
  </div>
</body></html>`))
			case "/lite/":
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("temporarily unavailable"))
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
		},
	))
	defer server.Close()

	ddgTool := &ddgTool{
		httpClient: server.Client(),
		baseURL:    server.URL + "/html/",
		backend:    backendHTML,
		userAgent:  "test-agent/1.0",
	}
	result, err := ddgTool.search(
		context.Background(),
		searchRequest{Query: "GAIA benchmark"},
	)
	require.Error(t, err)
	require.Empty(t, result.Results)
	require.Contains(t, result.Summary, "fallback lite failed")
	require.Contains(t, err.Error(), "fallback lite failed")
}

func TestDDGTool_SERPHTTPFailures(t *testing.T) {
	t.Parallel()

	req := searchRequest{Query: "GAIA benchmark"}
	searchTool := &ddgTool{httpClient: http.DefaultClient}
	_, err := searchTool.searchSERP(
		context.Background(),
		req,
		backendHTML,
		"http://%",
	)
	require.Error(t, err)

	dialErr := errors.New("dial failed")
	searchTool = &ddgTool{
		httpClient: &http.Client{Transport: roundTripFunc(
			func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
				require.Contains(t, r.Header.Get("Accept"), "text/html")
				require.Equal(t, "en-US,en;q=0.9", r.Header.Get("Accept-Language"))
				return nil, dialErr
			},
		)},
		userAgent: "test-agent/1.0",
	}
	result, err := searchTool.searchSERP(
		context.Background(),
		req,
		backendHTML,
		"http://example.com/html/",
	)
	require.Error(t, err)
	require.ErrorIs(t, err, dialErr)
	require.Empty(t, result.Results)
	require.Contains(t, result.Summary, "dial failed")

	searchTool.httpClient = &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errReadCloser{},
				Header:     make(http.Header),
			}, nil
		},
	)}
	result, err = searchTool.searchSERP(
		context.Background(),
		req,
		backendHTML,
		"http://example.com/html/",
	)
	require.Error(t, err)
	require.ErrorIs(t, err, errReadFailed)
	require.Empty(t, result.Results)
	require.Contains(t, result.Summary, "read failed")
}

func TestDDGTool_SERPDoesNotFallbackAfterContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	searchTool := &ddgTool{
		httpClient: &http.Client{Transport: roundTripFunc(
			func(*http.Request) (*http.Response, error) {
				calls++
				cancel()
				return nil, context.Canceled
			},
		)},
		baseURL: "http://example.com/html/",
		backend: backendHTML,
	}
	result, err := searchTool.searchSERPWithFallback(
		ctx,
		searchRequest{Query: "GAIA benchmark"},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.Results)
	require.Equal(t, 1, calls)
}

func TestParseSERPResultsDedupesBeforeLimit(t *testing.T) {
	t.Parallel()

	results := parseSERPResults([]byte(`
<html><body>
  <a class="result__a" href="">empty result</a>
  <a class="result__a">missing href</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F1">One</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F1">One duplicate</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F2">Two</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F3">Three</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F4">Four</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F5">Five</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2F6">Six</a>
</body></html>`))

	require.Len(t, results, maxResults)
	require.Equal(t, "https://example.com/1", results[0].URL)
	require.Equal(t, "https://example.com/5", results[len(results)-1].URL)
}

func TestParseSERPResultsSkipsAds(t *testing.T) {
	t.Parallel()

	results := parseSERPResults([]byte(`
<html><body>
  <a class="result__a" href="https://duckduckgo.com/y.js?ad_domain=example.com&ad_provider=bing">Ad result</a>
  <div class="result__snippet">Sponsored snippet.</div>
  <a class="result__a" href="https://www.bing.com/aclick?ld=abc">Bing ad</a>
  <div class="result__snippet">Another ad.</div>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Freal">Real result</a>
  <div class="result__snippet">Organic snippet.</div>
</body></html>`))

	require.Len(t, results, 1)
	require.Equal(t, "Real result", results[0].Title)
	require.Equal(t, "https://example.com/real", results[0].URL)
}

func TestNormalizeSERPURL(t *testing.T) {
	t.Parallel()

	require.Empty(t, normalizeSERPURL(" "))
	require.Equal(t, "http://%zz", normalizeSERPURL("http://%zz"))
	require.Equal(t, "https://example.com/path",
		normalizeSERPURL("//example.com/path"))
	require.Equal(t, "https://example.com/gaia",
		normalizeSERPURL(
			"https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fgaia",
		))
}

func TestIsSERPAdURL(t *testing.T) {
	t.Parallel()

	require.True(
		t,
		isSERPAdURL(
			"https://duckduckgo.com/y.js?ad_domain=x&ad_provider=bing",
		),
	)
	require.True(t, isSERPAdURL("https://www.bing.com/aclick?ld=abc"))
	require.False(t, isSERPAdURL("https://example.com/organic"))
}

func TestFallbackSERPBaseURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultLiteBaseURL,
		fallbackSERPBaseURL(backendHTML, defaultHTMLBaseURL))
	require.Equal(t, defaultHTMLBaseURL,
		fallbackSERPBaseURL(backendLite, defaultLiteBaseURL))
	require.Equal(t, "http://example.com/lite/",
		fallbackSERPBaseURL(backendHTML, "http://example.com/html/"))
	require.Empty(t,
		fallbackSERPBaseURL(backendHTML, "http://example.com/search"))
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
		name          string
		opts          []Option
		wantDescMatch string
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
			name:          "custom backend",
			opts:          []Option{WithBackend(backendHTML)},
			wantDescMatch: "html search",
		},
		{
			name: "all options combined",
			opts: []Option{
				WithBaseURL(customURL),
				WithBackend(backendLite),
				WithUserAgent(customAgent),
				WithHTTPClient(customClient),
			},
			wantDescMatch: "lite search",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewTool(tc.opts...)
			if tool == nil {
				t.Fatalf("NewTool() returned nil for %s", tc.name)
			}
			if tc.wantDescMatch != "" {
				require.Contains(
					t,
					tool.Declaration().Description,
					tc.wantDescMatch,
				)
			}
		})
	}
}

func TestNewTool_WithBackendUsesSERPBackend(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
			require.Equal(t, "custom-agent/2.0", r.Header.Get("User-Agent"))
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result-link" href="/l/?uddg=https%3A%2F%2Fexample.org%2Flite">Lite GAIA</a>
  <div class="result-snippet">Lite result snippet.</div>
</body></html>`))
		},
	))
	defer server.Close()

	searchTool := NewTool(
		WithBaseURL(server.URL),
		WithBackend(backendLite),
		WithUserAgent("custom-agent/2.0"),
		WithHTTPClient(server.Client()),
	)
	require.Contains(t, searchTool.Declaration().Description, "lite search")
	raw, err := searchTool.Call(
		context.Background(),
		[]byte(`{"query":"GAIA benchmark"}`),
	)
	require.NoError(t, err)
	result, ok := raw.(searchResponse)
	require.True(t, ok)
	require.Len(t, result.Results, 1)
	require.Equal(t, "https://example.org/lite", result.Results[0].URL)
	require.Contains(t, result.Summary, "lite")
}

func TestNewTool_SERPBackendUsesBrowserDefaultUserAgent(t *testing.T) {
	t.Parallel()

	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotUserAgent = r.Header.Get("User-Agent")
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a" href="https://example.com/">Example</a>
</body></html>`))
		},
	))
	defer server.Close()

	searchTool := NewTool(
		WithBaseURL(server.URL),
		WithBackend(backendHTML),
		WithHTTPClient(server.Client()),
	)
	raw, err := searchTool.Call(
		context.Background(),
		[]byte(`{"query":"browser ua"}`),
	)
	require.NoError(t, err)
	result, ok := raw.(searchResponse)
	require.True(t, ok)
	require.Len(t, result.Results, 1)
	require.Contains(t, gotUserAgent, "Mozilla/5.0")
	require.Contains(t, gotUserAgent, "Chrome/")
}

func TestNormalizeBackend(t *testing.T) {
	require.Equal(t, backendAPI, normalizeBackend(""))
	require.Equal(t, backendAPI, normalizeBackend("instant-answer"))
	require.Equal(t, backendHTML, normalizeBackend(" HTML "))
	require.Equal(t, backendLite, normalizeBackend("lite"))
	require.Equal(t, "unknown", normalizeBackend("unknown"))
}

func TestNewDefaultHTTPClient_DisablesHTTP2(t *testing.T) {
	t.Parallel()

	httpClient := newDefaultHTTPClient(10 * time.Second)
	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	require.False(t, transport.ForceAttemptHTTP2)
	require.True(t,
		transport.TLSClientConfig == nil ||
			!transport.TLSClientConfig.InsecureSkipVerify,
	)
	require.Equal(t, 10*time.Second, httpClient.Timeout)
	require.Equal(t, defaultTLSHandshakeTimeout,
		transport.TLSHandshakeTimeout)
	require.Equal(t, defaultExpectContinueTimeout,
		transport.ExpectContinueTimeout)
}

func TestNewDefaultHTTPClient_UsesTransportNetwork(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are not available on windows")
	}

	socketPath := t.TempDir() + "/duckduckgo-test.sock"
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()

	httpClient := newDefaultHTTPClient(10 * time.Second)
	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok)

	conn, err := transport.DialContext(context.Background(), "unix", socketPath)
	require.NoError(t, err)
	defer conn.Close()

	serverConn := <-accepted
	require.NotNil(t, serverConn)
	serverConn.Close()
}

func TestWithTimeout_UsesDefaultTransport(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithTimeout(10 * time.Second)(cfg)
	httpClient := configuredHTTPClient(cfg)

	require.Equal(t, 10*time.Second, httpClient.Timeout)
	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	require.False(t, transport.ForceAttemptHTTP2)
}

func TestWithTimeout_PreservesCustomHTTPClientTransport(t *testing.T) {
	t.Parallel()

	for _, opts := range [][]Option{
		{
			WithHTTPClient(&http.Client{
				Timeout:   time.Second,
				Transport: http.DefaultTransport,
			}),
			WithTimeout(10 * time.Second),
		},
		{
			WithTimeout(10 * time.Second),
			WithHTTPClient(&http.Client{
				Timeout:   time.Second,
				Transport: http.DefaultTransport,
			}),
		},
	} {
		cfg := &config{}
		for _, opt := range opts {
			opt(cfg)
		}
		original := cfg.httpClient
		httpClient := configuredHTTPClient(cfg)
		require.NotSame(t, original, httpClient)
		require.Equal(t, time.Second, original.Timeout)
		require.Equal(t, 10*time.Second, httpClient.Timeout)
		require.Equal(t, http.DefaultTransport, httpClient.Transport)
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
