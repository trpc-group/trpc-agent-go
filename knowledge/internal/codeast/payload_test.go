//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeast

import (
	"path/filepath"
	"reflect"
	"testing"
)

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
		Metadata:   map[string]any{},
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

func TestNodesToDocumentPayloads(t *testing.T) {
	result := &Result{
		Nodes: []*Node{
			nil,
			{
				Name:     "A",
				Type:     EntityFunction,
				Code:     "func A() {}",
				Language: LanguageGo,
				Scope:    ScopeCode,
			},
			{
				Name:     "B",
				Type:     EntityMethod,
				Code:     "func (s *S) B() {}",
				Language: LanguageGo,
				Scope:    ScopeCode,
			},
		},
	}

	payloads := NodesToDocumentPayloads(result, NodeDocumentPayloadOptions{})
	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
	if payloads[0].Name != "A" || payloads[1].Name != "B" {
		t.Fatalf("unexpected payload order: %q, %q", payloads[0].Name, payloads[1].Name)
	}
}

func TestNodeToDocumentPayloadOptionalBranches(t *testing.T) {
	node := &Node{
		Name:       "Do",
		Type:       EntityMethod,
		FullName:   "demo.Service.Do",
		Scope:      ScopeCode,
		Language:   LanguageGo,
		Comment:    "  comment with spaces  \n",
		Code:       "func (s *Service) Do() {}",
		ChunkIndex: 2,
		Metadata: map[string]any{
			"receiver_type": "Service",
		},
	}
	payload := NodeToDocumentPayload(node, NodeDocumentPayloadOptions{
		BaseMetadata: map[string]any{"source": "unit"},
		FileInfo: &FileInfo{
			Imports: []string{"context", "fmt"},
		},
	})
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload.Metadata[TrpcAstMetaPrefix+"type"] != "Method" {
		t.Fatalf("unexpected type metadata: %v", payload.Metadata[TrpcAstMetaPrefix+"type"])
	}
	if payload.Metadata[TrpcAstMetaPrefix+"comment"] != "comment with spaces" {
		t.Fatalf("unexpected trimmed comment: %v", payload.Metadata[TrpcAstMetaPrefix+"comment"])
	}
	imports, ok := payload.Metadata[TrpcAstMetaPrefix+"imports"].([]string)
	if !ok {
		t.Fatalf("imports metadata type mismatch: %T", payload.Metadata[TrpcAstMetaPrefix+"imports"])
	}
	if !reflect.DeepEqual(imports, []string{"context", "fmt"}) {
		t.Fatalf("unexpected imports metadata: %v", imports)
	}
	if payload.Metadata[TrpcAstMetaPrefix+"import_count"] != 2 {
		t.Fatalf("unexpected import count: %v", payload.Metadata[TrpcAstMetaPrefix+"import_count"])
	}
	if payload.Metadata[TrpcAstMetaPrefix+"receiver_type"] != "Service" {
		t.Fatalf("missing prefixed node metadata: %v", payload.Metadata[TrpcAstMetaPrefix+"receiver_type"])
	}
}

func TestIsExamplePath(t *testing.T) {
	repoRoot := filepath.Join("workspace", "repo")

	if !IsExamplePath(filepath.Join(repoRoot, "examples", "demo", "main.go"), repoRoot) {
		t.Fatal("expected examples path to be detected")
	}
	if !IsExamplePath(filepath.Join(repoRoot, "Example", "demo.go"), repoRoot) {
		t.Fatal("expected case-insensitive example path to be detected")
	}
	if IsExamplePath(filepath.Join(repoRoot, "internal", "service.go"), repoRoot) {
		t.Fatal("non-example path should not be detected as example")
	}
}

func TestSplitPath(t *testing.T) {
	parts := splitPath(filepath.Join("a", "b", "c.go"))
	if !reflect.DeepEqual(parts, []string{"a", "b", "c.go"}) {
		t.Fatalf("unexpected split parts: %v", parts)
	}

	if parts := splitPath(filepath.Join("a", "b", "")); !reflect.DeepEqual(parts, []string{"a", "b"}) {
		t.Fatalf("unexpected split parts for dir path: %v", parts)
	}

	if got := splitPath(""); len(got) != 0 {
		t.Fatalf("expected empty parts for empty path, got %v", got)
	}
}

func TestNodesToDocumentPayloadsNilResult(t *testing.T) {
	if payloads := NodesToDocumentPayloads(nil, NodeDocumentPayloadOptions{}); payloads != nil {
		t.Fatalf("expected nil payloads, got %v", payloads)
	}
}
