//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package youcom

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOption(t *testing.T) {
	type testCase struct {
		name    string
		opts    []Option
		wantCfg config
	}

	testCases := []testCase{
		{
			name:    "empty",
			opts:    nil,
			wantCfg: config{},
		},
		{
			name: "with options",
			opts: []Option{
				WithAPIKey("key-123"),
				WithBaseURL("https://example.com/api/search"),
				WithUserAgent("custom-agent/2.0"),
				WithNumResults(5),
				WithCountry("de"),
				WithSafeSearch("strict"),
			},
			wantCfg: config{
				apiKey:     "key-123",
				baseURL:    "https://example.com/api/search",
				userAgent:  "custom-agent/2.0",
				numResults: 5,
				country:    "DE",
				safeSearch: "strict",
			},
		},
		{
			name: "num results clamped to max",
			opts: []Option{
				WithNumResults(50),
			},
			wantCfg: config{
				numResults: maxResults,
			},
		},
		{
			name: "num results clamped to default",
			opts: []Option{
				WithNumResults(-3),
			},
			wantCfg: config{
				numResults: maxResults,
			},
		},
		{
			name: "invalid safe search ignored",
			opts: []Option{
				WithSafeSearch("bogus"),
			},
			wantCfg: config{
				safeSearch: "",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config{}
			for _, opt := range tc.opts {
				opt(cfg)
			}
			if !reflect.DeepEqual(*cfg, tc.wantCfg) {
				t.Errorf("got config %+v, want %+v", *cfg, tc.wantCfg)
			}
		})
	}
}

func TestNewToolSet(t *testing.T) {
	type testCase struct {
		name      string
		opts      []Option
		wantErr   bool
		wantTools int
	}

	testCases := []testCase{
		{
			name:    "missing api key",
			opts:    nil,
			wantErr: true,
		},
		{
			name: "empty api key",
			opts: []Option{
				WithAPIKey("   "),
			},
			wantErr: true,
		},
		{
			name: "with api key",
			opts: []Option{
				WithAPIKey("key-123"),
			},
			wantErr:   false,
			wantTools: 1,
		},
		{
			name: "with api key and base url",
			opts: []Option{
				WithAPIKey("key-123"),
				WithBaseURL("https://example.com/api/search"),
				WithNumResults(5),
				WithCountry("US"),
				WithSafeSearch("moderate"),
			},
			wantErr:   false,
			wantTools: 1,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toolSet, err := NewToolSet(tc.opts...)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(toolSet.tools) != tc.wantTools {
				t.Errorf("got %d tools, want %d", len(toolSet.tools), tc.wantTools)
			}
		})
	}
}

func TestTools(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	tools := toolSet.Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
}

func TestName(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if toolSet.Name() != "youcom" {
		t.Fatalf("got name %s, want youcom", toolSet.Name())
	}
}

func TestClose(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if err := toolSet.Close(); err != nil {
		t.Fatalf("failed to close tool set: %v", err)
	}
}

// newFakeYoucomAPI returns an httptest TLS server that emits the given
// results as a You.com Web Search API response and records the latest
// request. The captured request is guarded by a mutex and accessed through
// the returned function so the test does not race with the HTTP handler.
func newFakeYoucomAPI(t *testing.T, web, news []youcomAPIResult) (*httptest.Server, func() *http.Request) {
	t.Helper()
	var mu sync.Mutex
	var captured *http.Request
	srv := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			mu.Lock()
			captured = r.Clone(r.Context())
			// r.Clone shares the original (already consumed) body; replace it
			// with a fresh reader so tests can decode it afterwards.
			captured.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			resp := youcomAPIResponse{Results: youcomAPISections{Web: web, News: news}}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encode fake response: %v", err)
			}
		},
	))
	t.Cleanup(srv.Close)
	return srv, func() *http.Request {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// fakeTLSClient returns an HTTP client that trusts the httptest TLS server.
func fakeTLSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only
		},
	}
}

