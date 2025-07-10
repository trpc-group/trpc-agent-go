package retriever

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	q "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	r "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// dummyEmbedder returns constant vector.
type dummyEmbedder struct{}

func (dummyEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return []float64{1, 0, 0}, nil
}
func (dummyEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, _ := dummyEmbedder{}.GetEmbedding(ctx, text)
	return v, map[string]any{"t": 1}, nil
}
func (dummyEmbedder) GetDimensions() int { return 3 }

func TestDefaultRetriever(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc1", Content: "hello"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithQueryEnhancer(q.NewPassthroughEnhancer()),
		WithReranker(r.NewTopKReranker()),
	)

	res, err := d.Retrieve(context.Background(), &Query{Text: "hi", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve err: %v", err)
	}
	if len(res.Documents) != 1 || res.Documents[0].Document.ID != "doc1" {
		t.Fatalf("unexpected results")
	}
}
