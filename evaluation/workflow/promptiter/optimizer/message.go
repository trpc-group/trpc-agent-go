//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageBuilder encodes one optimization request into one runner message.
type MessageBuilder func(ctx context.Context, request *Request) (*model.Message, error)

const defaultMessageTemplateText = `Optimize one PromptIter surface from the provided current value and aggregated gradients.

You will receive one optimization request for a single surface.
Return exactly one JSON object that can be unmarshaled into promptiter.SurfacePatch.

Requirements:
- Keep the response as raw JSON only.
- Do not wrap the response in markdown code fences.
- Preserve the request surface identity.
- The patch value must match the request surface type.
- Produce one replacement value and one concise reason.

Request JSON:
{{ toPrettyJSON . }}
`

func defaultMessageBuilder() MessageBuilder {
	tmpl, err := template.New("optimizer_default_message").Funcs(template.FuncMap{
		"toPrettyJSON": toPrettyJSON,
	}).Parse(defaultMessageTemplateText)
	if err != nil {
		return func(ctx context.Context, request *Request) (*model.Message, error) {
			return nil, fmt.Errorf("parse default optimization message template: %w", err)
		}
	}
	return func(ctx context.Context, request *Request) (*model.Message, error) {
		var content bytes.Buffer
		if err := tmpl.Execute(&content, request); err != nil {
			return nil, fmt.Errorf("render optimization message template: %w", err)
		}
		message := model.NewUserMessage(content.String())
		return &message, nil
	}
}

// toPrettyJSON renders one value as indented JSON for prompts.
func toPrettyJSON(value any) (string, error) {
	payloadJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal optimization request: %w", err)
	}
	return string(payloadJSON), nil
}
