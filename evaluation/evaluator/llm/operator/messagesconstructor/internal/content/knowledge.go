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

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
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
		if _, ok := knowledgeTools[resp.Name]; !ok {
			continue
		}
		kResp, err := parseKnowledgeSearchResponse(resp)
		if err != nil {
			return "", fmt.Errorf("parse tool %s response: %w", resp.Name, err)
		}
		if kResp == nil {
			continue
		}
		payload, err := json.Marshal(kResp)
		if err != nil {
			return "", fmt.Errorf("marshal tool %s response: %w", resp.Name, err)
		}
		builder.Write(payload)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func parseKnowledgeSearchResponse(resp *genai.FunctionResponse) (*tool.KnowledgeSearchResponse, error) {
	if resp == nil || resp.Response == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(resp.Response)
	if err != nil {
		return nil, err
	}
	var res tool.KnowledgeSearchResponse
	if err := json.Unmarshal(bytes, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
