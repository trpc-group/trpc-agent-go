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
	"encoding/base64"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewBase64ConverterTool creates a Base64 converter tool.
func NewBase64ConverterTool() tool.CallableTool {
	return function.NewFunctionTool(
		encodeDecodeBase64,
		function.WithName("base64_converter"),
		function.WithDescription("Encode or decode text using Base64 encoding. Operations: 'encode' converts text to Base64, 'decode' converts Base64 back to original text."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Base64 conversion request",
			Required:    []string{"text", "operation"},
			Properties: map[string]*tool.Schema{
				"text": {
					Type:        "string",
					Description: "Text to encode or decode",
				},
				"operation": {
					Type:        "string",
					Description: "Operation type",
					Enum:        []any{"encode", "decode"},
				},
			},
		}),
	)
}

type base64Request struct {
	Text      string `json:"text"`
	Operation string `json:"operation"`
}

type base64Response struct {
	OriginalText string `json:"original_text"`
	Operation    string `json:"operation"`
	Result       string `json:"result"`
	Message      string `json:"message"`
}

func encodeDecodeBase64(_ context.Context, req base64Request) (base64Response, error) {
	var result string
	var message string

	switch req.Operation {
	case "encode":
		result = base64.StdEncoding.EncodeToString([]byte(req.Text))
		message = "Text Base64-encoded"
	case "decode":
		var err error
		decoded, err := base64.StdEncoding.DecodeString(req.Text)
		if err != nil {
			return base64Response{
				OriginalText: req.Text,
				Operation:    req.Operation,
				Result:       "",
				Message:      "Error: Invalid Base64 string",
			}, fmt.Errorf("invalid Base64 string: %w", err)
		}
		result = string(decoded)
		message = "Text Base64-decoded"
	default:
		return base64Response{
			OriginalText: req.Text,
			Operation:    req.Operation,
			Result:       "",
			Message:      "Error: Invalid operation. Use 'encode' or 'decode'",
		}, fmt.Errorf("invalid operation: %s", req.Operation)
	}

	return base64Response{
		OriginalText: req.Text,
		Operation:    req.Operation,
		Result:       result,
		Message:      message,
	}, nil
}
