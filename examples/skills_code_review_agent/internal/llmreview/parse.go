//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmreview

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

// ParseFindings extracts structured findings from an LLM response.
func ParseFindings(content string) ([]findings.Finding, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}
	content = stripCodeFence(content)

	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, nil
	}

	var items []findings.Finding
	if err := json.Unmarshal([]byte(content[start:end+1]), &items); err != nil {
		return nil, fmt.Errorf("decode llm findings: %w", err)
	}
	for i := range items {
		if items[i].Source == "" {
			items[i].Source = "llm"
		}
	}
	return items, nil
}

func stripCodeFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 3 {
		return content
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, "```") {
		return content
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
