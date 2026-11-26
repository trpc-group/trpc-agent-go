//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type semanticTestEmbedder struct {
	mu    sync.Mutex
	texts []string
	embed func(context.Context, string) ([]float64, error)
}

func (e *semanticTestEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	e.texts = append(e.texts, text)
	e.mu.Unlock()
	return e.embed(ctx, text)
}

func (e *semanticTestEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	vector, err := e.GetEmbedding(ctx, text)
	return vector, nil, err
}

func (*semanticTestEmbedder) GetDimensions() int { return 0 }

func (e *semanticTestEmbedder) queryCalls(query string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, text := range e.texts {
		if text == query {
			count++
		}
	}
	return count
}

func (e *semanticTestEmbedder) candidateTexts(query string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var texts []string
	for _, text := range e.texts {
		if text != query {
			texts = append(texts, text)
		}
	}
	return texts
}

type semanticTestTool struct {
	declaration tool.Declaration
	result      string
}

func (t *semanticTestTool) Declaration() *tool.Declaration { return &t.declaration }
func (t *semanticTestTool) Call(context.Context, []byte) (any, error) {
	return t.result, nil
}

func semanticEntry(name, description string) responses.ToolSearchEntry {
	return responses.ToolSearchEntry{Tool: &semanticTestTool{declaration: tool.Declaration{
		Name: name, Description: description,
		InputSchema: &tool.Schema{Type: "object"},
	}}}
}

func TestSemanticToolSearchRanksCosineAndAppliesDefaults(t *testing.T) {
	scores := map[string]float64{"a": .5, "b": .9, "c": .8, "d": .9, "e": 1, "f": .4, "g": .39, "h": -.2}
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text == "find tools" {
			return []float64{10, 0}, nil
		}
		for name, score := range scores {
			if strings.Contains(text, "candidate-"+name) {
				return []float64{3 * score, 3 * math.Sqrt(1-score*score)}, nil
			}
		}
		return nil, errors.New("unexpected candidate text")
	}}
	handler, err := toolsearch.NewSemantic(embeddings)
	require.NoError(t, err)
	var candidates []responses.ToolSearchEntry
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		candidates = append(candidates, semanticEntry(name, "candidate-"+name))
	}
	request := &responses.ToolSearchRequest{Query: "find tools", Candidates: candidates}
	got, err := handler(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{candidates[4], candidates[1], candidates[3], candidates[2], candidates[0]}, got,
		"the five highest cosine scores win, with candidate order breaking ties")
	require.Equal(t, "a", request.Candidates[0].Tool.Declaration().Name, "ranking leaves the request order intact")

	got, err = handler(context.Background(), &responses.ToolSearchRequest{Query: request.Query, Candidates: candidates[5:]})
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{candidates[5]}, got, "the 0.4 threshold is inclusive")
}

func TestSemanticToolSearchMinScoreIncludesOppositeVector(t *testing.T) {
	const query = "find tools"
	// These opposite vectors yield a normalized dot product just below -1 in float64.
	const small = 0x1p-26
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text == query {
			return []float64{1, small, small, small}, nil
		}
		return []float64{-1, -small, -small, -small}, nil
	}}
	handler, err := toolsearch.NewSemantic(embeddings, toolsearch.WithSemanticMinScore(-1))
	require.NoError(t, err)
	candidates := []responses.ToolSearchEntry{semanticEntry("opposite", "")}
	got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: candidates})
	require.NoError(t, err)
	require.Equal(t, candidates, got)
}

func TestSemanticToolSearchOptions(t *testing.T) {
	const query = "find a related tool"
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text == query {
			return []float64{1, 0}, nil
		}
		if strings.Contains(text, "ahead") {
			return []float64{3, 4}, nil
		}
		return []float64{0, 1}, nil
	}}
	candidates := []responses.ToolSearchEntry{
		semanticEntry("ahead", ""), semanticEntry("sideways", ""), semanticEntry("ahead_other", ""),
	}
	handler, err := toolsearch.NewSemantic(embeddings,
		toolsearch.WithSemanticMaxResults(1), nil, toolsearch.WithSemanticMinScore(0),
		toolsearch.WithSemanticMaxResults(3),
	)
	require.NoError(t, err)
	got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: candidates})
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{candidates[0], candidates[2], candidates[1]}, got)

	handler, err = toolsearch.NewSemantic(embeddings, toolsearch.WithSemanticMaxResults(1))
	require.NoError(t, err)
	got, err = handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: candidates})
	require.NoError(t, err)
	require.Equal(t, candidates[:1], got)

	for _, tc := range []struct {
		name string
		opt  toolsearch.SemanticOption
	}{
		{"non-positive limit", toolsearch.WithSemanticMaxResults(0)},
		{"too low", toolsearch.WithSemanticMinScore(-1.1)},
		{"too high", toolsearch.WithSemanticMinScore(1.1)},
		{"NaN", toolsearch.WithSemanticMinScore(math.NaN())},
		{"infinity", toolsearch.WithSemanticMinScore(math.Inf(1))},
		{"negative capacity", toolsearch.WithSemanticCacheSize(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := toolsearch.NewSemantic(embeddings, tc.opt)
			require.Error(t, err)
			require.Nil(t, handler)
		})
	}
}

