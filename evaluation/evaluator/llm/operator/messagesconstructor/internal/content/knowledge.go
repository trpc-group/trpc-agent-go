//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package content

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

var knowledgeTools = map[string]struct{}{
	"knowledge_search":                     {},
	"knowledge_search_with_agentic_filter": {},
}

// ExtractKnowledgeRecall builds a human-readable summary of knowledge tool responses.
func ExtractKnowledgeRecall(tools []*evalset.Tool) (string, error) {
	if len(tools) == 0 {
		return "", nil
	}
	var builder strings.Builder
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if _, ok := knowledgeTools[tool.Name]; !ok {
			continue
		}
		kResp, err := parseKnowledgeSearchResponse(tool)
		if err != nil {
			return "", fmt.Errorf("parse tool %s response: %w", tool.Name, err)
		}
		if kResp == nil {
			continue
		}
		payload, err := json.Marshal(kResp)
		if err != nil {
			return "", fmt.Errorf("marshal tool %s response: %w", tool.Name, err)
		}
		builder.Write(payload)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

// parseKnowledgeSearchResponse converts a function response payload into a typed knowledge search response.
func parseKnowledgeSearchResponse(t *evalset.Tool) (*tool.KnowledgeSearchResponse, error) {
	if t == nil {
		return nil, nil
	}
	data, err := json.Marshal(t.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tool %s result: %w", t.Name, err)
	}
	// var res tool.KnowledgeSearchResponse
	var res tool.KnowledgeSearchResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("unmarshal tool %s result: %w", t.Name, err)
	}
	return &res, nil
}