func TestSearchTool(t *testing.T) {
	fakeWeb := []youcomAPIResult{
		{
			URL:      "https://example.com/go",
			Title:    "Example Go page",
			Snippets: []string{" An excerpt about Go. ", ""},
		},
		{
			URL:          "https://example.com/preview",
			Title:        "Example with thumbnail",
			Snippets:     []string{"Another excerpt."},
			ThumbnailURL: "https://example.com/thumb.png",
		},
		{
			// No URL and no title: must be dropped from output.
			Snippets: []string{"orphan snippet"},
		},
	}
	fakeNews := []youcomAPIResult{
		{
			URL:         "https://example.com/news",
			Title:       "News item with description only",
			Description: "A news description used as fallback snippet.",
		},
	}
	srv, captured := newFakeYoucomAPI(t, fakeWeb, fakeNews)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
		WithNumResults(5),
		WithCountry("US"),
		WithSafeSearch("moderate"),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "golang testing"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("search call failed: %v", err)
	}

	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if resp.Query != "golang testing" {
		t.Errorf("expected query 'golang testing', got %q", resp.Query)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results (2 web + 1 news), got %d", len(resp.Results))
	}
	if resp.Results[0].URL != "https://example.com/go" {
		t.Errorf("unexpected URL: %q", resp.Results[0].URL)
	}
	if resp.Results[0].Title != "Example Go page" {
		t.Errorf("unexpected title: %q", resp.Results[0].Title)
	}
	// Empty snippets must be dropped, non-empty kept.
	if len(resp.Results[0].Snippets) != 1 ||
		resp.Results[0].Snippets[0] != "An excerpt about Go." {
		t.Errorf("unexpected snippets: %#v", resp.Results[0].Snippets)
	}
	if resp.Results[1].ThumbnailURL != "https://example.com/thumb.png" {
		t.Errorf("unexpected thumbnail URL: %q", resp.Results[1].ThumbnailURL)
	}
	// News results carry a description instead of snippets; it must be used
	// as a fallback snippet.
	if resp.Results[2].URL != "https://example.com/news" {
		t.Fatalf("expected news result last, got %q", resp.Results[2].URL)
	}
	if len(resp.Results[2].Snippets) != 1 ||
		resp.Results[2].Snippets[0] != "A news description used as fallback snippet." {
		t.Errorf("unexpected news snippets: %#v", resp.Results[2].Snippets)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error field: %q", resp.Error)
	}

	// Verify the request reached the fake API with the expected parameters.
	req := captured()
	if req == nil {
		t.Fatalf("no request was captured")
	}
	if req.Method != http.MethodPost {
		t.Errorf("expected POST request, got %s", req.Method)
	}
	var body struct {
		Query      string `json:"query"`
		Count      int    `json:"count"`
		Country    string `json:"country"`
		SafeSearch string `json:"safesearch"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body.Query != "golang testing" {
		t.Errorf("expected query 'golang testing', got %q", body.Query)
	}
	if body.Count != 5 {
		t.Errorf("expected count '5', got %d", body.Count)
	}
	if body.Country != "US" {
		t.Errorf("expected country 'US', got %q", body.Country)
	}
	if body.SafeSearch != "moderate" {
		t.Errorf("expected safesearch 'moderate', got %q", body.SafeSearch)
	}
	if got := req.Header.Get("X-API-Key"); got != "key-123" {
		t.Errorf("expected X-API-Key header, got %q", got)
	}
}

func TestSearchToolRequestOverrides(t *testing.T) {
	srv, captured := newFakeYoucomAPI(t, []youcomAPIResult{
		{URL: "https://example.com/1", Title: "One"},
	}, nil)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{
		Query:      "override test",
		NumResults: 2,
		Country:    "de",
		SafeSearch: "strict",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := searchTool.Call(context.Background(), reqJSON); err != nil {
		t.Fatalf("search call failed: %v", err)
	}

	req := captured()
	if req == nil {
		t.Fatalf("no request was captured")
	}
	var body struct {
		Count      int    `json:"count"`
		Country    string `json:"country"`
		SafeSearch string `json:"safesearch"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body.Count != 2 {
		t.Errorf("expected count '2', got %d", body.Count)
	}
	if body.Country != "DE" {
		t.Errorf("expected country 'DE', got %q", body.Country)
	}
	if body.SafeSearch != "strict" {
		t.Errorf("expected safesearch 'strict', got %q", body.SafeSearch)
	}
}