func TestSemanticToolSearchCachesContentAndReturnsCurrentEntries(t *testing.T) {
	const query = "find customer"
	embeddings := &semanticTestEmbedder{embed: func(context.Context, string) ([]float64, error) {
		return []float64{1, 0}, nil
	}}
	handler, err := toolsearch.NewSemantic(embeddings)
	require.NoError(t, err)
	makeEntry := func() responses.ToolSearchEntry {
		return responses.ToolSearchEntry{
			Namespace: "crm", NamespaceDescription: "Customer records.", Name: "lookup",
			Tool: &semanticTestTool{declaration: tool.Declaration{
				Name: "internal_registered_name", Description: "Find a customer record.",
				InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{
					"region":      {Type: "string", Description: "The customer region."},
					"account_ref": {Type: "string", Description: "The customer account reference."},
					"skipped":     nil,
				}},
			}},
		}
	}
	first := makeEntry()
	second := makeEntry()
	for _, entry := range []responses.ToolSearchEntry{first, second} {
		got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Same(t, entry.Tool, got[0].Tool, "cached vectors cannot retain a previous run's executor")
		require.Equal(t, entry, got[0])
	}
	texts := embeddings.candidateTexts(query)
	require.Len(t, texts, 1, "identical catalog content is embedded once across runs")
	require.Equal(t, 2, embeddings.queryCalls(query), "the query is embedded on both calls")
	for _, part := range []string{"crm", "lookup", "Customer records.", "Find a customer record.", "account_ref", "The customer account reference."} {
		require.Contains(t, texts[0], part)
	}
	require.Contains(t, texts[0], "account_ref (string): The customer account reference., region (string): The customer region.")
	require.NotContains(t, texts[0], "skipped")
	require.NotContains(t, texts[0], "internal_registered_name", "the catalog text uses the model-visible name")

	second.Tool.Declaration().InputSchema.Properties["account_ref"].Description = "The newly required account identifier."
	got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{second}})
	require.NoError(t, err)
	require.Same(t, second.Tool, got[0].Tool)
	texts = embeddings.candidateTexts(query)
	require.Len(t, texts, 2, "changing a parameter description invalidates its vector")
	require.Contains(t, texts[1], "The newly required account identifier.")
	second.NamespaceDescription = "Updated customer directory."
	_, err = handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{second}})
	require.NoError(t, err)
	require.Len(t, embeddings.candidateTexts(query), 3, "namespace descriptions participate in the cache key")
	require.Equal(t, 4, embeddings.queryCalls(query))
}

func TestSemanticToolSearchCacheCapacity(t *testing.T) {
	const query = "find records"
	for _, tc := range []struct {
		name          string
		size          int
		wantCandidate int
	}{
		{name: "disabled", size: 0, wantCandidate: 5},
		{name: "one entry", size: 1, wantCandidate: 3},
		{name: "two entries", size: 2, wantCandidate: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			embeddings := &semanticTestEmbedder{embed: func(context.Context, string) ([]float64, error) {
				return []float64{1, 0}, nil
			}}
			handler, err := toolsearch.NewSemantic(embeddings, toolsearch.WithSemanticCacheSize(tc.size))
			require.NoError(t, err)
			a, b := semanticEntry("find_org", ""), semanticEntry("find_customer", "")
			for _, entry := range []responses.ToolSearchEntry{a, a, b, b, a} {
				got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}})
				require.NoError(t, err)
				require.Equal(t, []responses.ToolSearchEntry{entry}, got)
			}
			require.Equal(t, 5, embeddings.queryCalls(query))
			require.Len(t, embeddings.candidateTexts(query), tc.wantCandidate)
		})
	}
}

