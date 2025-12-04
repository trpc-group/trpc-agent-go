//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/googleapi"
)

// searchRequest represents the input for the Google search tool.
type searchRequest struct {
	Query  string `json:"query" jsonschema:"description=The search query to execute on Google"`
	Size   int    `json:"size" jsonschema:"description=The number of results to return"`
	Offset int    `json:"offset" jsonschema:"description=The offset of the results to return"`
	Lang   string `json:"lang" jsonschema:"description=The language of the results to return(en/ja/zh-CN/etc)"`
}

// search executes a search query on Google and returns the results.
func (t *ToolSet) search(ctx context.Context, req searchRequest) (result, error) {
	if req.Query == "" {
		return result{}, fmt.Errorf("query is empty")
	}
	cseCall := t.srv.Cse.List().Context(ctx).Cx(t.cfg.engineID).Q(req.Query)

	if req.Size > 0 {
		cseCall = cseCall.Num(int64(req.Size))
	} else {
		cseCall = cseCall.Num(int64(t.cfg.size))
	}
	if req.Lang != "" {
		cseCall = cseCall.Hl(req.Lang)
	} else if t.cfg.lang != "" {
		cseCall = cseCall.Hl(t.cfg.lang)
	}
	if req.Offset > 0 {
		cseCall = cseCall.Start(int64(req.Offset))
	} else if t.cfg.offset > 0 {
		cseCall = cseCall.Start(int64(t.cfg.offset))
	}

	resp, err := cseCall.Do()
	if err != nil {
		return result{}, err
	}
	simpleItems := make([]*simplifiedSearchItem, 0, len(resp.Items))
	for _, item := range resp.Items {
		ssi := &simplifiedSearchItem{
			Link:    item.Link,
			Title:   item.Title,
			Snippet: item.Snippet,
		}
		desc, okk, err := getDescFromPageMap(item.Pagemap)
		if err != nil {
			return result{}, err
		}
		if okk {
			ssi.Desc = desc
		}

		simpleItems = append(simpleItems, ssi)
	}

	sr := result{
		Query: getQuery(resp.Queries.Request),
		Items: simpleItems,
	}

	return sr, nil
}

func getQuery(reqs []*customsearch.SearchQueriesRequest) string {
	var sb strings.Builder
	isFirst := true
	for _, r := range reqs {
		if !isFirst {
			sb.WriteString(" ")
		}
		sb.WriteString(r.SearchTerms)
		isFirst = false
	}

	return sb.String()
}

func getDescFromPageMap(pageMap googleapi.RawMessage) (string, bool, error) {

	var pages map[string]any
	err := json.Unmarshal(pageMap, &pages)
	if err != nil {
		return "", false, fmt.Errorf("json.Unmarshal Pagemap failed: %w", err)
	}
	const (
		metaTagsKey = "metatags"
		descTag     = "description"
		ogDescTag   = "og:description"
	)
	metaTags, ok := pages[metaTagsKey].([]any)
	if !ok {
		return "", false, nil
	}

	var siteDesc strings.Builder
	foundDesc := false

	for _, mt := range metaTags {
		metas, okk := mt.(map[string]any)
		if !okk {
			continue
		}

		if desc, okk := metas[descTag].(string); okk {
			if foundDesc {
				siteDesc.WriteString("\n")
			}
			siteDesc.WriteString(desc)

			foundDesc = true
			continue
		}

		if desc, okk := metas[ogDescTag].(string); okk {
			if foundDesc {
				siteDesc.WriteString("\n")
			}
			siteDesc.WriteString(desc)

			foundDesc = true
		}
	}

	return siteDesc.String(), foundDesc, nil
}

// result represents the search results from Google Search.
type result struct {
	Query string                  `json:"query,omitempty"`
	Items []*simplifiedSearchItem `json:"items"`
}

// simplifiedSearchItem represents a simplified search item.
type simplifiedSearchItem struct {
	Link    string `json:"link"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Desc    string `json:"desc,omitempty"`
}
