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
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClient_buildQueryURL tests the buildQueryURL method of the Client struct.
func TestClient_buildQueryURL(t *testing.T) {
	type fields struct {
		baseURL     string
		config      ClientConfig
		httpClient  *http.Client
		lastRequest *time.Time
	}
	type args struct {
		search     Search
		start      int
		maxResults int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "normal case with all fields",
			fields: fields{
				baseURL: "http://arxiv.org",
			},
			args: args{
				search: Search{
					Query:     "quantum computing",
					IDList:    []string{"1234.5678", "8765.4321"},
					SortBy:    SortCriterionRelevance,
					SortOrder: SortOrderDescending,
				},
				start:      0,
				maxResults: 10,
			},
			want: "http://arxiv.org?id_list=1234.5678%2C8765.4321&max_results=10&search_query=quantum+computing&sortBy=relevance&sortOrder=descending&start=0",
		},
		{
			name: "empty IDList",
			fields: fields{
				baseURL: "http://arxiv.org",
			},
			args: args{
				search: Search{
					Query:     "test",
					IDList:    []string{},
					SortBy:    "",
					SortOrder: "",
				},
				start:      5,
				maxResults: 20,
			},
			want: "http://arxiv.org?max_results=20&search_query=test&start=5",
		},
		{
			name: "zero value sort parameters",
			fields: fields{
				baseURL: "http://arxiv.org",
			},
			args: args{
				search: Search{
					Query:     "another query",
					IDList:    []string{},
					SortBy:    "",
					SortOrder: "",
				},
				start:      10,
				maxResults: 100,
			},
			want: "http://arxiv.org?max_results=100&search_query=another+query&start=10",
		},
		{
			name: "negative start value",
			fields: fields{
				baseURL: "http://arxiv.org",
			},
			args: args{
				search: Search{
					Query:     "negative start",
					IDList:    []string{"1234.5678"},
					SortBy:    SortCriterionLastUpdatedDate,
					SortOrder: SortOrderAscending,
				},
				start:      -5,
				maxResults: 5,
			},
			want: "http://arxiv.org?id_list=1234.5678&max_results=5&search_query=negative+start&sortBy=lastUpdatedDate&sortOrder=ascending",
		},
		{
			name: "special characters in query",
			fields: fields{
				baseURL: "http://example.com",
			},
			args: args{
				search: Search{
					Query:     "hello world!",
					IDList:    []string{},
					SortBy:    "",
					SortOrder: "",
				},
				start:      0,
				maxResults: 1,
			},
			want: "http://example.com?max_results=1&search_query=hello+world%21&start=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				BaseURL:     tt.fields.baseURL,
				config:      tt.fields.config,
				httpClient:  tt.fields.httpClient,
				lastRequest: tt.fields.lastRequest,
			}
			got, err := c.buildQueryURL(tt.args.search, tt.args.start, tt.args.maxResults)
			if (err != nil) != tt.wantErr {
				t.Errorf("Client.buildQueryURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Client.buildQueryURL() = \n%v, \nwant \n%v", got, tt.want)
			}
		})
	}
}