func TestSemanticToolSearchConcurrentRuns(t *testing.T) {
	const query = "find records"
	embeddings := &semanticTestEmbedder{embed: func(context.Context, string) ([]float64, error) {
		return []float64{1, 0}, nil
	}}
	handler, err := toolsearch.NewSemantic(embeddings, toolsearch.WithSemanticCacheSize(2))
	require.NoError(t, err)

	// Populate both candidate vectors, then share the handler between runs with
	// different Tool objects and the same visible descriptions.
	seed := []responses.ToolSearchEntry{semanticEntry("find_org", ""), semanticEntry("find_customer", "")}
	_, err = handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: seed})
	require.NoError(t, err)

	const runs = 30
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := semanticEntry(seed[i%len(seed)].Tool.Declaration().Name, "")
			<-start
			got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}})
			if err != nil || len(got) != 1 || got[0].Tool != entry.Tool {
				t.Errorf("run %d returned %v, %v; want its own candidate", i, got, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	require.Equal(t, runs+1, embeddings.queryCalls(query))
	require.Len(t, embeddings.candidateTexts(query), len(seed))

	// A shared cache with concurrent misses must stay valid through insertions
	// and evictions as other searches are in progress.
	for i, name := range []string{"billing", "crm", "shipping", "orders", "support", "documents"} {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			entry := semanticEntry(name, "")
			got, err := handler(context.Background(), &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}})
			if err != nil || len(got) != 1 || got[0].Tool != entry.Tool {
				t.Errorf("cold run %d returned %v, %v; want its own candidate", i, got, err)
			}
		}(i, name)
	}
	wg.Wait()
}

func TestSemanticToolSearchCancellation(t *testing.T) {
	const query = "find records"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text != query {
			cancel()
		}
		return []float64{1, 0}, nil
	}}
	handler, err := toolsearch.NewSemantic(embeddings)
	require.NoError(t, err)
	entry := semanticEntry("lookup", "")
	req := &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}}
	got, err := handler(ctx, req)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got)
	got, err = handler(ctx, req)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got)
	require.Equal(t, 1, embeddings.queryCalls(query), "pre-canceled context does not reach the embedder")
	require.Len(t, embeddings.candidateTexts(query), 1)

	got, err = handler(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{entry}, got)
	require.Len(t, embeddings.candidateTexts(query), 2, "cancellation during embedding leaves the vector uncached")
}

func TestSemanticToolSearchEmptyInputsAndRetryableEmbeddingErrors(t *testing.T) {
	handler, err := toolsearch.NewSemantic(nil)
	require.Error(t, err)
	require.Nil(t, handler)

	const query = "find tools"
	wantErr := errors.New("embedding temporarily unavailable")
	var failCandidate, failQuery bool
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text == query && failQuery || text != query && failCandidate {
			return nil, wantErr
		}
		return []float64{1, 0}, nil
	}}
	handler, err = toolsearch.NewSemantic(embeddings)
	require.NoError(t, err)
	entry := semanticEntry("lookup", "Look up a record.")
	for _, request := range []*responses.ToolSearchRequest{
		nil, {Query: query}, {Query: query, Candidates: []responses.ToolSearchEntry{{}}},
		{Candidates: []responses.ToolSearchEntry{entry}}, {Query: " \n\t ", Candidates: []responses.ToolSearchEntry{entry}},
	} {
		got, err := handler(context.Background(), request)
		require.NoError(t, err)
		require.Empty(t, got)
	}
	require.Empty(t, embeddings.texts, "empty searches do not consume embedding calls")

	request := &responses.ToolSearchRequest{Query: query, Candidates: []responses.ToolSearchEntry{entry}}
	failCandidate = true
	_, err = handler(context.Background(), request)
	require.ErrorIs(t, err, wantErr)
	failCandidate = false
	failQuery = true
	_, err = handler(context.Background(), request)
	require.ErrorIs(t, err, wantErr)
	failQuery = false
	got, err := handler(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, []responses.ToolSearchEntry{entry}, got, "failed embeddings remain retryable")

	mismatched, err := toolsearch.NewSemantic(&semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if text == query {
			return []float64{1, 0}, nil
		}
		return []float64{1}, nil
	}})
	require.NoError(t, err)
	_, err = mismatched(context.Background(), request)
	require.Error(t, err, "different dimensions cannot be scored as a cosine similarity")
}