func TestSearchToolEmptyQuery(t *testing.T) {
	srv, _ := newFakeYoucomAPI(t, nil, nil)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "   "})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected error for empty query, got nil")
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
	if !strings.Contains(resp.Error, "empty search query") {
		t.Errorf("unexpected error field: %q", resp.Error)
	}
}

func TestSearchToolServerError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		},
	))
	defer srv.Close()

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "anything"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected error on 401, got nil")
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
}

func TestNewToolSetRejectsNonHTTPSBaseURL(t *testing.T) {
	_, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL("http://api.you.com/api/search"),
	)
	if err == nil {
		t.Fatalf("expected error for http base URL, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("expected HTTPS error, got %v", err)
	}
}

func TestSearchToolStripsAPIKeyOnCrossOriginRedirect(t *testing.T) {
	var mu sync.Mutex
	var gotKeys []string
	final := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotKeys = append(gotKeys, r.Header.Get("X-API-Key"))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":{"web":[],"news":[]}}`))
		},
	))
	defer final.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// Cross-origin redirect: the API key must not follow.
			http.Redirect(w, r, final.URL+"/v1/search", http.StatusFound)
		},
	))
	defer origin.Close()

	toolSet, err := NewToolSet(
		WithAPIKey("secret-key"),
		WithBaseURL(origin.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}
	reqJSON, err := json.Marshal(searchRequest{Query: "redirect test"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := searchTool.Call(context.Background(), reqJSON); err != nil {
		t.Fatalf("search call failed: %v", err)
	}
	mu.Lock()
	keys := append([]string(nil), gotKeys...)
	mu.Unlock()
	if len(keys) != 1 {
		t.Fatalf("expected 1 request at final origin, got %d", len(keys))
	}
	if keys[0] != "" {
		t.Errorf("expected X-API-Key stripped on cross-origin redirect, got %q", keys[0])
	}
}

func TestSearchToolPreservesCallerRedirectPolicy(t *testing.T) {
	var mu sync.Mutex
	hookCalled := false
	var gotKeys []string
	final := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotKeys = append(gotKeys, r.Header.Get("X-API-Key"))
			mu.Unlock()
		},
	))
	defer final.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, final.URL+"/v1/search", http.StatusFound)
		},
	))
	defer origin.Close()

	base := fakeTLSClient()
	base.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		mu.Lock()
		hookCalled = true
		mu.Unlock()
		// Caller policy: never follow redirects.
		return http.ErrUseLastResponse
	}

	toolSet, err := NewToolSet(
		WithAPIKey("secret-key"),
		WithBaseURL(origin.URL),
		WithHTTPClient(base),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}
	reqJSON, err := json.Marshal(searchRequest{Query: "redirect test"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	// The redirect response (302) is returned as an error by the tool since
	// it is a non-2xx status; the caller hook must have prevented the
	// redirect from being followed.
	_, callErr := searchTool.Call(context.Background(), reqJSON)
	if callErr == nil {
		t.Fatal("expected an error from the 302 response returned by ErrUseLastResponse")
	}
	if !strings.Contains(callErr.Error(), "302") {
		t.Errorf("expected 302 status error from unfollowed redirect, got %v", callErr)
	}
	mu.Lock()
	called, keys := hookCalled, append([]string(nil), gotKeys...)
	mu.Unlock()
	if !called {
		t.Error("caller CheckRedirect hook was not invoked")
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 requests at final origin (redirect must not be followed), got %d", len(keys))
	}
}

func TestSearchToolDefaultRedirectLimit(t *testing.T) {
	redirects := 0
	final := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	))
	defer final.Close()

	// origin responds with an endless same-origin redirect loop.
	origin := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			redirects++
			http.Redirect(w, r, r.URL.String(), http.StatusFound)
		},
	))
	defer origin.Close()

	toolSet, err := NewToolSet(
		WithAPIKey("secret-key"),
		WithBaseURL(origin.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}
	reqJSON, err := json.Marshal(searchRequest{Query: "redirect loop"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, callErr := searchTool.Call(context.Background(), reqJSON); callErr == nil {
		t.Fatal("expected an error from the redirect loop")
	}
	if redirects > 10 {
		t.Errorf("expected the default 10-redirect limit to remain effective, followed %d redirects", redirects)
	}
}

func TestTrimSnippetsRuneBoundary(t *testing.T) {
	// 501 bytes whose 500th byte falls inside a multi-byte rune.
	cjk := strings.Repeat("界", 200) // 600 bytes of 3-byte runes
	got := trimSnippets([]string{cjk})
	if len(got) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(got))
	}
	if !utf8.ValidString(got[0]) {
		t.Errorf("truncated snippet is not valid UTF-8: %q", got[0])
	}
}

