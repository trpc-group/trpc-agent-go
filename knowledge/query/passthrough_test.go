package query

import "testing"

func TestPassthroughEnhancer(t *testing.T) {
	pe := NewPassthroughEnhancer()
	req := &Request{Query: "hello"}
	enhanced, err := pe.EnhanceQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enhanced.Enhanced != "hello" {
		t.Fatalf("expected same query")
	}
	if len(enhanced.Keywords) != 1 || enhanced.Keywords[0] != "hello" {
		t.Fatalf("keywords mismatch")
	}
}
