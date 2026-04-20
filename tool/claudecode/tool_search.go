//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newToolSearchTool(listTools func() []tool.Tool) (tool.Tool, error) {
	if listTools == nil {
		return nil, fmt.Errorf("tool list provider is required")
	}
	return function.NewFunctionTool(
		func(_ context.Context, in toolSearchInput) (toolSearchOutput, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return toolSearchOutput{}, fmt.Errorf("query is required")
			}
			maxResults := 5
			if in.MaxResults != nil && *in.MaxResults > 0 {
				maxResults = *in.MaxResults
			}
			allTools := searchableTools(listTools())
			if len(allTools) == 0 {
				return toolSearchOutput{
					Matches:            []string{},
					Query:              query,
					TotalDeferredTools: 0,
				}, nil
			}
			var matches []string
			if strings.HasPrefix(strings.ToLower(query), "select:") {
				matches = selectToolNames(strings.TrimSpace(query[len("select:"):]), allTools)
			} else {
				matches = keywordSearchTools(query, allTools, maxResults)
			}
			return toolSearchOutput{
				Matches:            matches,
				Query:              query,
				TotalDeferredTools: 0,
			}, nil
		},
		function.WithName(toolToolSearch),
		function.WithDescription(toolSearchDescription()),
	), nil
}

func toolSearchDescription() string {
	return `Search the currently available tools by keyword or direct selection.

Usage:
- Use query="select:ToolA,ToolB" to request exact tool names directly.
- Use a normal keyword query to search currently exposed tools by name and description.
- This tool is for discovery only. It returns matching tool names, not tool schemas or tool results.`
}

func searchableTools(tools []tool.Tool) []tool.Tool {
	out := make([]tool.Tool, 0, len(tools))
	seen := map[string]struct{}{}
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		name := strings.TrimSpace(candidate.Declaration().Name)
		if name == "" || name == toolToolSearch {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func selectToolNames(raw string, tools []tool.Tool) []string {
	requested := strings.Split(raw, ",")
	out := make([]string, 0, len(requested))
	seen := map[string]struct{}{}
	for _, item := range requested {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if candidateName, ok := findToolName(name, tools); ok {
			if _, exists := seen[candidateName]; !exists {
				seen[candidateName] = struct{}{}
				out = append(out, candidateName)
			}
		}
	}
	return out
}

func findToolName(name string, tools []tool.Tool) (string, bool) {
	for _, candidate := range tools {
		decl := candidate.Declaration()
		if decl == nil || decl.Name != name {
			continue
		}
		return decl.Name, true
	}
	return "", false
}

func keywordSearchTools(query string, tools []tool.Tool, maxResults int) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return []string{}
	}
	queryLower := strings.ToLower(query)
	if exact := exactToolMatch(queryLower, tools); exact != "" {
		return []string{exact}
	}
	requiredTerms, optionalTerms := splitToolSearchTerms(queryLower)
	allTerms := optionalTerms
	if len(requiredTerms) > 0 {
		allTerms = append(append([]string{}, requiredTerms...), optionalTerms...)
	}
	if len(allTerms) == 0 {
		return []string{}
	}
	return scoreKeywordToolMatches(requiredTerms, allTerms, tools, maxResults)
}

func exactToolMatch(queryLower string, tools []tool.Tool) string {
	for _, candidate := range tools {
		decl := candidate.Declaration()
		if decl == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(decl.Name), queryLower) {
			return decl.Name
		}
	}
	return ""
}

func scoreKeywordToolMatches(requiredTerms []string, allTerms []string, tools []tool.Tool, maxResults int) []string {
	type scoredTool struct {
		Name  string
		Score int
	}
	scored := make([]scoredTool, 0, len(tools))
	for _, candidate := range tools {
		decl := candidate.Declaration()
		if decl == nil {
			continue
		}
		nameInfo := parseToolName(decl.Name)
		desc := strings.ToLower(decl.Description)
		if len(requiredTerms) > 0 && !toolMatchesRequiredTerms(nameInfo, desc, requiredTerms) {
			continue
		}
		score := 0
		for _, term := range allTerms {
			switch {
			case slices.Contains(nameInfo.Parts, term):
				score += 10
			case containsAnyPart(nameInfo.Parts, term):
				score += 5
			case strings.Contains(nameInfo.Full, term):
				score += 3
			}
			if containsWholeWord(desc, term) {
				score += 2
			}
		}
		if score == 0 {
			continue
		}
		scored = append(scored, scoredTool{Name: decl.Name, Score: score})
	}
	slices.SortStableFunc(scored, func(left scoredTool, right scoredTool) int {
		if left.Score == right.Score {
			return strings.Compare(left.Name, right.Name)
		}
		if left.Score > right.Score {
			return -1
		}
		return 1
	})
	if maxResults > len(scored) {
		maxResults = len(scored)
	}
	out := make([]string, 0, maxResults)
	for _, item := range scored[:maxResults] {
		out = append(out, item.Name)
	}
	return out
}

func splitToolSearchTerms(query string) ([]string, []string) {
	parts := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '+')
	})
	required := make([]string, 0, len(parts))
	optional := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "+") {
			term := strings.TrimPrefix(part, "+")
			if term != "" {
				required = append(required, term)
			}
			continue
		}
		if len(part) < 2 {
			continue
		}
		optional = append(optional, part)
	}
	return required, optional
}

type toolNameInfo struct {
	Parts []string
	Full  string
}

func parseToolName(name string) toolNameInfo {
	parts := make([]string, 0, 8)
	var current strings.Builder
	for _, r := range name {
		switch {
		case r == '_':
			if current.Len() > 0 {
				parts = append(parts, strings.ToLower(current.String()))
				current.Reset()
			}
		case unicode.IsUpper(r) && current.Len() > 0:
			parts = append(parts, strings.ToLower(current.String()))
			current.Reset()
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, strings.ToLower(current.String()))
	}
	return toolNameInfo{
		Parts: parts,
		Full:  strings.Join(parts, " "),
	}
}

func toolMatchesRequiredTerms(
	nameInfo toolNameInfo,
	description string,
	requiredTerms []string,
) bool {
	for _, term := range requiredTerms {
		if slices.Contains(nameInfo.Parts, term) {
			continue
		}
		if containsAnyPart(nameInfo.Parts, term) {
			continue
		}
		if containsWholeWord(description, term) {
			continue
		}
		return false
	}
	return true
}

func containsAnyPart(parts []string, term string) bool {
	for _, part := range parts {
		if strings.Contains(part, term) {
			return true
		}
	}
	return false
}

func containsWholeWord(text string, term string) bool {
	if text == "" || term == "" {
		return false
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	})
	for _, part := range parts {
		if part == term {
			return true
		}
	}
	return false
}
