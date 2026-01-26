//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewTextTool creates a text processing tool.
func NewTextTool() tool.CallableTool {
	return function.NewFunctionTool(
		processText,
		function.WithName("text_tool"),
		function.WithDescription("Process text content. Supported operations: 'uppercase'(to uppercase), 'lowercase'(to lowercase), 'length'(calculate length), 'reverse'(reverse text), 'words'(count words), 'trim'(remove whitespace)"),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Text processing request",
			Required:    []string{"text", "operation"},
			Properties: map[string]*tool.Schema{
				"text": {
					Type:        "string",
					Description: "Text content to process",
				},
				"operation": {
					Type:        "string",
					Description: "Text operation type to perform",
					Enum:        []any{"uppercase", "lowercase", "length", "reverse", "words", "trim"},
				},
			},
		}),
	)
}

type textRequest struct {
	Text      string `json:"text"`
	Operation string `json:"operation"`
}

type textResponse struct {
	OriginalText string `json:"original_text"`
	Operation    string `json:"operation"`
	Result       string `json:"result"`
	Info         string `json:"info"`
}

func processText(_ context.Context, req textRequest) (textResponse, error) {
	var result string
	var info string

	switch req.Operation {
	case "uppercase":
		result = strings.ToUpper(req.Text)
		info = "Text converted to uppercase"
	case "lowercase":
		result = strings.ToLower(req.Text)
		info = "Text converted to lowercase"
	case "length":
		length := len([]rune(req.Text))
		result = fmt.Sprintf("%d", length)
		info = fmt.Sprintf("Text length is %d characters", length)
	case "reverse":
		runes := []rune(req.Text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		result = string(runes)
		info = "Text reversed"
	case "words":
		words := strings.Fields(req.Text)
		result = fmt.Sprintf("%d", len(words))
		info = fmt.Sprintf("Text contains %d words", len(words))
	case "trim":
		result = strings.TrimSpace(req.Text)
		info = "Leading and trailing whitespace removed"
	default:
		result = req.Text
		info = "Invalid operation type"
	}

	return textResponse{
		OriginalText: req.Text,
		Operation:    req.Operation,
		Result:       result,
		Info:         info,
	}, nil
}
