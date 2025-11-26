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
	"fmt"
	"time"

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

// content define an article content download from pdf
type content struct {
	Page int    `json:"page"`
	Text string `json:"text"`
}

// article define an article
type article struct {
	Title           string    `json:"title"`
	ID              string    `json:"id"`
	EntryID         string    `json:"entry_id"`
	Authors         []string  `json:"authors"`
	PrimaryCategory string    `json:"primary_category"`
	Categories      []string  `json:"categories"`
	Published       string    `json:"published"`
	PdfURL          string    `json:"pdf_url"`
	Links           []string  `json:"links"`
	Summary         string    `json:"summary"`
	Comment         string    `json:"comment"`
	Content         []content `json:"content"`
}

// searchRequest define an arxiv search request
type searchRequest struct {
	Search          arxiv.Search `json:"search" jsonschema:"description=Search query"`
	ReadArxivPapers bool         `json:"read_arxiv_papers" jsonschema:"description=Whether to read the content from PDF"`
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

func (t *ToolSet) search(ctx context.Context, req searchRequest) ([]article, error) {
	if len(req.Search.Query) == 0 && len(req.Search.IDList) == 0 {
		return nil, fmt.Errorf("query or id list is empty")
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
			for pageNum, doc := range documents {
				arti.Content = append(arti.Content, content{
					Page: pageNum + 1,
					Text: doc.Content,
				})
			}
		}
		articles = append(articles, arti)
	}
	return articles, nil
}