func TestSemanticToolSearchCompletesResponsesAndBindsEachRunTool(t *testing.T) {
	embeddings := &semanticTestEmbedder{embed: func(_ context.Context, text string) ([]float64, error) {
		if strings.Contains(text, "\nNamespace: billing ") {
			return []float64{0, 1}, nil
		}
		return []float64{1, 0}, nil
	}}
	handler, err := toolsearch.NewSemantic(embeddings)
	require.NoError(t, err)
	for _, result := range []string{"original executor", "refreshed dynamic executor"} {
		t.Run(result, func(t *testing.T) {
			member := searchTestTool{name: "lookup", description: "Look up a customer."}
			actual := &semanticTestTool{declaration: *member.Declaration(), result: result}
			tools := agentTools(t, nil, deferred.ToolSet(searchTestToolSet{
				name: "crm", description: "Customer tools.", tools: []tool.Tool{actual},
			}), deferred.ToolSet(searchTestToolSet{
				name: "billing", description: "Billing tools.", tools: []tool.Tool{
					&semanticTestTool{declaration: *member.Declaration(), result: "other namespace"},
				},
			}))
			var bodies []map[string]any
			m := responses.New("gpt-5", responses.WithAPIKey("test"), responses.WithClientToolSearch(handler), responses.WithOpenAIOptions(
				option.WithMaxRetries(0),
				option.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					bodies = append(bodies, body)
					output := clientSearchOutput
					if len(bodies) == 2 {
						output = `{"id":"r2","status":"completed","output":[{"type":"function_call","id":"fc1","call_id":"call1","namespace":"crm","name":"lookup","arguments":"{\"id\":\"42\"}"}]}`
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(output))}, nil
				})}),
			))
			request := &model.Request{Messages: []model.Message{model.NewUserMessage("find customer 42")}, Tools: tools}
			ch, err := m.GenerateContent(context.Background(), request)
			require.NoError(t, err)
			responses := collect(t, ch)
			require.Len(t, responses, 1)
			require.Nil(t, responses[0].Error)
			require.False(t, responses[0].Done)

			var bound tool.Tool
			ctx := providerdata.WithActionTools(context.Background(), &providerdata.ActionTools{
				Bind: func(callID, name string, identity providerdata.ActionToolIdentity, tl tool.Tool) bool {
					require.Equal(t, "search_1", callID)
					require.Equal(t, "crm_lookup", name)
					require.Equal(t, providerdata.ActionToolIdentity{Namespace: "crm", Name: "lookup"}, identity)
					bound = tl
					return true
				},
			})
			messages, err := m.CompleteProviderActions(ctx, request, responses[0])
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.Same(t, tools["crm_lookup"], bound)
			require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[
			  {"type":"namespace","name":"crm","description":"Customer tools.","tools":[
			    {"type":"function","name":"lookup","description":"Look up a customer.","defer_loading":true,"strict":false,
			     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]}]}]`, string(messages[0].ProviderData.Payload))
			request.Messages = append(request.Messages, responses[0].Choices[0].Message)
			request.Messages = append(request.Messages, messages...)
			ch, err = m.GenerateContent(ctx, request)
			require.NoError(t, err)
			responses = collect(t, ch)
			require.Len(t, responses, 1)
			require.Nil(t, responses[0].Error)
			require.Equal(t, "crm_lookup", responses[0].Choices[0].Message.ToolCalls[0].Function.Name)
			value, err := bound.(tool.CallableTool).Call(ctx, []byte(`{"id":"42"}`))
			require.NoError(t, err)
			require.Equal(t, result, value, "each run dispatches to its own selected object")
			require.Len(t, bodies, 2)
			encoded, err := json.Marshal(bodies[1]["input"])
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"type":"tool_search_output"`)
			require.Len(t, request.Tools, 2, "search completion does not replace the request's registered tools")
		})
	}
	require.Len(t, embeddings.candidateTexts("orders"), 2, "the adapter caches both namespaced vectors without reusing a run's Tool")
	require.Equal(t, 2, embeddings.queryCalls("orders"))
}

// These fixtures register a deferred ToolSet in the same way as an LLM agent.
type searchTestTool struct {
	name        string
	description string
}

func (s searchTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: s.description,
		InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"id": {Type: "string"}}},
	}
}

type searchTestToolSet struct {
	name        string
	description string
	tools       []tool.Tool
}

func (s searchTestToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s searchTestToolSet) Close() error                      { return nil }
func (s searchTestToolSet) Name() string                      { return s.name }
func (s searchTestToolSet) Description() string               { return s.description }

func agentTools(t *testing.T, standalone []tool.Tool, sets ...tool.ToolSet) map[string]tool.Tool {
	t.Helper()
	out := make(map[string]tool.Tool)
	for _, tl := range standalone {
		out[tl.Declaration().Name] = tl
	}
	for _, set := range sets {
		for _, tl := range itool.NewNamedToolSet(set).Tools(context.Background()) {
			out[tl.Declaration().Name] = tl
		}
	}
	return out
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func collect(t *testing.T, ch <-chan *model.Response) []*model.Response {
	t.Helper()
	var out []*model.Response
	for resp := range ch {
		out = append(out, resp)
	}
	return out
}

const clientSearchOutput = `{"id":"r1","status":"completed","output":[
  {"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"opaque"},
  {"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"orders"}}]}`