// TestNewClient tests the NewClient function.
func TestNewClient(t *testing.T) {
	type args struct {
		config ClientConfig
	}
	tests := []struct {
		name       string
		args       args
		wantConfig ClientConfig
	}{
		{
			name: "normal configuration",
			args: args{
				config: ClientConfig{
					PageSize:     50,
					DelaySeconds: 5 * time.Second,
					NumRetries:   5,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     50,
				DelaySeconds: 5 * time.Second,
				NumRetries:   5,
			},
		},
		{
			name: "zero page size uses default",
			args: args{
				config: ClientConfig{
					PageSize:     0,
					DelaySeconds: 5 * time.Second,
					NumRetries:   5,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     100,
				DelaySeconds: 5 * time.Second,
				NumRetries:   5,
			},
		},
		{
			name: "zero delay seconds uses default",
			args: args{
				config: ClientConfig{
					PageSize:     50,
					DelaySeconds: 0,
					NumRetries:   5,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     50,
				DelaySeconds: 3 * time.Second,
				NumRetries:   5,
			},
		},
		{
			name: "zero num retries uses default",
			args: args{
				config: ClientConfig{
					PageSize:     50,
					DelaySeconds: 5 * time.Second,
					NumRetries:   0,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     50,
				DelaySeconds: 5 * time.Second,
				NumRetries:   3,
			},
		},
		{
			name: "all fields zero use defaults",
			args: args{
				config: ClientConfig{
					PageSize:     0,
					DelaySeconds: 0,
					NumRetries:   0,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     100,
				DelaySeconds: 3 * time.Second,
				NumRetries:   3,
			},
		},
		{
			name: "negative values use defaults",
			args: args{
				config: ClientConfig{
					PageSize:     -10,
					DelaySeconds: -1 * time.Second,
					NumRetries:   -1,
				},
			},
			wantConfig: ClientConfig{
				PageSize:     100,
				DelaySeconds: 3 * time.Second,
				NumRetries:   3,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClient(tt.args.config)

			assert.Equal(t, tt.wantConfig, got.config, "config mismatch")

			assert.Equal(t, baseURL, got.BaseURL, "BaseURL should be default")

			assert.Equal(t, 30*time.Second, got.httpClient.Timeout, "HTTP client timeout mismatch")

			assert.Nil(t, got.lastRequest, "lastRequest should be nil initially")
		})
	}
}

func parseTime(t string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, t)
	return parsed
}

// Test_parseEntry tests the parseEntry function
func Test_parseEntry(t *testing.T) {
	type args struct {
		entry AtomEntry
	}
	tests := []struct {
		name    string
		args    args
		want    Result
		wantErr bool
	}{
		{
			name: "normal case with all fields",
			args: args{
				entry: AtomEntry{
					ID:        "http://arxiv.org/abs/1234.5678v1",
					Updated:   "2023-01-01T12:00:00Z",
					Published: "2023-01-01T10:00:00Z",
					Title:     "  Test Paper  ",
					Summary:   "Test summary  ",
					Authors: []AtomAuthor{
						{Name: "Author1"},
						{Name: "Author2"},
					},
					Categories: []AtomCategory{
						{Term: "cs.AI"},
						{Term: "cs.LG"},
					},
					Links: []AtomLink{
						{
							Href:  "http://arxiv.org/pdf/1234.5678v1",
							Rel:   "related",
							Type:  "application/pdf",
							Title: "pdf",
						},
						{
							Href:  "http://example.com/html",
							Rel:   "alternate",
							Type:  "text/html",
							Title: "html",
						},
					},
				},
			},
			want: Result{
				EntryID:   "1234.5678v1",
				Updated:   parseTime("2023-01-01T12:00:00Z"),
				Published: parseTime("2023-01-01T10:00:00Z"),
				Title:     "Test Paper",
				Summary:   "Test summary",
				Authors: []Author{
					{Name: "Author1"},
					{Name: "Author2"},
				},
				Categories:      []string{"cs.AI", "cs.LG"},
				PrimaryCategory: "cs.AI",
				Links: []Link{
					{
						Href:        "http://arxiv.org/pdf/1234.5678v1",
						Title:       "pdf",
						Rel:         "related",
						ContentType: "application/pdf",
					},
					{
						Href:        "http://example.com/html",
						Title:       "html",
						Rel:         "alternate",
						ContentType: "text/html",
					},
				},
				PdfURL: "http://arxiv.org/pdf/1234.5678v1",
			},
			wantErr: false,
		},
		{
			name: "ID without arxiv.org/abs/",
			args: args{
				entry: AtomEntry{
					ID:         "http://example.com/1234",
					Updated:    "2023-01-01T12:00:00Z",
					Published:  "2023-01-01T10:00:00Z",
					Title:      "Test Paper",
					Summary:    "Test summary",
					Authors:    []AtomAuthor{{Name: "Author1"}},
					Categories: []AtomCategory{{Term: "cs.AI"}},
					Links:      []AtomLink{},
				},
			},
			want: Result{
				EntryID:         "http://example.com/1234",
				Updated:         parseTime("2023-01-01T12:00:00Z"),
				Published:       parseTime("2023-01-01T10:00:00Z"),
				Title:           "Test Paper",
				Summary:         "Test summary",
				Authors:         []Author{{Name: "Author1"}},
				Categories:      []string{"cs.AI"},
				PrimaryCategory: "cs.AI",
				Links:           []Link{},
				PdfURL:          "",
			},
			wantErr: false,
		},
		{
			name: "no PDF link",
			args: args{
				entry: AtomEntry{
					ID:         "http://arxiv.org/abs/1234.5678v1",
					Updated:    "2023-01-01T12:00:00Z",
					Published:  "2023-01-01T10:00:00Z",
					Title:      "Test Paper",
					Summary:    "Test summary",
					Authors:    []AtomAuthor{{Name: "Author1"}},
					Categories: []AtomCategory{{Term: "cs.AI"}},
					Links: []AtomLink{
						{
							Href:  "http://example.com/html",
							Rel:   "alternate",
							Type:  "text/html",
							Title: "html",
						},
					},
				},
			},
			want: Result{
				EntryID:         "1234.5678v1",
				Updated:         parseTime("2023-01-01T12:00:00Z"),
				Published:       parseTime("2023-01-01T10:00:00Z"),
				Title:           "Test Paper",
				Summary:         "Test summary",
				Authors:         []Author{{Name: "Author1"}},
				Categories:      []string{"cs.AI"},
				PrimaryCategory: "cs.AI",
				Links: []Link{
					{
						Href:        "http://example.com/html",
						Title:       "html",
						Rel:         "alternate",
						ContentType: "text/html",
					},
				},
				PdfURL: "",
			},
			wantErr: false,
		},
		{
			name: "multiple PDF links",
			args: args{
				entry: AtomEntry{
					ID:         "http://arxiv.org/abs/1234.5678v1",
					Updated:    "2023-01-01T12:00:00Z",
					Published:  "2023-01-01T10:00:00Z",
					Title:      "Test Paper",
					Summary:    "Test summary",
					Authors:    []AtomAuthor{{Name: "Author1"}},
					Categories: []AtomCategory{{Term: "cs.AI"}},
					Links: []AtomLink{
						{
							Href: "first.pdf",
							Rel:  "related",
							Type: "application/pdf",
						},
						{
							Href: "second.pdf",
							Rel:  "related",
							Type: "application/pdf",
						},
					},
				},
			},
			want: Result{
				EntryID:         "1234.5678v1",
				Updated:         parseTime("2023-01-01T12:00:00Z"),
				Published:       parseTime("2023-01-01T10:00:00Z"),
				Title:           "Test Paper",
				Summary:         "Test summary",
				Authors:         []Author{{Name: "Author1"}},
				Categories:      []string{"cs.AI"},
				PrimaryCategory: "cs.AI",
				Links: []Link{
					{
						Href:        "first.pdf",
						Rel:         "related",
						ContentType: "application/pdf",
					},
					{
						Href:        "second.pdf",
						Rel:         "related",
						ContentType: "application/pdf",
					},
				},
				PdfURL: "second.pdf",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEntry(tt.args.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseEntry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestClient_Search tests the Search method of the Client.
func TestClient_Search(t *testing.T) {
	t.Run("normal case", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			feed := AtomFeed{
				TotalResults: "2",
				Entries: []AtomEntry{
					{
						ID:         "http://arxiv.org/abs/1234.5678v1",
						Title:      "Test Paper 1",
						Summary:    "Summary 1",
						Authors:    []AtomAuthor{{Name: "Author1"}},
						Categories: []AtomCategory{{Term: "cs.AI"}},
						Links: []AtomLink{
							{Href: "http://arxiv.org/pdf/1234.5678v1", Rel: "related", Type: "application/pdf"},
						},
					},
					{
						ID:         "http://arxiv.org/abs/8765.4321v1",
						Title:      "Test Paper 2",
						Summary:    "Summary 2",
						Authors:    []AtomAuthor{{Name: "Author2"}},
						Categories: []AtomCategory{{Term: "cs.LG"}},
						Links: []AtomLink{
							{Href: "http://arxiv.org/pdf/8765.4321v1", Rel: "related", Type: "application/pdf"},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/atom+xml")
			xml.NewEncoder(w).Encode(feed)
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     DefaultConfig(),
			httpClient: server.Client(),
		}

		search := NewSearch("test")
		results, err := client.Search(search)

		require.NoError(t, err)
		require.Len(t, results, 2)

		result1 := results[0]
		assert.Equal(t, "1234.5678v1", result1.EntryID)
		assert.Equal(t, "Test Paper 1", result1.Title)
		assert.Equal(t, "Summary 1", result1.Summary)
		assert.Equal(t, "cs.AI", result1.PrimaryCategory)
		assert.Equal(t, "http://arxiv.org/pdf/1234.5678v1", result1.PdfURL)

		result2 := results[1]
		assert.Equal(t, "8765.4321v1", result2.EntryID)
		assert.Equal(t, "Test Paper 2", result2.Title)
	})

	t.Run("max results with pagination", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start, _ := strconv.Atoi(r.URL.Query().Get("start"))
			if start == 0 {
				feed := AtomFeed{
					TotalResults: "5",
					Entries: []AtomEntry{
						{ID: "1", Title: "Test1"},
						{ID: "2", Title: "Test2"},
					},
				}
				xml.NewEncoder(w).Encode(feed)
			} else if start == 2 {
				feed := AtomFeed{
					TotalResults: "5",
					Entries: []AtomEntry{
						{ID: "3", Title: "Test3"},
						{ID: "4", Title: "Test4"},
						{ID: "5", Title: "Test5"},
					},
				}
				xml.NewEncoder(w).Encode(feed)
			}
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     ClientConfig{PageSize: 2, NumRetries: 3},
			httpClient: server.Client(),
		}

		maxResults := 3
		search := NewSearch("test", WithMaxResults(maxResults))
		results, err := client.Search(search)

		require.NoError(t, err)
		require.Len(t, results, 3)
		assert.Equal(t, "1", results[0].EntryID)
		assert.Equal(t, "2", results[1].EntryID)
		assert.Equal(t, "3", results[2].EntryID)
	})

	t.Run("fetch page error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     ClientConfig{NumRetries: 3},
			httpClient: server.Client(),
		}

		search := NewSearch("test")
		_, err := client.Search(search)

		assert.Error(t, err)
	})

	t.Run("parse entry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			feed := AtomFeed{
				TotalResults: "1",
				Entries: []AtomEntry{
					{Title: "Test"},
				},
			}
			xml.NewEncoder(w).Encode(feed)
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     DefaultConfig(),
			httpClient: server.Client(),
		}

		search := NewSearch("test")
		results, err := client.Search(search)

		require.NoError(t, err)
		assert.Len(t, results, 1)
	})

	t.Run("test limit", func(t *testing.T) {
		delay := 2 * time.Second
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			feed := AtomFeed{
				TotalResults: "2",
				Entries: []AtomEntry{
					{ID: "1", Title: "Test1"},
					{ID: "2", Title: "Test2"},
				},
			}
			xml.NewEncoder(w).Encode(feed)
		}))
		defer server.Close()

		client := &Client{
			BaseURL:     server.URL,
			config:      ClientConfig{DelaySeconds: delay, PageSize: 2},
			httpClient:  server.Client(),
			lastRequest: nil,
		}

		_, err := client.Search(NewSearch("test"))
		assert.Error(t, err)
	})

	t.Run("retry", func(t *testing.T) {
		attempt := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempt++
			if attempt < 3 {
				w.WriteHeader(http.StatusInternalServerError)
			} else {
				feed := AtomFeed{
					TotalResults: "1",
					Entries: []AtomEntry{
						{ID: "1", Title: "Test"},
					},
				}
				xml.NewEncoder(w).Encode(feed)
			}
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     ClientConfig{NumRetries: 3},
			httpClient: server.Client(),
		}

		_, err := client.Search(NewSearch("test"))
		require.NoError(t, err)
		assert.Equal(t, 3, attempt)
	})

	t.Run("max results nil uses page size", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "100", r.URL.Query().Get("max_results"))
			feed := AtomFeed{
				TotalResults: "100",
				Entries:      make([]AtomEntry, 100),
			}
			xml.NewEncoder(w).Encode(feed)
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     ClientConfig{PageSize: 100},
			httpClient: server.Client(),
		}

		search := NewSearch("test")
		_, err := client.Search(search)

		assert.Error(t, err)
	})

	t.Run("empty entries on subsequent page", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start, _ := strconv.Atoi(r.URL.Query().Get("start"))
			if start == 0 {
				feed := AtomFeed{
					TotalResults: "3",
					Entries: []AtomEntry{
						{ID: "1", Title: "Test1"},
					},
				}
				xml.NewEncoder(w).Encode(feed)
			} else {
				feed := AtomFeed{
					TotalResults: "3",
					Entries:      []AtomEntry{},
				}
				xml.NewEncoder(w).Encode(feed)
			}
		}))
		defer server.Close()

		client := &Client{
			BaseURL:    server.URL,
			config:     ClientConfig{PageSize: 1, NumRetries: 3},
			httpClient: server.Client(),
		}

		search := NewSearch("test", WithMaxResults(3))
		results, err := client.Search(search)

		require.NoError(t, err)
		assert.Len(t, results, 1)
	})
}
