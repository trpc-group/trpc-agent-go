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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var knowledgeTools = map[string]struct{}{
	"knowledge_search":                     {},
	"knowledge_search_with_agentic_filter": {},
}

// ExtractKnowledgeRecall builds a human-readable summary of knowledge tool responses.
func ExtractKnowledgeRecall(intermediateData *evalset.IntermediateData) (string, error) {
	if intermediateData == nil || len(intermediateData.ToolResponses) == 0 {
		return "", nil
	}
	var builder strings.Builder
	for _, resp := range intermediateData.ToolResponses {
		if resp == nil {
			continue
		}
		if _, ok := knowledgeTools[resp.ToolName]; !ok {
			continue
		}
		kResp, err := parseKnowledgeSearchResponse(resp)
		if err != nil {
			return "", fmt.Errorf("parse tool %s response: %w", resp.ToolName, err)
		}
		if kResp == nil {
			continue
		}
		payload, err := json.Marshal(kResp)
		if err != nil {
			return "", fmt.Errorf("marshal tool %s response: %w", resp.ToolName, err)
		}
		builder.Write(payload)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

// parseKnowledgeSearchResponse converts a function response payload into a typed knowledge search response.
func parseKnowledgeSearchResponse(resp *model.Message) (*tool.KnowledgeSearchResponse, error) {
	if resp == nil {
		return nil, nil
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return nil, fmt.Errorf("empty tool response content")
	}
	var res tool.KnowledgeSearchResponse
	if err := json.Unmarshal([]byte(content), &res); err != nil {
		return nil, err
	}
	return &res, nil
}
