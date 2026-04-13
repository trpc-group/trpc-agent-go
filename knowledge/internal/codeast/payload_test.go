//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeast

import "testing"

func TestNodeToDocumentPayload(t *testing.T) {
	node := &Node{
		ID:         "demo.Service.Do",
		Type:       EntityMethod,
		Name:       "Do",
		FullName:   "demo.Service.Do",
		Scope:      ScopeCode,
		Language:   LanguageGo,
		Signature:  "func (s *Service) Do() error",
		Comment:    " Do something.\n",
		Code:       "func (s *Service) Do() error { return nil }",
		FilePath:   "service.go",
		LineStart:  12,
		LineEnd:    12,
		ChunkIndex: 3,
		Package:    "example.com/demo",
		Metadata: map[string]any{},
	}

	payload := NodeToDocumentPayload(node, NodeDocumentPayloadOptions{
		BaseMetadata: map[string]any{"source": "unit-test"},
		FileInfo: &FileInfo{
			Imports: []string{"context"},
		},
		FormatType: func(entityType EntityType) string {
			return string(entityType)
		},
		BuildEmbeddingText: func(node *Node) string {
			return node.FullName
		},
	})

	if payload == nil {
		t.Fatal("expected payload")
	}
	if got := payload.Metadata[TrpcAstMetaPrefix+"type"]; got != "Method" {
		t.Fatalf("unexpected type metadata: %v", got)
	}
	if got := payload.Metadata[TrpcAstMetaPrefix+"imports"]; got == nil {
		t.Fatal("expected imports metadata")
	}
	if got := payload.Metadata["trpc_agent_go_chunk_index"]; got != 3 {
		t.Fatalf("unexpected chunk index: %v", got)
	}
	if payload.EmbeddingText != "demo.Service.Do" {
		t.Fatalf("unexpected embedding text: %s", payload.EmbeddingText)
	}
}

func TestNodesToDocumentPayloadsNilResult(t *testing.T) {
	if payloads := NodesToDocumentPayloads(nil, NodeDocumentPayloadOptions{}); payloads != nil {
		t.Fatalf("expected nil payloads, got %v", payloads)
	}
}
