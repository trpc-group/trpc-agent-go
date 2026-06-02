//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	maxPrintedToolItems = 2
	maxPrintedTextRunes = 120
	maxPrintedCodeRunes = 360
)

func formatToolResponse(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "<empty>"
	}
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return compactPreview(content, maxPrintedTextRunes*2)
	}
	if summary, ok := renderToolSummary(payload); ok {
		return summary
	}
	truncateToolPayload(payload)
	truncateLongStrings(payload)
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return compactPreview(content, maxPrintedTextRunes*2)
	}
	return string(pretty)
}

func formatToolArguments(content string) string {
	content = strings.TrimSpace(content)
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return content
	}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return content
	}
	return string(pretty)
}

func renderToolSummary(payload any) (string, bool) {
	obj, ok := payload.(map[string]any)
	if !ok {
		return "", false
	}
	var b strings.Builder
	rendered := false
	if documents, ok := obj["documents"].([]any); ok {
		renderDocuments(&b, documents)
		rendered = true
	}
	if nodes, ok := obj["nodes"].([]any); ok {
		renderNodes(&b, nodes)
		rendered = true
	}
	if edges, ok := obj["edges"].([]any); ok {
		renderEdges(&b, edges)
		rendered = true
	}
	if paths, ok := obj["paths"].([]any); ok {
		renderPaths(&b, paths)
		rendered = true
	}
	if !rendered {
		return "", false
	}
	if message := stringValue(obj, "message"); message != "" {
		fmt.Fprintf(&b, "message: %s\n", compactPreview(message, maxPrintedTextRunes*2))
	}
	if truncated := stringValue(obj, "truncated"); truncated != "" {
		fmt.Fprintf(&b, "truncated: %s\n", truncated)
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func renderDocuments(b *strings.Builder, documents []any) {
	fmt.Fprintf(b, "documents: %d\n", len(documents))
	for i, item := range firstToolItems(documents) {
		doc, ok := item.(map[string]any)
		if !ok {
			fmt.Fprintf(b, "  [%d] %s\n", i+1, compactPreview(item, maxPrintedTextRunes))
			continue
		}
		metadata, _ := doc["metadata"].(map[string]any)
		title := firstNonEmpty(
			stringValue(metadata, "trpc_ast_name"),
			stringValue(metadata, "trpc_ast_full_name"),
			stringValue(metadata, "name"),
			stringValue(metadata, "full_name"),
		)
		if title == "" {
			title = "document"
		}
		score := numberValue(doc, "score")
		if score != "" {
			fmt.Fprintf(b, "  [%d] %s score=%s\n", i+1, title, score)
		} else {
			fmt.Fprintf(b, "  [%d] %s\n", i+1, title)
		}
		if fullName := stringValue(metadata, "trpc_ast_full_name"); fullName != "" && fullName != title {
			fmt.Fprintf(b, "      full: %s\n", compactPreview(fullName, maxPrintedTextRunes*2))
		}
		if location := metadataLocation(metadata); location != "" {
			fmt.Fprintf(b, "      file: %s\n", location)
		}
		if signature := stringValue(metadata, "trpc_ast_signature"); signature != "" {
			fmt.Fprintf(b, "      sig: %s\n", compactPreview(signature, maxPrintedTextRunes*2))
		}
		if i == 0 {
			if text := codePreview(doc["text"]); text != "" {
				fmt.Fprintf(b, "      code:\n%s\n", indentBlock(text, "        "))
			}
			continue
		}
		if text := compactPreview(doc["text"], maxPrintedTextRunes); text != "" {
			fmt.Fprintf(b, "      code: %s\n", text)
		}
	}
	renderOmitted(b, len(documents), "document")
}

func renderNodes(b *strings.Builder, nodes []any) {
	fmt.Fprintf(b, "nodes: %d\n", len(nodes))
	for i, item := range firstToolItems(nodes) {
		node, ok := item.(map[string]any)
		if !ok {
			fmt.Fprintf(b, "  [%d] %s\n", i+1, compactPreview(item, maxPrintedTextRunes))
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		title := firstNonEmpty(
			stringValue(node, "name"),
			stringValue(metadata, "trpc_ast_name"),
			stringValue(metadata, "trpc_ast_full_name"),
			stringValue(node, "id"),
		)
		fmt.Fprintf(b, "  [%d] %s\n", i+1, compactPreview(title, maxPrintedTextRunes))
		if fullName := stringValue(metadata, "trpc_ast_full_name"); fullName != "" && fullName != title {
			fmt.Fprintf(b, "      full: %s\n", compactPreview(fullName, maxPrintedTextRunes*2))
		}
		if location := metadataLocation(metadata); location != "" {
			fmt.Fprintf(b, "      file: %s\n", location)
		}
		if i == 0 {
			if content := codePreview(node["content"]); content != "" {
				fmt.Fprintf(b, "      content:\n%s\n", indentBlock(content, "        "))
			}
			continue
		}
		if content := compactPreview(node["content"], maxPrintedTextRunes); content != "" {
			fmt.Fprintf(b, "      content: %s\n", content)
		}
	}
	renderOmitted(b, len(nodes), "node")
}

func renderEdges(b *strings.Builder, edges []any) {
	fmt.Fprintf(b, "edges: %d\n", len(edges))
	for i, item := range firstToolItems(edges) {
		edge, ok := item.(map[string]any)
		if !ok {
			fmt.Fprintf(b, "  [%d] %s\n", i+1, compactPreview(item, maxPrintedTextRunes))
			continue
		}
		edgeType := firstNonEmpty(stringValue(edge, "type"), stringValue(edge, "label"), stringValue(edge, "name"))
		if edgeType == "" {
			edgeType = "edge"
		}
		from := firstNonEmpty(stringValue(edge, "from_id"), stringValue(edge, "from"), stringValue(edge, "source"))
		to := firstNonEmpty(stringValue(edge, "to_id"), stringValue(edge, "to"), stringValue(edge, "target"))
		fmt.Fprintf(b, "  [%d] %s: %s -> %s\n", i+1, compactPreview(edgeType, 80), compactPreview(from, 120), compactPreview(to, 120))
	}
	renderOmitted(b, len(edges), "edge")
}

func renderPaths(b *strings.Builder, paths []any) {
	fmt.Fprintf(b, "paths: %d\n", len(paths))
	for i, item := range firstToolItems(paths) {
		path, ok := item.(map[string]any)
		if !ok {
			fmt.Fprintf(b, "  [%d] %s\n", i+1, compactPreview(item, maxPrintedTextRunes))
			continue
		}
		nodes, _ := path["nodes"].([]any)
		edges, _ := path["edges"].([]any)
		fmt.Fprintf(b, "  [%d] nodes=%d edges=%d\n", i+1, len(nodes), len(edges))
		if nodeLine := compactGraphNodeLine(nodes); nodeLine != "" {
			fmt.Fprintf(b, "      nodes: %s\n", nodeLine)
		}
		if edgeLine := compactGraphEdgeLine(edges); edgeLine != "" {
			fmt.Fprintf(b, "      edges: %s\n", edgeLine)
		}
	}
	renderOmitted(b, len(paths), "path")
}

func truncateToolPayload(payload any) {
	obj, ok := payload.(map[string]any)
	if !ok {
		return
	}
	truncateToolArray(obj, "documents", "document")
	truncateToolArray(obj, "nodes", "node")
	truncateToolArray(obj, "edges", "edge")
	truncateToolArray(obj, "paths", "path")
}

func truncateToolArray(obj map[string]any, key, label string) {
	items, ok := obj[key].([]any)
	if !ok || len(items) <= maxPrintedToolItems {
		return
	}
	omitted := len(items) - maxPrintedToolItems
	truncated := append([]any(nil), items[:maxPrintedToolItems]...)
	truncated = append(truncated, map[string]any{
		"omitted": fmt.Sprintf("... %d more %s(s) omitted", omitted, label),
	})
	obj[key] = truncated
}

func truncateLongStrings(payload any) {
	switch v := payload.(type) {
	case map[string]any:
		for key, value := range v {
			if text, ok := value.(string); ok {
				v[key] = compactPreview(text, maxPrintedTextRunes)
				continue
			}
			truncateLongStrings(value)
		}
	case []any:
		for _, item := range v {
			truncateLongStrings(item)
		}
	}
}

func firstToolItems(items []any) []any {
	if len(items) <= maxPrintedToolItems {
		return items
	}
	return items[:maxPrintedToolItems]
}

func renderOmitted(b *strings.Builder, total int, label string) {
	if omitted := total - maxPrintedToolItems; omitted > 0 {
		fmt.Fprintf(b, "  ... %d more %s(s) omitted\n", omitted, label)
	}
}

func compactGraphNodeLine(nodes []any) string {
	names := make([]string, 0, len(firstToolItems(nodes)))
	for _, item := range firstToolItems(nodes) {
		node, ok := item.(map[string]any)
		if !ok {
			names = append(names, compactPreview(item, 80))
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		names = append(names, compactPreview(firstNonEmpty(
			stringValue(node, "name"),
			stringValue(metadata, "trpc_ast_name"),
			stringValue(node, "id"),
		), 80))
	}
	if omitted := len(nodes) - maxPrintedToolItems; omitted > 0 {
		names = append(names, fmt.Sprintf("... %d more", omitted))
	}
	return strings.Join(names, " -> ")
}

func compactGraphEdgeLine(edges []any) string {
	types := make([]string, 0, len(firstToolItems(edges)))
	for _, item := range firstToolItems(edges) {
		edge, ok := item.(map[string]any)
		if !ok {
			types = append(types, compactPreview(item, 60))
			continue
		}
		types = append(types, compactPreview(firstNonEmpty(
			stringValue(edge, "type"),
			stringValue(edge, "label"),
			stringValue(edge, "name"),
		), 60))
	}
	if omitted := len(edges) - maxPrintedToolItems; omitted > 0 {
		types = append(types, fmt.Sprintf("... %d more", omitted))
	}
	return strings.Join(types, " -> ")
}

func metadataLocation(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	file := firstNonEmpty(stringValue(metadata, "trpc_ast_file_path"), stringValue(metadata, "file_path"))
	repo := firstNonEmpty(stringValue(metadata, "trpc_ast_repo_name"), stringValue(metadata, "repo_name"))
	start := stringValue(metadata, "trpc_ast_line_start")
	end := stringValue(metadata, "trpc_ast_line_end")
	location := file
	if file != "" && start != "" && end != "" {
		location = fmt.Sprintf("%s:%s-%s", file, start, end)
	} else if file != "" && start != "" {
		location = fmt.Sprintf("%s:%s", file, start)
	}
	if repo != "" && location != "" {
		return fmt.Sprintf("%s %s", repo, location)
	}
	return firstNonEmpty(location, repo)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringValue(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	switch value := obj[key].(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func numberValue(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	switch value := obj[key].(type) {
	case float64:
		return strconv.FormatFloat(value, 'f', 3, 64)
	case json.Number:
		return value.String()
	case string:
		return value
	default:
		return ""
	}
}

func compactPreview(value any, limit int) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func codePreview(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxPrintedCodeRunes {
		return text
	}
	return string(runes[:maxPrintedCodeRunes]) + "\n..."
}

func indentBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
