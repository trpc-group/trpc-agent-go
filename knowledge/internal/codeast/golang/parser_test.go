//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package golang

import "testing"

func TestParseContentBuildsNodesAndReservesEdges(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseContent("service.go", `package demo

import "context"

type Service struct{}

func (s *Service) Do(ctx context.Context) error {
	return nil
}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if result.File == nil {
		t.Fatal("expected file info to be present")
	}
	if result.File.Package != "demo" {
		t.Fatalf("package = %s, want demo", result.File.Package)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(result.Nodes))
	}
	if result.Nodes[0].ID != "demo.Service" {
		t.Fatalf("first node id = %s, want demo.Service", result.Nodes[0].ID)
	}
	if len(result.Nodes[1].Imports) != 1 || result.Nodes[1].Imports[0] != "context" {
		t.Fatalf("method imports = %v, want [context]", result.Nodes[1].Imports)
	}
	if result.Nodes[1].ChunkIndex != 1 {
		t.Fatalf("method chunk index = %d, want 1", result.Nodes[1].ChunkIndex)
	}
	if result.Edges == nil {
		t.Fatal("expected edges slice to be initialized")
	}
	if len(result.Edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(result.Edges))
	}
}
