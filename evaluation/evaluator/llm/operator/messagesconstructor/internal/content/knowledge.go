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
	"unicode"
	"unicode/utf8"

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
		kResp = sanitizeKnowledgeSearchResponse(kResp)
		payload, err := json.Marshal(kResp)
		if err != nil {
			return "", fmt.Errorf("marshal tool %s response: %w", resp.Name, err)
		}
		builder.Write(payload)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

// parseKnowledgeSearchResponse converts a function response payload into a typed knowledge search response.
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

// sanitizeKnowledgeSearchResponse cleans text fields while preserving original structure.
func sanitizeKnowledgeSearchResponse(resp *tool.KnowledgeSearchResponse) *tool.KnowledgeSearchResponse {
	if resp == nil {
		return nil
	}
	clean := *resp
	if len(resp.Documents) == 0 {
		return &clean
	}
	clean.Documents = make([]*tool.DocumentResult, len(resp.Documents))
	for i, doc := range resp.Documents {
		if doc == nil {
			continue
		}
		cp := *doc
		cp.Text = sanitizeKnowledgeText(cp.Text)
		clean.Documents[i] = &cp
	}
	return &clean
}

// sanitizeKnowledgeText guards against non-text payloads by pruning invalid or mostly non-printable content.
func sanitizeKnowledgeText(text string) string {
	if text == "" {
		return text
	}
	if !utf8.ValidString(text) {
		return "[non-text content omitted]"
	}
	var (
		printable int
		total     int
	)
	for _, r := range text {
		total++
		if unicode.IsPrint(r) || r == '\n' || r == '\t' || r == '\r' {
			printable++
		}
	}
	if total == 0 {
		return text
	}
	ratio := float64(printable) / float64(total)
	if ratio < 0.6 {
		return "[non-text content omitted]"
	}
	return text
}
