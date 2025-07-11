package knowledge

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/retriever"
)

// stubRetriever implements retriever.Retriever for tests.
type stubRetriever struct {
	result *retriever.Result
	err    error
}

func (s stubRetriever) Retrieve(ctx context.Context, q *retriever.Query) (*retriever.Result, error) {
	return s.result, s.err
}

func (s stubRetriever) Close() error { return nil }

// stubEnhancer implements query.Enhancer for tests.

type stubEnhancer struct {
	err bool
}

func (s stubEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
	if s.err {
		return nil, errors.New("enhance failed")
	}
	return &query.Enhanced{Enhanced: req.Query + " enhanced"}, nil
}

func TestBuiltinKnowledge_Search(t *testing.T) {
	ctx := context.Background()

	// Case 1: missing retriever should error.
	kb := &BuiltinKnowledge{}
	_, err := kb.Search(ctx, &SearchRequest{Query: "hello"})
	if err == nil {
		t.Fatalf("expected error when retriever not configured")
	}

	// Case 2: retriever returns no docs.
	kb = &BuiltinKnowledge{retriever: stubRetriever{result: &retriever.Result{Documents: nil}}}
	_, err = kb.Search(ctx, &SearchRequest{Query: "hello"})
	if err == nil {
		t.Fatalf("expected error when no docs returned")
	}

	// Case 3: successful path with enhancer.
	doc := &document.Document{ID: "1", Content: "content"}
	rel := &retriever.RelevantDocument{Document: doc, Score: 0.9}
	kb = &BuiltinKnowledge{
		retriever:     stubRetriever{result: &retriever.Result{Documents: []*retriever.RelevantDocument{rel}}},
		queryEnhancer: stubEnhancer{},
	}
	res, err := kb.Search(ctx, &SearchRequest{Query: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Document.ID != "1" {
		t.Fatalf("unexpected doc id: %s", res.Document.ID)
	}

	// Case 4: enhancer error propagates.
	kb = &BuiltinKnowledge{
		retriever:     stubRetriever{result: &retriever.Result{Documents: []*retriever.RelevantDocument{rel}}},
		queryEnhancer: stubEnhancer{err: true},
	}
	_, err = kb.Search(ctx, &SearchRequest{Query: "hello"})
	if err == nil {
		t.Fatalf("expected error from enhancer")
	}
}