func TestNewToolSetInvalidBaseURL(t *testing.T) {
	_, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL("://not a url"),
	)
	if err == nil {
		t.Fatalf("expected error for invalid base URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid base URL") {
		t.Errorf("expected invalid base URL error, got %v", err)
	}
}

func TestNewToolSetEmptyBaseURLFallsBackToDefault(t *testing.T) {
	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL("   "),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolSet == nil {
		t.Fatal("expected tool set, got nil")
	}
	// The default base URL is HTTPS and valid, so construction must succeed;
	// an invalid scheme would have failed above.
	if len(toolSet.tools) != 1 {
		t.Errorf("expected 1 tool after empty base URL fallback, got %d", len(toolSet.tools))
	}
}

func TestWithTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		wantNil bool
	}{
		{"positive", 5 * time.Second, false},
		{"zero disables", 0, false},
		{"negative ignored", -1 * time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config{}
			WithTimeout(tc.timeout)(cfg)
			if tc.wantNil {
				if cfg.timeout != nil {
					t.Errorf("expected nil timeout, got %v", *cfg.timeout)
				}
				return
			}
			if cfg.timeout == nil {
				t.Fatalf("expected timeout %v, got nil", tc.timeout)
			}
			if *cfg.timeout != tc.timeout {
				t.Errorf("expected timeout %v, got %v", tc.timeout, *cfg.timeout)
			}
		})
	}
}

func TestResolveHTTPClientTimeoutOverride(t *testing.T) {
	// Default client: timeout override must be applied.
	cfg := &config{timeout: durationPtr(7 * time.Second)}
	client := resolveHTTPClient(cfg)
	if client.Timeout != 7*time.Second {
		t.Errorf("expected 7s timeout, got %v", client.Timeout)
	}

	// Custom client: the caller's client must not be mutated, and custom
	// Transport settings must survive the shallow copy.
	custom := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}
	cfg = &config{httpClient: custom, timeout: durationPtr(9 * time.Second)}
	client = resolveHTTPClient(cfg)
	if client == custom {
		t.Fatal("expected a cloned client, got the caller's instance")
	}
	if client.Timeout != 9*time.Second {
		t.Errorf("expected 9s timeout on clone, got %v", client.Timeout)
	}
	if custom.Timeout != time.Second {
		t.Errorf("caller's client was mutated: timeout %v", custom.Timeout)
	}
	if client.Transport != custom.Transport {
		t.Error("expected custom Transport preserved on clone")
	}

	// nil timeout: caller's timeout stays untouched.
	cfg = &config{httpClient: custom}
	client = resolveHTTPClient(cfg)
	if client != custom {
		t.Error("expected the caller's client returned unchanged")
	}
}

func durationPtr(d time.Duration) *time.Duration { return &d }

func TestNormalizeSafeSearchOff(t *testing.T) {
	if got := normalizeSafeSearch(" OFF "); got != "off" {
		t.Errorf("expected 'off', got %q", got)
	}
}

func TestSearchToolUnreachableEndpoint(t *testing.T) {
	// A RoundTripper returning a fixed error keeps the request-execution
	// error path deterministic — no DNS or proxy configuration involved.
	boom := errors.New("boom: transport failure")
	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL("https://example.com/v1/search"),
		WithHTTPClient(&http.Client{
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, boom
			}),
		}),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "unreachable"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected error for unreachable endpoint, got nil")
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if !strings.Contains(resp.Error, "execute request") {
		t.Errorf("unexpected error field: %q", resp.Error)
	}
}

func TestSearchToolInvalidJSONResponse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":`)) // truncated JSON
		},
	))
	defer srv.Close()

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "anything"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	_, err = searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("expected decode response error, got %v", err)
	}
}

