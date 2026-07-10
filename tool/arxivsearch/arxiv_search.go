//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package arxivsearch provides an arxiv search tool.
package arxivsearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch/internal/arxiv"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	// defaultArxivConfig is the default arxiv client config
	defaultArxivConfig = arxiv.ClientConfig{
		PageSize:     5,
		DelaySeconds: 1,
		NumRetries:   3,
	}
	// maxResults is the maximum number of articles to return
	maxResults = 5
)

const (
	defaultArticleContentRunes = 20000
	maxArticleContentRunes     = 200000
	errEmptySearch             = "query, id list, or submitted date is required"
)

// content define an article content download from pdf
type content struct {
	Page      int    `json:"page"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// article define an article
type article struct {
	Title                string    `json:"title"`
	ID                   string    `json:"id"`
	EntryID              string    `json:"entry_id"`
	Authors              []string  `json:"authors"`
	PrimaryCategory      string    `json:"primary_category"`
	Categories           []string  `json:"categories"`
	Published            string    `json:"published"`
	PdfURL               string    `json:"pdf_url"`
	Links                []string  `json:"links"`
	Summary              string    `json:"summary"`
	Comment              string    `json:"comment"`
	Content              []content `json:"content"`
	ContentRunes         int       `json:"content_runes,omitempty"`
	ReturnedContentRunes int       `json:"returned_content_runes,omitempty"`
	ContentTruncated     bool      `json:"content_truncated,omitempty"`
}

// searchRequest define an arxiv search request
type searchRequest struct {
	Search          arxiv.Search `json:"search" jsonschema:"description=Search query"`
	ReadArxivPapers bool         `json:"read_arxiv_papers" jsonschema:"description=Whether to read the content from PDF"`
	// MaxContentRunes limits PDF text returned per article.
	// It defaults to 20000 and is capped at 200000.
	MaxContentRunes int `json:"max_content_runes,omitempty"`
}

// Option define an option for arxiv tool
type Option func(config *arxiv.ClientConfig)

// WithBaseURL set the base url for arxiv tool
func WithBaseURL(baseURL string) Option {
	return func(config *arxiv.ClientConfig) {
		config.BaseURL = baseURL
	}
}

// WithPageSize set the page size for arxiv tool
func WithPageSize(pageSize int) Option {
	return func(config *arxiv.ClientConfig) {
		config.PageSize = pageSize
	}
}

// WithDelaySeconds set the delay seconds for arxiv tool
func WithDelaySeconds(delaySeconds time.Duration) Option {
	return func(config *arxiv.ClientConfig) {
		config.DelaySeconds = delaySeconds
	}
}

// WithNumRetries set the num retries for arxiv tool
func WithNumRetries(numRetries int) Option {
	return func(config *arxiv.ClientConfig) {
		config.NumRetries = numRetries
	}
}

// WithHTTPClient sets the underlying HTTP client used to call the arXiv API.
// When combined with WithTimeout, the caller's *http.Client is never mutated -
// a shallow copy is used to apply timeout overrides, preserving custom
// Transport/Proxy/Jar settings. Passing nil falls back to a default client
// with the default 30s timeout.
func WithHTTPClient(c *http.Client) Option {
	return func(config *arxiv.ClientConfig) {
		config.HTTPClient = c
	}
}

// WithTimeout sets the HTTP request timeout. When combined with WithHTTPClient,
// the custom client's Transport/Proxy/Jar settings are preserved via shallow
// copy - the caller's original *http.Client is never mutated.
// Passing 0 explicitly disables the default 30s timeout (Go http.Client
// treats Timeout==0 as "no timeout"). Negative values are ignored.
func WithTimeout(timeout time.Duration) Option {
	return func(config *arxiv.ClientConfig) {
		if timeout < 0 {
			return
		}
		config.Timeout = &timeout
	}
}

// ToolSet represent an arxiv search tool
type ToolSet struct {
	name   string
	cfg    *arxiv.ClientConfig
	client *arxiv.Client
	tools  []tool.Tool
}

func (t *ToolSet) Tools(ctx context.Context) []tool.Tool {
	return t.tools
}

// Close implements the ToolSet interface.
func (t *ToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// Name implements the ToolSet interface.
func (t *ToolSet) Name() string {
	return t.name
}

// NewToolSet creates a new ArXiv search tool with the provided options.
func NewToolSet(opts ...Option) (*ToolSet, error) {
	t := &ToolSet{
		name: "arxiv_search",
		cfg: &arxiv.ClientConfig{
			BaseURL:      defaultArxivConfig.BaseURL,
			PageSize:     defaultArxivConfig.PageSize,
			DelaySeconds: defaultArxivConfig.DelaySeconds,
			NumRetries:   defaultArxivConfig.NumRetries,
		},
	}
	for _, opt := range opts {
		opt(t.cfg)
	}
	t.client = arxiv.NewClient(*t.cfg)
	var tools []tool.Tool
	tools = append(tools, function.NewFunctionTool(
		t.search,
		function.WithName("search"),
		function.WithDescription("A helpful AI assistant with access to ArXiv scholarly article repository. "+
			"ArXiv is a free distribution service and an open-access archive for nearly 2.4 million scholarly articles in "+
			"the fields of physics, mathematics, computer science, quantitative biology, quantitative finance, statistics, "+
			"electrical engineering and systems science, and economics. "+
			"Returns a list of articles, containing title, authors, primary category, categories, published date, PDF URL, "+
			"links, summary, comment, and content (if read_arxiv_papers is true)."),
	))
	t.tools = tools
	return t, nil
}

func (t *ToolSet) search(
	ctx context.Context,
	req searchRequest,
) ([]article, error) {
	if isEmptySearch(req.Search) {
		return nil, errors.New(errEmptySearch)
	}
	if req.Search.MaxResults == nil {
		req.Search.MaxResults = &maxResults
	}
	results, err := t.client.Search(req.Search)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	articles := make([]article, 0, len(results))
	var pdfReader reader.Reader
	maxContentRunes := normalizedMaxContentRunes(req.MaxContentRunes)
	if req.ReadArxivPapers {
		pdfReader = pdf.New()
	}
	for _, result := range results {
		arti := article{
			Title:           result.Title,
			ID:              result.GetShortID(),
			EntryID:         result.EntryID,
			PrimaryCategory: result.PrimaryCategory,
			Categories:      result.Categories,
			Published:       result.Published.Format(time.RFC3339),
			PdfURL:          result.PdfURL,
			Summary:         result.Summary,
			Comment:         result.Comment,
		}
		for _, author := range result.Authors {
			arti.Authors = append(arti.Authors, author.Name)
		}
		for _, link := range result.Links {
			arti.Links = append(arti.Links, link.Href)
		}
		if !result.Published.IsZero() {
			arti.Published = result.Published.Format(time.RFC3339)
		}
		if req.ReadArxivPapers {
			documents, err := pdfReader.ReadFromURL(result.PdfURL)
			if err != nil {
				return nil, fmt.Errorf("failed to read PDF from URL: %w", err)
			}
			appendArticleContent(&arti, documents, maxContentRunes)
		}
		articles = append(articles, arti)
	}
	return articles, nil
}

func isEmptySearch(search arxiv.Search) bool {
	return len(search.Query) == 0 &&
		len(search.IDList) == 0 &&
		len(search.SubmittedDateFrom) == 0 &&
		len(search.SubmittedDateTo) == 0
}

func normalizedMaxContentRunes(maxRunes int) int {
	if maxRunes <= 0 {
		return defaultArticleContentRunes
	}
	if maxRunes > maxArticleContentRunes {
		return maxArticleContentRunes
	}
	return maxRunes
}

func appendArticleContent(
	arti *article,
	documents []*document.Document,
	maxRunes int,
) {
	if arti == nil {
		return
	}
	remaining := maxRunes
	for pageNum, doc := range documents {
		if doc == nil {
			continue
		}
		text := doc.Content
		contentRunes := utf8.RuneCountInString(text)
		arti.ContentRunes += contentRunes
		page := content{
			Page: pageNum + 1,
			Text: text,
		}
		if remaining <= 0 {
			if contentRunes > 0 {
				arti.ContentTruncated = true
			}
			continue
		}
		if contentRunes > remaining {
			page.Text = truncateRunes(text, remaining)
			page.Truncated = true
			arti.ContentTruncated = true
		}
		returnedRunes := utf8.RuneCountInString(page.Text)
		arti.ReturnedContentRunes += returnedRunes
		remaining -= returnedRunes
		arti.Content = append(arti.Content, page)
	}
}

func truncateRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	for idx := range text {
		if maxRunes == 0 {
			return text[:idx]
		}
		maxRunes--
	}
	return text
}
