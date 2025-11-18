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
	"net/http"
	"net/http/httptest"
	"testing"
)

func newFakeGoogleSearch(invalid bool) *httptest.Server {
	if invalid {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{invalid json`))
		}))
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(searchResult)
	}))
}

func Test_search(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()

	tools, err := NewToolSet(context.Background(), WithBaseURL(srv.URL), WithAPIKey("test"), WithEngineID("test"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	defer tools.Close()

	res, err := tools.search(context.Background(), searchRequest{
		Query: "trpc-agent-go",
	})
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(res.Items) != 10 {
		t.Fatalf("expected 10 results, got %d", len(res.Items))
	}
	if res.Query != "trpc-agent-go" {
		t.Fatalf("expected query to be 'trpc-agent-go', got '%s'", res.Query)
	}
	if res.Items[0].Desc !=
		"trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. - trpc-group/trpc-agent-go" {
		t.Fatalf("expected description to match, got '%s'", res.Items[0].Desc)
	}
}

func Test_search_with_options(t *testing.T) {
	srv := newFakeGoogleSearch(false)
	defer srv.Close()

	tools, err := NewToolSet(context.Background(), WithBaseURL(srv.URL), WithAPIKey("test"), WithEngineID("test"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	defer tools.Close()

	// empty query
	_, err = tools.search(context.Background(), searchRequest{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	res, err := tools.search(context.Background(), searchRequest{
		Query:  "trpc-agent-go",
		Size:   10,
		Offset: 1,
		Lang:   "en",
	})
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(res.Items) != 10 {
		t.Fatalf("expected 10 results, got %d", len(res.Items))
	}
	if res.Query != "trpc-agent-go" {
		t.Fatalf("expected query to be 'trpc-agent-go', got '%s'", res.Query)
	}
}

func Test_search_invalid_json(t *testing.T) {
	srv := newFakeGoogleSearch(true)
	defer srv.Close()

	tools, err := NewToolSet(context.Background(), WithBaseURL(srv.URL), WithAPIKey("test"), WithEngineID("test"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	defer tools.Close()

	_, err = tools.search(context.Background(), searchRequest{
		Query: "trpc-agent-go",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func Test_search_invalid_page(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "kind": "customsearch#search",
  "url": {
    "type": "application/json",
    "template": "https://www.googleapis.com/customsearch/v1?q={searchTerms}&num={count?}&start={startIndex?}&lr={language?}&safe={safe?}&cx={cx?}&sort={sort?}&filter={filter?}&gl={gl?}&cr={cr?}&googlehost={googleHost?}&c2coff={disableCnTwTranslation?}&hq={hq?}&hl={hl?}&siteSearch={siteSearch?}&siteSearchFilter={siteSearchFilter?}&exactTerms={exactTerms?}&excludeTerms={excludeTerms?}&linkSite={linkSite?}&orTerms={orTerms?}&dateRestrict={dateRestrict?}&lowRange={lowRange?}&highRange={highRange?}&searchType={searchType}&fileType={fileType?}&rights={rights?}&imgSize={imgSize?}&imgType={imgType?}&imgColorType={imgColorType?}&imgDominantColor={imgDominantColor?}&alt=json"
  },
  "queries": {
    "request": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "totalResults": "27700",
        "searchTerms": "trpc-agent-go"
      }
    ],
    "nextPage": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "searchTerms": "trpc-agent-go"
      }
    ]
  },
  "context": {
    "title": "custom-search"
  },
  "items": [
    {
      "kind": "customsearch#result",
      "title": "GitHub - trpc-group/trpc-agent-go",
      "htmlTitle": "GitHub - trpc-group/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e",
      "link": "https://github.com/trpc-group/trpc-agent-go",
      "displayLink": "github.com",
      "snippet": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github ...",
      "htmlSnippet": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github&nbsp;...",
      "formattedUrl": "https://github.com/trpc-group/trpc-agent-go",
      "htmlFormattedUrl": "https://github.com/trpc-group/\u003cb\u003etrpc-agent-go\u003c/b\u003e",
      "pagemap": []
    }
  ]
}`))
	}))
	defer srv.Close()
	tools, err := NewToolSet(context.Background(), WithBaseURL(srv.URL), WithAPIKey("test"), WithEngineID("test"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	defer tools.Close()
	_, err = tools.search(context.Background(), searchRequest{
		Query: "trpc-agent-go",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func Test_search_with_description(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "kind": "customsearch#search",
  "url": {
    "type": "application/json",
    "template": "https://www.googleapis.com/customsearch/v1?q={searchTerms}&num={count?}&start={startIndex?}&lr={language?}&safe={safe?}&cx={cx?}&sort={sort?}&filter={filter?}&gl={gl?}&cr={cr?}&googlehost={googleHost?}&c2coff={disableCnTwTranslation?}&hq={hq?}&hl={hl?}&siteSearch={siteSearch?}&siteSearchFilter={siteSearchFilter?}&exactTerms={exactTerms?}&excludeTerms={excludeTerms?}&linkSite={linkSite?}&orTerms={orTerms?}&dateRestrict={dateRestrict?}&lowRange={lowRange?}&highRange={highRange?}&searchType={searchType}&fileType={fileType?}&rights={rights?}&imgSize={imgSize?}&imgType={imgType?}&imgColorType={imgColorType?}&imgDominantColor={imgDominantColor?}&alt=json"
  },
  "queries": {
    "request": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "totalResults": "27700",
        "searchTerms": "trpc-agent-go"
      }
    ],
    "nextPage": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "searchTerms": "trpc-agent-go"
      }
    ]
  },
  "context": {
    "title": "custom-search"
  },
  "items": [
    {
      "kind": "customsearch#result",
      "title": "GitHub - trpc-group/trpc-agent-go",
      "htmlTitle": "GitHub - trpc-group/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e",
      "link": "https://github.com/trpc-group/trpc-agent-go",
      "displayLink": "github.com",
      "snippet": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github ...",
      "htmlSnippet": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github&nbsp;...",
      "formattedUrl": "https://github.com/trpc-group/trpc-agent-go",
      "htmlFormattedUrl": "https://github.com/trpc-group/\u003cb\u003etrpc-agent-go\u003c/b\u003e",
      "pagemap": {
        "metatags": [
          {
            "expected-hostname": "github.com",
            "description": "trpc-agent-go description"
          }
        ]
      }
    }
  ]
}`))
	}))
	defer srv.Close()
	tools, err := NewToolSet(context.Background(), WithBaseURL(srv.URL), WithAPIKey("test"), WithEngineID("test"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	defer tools.Close()
	results, err := tools.search(context.Background(), searchRequest{
		Query: "trpc-agent-go",
	})
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(results.Items) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results.Items))
	}
	if results.Items[0].Desc != "trpc-agent-go description" {
		t.Fatalf("expected trpc-agent-go description, got %s", results.Items[0].Desc)
	}
}

var (
	searchResult = []byte(`{
  "kind": "customsearch#search",
  "url": {
    "type": "application/json",
    "template": "https://www.googleapis.com/customsearch/v1?q={searchTerms}&num={count?}&start={startIndex?}&lr={language?}&safe={safe?}&cx={cx?}&sort={sort?}&filter={filter?}&gl={gl?}&cr={cr?}&googlehost={googleHost?}&c2coff={disableCnTwTranslation?}&hq={hq?}&hl={hl?}&siteSearch={siteSearch?}&siteSearchFilter={siteSearchFilter?}&exactTerms={exactTerms?}&excludeTerms={excludeTerms?}&linkSite={linkSite?}&orTerms={orTerms?}&dateRestrict={dateRestrict?}&lowRange={lowRange?}&highRange={highRange?}&searchType={searchType}&fileType={fileType?}&rights={rights?}&imgSize={imgSize?}&imgType={imgType?}&imgColorType={imgColorType?}&imgDominantColor={imgDominantColor?}&alt=json"
  },
  "queries": {
    "request": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "totalResults": "27700",
        "searchTerms": "trpc-agent-go",
        "count": 10,
        "startIndex": 1,
        "inputEncoding": "utf8",
        "outputEncoding": "utf8",
        "safe": "off",
        "cx": "xxxxxxx"
      }
    ],
    "nextPage": [
      {
        "title": "Google Custom Search - trpc-agent-go",
        "totalResults": "27700",
        "searchTerms": "trpc-agent-go",
        "count": 10,
        "startIndex": 11,
        "inputEncoding": "utf8",
        "outputEncoding": "utf8",
        "safe": "off",
        "cx": "xxxxxxx"
      }
    ]
  },
  "context": {
    "title": "custom-search"
  },
  "searchInformation": {
    "searchTime": 0.399202,
    "formattedSearchTime": "0.40",
    "totalResults": "27700",
    "formattedTotalResults": "27,700"
  },
  "items": [
    {
      "kind": "customsearch#result",
      "title": "GitHub - trpc-group/trpc-agent-go",
      "htmlTitle": "GitHub - trpc-group/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e",
      "link": "https://github.com/trpc-group/trpc-agent-go",
      "displayLink": "github.com",
      "snippet": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github ...",
      "htmlSnippet": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. trpc-group.github&nbsp;...",
      "formattedUrl": "https://github.com/trpc-group/trpc-agent-go",
      "htmlFormattedUrl": "https://github.com/trpc-group/\u003cb\u003etrpc-agent-go\u003c/b\u003e",
      "pagemap": {
        "cse_thumbnail": [
          {
            "src": "https://encrypted-tbn0.gstatic.com/images?q=tbn:ANd9GcQuYi77hOgGQ7ErvVneqFSp66wELvdQzr9xj5ijHyZdJ9mlb-ZeuayNeCkL&s",
            "width": "318",
            "height": "159"
          }
        ],
        "softwaresourcecode": [
          {
            "author": "trpc-group",
            "name": "trpc-agent-go",
            "text": "English | 中文 tRPC-Agent-Go 🚀 A powerful Go framework for building intelligent agent systems that transforms how you create AI applications. Build autonomous agents that think, remember,..."
          }
        ],
        "metatags": [
          {
            "og:image": "https://opengraph.githubassets.com/8c568cd9ebf90babd33688ced141bb5fe547ce4326f0edc2458f2cddac871d78/trpc-group/trpc-agent-go",
            "twitter:card": "summary_large_image",
            "og:image:width": "1200",
            "og:site_name": "GitHub",
            "release": "c35062240e0651f7fb6eaee6a254f7a143cc59cf",
            "html-safe-nonce": "37edfaca3993d500fbdb5c22665efd65982bf0dee6b01bebaf22f2b5076d6ed0",
            "expected-hostname": "github.com",
            "og:description": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. - trpc-group/trpc-agent-go",
            "browser-errors-url": "https://api.github.com/_private/browser/errors",
            "octolytics-dimension-user_login": "trpc-group",
            "hostname": "github.com",
            "browser-stats-url": "https://api.github.com/_private/browser/stats",
            "route-pattern": "/:user_id/:repository",
            "octolytics-dimension-repository_id": "983515771",
            "octolytics-dimension-repository_network_root_now": "trpc-group/trpc-agent-go",
            "og:image:alt": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. - trpc-group/trpc-agent-go",
            "visitor-hmac": "8a7089e1f40fa616d5c47fe5b0aaae649354347021b419f2148bd92b5c10df2c",
            "turbo-cache-control": "no-preview",
            "request-id": "9331:93F12:591D28:749B7D:691785A0",
            "octolytics-dimension-repository_is_fork": "false",
            "go-import": "github.com/trpc-group/trpc-agent-go git https://github.com/trpc-group/trpc-agent-go.git",
            "octolytics-dimension-user_id": "140800716",
            "octolytics-dimension-repository_network_root_id": "983515771",
            "route-controller": "files",
            "octolytics-url": "https://collector.github.com/github/collect",
            "apple-itunes-app": "app-id=1477376905, app-argument=https://github.com/trpc-group/trpc-agent-go",
            "theme-color": "#1e2327",
            "hovercard-subject-tag": "repository:983515771",
            "turbo-body-classes": "logged-out env-production page-responsive",
            "twitter:image": "https://opengraph.githubassets.com/8c568cd9ebf90babd33688ced141bb5fe547ce4326f0edc2458f2cddac871d78/trpc-group/trpc-agent-go",
            "twitter:site": "@github",
            "visitor-payload": "eyJyZWZlcnJlciI6IiIsInJlcXVlc3RfaWQiOiI5MzMxOjkzRjEyOjU5MUQyODo3NDlCN0Q6NjkxNzg1QTAiLCJ2aXNpdG9yX2lkIjoiOTYwNjMyNDY2MTU4NjE4MDI0IiwicmVnaW9uX2VkZ2UiOiJpYWQiLCJyZWdpb25fcmVuZGVyIjoiaWFkIn0=",
            "github-keyboard-shortcuts": "repository,copilot",
            "fetch-nonce": "v2:7caf4741-a49a-030b-54c1-c197815a0df0",
            "twitter:title": "GitHub - trpc-group/trpc-agent-go: trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools.",
            "og:type": "object",
            "og:title": "GitHub - trpc-group/trpc-agent-go: trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools.",
            "og:image:height": "600",
            "route-action": "disambiguate",
            "analytics-location": "/\u003cuser-name\u003e/\u003crepo-name\u003e",
            "color-scheme": "light dark",
            "octolytics-dimension-repository_public": "true",
            "fb:app_id": "1401488693436528",
            "ui-target": "full",
            "octolytics-dimension-repository_now": "trpc-group/trpc-agent-go",
            "viewport": "width=device-width",
            "twitter:description": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. - trpc-group/trpc-agent-go",
            "current-catalog-service-hash": "f3abb0cc802f3d7b95fc8762b94bdcb13bf39634c40c357301c4aa1d67a256fb",
            "og:url": "https://github.com/trpc-group/trpc-agent-go"
          }
        ],
        "cse_image": [
          {
            "src": "https://opengraph.githubassets.com/8c568cd9ebf90babd33688ced141bb5fe547ce4326f0edc2458f2cddac871d78/trpc-group/trpc-agent-go"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "Is there a Go project similar to rspc.dev or tRPC? : r/golang",
      "htmlTitle": "Is there a \u003cb\u003eGo\u003c/b\u003e project similar to rspc.dev or \u003cb\u003etRPC\u003c/b\u003e? : r/golang",
      "link": "https://www.reddit.com/r/golang/comments/1dxcdro/is_there_a_go_project_similar_to_rspcdev_or_trpc/",
      "displayLink": "www.reddit.com",
      "snippet": "Jul 7, 2024 ... Push go deeper back in your stack and make your BFF typescript/trpc ... trpc-agent-go: a powerful Go Agent framework for building intelligent ...",
      "htmlSnippet": "Jul 7, 2024 \u003cb\u003e...\u003c/b\u003e Push go deeper back in your stack and make your BFF typescript/trpc ... \u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e: a powerful Go Agent framework for building intelligent&nbsp;...",
      "formattedUrl": "https://www.reddit.com/.../is_there_a_go_project_similar_to_rspcdev_or_tr...",
      "htmlFormattedUrl": "https://www.reddit.com/.../is_there_a_go_project_similar_to_rspcdev_or_tr...",
      "pagemap": {
        "metatags": [
          {
            "og:image": "https://share.redd.it/preview/post/1dxcdro",
            "theme-color": "#000000",
            "og:image:width": "1200",
            "og:type": "website",
            "og:image:alt": "An image containing a preview of the post",
            "twitter:card": "summary_large_image",
            "twitter:title": "r/golang on Reddit: Is there a Go project similar to rspc.dev or tRPC?",
            "og:site_name": "Reddit",
            "og:title": "r/golang on Reddit: Is there a Go project similar to rspc.dev or tRPC?",
            "og:image:height": "630",
            "msapplication-navbutton-color": "#000000",
            "og:description": "Posted by u/blankeos - 8 votes and 19 comments",
            "twitter:image": "https://share.redd.it/preview/post/1dxcdro",
            "apple-mobile-web-app-status-bar-style": "black",
            "twitter:site": "@reddit",
            "viewport": "width=device-width, initial-scale=1, viewport-fit=cover",
            "apple-mobile-web-app-capable": "yes",
            "og:ttl": "600",
            "og:url": "https://www.reddit.com/r/golang/comments/1dxcdro/is_there_a_go_project_similar_to_rspcdev_or_trpc/?seeker-session=true"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "trpc-group/trpc-a2a-go: Go implementation for A2A ... - GitHub",
      "htmlTitle": "trpc-group/trpc-a2a-go: Go implementation for A2A ... - GitHub",
      "link": "https://github.com/trpc-group/trpc-a2a-go",
      "displayLink": "github.com",
      "snippet": "trpc-agent-go : A powerful Go framework for building intelligent agent ... # Connect to a specific agent go run main.go --agent http://localhost:9000/ # ...",
      "htmlSnippet": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e : A powerful Go framework for building intelligent agent ... # Connect to a specific agent go run main.go --agent http://localhost:9000/ #&nbsp;...",
      "formattedUrl": "https://github.com/trpc-group/trpc-a2a-go",
      "htmlFormattedUrl": "https://github.com/trpc-group/trpc-a2a-go",
      "pagemap": {
        "softwaresourcecode": [
          {
            "author": "trpc-group",
            "name": "trpc-a2a-go",
            "text": "tRPC-A2A-Go This is tRPC group's Go implementation of the A2A protocol, enabling different AI agents to discover and collaborate with each other. Related Projects tRPC AI ecosystem trpc-agent-go..."
          }
        ],
        "metatags": [
          {
            "og:image": "https://opengraph.githubassets.com/14f7b1f0c013f11dbfaae8becfc6123d389beac30258c9051d5763952d042095/trpc-group/trpc-a2a-go",
            "twitter:card": "summary_large_image",
            "og:image:width": "1200",
            "og:site_name": "GitHub",
            "release": "f3bc89dfdd8bc2c62a65498b7807f878156dfbef",
            "html-safe-nonce": "9f2234f7974ff2562c57f3add57d86dfde4dcc958c623e9f310e25640fb06166",
            "expected-hostname": "github.com",
            "og:description": "Go implementation for A2A (Agent2Agent) protocol. Contribute to trpc-group/trpc-a2a-go development by creating an account on GitHub.",
            "browser-errors-url": "https://api.github.com/_private/browser/errors",
            "octolytics-dimension-user_login": "trpc-group",
            "hostname": "github.com",
            "browser-stats-url": "https://api.github.com/_private/browser/stats",
            "route-pattern": "/:user_id/:repository",
            "octolytics-dimension-repository_id": "967877784",
            "octolytics-dimension-repository_network_root_now": "trpc-group/trpc-a2a-go",
            "og:image:alt": "Go implementation for A2A (Agent2Agent) protocol. Contribute to trpc-group/trpc-a2a-go development by creating an account on GitHub.",
            "visitor-hmac": "cf95cb7cb8469451f27e20b3499b033a5ed7e08906070b7a98e3f622d75e31ba",
            "turbo-cache-control": "no-preview",
            "request-id": "E5BE:AA92F:10E905:158F55:691401C0",
            "octolytics-dimension-repository_is_fork": "false",
            "go-import": "github.com/trpc-group/trpc-a2a-go git https://github.com/trpc-group/trpc-a2a-go.git",
            "octolytics-dimension-user_id": "140800716",
            "octolytics-dimension-repository_network_root_id": "967877784",
            "route-controller": "files",
            "octolytics-url": "https://collector.github.com/github/collect",
            "apple-itunes-app": "app-id=1477376905, app-argument=https://github.com/trpc-group/trpc-a2a-go",
            "theme-color": "#1e2327",
            "hovercard-subject-tag": "repository:967877784",
            "turbo-body-classes": "logged-out env-production page-responsive",
            "twitter:image": "https://opengraph.githubassets.com/14f7b1f0c013f11dbfaae8becfc6123d389beac30258c9051d5763952d042095/trpc-group/trpc-a2a-go",
            "twitter:site": "@github",
            "visitor-payload": "eyJyZWZlcnJlciI6IiIsInJlcXVlc3RfaWQiOiJFNUJFOkFBOTJGOjEwRTkwNToxNThGNTU6NjkxNDAxQzAiLCJ2aXNpdG9yX2lkIjoiMzU3NzI2NzMwNjEzOTc0Njc1MiIsInJlZ2lvbl9lZGdlIjoiaWFkIiwicmVnaW9uX3JlbmRlciI6ImlhZCJ9",
            "github-keyboard-shortcuts": "repository,copilot",
            "fetch-nonce": "v2:ec6b75a7-5678-326c-b523-a435e6a1b150",
            "twitter:title": "GitHub - trpc-group/trpc-a2a-go: Go implementation for A2A (Agent2Agent) protocol",
            "og:type": "object",
            "og:title": "GitHub - trpc-group/trpc-a2a-go: Go implementation for A2A (Agent2Agent) protocol",
            "og:image:height": "600",
            "route-action": "disambiguate",
            "analytics-location": "/\u003cuser-name\u003e/\u003crepo-name\u003e",
            "color-scheme": "light dark",
            "octolytics-dimension-repository_public": "true",
            "fb:app_id": "1401488693436528",
            "ui-target": "canary-1",
            "octolytics-dimension-repository_now": "trpc-group/trpc-a2a-go",
            "viewport": "width=device-width",
            "twitter:description": "Go implementation for A2A (Agent2Agent) protocol. Contribute to trpc-group/trpc-a2a-go development by creating an account on GitHub.",
            "current-catalog-service-hash": "f3abb0cc802f3d7b95fc8762b94bdcb13bf39634c40c357301c4aa1d67a256fb",
            "og:url": "https://github.com/trpc-group/trpc-a2a-go"
          }
        ],
        "cse_image": [
          {
            "src": "https://opengraph.githubassets.com/14f7b1f0c013f11dbfaae8becfc6123d389beac30258c9051d5763952d042095/trpc-group/trpc-a2a-go"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "trpc-agent-go: a powerful Go Agent framework for building intelligent ...",
      "htmlTitle": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e: a powerful Go Agent framework for building intelligent ...",
      "link": "https://www.reddit.com/r/golang/comments/1n777b4/trpcagentgo_a_powerful_go_agent_framework_for/",
      "displayLink": "www.reddit.com",
      "snippet": "Sep 3, 2025 ... https://medium.com/@sandyskieschan/trpc-agent-go-a-powerful-go-framework-for-building-intelligent-agent-systems-ef7111f24ece.",
      "htmlSnippet": "Sep 3, 2025 \u003cb\u003e...\u003c/b\u003e https://medium.com/@sandyskieschan/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e-a-powerful-go-framework-for-building-intelligent-agent-systems-ef7111f24ece.",
      "formattedUrl": "https://www.reddit.com/.../trpcagentgo_a_powerful_go_agent_framework_...",
      "htmlFormattedUrl": "https://www.reddit.com/.../\u003cb\u003etrpcagentgo\u003c/b\u003e_a_powerful_go_agent_framework_...",
      "pagemap": {
        "metatags": [
          {
            "og:image": "https://share.redd.it/preview/post/1n777b4",
            "theme-color": "#000000",
            "og:image:width": "1200",
            "og:type": "website",
            "og:image:alt": "An image containing a preview of the post",
            "twitter:card": "summary_large_image",
            "twitter:title": "r/golang on Reddit: trpc-agent-go: a powerful Go Agent framework for building intelligent agent systems",
            "og:site_name": "Reddit",
            "og:title": "r/golang on Reddit: trpc-agent-go: a powerful Go Agent framework for building intelligent agent systems",
            "og:image:height": "630",
            "msapplication-navbutton-color": "#000000",
            "og:description": "Posted by u/Last-Ad607 - 2 votes and 3 comments",
            "twitter:image": "https://share.redd.it/preview/post/1n777b4",
            "apple-mobile-web-app-status-bar-style": "black",
            "twitter:site": "@reddit",
            "viewport": "width=device-width, initial-scale=1, viewport-fit=cover",
            "apple-mobile-web-app-capable": "yes",
            "og:ttl": "600",
            "og:url": "https://www.reddit.com/r/golang/comments/1n777b4/trpcagentgo_a_powerful_go_agent_framework_for/?seeker-session=true"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "trpc-group · GitHub",
      "htmlTitle": "\u003cb\u003etrpc\u003c/b\u003e-group · GitHub",
      "link": "https://github.com/trpc-group",
      "displayLink": "github.com",
      "snippet": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. ... Go implementation of the Model ...",
      "htmlSnippet": "\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools. ... Go implementation of the Model&nbsp;...",
      "formattedUrl": "https://github.com/trpc-group",
      "htmlFormattedUrl": "https://github.com/trpc-group",
      "pagemap": {
        "cse_thumbnail": [
          {
            "src": "https://encrypted-tbn0.gstatic.com/images?q=tbn:ANd9GcQLjx_U-syQQUucVDTCe_tjrtFEL0aTPZiqPhgL_aauIccmugJFiaj880o&s",
            "width": "224",
            "height": "224"
          }
        ],
        "code": [
          {
            "name": "trpc-agent-go",
            "description": "trpc-agent-go is a powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools.",
            "coderepository": "trpc-agent-go",
            "programminglanguage": "Go"
          },
          {
            "name": "cla-database",
            "coderepository": "cla-database"
          },
          {
            "name": "trpc-mcp-go",
            "description": "Go implementation of the Model Context Protocol (MCP) with comprehensive Streamable HTTP, STDIO, and SSE support.",
            "coderepository": "trpc-mcp-go",
            "programminglanguage": "Go"
          },
          {
            "name": "trpc-java",
            "description": "A pluggable, high-performance RPC framework written in java",
            "coderepository": "trpc-java",
            "programminglanguage": "Java"
          },
          {
            "name": "trpc-a2a-go",
            "description": "Go implementation for A2A (Agent2Agent) protocol",
            "coderepository": "trpc-a2a-go",
            "programminglanguage": "Go"
          },
          {
            "name": "trpc-go",
            "description": "A pluggable, high-performance RPC framework written in golang",
            "coderepository": "trpc-go",
            "programminglanguage": "Go"
          },
          {
            "name": "trpc-cpp",
            "description": "A pluggable, high-performance RPC framework written in cpp",
            "coderepository": "trpc-cpp",
            "programminglanguage": "C++"
          },
          {
            "name": "trpc-cpp.github.io",
            "coderepository": "trpc-cpp.github.io",
            "programminglanguage": "HTML"
          },
          {
            "name": "trpc-cmdline",
            "description": "Command line tool for tRPC",
            "coderepository": "trpc-cmdline",
            "programminglanguage": "Go"
          },
          {
            "name": "trpc",
            "description": "A multi-language, pluggable, high-performance RPC framework",
            "coderepository": "trpc",
            "programminglanguage": "Lua"
          }
        ],
        "organization": [
          {
            "image": "https://avatars.githubusercontent.com/u/140800716?s=200&v=4",
            "programminglanguage": "Lua"
          }
        ],
        "metatags": [
          {
            "octolytics-url": "https://collector.github.com/github/collect",
            "apple-itunes-app": "app-id=1477376905, app-argument=https://github.com/trpc-group",
            "og:image": "https://avatars.githubusercontent.com/u/140800716?s=280&v=4",
            "twitter:card": "summary_large_image",
            "theme-color": "#1e2327",
            "og:site_name": "GitHub",
            "hovercard-subject-tag": "organization:140800716",
            "turbo-body-classes": "logged-out env-production page-responsive",
            "release": "8fc841bcfb8d3177f768033219ef8ade60901419",
            "html-safe-nonce": "1f47852f41553a97d6eb9d19464575cc3bc7f30d56bad9e90ccd074bf7bb23fa",
            "expected-hostname": "github.com",
            "og:description": "tRPC is a multi-language, pluggable, high performance RPC framework. - trpc-group",
            "twitter:image": "https://avatars.githubusercontent.com/u/140800716?s=280&v=4",
            "browser-errors-url": "https://api.github.com/_private/browser/errors",
            "hostname": "github.com",
            "twitter:site": "@github",
            "browser-stats-url": "https://api.github.com/_private/browser/stats",
            "route-pattern": "/:user_id(.:format)",
            "visitor-payload": "eyJyZWZlcnJlciI6IiIsInJlcXVlc3RfaWQiOiI4QTZFOjIxQkI4RTo3N0VCMURGOkEzMEZDMjI6NjkxNjhGRTAiLCJ2aXNpdG9yX2lkIjoiMTcwMDQ1OTA0NTY0Njg2ODI3IiwicmVnaW9uX2VkZ2UiOiJpYWQiLCJyZWdpb25fcmVuZGVyIjoiaWFkIn0=",
            "github-keyboard-shortcuts": "copilot",
            "fetch-nonce": "v2:4bdba5e9-24f5-3ad0-24bd-87b3e6ac29c6",
            "twitter:title": "trpc-group",
            "og:image:alt": "tRPC is a multi-language, pluggable, high performance RPC framework. - trpc-group",
            "og:type": "profile",
            "profile:username": "trpc-group",
            "og:title": "trpc-group",
            "visitor-hmac": "65faf8a02a645b7e66553b0a620a748c30f3f66698f195e938b8a6793f755867",
            "turbo-cache-control": "no-preview",
            "route-action": "show",
            "request-id": "8A6E:21BB8E:77EB1DF:A30FC22:69168FE0",
            "analytics-location": "/\u003corg-login\u003e",
            "color-scheme": "light dark",
            "fb:app_id": "1401488693436528",
            "ui-target": "full",
            "viewport": "width=device-width",
            "twitter:description": "tRPC is a multi-language, pluggable, high performance RPC framework. - trpc-group",
            "route-controller": "profiles",
            "current-catalog-service-hash": "4a1c50a83cf6cc4b55b6b9c53e553e3f847c876b87fb333f71f5d05db8f1a7db",
            "og:url": "https://github.com/trpc-group"
          }
        ],
        "cse_image": [
          {
            "src": "https://avatars.githubusercontent.com/u/140800716?s=280&v=4"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "[IDEA] tRPC Like framework for GO : r/golang",
      "htmlTitle": "[IDEA] \u003cb\u003etRPC\u003c/b\u003e Like framework for \u003cb\u003eGO\u003c/b\u003e : r/golang",
      "link": "https://www.reddit.com/r/golang/comments/1hoxmo4/idea_trpc_like_framework_for_go/",
      "displayLink": "www.reddit.com",
      "snippet": "Dec 29, 2024 ... - trpc/go-server-emitter. - trpc/js-client-emitter ... trpc-agent-go: a powerful Go Agent framework for building intelligent agent systems.",
      "htmlSnippet": "Dec 29, 2024 \u003cb\u003e...\u003c/b\u003e - trpc/go-server-emitter. - trpc/js-client-emitter ... \u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e: a powerful Go Agent framework for building intelligent agent systems.",
      "formattedUrl": "https://www.reddit.com/r/golang/.../idea_trpc_like_framework_for_go/",
      "htmlFormattedUrl": "https://www.reddit.com/r/golang/.../idea_trpc_like_framework_for_go/",
      "pagemap": {
        "metatags": [
          {
            "og:image": "https://share.redd.it/preview/post/1hoxmo4",
            "theme-color": "#000000",
            "og:image:width": "1200",
            "og:type": "website",
            "og:image:alt": "An image containing a preview of the post",
            "twitter:card": "summary_large_image",
            "twitter:title": "r/golang on Reddit: [IDEA] tRPC Like framework for GO",
            "og:site_name": "Reddit",
            "og:title": "r/golang on Reddit: [IDEA] tRPC Like framework for GO",
            "og:image:height": "630",
            "msapplication-navbutton-color": "#000000",
            "og:description": "Posted by u/Delicious-Ad1453 - 0 votes and 7 comments",
            "twitter:image": "https://share.redd.it/preview/post/1hoxmo4",
            "apple-mobile-web-app-status-bar-style": "black",
            "twitter:site": "@reddit",
            "viewport": "width=device-width, initial-scale=1, viewport-fit=cover",
            "apple-mobile-web-app-capable": "yes",
            "og:ttl": "600",
            "og:url": "https://www.reddit.com/r/golang/comments/1hoxmo4/idea_trpc_like_framework_for_go/?seeker-session=true"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "inmemory package - trpc.group/trpc-go/trpc-agent-go/evaluation ...",
      "htmlTitle": "inmemory package - trpc.group/trpc-go/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e/evaluation ...",
      "link": "https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory",
      "displayLink": "pkg.go.dev",
      "snippet": "3 days ago ... Package inmemory provides an in-memory metric manager implementation.",
      "htmlSnippet": "3 days ago \u003cb\u003e...\u003c/b\u003e Package inmemory provides an in-memory metric manager implementation.",
      "formattedUrl": "https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/evaluation/.../inmemor...",
      "htmlFormattedUrl": "https://pkg.go.dev/trpc.group/trpc-go/\u003cb\u003etrpc-agent-go\u003c/b\u003e/evaluation/.../inmemor...",
      "pagemap": {
        "metatags": [
          {
            "viewport": "width=device-width, initial-scale=1.0"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "Any good and simple AI Agent frameworks for Go? : r/golang",
      "htmlTitle": "Any good and simple AI \u003cb\u003eAgent\u003c/b\u003e frameworks for \u003cb\u003eGo\u003c/b\u003e? : r/golang",
      "link": "https://www.reddit.com/r/golang/comments/1i51m8w/any_good_and_simple_ai_agent_frameworks_for_go/",
      "displayLink": "www.reddit.com",
      "snippet": "Jan 19, 2025 ... There are bunch of AI Agent frameworks for Python. I've been researching simple frameworks to use for Go but couldn't find much.",
      "htmlSnippet": "Jan 19, 2025 \u003cb\u003e...\u003c/b\u003e There are bunch of AI \u003cb\u003eAgent\u003c/b\u003e frameworks for Python. I&#39;ve been researching simple frameworks to use for \u003cb\u003eGo\u003c/b\u003e but couldn&#39;t find much.",
      "formattedUrl": "https://www.reddit.com/.../any_good_and_simple_ai_agent_frameworks_fo...",
      "htmlFormattedUrl": "https://www.reddit.com/.../any_good_and_simple_ai_agent_frameworks_fo...",
      "pagemap": {
        "metatags": [
          {
            "og:image": "https://share.redd.it/preview/post/1i51m8w",
            "theme-color": "#000000",
            "og:image:width": "1200",
            "og:type": "website",
            "og:image:alt": "An image containing a preview of the post",
            "twitter:card": "summary_large_image",
            "twitter:title": "r/golang on Reddit: Any good and simple AI Agent frameworks for Go?",
            "og:site_name": "Reddit",
            "og:title": "r/golang on Reddit: Any good and simple AI Agent frameworks for Go?",
            "og:image:height": "630",
            "msapplication-navbutton-color": "#000000",
            "og:description": "Posted by u/D3ntrax - 5 votes and 19 comments",
            "twitter:image": "https://share.redd.it/preview/post/1i51m8w",
            "apple-mobile-web-app-status-bar-style": "black",
            "twitter:site": "@reddit",
            "viewport": "width=device-width, initial-scale=1, viewport-fit=cover",
            "apple-mobile-web-app-capable": "yes",
            "og:ttl": "600",
            "og:url": "https://www.reddit.com/r/golang/comments/1i51m8w/any_good_and_simple_ai_agent_frameworks_for_go/?seeker-session=true"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "Introducing a2a-go, the Go Implementation of A2A Protocol | by ...",
      "htmlTitle": "Introducing a2a-\u003cb\u003ego\u003c/b\u003e, the \u003cb\u003eGo\u003c/b\u003e Implementation of A2A Protocol | by ...",
      "link": "https://medium.com/@guoqizhou123123/introducing-a2a-go-the-go-implementation-of-a2a-protocol-605131cb837c",
      "displayLink": "medium.com",
      "snippet": "Apr 21, 2025 ... \"trpc.group/trpc-go/a2a-go/taskmanager\" ) // TextProcessor ... go/pulls) on GitHub, and join the tRPC agent ecosystem building! A2a.",
      "htmlSnippet": "Apr 21, 2025 \u003cb\u003e...\u003c/b\u003e &quot;\u003cb\u003etrpc\u003c/b\u003e.group/\u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e/a2a-\u003cb\u003ego\u003c/b\u003e/taskmanager&quot; ) // TextProcessor ... \u003cb\u003ego\u003c/b\u003e/pulls) on GitHub, and join the \u003cb\u003etRPC agent\u003c/b\u003e ecosystem building! A2a.",
      "formattedUrl": "https://medium.com/.../introducing-a2a-go-the-go-implementation-of-a2a-p...",
      "htmlFormattedUrl": "https://medium.com/.../introducing-a2a-go-the-go-implementation-of-a2a-p...",
      "pagemap": {
        "cse_thumbnail": [
          {
            "src": "https://encrypted-tbn0.gstatic.com/images?q=tbn:ANd9GcSo5HjsYCqzLQ6GDHhtysevpjjCt3ykPidLAEhySctxnbNk-Mg4Nf_Zq-uk&s",
            "width": "255",
            "height": "198"
          }
        ],
        "metatags": [
          {
            "apple-itunes-app": "app-id=828256236, app-argument=/@guoqizhou123123/introducing-a2a-go-the-go-implementation-of-a2a-protocol-605131cb837c, affiliate-data=pt=698524&ct=smart_app_banner&mt=8",
            "og:image": "https://miro.medium.com/v2/resize:fit:573/1*KP_d_IcZp_yNILndhyRGyw.png",
            "twitter:app:url:iphone": "medium://p/605131cb837c",
            "theme-color": "#000000",
            "article:published_time": "2025-04-22T03:53:53.984Z",
            "twitter:card": "summary_large_image",
            "og:site_name": "Medium",
            "al:android:package": "com.medium.reader",
            "twitter:label1": "Reading time",
            "twitter:app:id:iphone": "828256236",
            "title": "Introducing a2a-go, the Go Implementation of A2A Protocol | by wineandchord | Medium",
            "al:ios:url": "medium://p/605131cb837c",
            "og:description": "The tRPC team has released a2a-go, a Go language implementation of the A2A protocol: https://github.com/trpc-group/trpc-a2a-go",
            "al:ios:app_store_id": "828256236",
            "twitter:data1": "10 min read",
            "twitter:site": "@Medium",
            "og:type": "article",
            "twitter:title": "Introducing a2a-go, the Go Implementation of A2A Protocol",
            "al:ios:app_name": "Medium",
            "author": "wineandchord",
            "og:title": "Introducing a2a-go, the Go Implementation of A2A Protocol",
            "al:web:url": "https://medium.com/@guoqizhou123123/introducing-a2a-go-the-go-implementation-of-a2a-protocol-605131cb837c",
            "article:author": "https://medium.com/@guoqizhou123123",
            "twitter:image:src": "https://miro.medium.com/v2/resize:fit:573/1*KP_d_IcZp_yNILndhyRGyw.png",
            "al:android:url": "medium://p/605131cb837c",
            "referrer": "unsafe-url",
            "fb:app_id": "542599432471018",
            "viewport": "width=device-width,minimum-scale=1,initial-scale=1,maximum-scale=1",
            "twitter:description": "The tRPC team has released a2a-go, a Go language implementation of the A2A protocol: https://github.com/trpc-group/trpc-a2a-go",
            "og:url": "https://medium.com/@guoqizhou123123/introducing-a2a-go-the-go-implementation-of-a2a-protocol-605131cb837c",
            "twitter:app:name:iphone": "Medium",
            "al:android:app_name": "Medium"
          }
        ],
        "cse_image": [
          {
            "src": "https://miro.medium.com/v2/resize:fit:573/1*KP_d_IcZp_yNILndhyRGyw.png"
          }
        ]
      }
    },
    {
      "kind": "customsearch#result",
      "title": "Why our company replaced Golang+GraphQL with TypeScript+ ...",
      "htmlTitle": "Why our company replaced Golang+GraphQL with TypeScript+ ...",
      "link": "https://www.reddit.com/r/golang/comments/xp0dpc/why_our_company_replaced_golanggraphql_with/",
      "displayLink": "www.reddit.com",
      "snippet": "Sep 27, 2022 ... trpc-agent-go: a powerful Go Agent framework for building intelligent agent systems ... Is there a Go project similar to rspc.dev or tRPC?",
      "htmlSnippet": "Sep 27, 2022 \u003cb\u003e...\u003c/b\u003e \u003cb\u003etrpc\u003c/b\u003e-\u003cb\u003eagent\u003c/b\u003e-\u003cb\u003ego\u003c/b\u003e: a powerful Go Agent framework for building intelligent agent systems ... Is there a Go project similar to rspc.dev or tRPC?",
      "formattedUrl": "https://www.reddit.com/.../why_our_company_replaced_golanggraphql_wi...",
      "htmlFormattedUrl": "https://www.reddit.com/.../why_our_company_replaced_golanggraphql_wi...",
      "pagemap": {
        "cse_thumbnail": [
          {
            "src": "https://encrypted-tbn0.gstatic.com/images?q=tbn:ANd9GcQiz0eP7bCQe0qZmOPqPA6P2_SkCW9ULJxR5x73_m64hRX4uSN1POXe71r8&s",
            "width": "275",
            "height": "183"
          }
        ],
        "metatags": [
          {
            "og:image": "https://external-preview.redd.it/pJI6K7euwWMO4xxGXay3lap_Pymrx8VWtF6XNcVqylM.jpg?width=1080&crop=smart&auto=webp&s=9c03ffb21c3c2f4b4a88383165355339806b1ea9",
            "theme-color": "#000000",
            "og:image:width": "1080",
            "og:type": "website",
            "twitter:card": "summary_large_image",
            "twitter:title": "r/golang on Reddit: Why our company replaced Golang+GraphQL with TypeScript+Prisma+tRPC",
            "og:site_name": "Reddit",
            "og:title": "r/golang on Reddit: Why our company replaced Golang+GraphQL with TypeScript+Prisma+tRPC",
            "og:image:height": "720",
            "msapplication-navbutton-color": "#000000",
            "og:description": "Posted by u/barekliton - 0 votes and 11 comments",
            "twitter:image": "https://external-preview.redd.it/pJI6K7euwWMO4xxGXay3lap_Pymrx8VWtF6XNcVqylM.jpg?width=1080&crop=smart&auto=webp&s=9c03ffb21c3c2f4b4a88383165355339806b1ea9",
            "apple-mobile-web-app-status-bar-style": "black",
            "twitter:site": "@reddit",
            "viewport": "width=device-width, initial-scale=1, viewport-fit=cover",
            "apple-mobile-web-app-capable": "yes",
            "og:ttl": "600",
            "og:url": "https://www.reddit.com/r/golang/comments/xp0dpc/why_our_company_replaced_golanggraphql_with/?seeker-session=true"
          }
        ],
        "cse_image": [
          {
            "src": "https://external-preview.redd.it/pJI6K7euwWMO4xxGXay3lap_Pymrx8VWtF6XNcVqylM.jpg?width=1080&crop=smart&auto=webp&s=9c03ffb21c3c2f4b4a88383165355339806b1ea9"
          }
        ]
      }
    }
  ]
}
`)
)