func TestSearchToolCapsCombinedSections(t *testing.T) {
	// The API treats `count` as a per-section limit, so both sections can
	// each return up to `count` entries. The tool must cap the combined
	// list at numResults so the result-count contract holds.
	web := make([]youcomAPIResult, 0, 4)
	for i := 0; i < 4; i++ {
		web = append(web, youcomAPIResult{
			URL:   fmt.Sprintf("https://example.com/web/%d", i),
			Title: fmt.Sprintf("Web %d", i),
		})
	}
	news := make([]youcomAPIResult, 0, 4)
	for i := 0; i < 4; i++ {
		news = append(news, youcomAPIResult{
			URL:         fmt.Sprintf("https://example.com/news/%d", i),
			Title:       fmt.Sprintf("News %d", i),
			Description: fmt.Sprintf("News description %d", i),
		})
	}
	srv, _ := newFakeYoucomAPI(t, web, news)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
		WithNumResults(5),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "sections"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("search call failed: %v", err)
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(resp.Results))
	}
	// Web results come first, then news up to the cap.
	for i, item := range resp.Results[:4] {
		if want := fmt.Sprintf("Web %d", i); item.Title != want {
			t.Errorf("result %d: expected title %q, got %q", i, want, item.Title)
		}
	}
	if resp.Results[4].Title != "News 0" {
		t.Errorf("result 4: expected title %q, got %q", "News 0", resp.Results[4].Title)
	}
}

func TestSearchToolCapsCombinedSectionsWithInvalidEntries(t *testing.T) {
	// Entries without a URL or title are dropped and do not consume the
	// result budget, so the cap applies to valid entries only.
	web := []youcomAPIResult{
		{URL: "https://example.com/web/0", Title: "Web 0"},
		{}, // invalid: no URL and no title
		{URL: "https://example.com/web/1", Title: "Web 1"},
	}
	news := []youcomAPIResult{
		{URL: "https://example.com/news/0", Title: "News 0", Description: "d0"},
		{}, // invalid: no URL and no title
		{URL: "https://example.com/news/1", Title: "News 1", Description: "d1"},
	}
	srv, _ := newFakeYoucomAPI(t, web, news)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithHTTPClient(fakeTLSClient()),
		WithNumResults(2),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "invalid entries"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("search call failed: %v", err)
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Title != "Web 0" || resp.Results[1].Title != "Web 1" {
		t.Errorf("unexpected results: %q, %q", resp.Results[0].Title, resp.Results[1].Title)
	}
}

func TestNewToolSetRejectsHostlessBaseURL(t *testing.T) {
	for _, raw := range []string{
		"https://",
		"https:foo",
		"https:///v1/search",
	} {
		_, err := NewToolSet(
			WithAPIKey("key-123"),
			WithBaseURL(raw),
		)
		if err == nil {
			t.Errorf("expected error for hostless base URL %q, got nil", raw)
			continue
		}
		if !strings.Contains(err.Error(), "host") {
			t.Errorf("expected host error for %q, got %v", raw, err)
		}
	}
}

func TestToolDeclarationSchema(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	decl := toolSet.tools[0].Declaration()
	if decl == nil {
		t.Fatalf("expected non-nil declaration")
	}
	if decl.Name != "search" {
		t.Errorf("expected tool name %q, got %q", "search", decl.Name)
	}
	props := decl.InputSchema.Properties
	if len(props) != 4 {
		t.Fatalf("expected 4 properties, got %d", len(props))
	}
	if props["query"] == nil {
		t.Fatalf("missing query property")
	}
	if got, want := props["num_results"].Description, "Maximum number of results to return (default 10 max 10)"; got != want {
		t.Errorf("num_results description: got %q, want %q", got, want)
	}
	if got, want := props["country"].Description, "Country code used to bias results (e.g. US or DE)"; got != want {
		t.Errorf("country description: got %q, want %q", got, want)
	}
	safe := props["safe_search"]
	if safe == nil {
		t.Fatalf("missing safe_search property")
	}
	if got, want := safe.Description, "Safe search strictness"; got != want {
		t.Errorf("safe_search description: got %q, want %q", got, want)
	}
	if len(safe.Enum) != 3 || safe.Enum[0] != "strict" || safe.Enum[1] != "moderate" || safe.Enum[2] != "off" {
		t.Errorf("safe_search enum: got %v, want [strict moderate off]", safe.Enum)
	}
}
