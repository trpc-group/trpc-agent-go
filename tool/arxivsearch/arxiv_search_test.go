//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package arxivsearch

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch/internal/arxiv"
)

func newTestPDF(t *testing.T) []byte {
	t.Helper()

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetFont("Helvetica", "", 12)
	pdf.AddPage()
	pdf.Cell(40, 10, "Hello World")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("failed to generate test PDF: %v", err)
	}
	return buf.Bytes()
}

// TestOption tests the functional options for configuring the Arxiv client.
func TestOption(t *testing.T) {
	type testCase struct {
		name    string
		config  arxiv.ClientConfig
		wantCfg arxiv.ClientConfig
		wantErr bool
	}

	tests := []testCase{
		{
			name: "default config values",
			config: arxiv.ClientConfig{
				PageSize:     100,
				DelaySeconds: 3 * time.Second,
				NumRetries:   3,
			},
			wantCfg: arxiv.ClientConfig{
				PageSize:     100,
				DelaySeconds: 3 * time.Second,
				NumRetries:   3,
			},
		},
		{
			name: "custom valid config",
			config: arxiv.ClientConfig{
				PageSize:     50,
				DelaySeconds: 5 * time.Second,
				NumRetries:   5,
			},
			wantCfg: arxiv.ClientConfig{
				PageSize:     50,
				DelaySeconds: 5 * time.Second,
				NumRetries:   5,
			},
		},
		{
			name: "partial default config",
			config: arxiv.ClientConfig{
				PageSize:     1,
				DelaySeconds: 3 * time.Second,
				NumRetries:   0,
			},
			wantCfg: arxiv.ClientConfig{
				PageSize:     1,
				DelaySeconds: 3 * time.Second,
				NumRetries:   0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opts := []Option{
				WithBaseURL(tt.config.BaseURL),
				WithDelaySeconds(tt.config.DelaySeconds),
				WithPageSize(tt.config.PageSize),
				WithNumRetries(tt.config.NumRetries),
			}

			tool := &ToolSet{cfg: &arxiv.ClientConfig{}}

			for _, opt := range opts {
				opt(tool.cfg)
			}
			assert.Equal(t, tt.wantCfg, *tool.cfg, "config mismatch")
		})
	}
}

func newTestContext() context.Context {
	return context.Background()
}

func defaultSearchRequest() searchRequest {
	return searchRequest{
		Search: arxiv.Search{
			Query: "machine learning",
		},
		ReadArxivPapers: false,
	}
}

