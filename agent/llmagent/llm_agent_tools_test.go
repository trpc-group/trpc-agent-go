//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// minimalKnowledge implements knowledge.Knowledge with no-op behaviors for unit tests.
type minimalKnowledge struct{}

func (m *minimalKnowledge) Search(_ context.Context, _ *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	return nil, nil
}

// dummyToolSet returns a fixed tool for coverage.
type dummyToolSet struct{}

func (d dummyToolSet) Tools(ctx context.Context) []tool.Tool {
	// Wrap the tool to a CallableTool by asserting to the known concrete type.
	kt := knowledgetool.NewKnowledgeSearchTool(&minimalKnowledge{}, nil)
	type callable interface{ tool.CallableTool }
	if c, ok := any(kt).(callable); ok {
		return []tool.Tool{c}
	}
	return nil
}
func (d dummyToolSet) Close() error { return nil }

// dummyTool implements tool.Tool.
type dummyTool struct{ decl *tool.Declaration }

func (d dummyTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }
func (d dummyTool) Declaration() *tool.Declaration                { return d.decl }

func TestRegisterTools_Combinations(t *testing.T) {
	base := []tool.Tool{dummyTool{decl: &tool.Declaration{Name: "a"}}}
	sets := []tool.ToolSet{dummyToolSet{}}
	kb := &minimalKnowledge{}

	// with tools, toolset and knowledge and nil memory.
	tools := registerTools(&Options{Tools: base, ToolSets: sets, Knowledge: kb})
	if len(tools) < 2 {
		t.Fatalf("expected aggregated tools from base and toolset")
	}
}

func TestLLMAgent_Tools_IncludesTransferWhenSubAgents(t *testing.T) {
	sub1 := New("sub-1")
	agt := New("main", WithSubAgents([]agent.Agent{sub1}))

	ts := agt.Tools()
	foundTransfer := false
	for _, tl := range ts {
		if tl.Declaration().Name == "transfer_to_agent" {
			foundTransfer = true
			break
		}
	}
	if !foundTransfer {
		t.Fatalf("expected transfer_to_agent tool when sub agents exist")
	}
}