// Test_arxivTool_search tests the search functionality of the ToolSet.
func Test_arxivTool_search(t *testing.T) {
	defaultCtx := newTestContext()
	defaultReq := defaultSearchRequest()
	defaultReq.Search.MaxResults = &[]int{10}[0]

	now := time.Now()

	pdfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(newTestPDF(t))
	}))
	defer pdfServer.Close()

	createFeedXML := func(entries []string) string {
		entriesXML := strings.Join(entries, "")
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>ArXiv Query: search_query=all</title>
  <id>http://arxiv.org/api/query?search_query=all</id>
  <updated>%s</updated>
  <opensearch:totalResults xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/">%d</opensearch:totalResults>
  <opensearch:startIndex xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/">0</opensearch:startIndex>
  <opensearch:itemsPerPage xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/">%d</opensearch:itemsPerPage>
  %s
</feed>`, now.Format(time.RFC3339), len(entries), len(entries), entriesXML)
	}

	createEntryXML := func(title, id, category, pdfURL string) string {
		return fmt.Sprintf(`<entry>
    <id>http://arxiv.org/abs/%s</id>
    <updated>%s</updated>
    <published>%s</published>
    <title>%s</title>
    <summary>Summary for %s</summary>
    <author>
      <name>Test Author</name>
    </author>
    <category term="%s" scheme="http://arxiv.org/schemas/atom"/>
    <link href="http://arxiv.org/abs/%s" rel="alternate" type="text/html"/>
    <link title="pdf" href="%s" rel="related" type="application/pdf"/>
  </entry>`, id, now.Format(time.RFC3339), now.Format(time.RFC3339), title, title, category, id, pdfURL)
	}

	tests := []struct {
		name         string
		req          searchRequest
		setupServer  func() *httptest.Server
		wantErr      bool
		wantArticles int
		wantContent  []content
	}{
		{
			name: "normal case with query",
			req:  defaultReq,
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					entryXML := createEntryXML("Test Paper", "1234.5678v1", "cs.AI", "http://example.com/1.pdf")
					feedXML := createFeedXML([]string{entryXML})
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantArticles: 1,
		},
		{
			name: "read pdf content",
			req: searchRequest{
				Search: arxiv.Search{
					Query: "pdf test",
				},
				ReadArxivPapers: true,
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					entryXML := createEntryXML("PDF Test Paper", "5678.9012v1", "cs.CV", pdfServer.URL+"/simple.pdf")
					feedXML := createFeedXML([]string{entryXML})
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantArticles: 1,
			wantContent: []content{
				{
					Page: 1,
					Text: "Hello World",
				},
			},
		},
		{
			name: "empty query and id list",
			req:  searchRequest{},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					feedXML := createFeedXML([]string{})
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantErr: true,
		},
		{
			name: "search returns error",
			req:  defaultReq,
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}))
			},
			wantErr: true,
		},
		{
			name: "nil max results uses default",
			req: searchRequest{
				Search: arxiv.Search{
					Query: "default max",
				},
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					queryParams := r.URL.Query()
					maxResults := queryParams.Get("max_results")
					assert.Equal(t, "5", maxResults)

					feedXML := createFeedXML([]string{})
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantArticles: 0,
		},
		{
			name: "max results functionality",
			req: searchRequest{
				Search: arxiv.Search{
					Query:      "max results test",
					MaxResults: &[]int{3}[0],
				},
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					queryParams := r.URL.Query()
					maxResults := queryParams.Get("max_results")
					assert.Equal(t, "3", maxResults)

					entryXML1 := createEntryXML("Paper 1", "1111.1111v1", "cs.AI", "http://example.com/1.pdf")
					entryXML2 := createEntryXML("Paper 2", "2222.2222v1", "cs.CV", "http://example.com/2.pdf")
					entryXML3 := createEntryXML("Paper 3", "3333.3333v1", "cs.NE", "http://example.com/3.pdf")
					feedXML := createFeedXML([]string{entryXML1, entryXML2, entryXML3})
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantArticles: 3,
		},
		{
			name: "pagination functionality",
			req: searchRequest{
				Search: arxiv.Search{
					Query:      "pagination test",
					MaxResults: &[]int{5}[0],
				},
			},
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					queryParams := r.URL.Query()
					start := queryParams.Get("start")

					var entries []string
					switch start {
					case "0":
						entries = []string{
							createEntryXML("Paper 1", "1111.1111v1", "cs.AI", "http://example.com/1.pdf"),
							createEntryXML("Paper 2", "2222.2222v1", "cs.CV", "http://example.com/2.pdf"),
							createEntryXML("Paper 3", "3333.3333v1", "cs.NE", "http://example.com/3.pdf"),
							createEntryXML("Paper 4", "4444.4444v1", "cs.LG", "http://example.com/4.pdf"),
							createEntryXML("Paper 5", "5555.5555v1", "cs.CL", "http://example.com/5.pdf"),
						}
					case "5":
						entries = []string{
							createEntryXML("Paper 6", "6666.6666v1", "cs.AI", "http://example.com/6.pdf"),
							createEntryXML("Paper 7", "7777.7777v1", "cs.CV", "http://example.com/7.pdf"),
							createEntryXML("Paper 8", "8888.8888v1", "cs.NE", "http://example.com/8.pdf"),
							createEntryXML("Paper 9", "9999.9999v1", "cs.LG", "http://example.com/9.pdf"),
							createEntryXML("Paper 10", "1010.1010v1", "cs.CL", "http://example.com/10.pdf"),
						}
					default:
						entries = []string{}
					}

					feedXML := createFeedXML(entries)
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(feedXML))
				}))
			},
			wantArticles: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			config := arxiv.ClientConfig{
				PageSize:     5,
				DelaySeconds: 1 * time.Second,
				NumRetries:   3,
			}
			client := arxiv.NewClient(config)
			client.BaseURL = server.URL

			a := &ToolSet{
				client: client,
			}

			got, err := a.search(defaultCtx, tt.req)

			if tt.wantErr {
				assert.Error(t, err, "Expected error")
				return
			}

			assert.NoError(t, err, "Unexpected error")
			assert.Len(t, got, tt.wantArticles, "Article count mismatch")

			if tt.wantArticles > 0 {
				article := got[0]
				assert.NotEmpty(t, article.Title, "Title should not be empty")
				assert.NotEmpty(t, article.ID, "Short ID should be generated")
			}
			if tt.req.ReadArxivPapers {
				for _, article := range got {
					assert.Equal(t, article.Content, tt.wantContent)
				}
			}
		})
	}
}

// applyOpts builds a ClientConfig and resolves the final *http.Client via
// the same code path used by NewClient inside NewToolSet.
func applyOpts(opts ...Option) (*arxiv.ClientConfig, *http.Client) {
	cfg := &arxiv.ClientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg, arxiv.NewClient(*cfg).HTTPClient()
}

func TestWithHTTPClient_Set(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second, Transport: http.DefaultTransport}
	_, got := applyOpts(WithHTTPClient(custom))
	assert.Same(t, custom, got, "WithHTTPClient should pass through caller's client (no clone) when no timeout override")
}

func TestWithHTTPClient_NilFallsBackToDefault(t *testing.T) {
	_, got := applyOpts(WithHTTPClient(nil))
	assert.NotNil(t, got)
	assert.Equal(t, 30*time.Second, got.Timeout)
}

func TestWithHTTPClient_NilPlusTimeout(t *testing.T) {
	_, got := applyOpts(WithHTTPClient(nil), WithTimeout(45*time.Second))
	assert.NotNil(t, got)
	assert.Equal(t, 45*time.Second, got.Timeout)
}

func TestWithTimeout_Standalone(t *testing.T) {
	_, got := applyOpts(WithTimeout(60 * time.Second))
	assert.Equal(t, 60*time.Second, got.Timeout)
}

func TestWithTimeout_DoesNotMutateOriginalClient(t *testing.T) {
	original := &http.Client{
		Timeout:   10 * time.Second,
		Transport: http.DefaultTransport,
	}
	_, got := applyOpts(WithHTTPClient(original), WithTimeout(99*time.Second))
	assert.Equal(t, 99*time.Second, got.Timeout)
	assert.Equal(t, 10*time.Second, original.Timeout, "original client timeout must not be mutated")
	assert.Equal(t, http.DefaultTransport, original.Transport, "original client transport must be preserved")
}

func TestWithTimeout_PreservesCustomTransport(t *testing.T) {
	customTransport := &http.Transport{MaxIdleConns: 42}
	original := &http.Client{
		Timeout:   5 * time.Second,
		Transport: customTransport,
	}
	_, got := applyOpts(WithHTTPClient(original), WithTimeout(120*time.Second))
	assert.Equal(t, 120*time.Second, got.Timeout)
	assert.Same(t, customTransport, got.Transport, "custom transport must be preserved (same object) after WithTimeout")
}

func TestWithTimeout_ZeroDisablesDefault(t *testing.T) {
	_, got := applyOpts(WithTimeout(0))
	assert.Equal(t, time.Duration(0), got.Timeout, "WithTimeout(0) should disable default timeout")
}

func TestWithTimeout_NegativeIgnored(t *testing.T) {
	_, got := applyOpts(WithTimeout(-1 * time.Second))
	assert.Equal(t, 30*time.Second, got.Timeout, "negative timeout should be ignored, keeping default")
}

func TestWithTimeout_OrderIndependent(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second, Transport: http.DefaultTransport}
	_, got1 := applyOpts(WithHTTPClient(custom), WithTimeout(60*time.Second))
	_, got2 := applyOpts(WithTimeout(60*time.Second), WithHTTPClient(custom))
	assert.Equal(t, 60*time.Second, got1.Timeout)
	assert.Equal(t, 60*time.Second, got2.Timeout)
	assert.Equal(t, 5*time.Second, custom.Timeout, "original must not be mutated regardless of order")
}

// blockingRoundTripper blocks until the request context is cancelled. This lets
// us exercise the Client.Timeout path deterministically - when the timeout
// fires, net/http cancels the request context and RoundTrip returns its error.
// No time.Sleep, no race, no real server.
//
// The short fail-safe branch guards against a regression where WithTimeout
// stops being applied: without it, the context would never cancel and the test
// would hang until the package-level go test timeout. The fail-safe returns a
// distinct (non-DeadlineExceeded) error so the timeout assertion fails fast.
type blockingRoundTripper struct{}

func (blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(200 * time.Millisecond):
		return nil, fmt.Errorf("blockingRoundTripper fail-safe: request context still active, WithTimeout may not be applied")
	}
}

func TestWithTimeout_EnforcedAtCallPath(t *testing.T) {
	original := &http.Client{
		Timeout:   30 * time.Second,
		Transport: blockingRoundTripper{},
	}
	toolSet, err := NewToolSet(
		WithBaseURL("http://arxiv.invalid/test"),
		WithHTTPClient(original),
		WithTimeout(10*time.Millisecond),
		WithDelaySeconds(time.Nanosecond),
		WithNumRetries(1),
	)
	require.NoError(t, err)

	_, err = toolSet.search(context.Background(), searchRequest{
		Search: arxiv.Search{Query: "anything"},
	})
	require.Error(t, err, "expected timeout error from blocking transport")
	low := strings.ToLower(err.Error())
	assert.True(t,
		strings.Contains(low, "deadline") || strings.Contains(low, "timeout") ||
			strings.Contains(low, "canceled") || strings.Contains(low, "cancelled"),
		"error should reference timeout/deadline/cancel, got: %s", err.Error())
	assert.Equal(t, 30*time.Second, original.Timeout, "original client must remain unmutated")
	assert.Equal(t, http.RoundTripper(blockingRoundTripper{}), original.Transport,
		"original transport must remain unmutated")
}

// TestNewTool tests the NewToolSet function
func TestNewTool(t *testing.T) {
	tool, err := NewToolSet()
	assert.NotNil(t, tool, "Tool should not be nil")
	assert.NoError(t, err, "Unexpected error")

	toolWithConfig, err := NewToolSet(WithBaseURL("http://example.com"))
	assert.NotNil(t, toolWithConfig, "Tool with config should not be nil")
	assert.NoError(t, err, "Unexpected error")
}

// TestClose tests the Close function
func TestClose(t *testing.T) {
	tool, err := NewToolSet()
	assert.NoError(t, err, "Unexpected error")

	err = tool.Close()
	assert.NoError(t, err, "Unexpected error")
}

// TestName tests the Name function
func TestName(t *testing.T) {
	tool, err := NewToolSet()
	assert.NoError(t, err, "Unexpected error")

	assert.Equal(t, tool.Name(), "arxiv_search", "Name mismatch")
}

// TestTools tests the Tools function
func TestNewToolSet(t *testing.T) {
	tool, err := NewToolSet()
	assert.NoError(t, err, "Unexpected error")

	tools := tool.Tools(context.Background())
	assert.Len(t, tools, 1, "Tool count mismatch")
}
