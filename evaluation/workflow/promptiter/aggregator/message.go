//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageBuilder encodes one aggregation request into one runner message.
type MessageBuilder func(ctx context.Context, request *Request) (*model.Message, error)

const defaultMessageTemplateText = `Aggregate PromptIter gradients for a single surface.

You will receive one aggregation request with all sample-level gradients that belong to the same surface.
Return exactly one JSON object with a Gradients field.

Requirements:
- Keep the response as raw JSON only.
- Do not wrap the response in markdown code fences.
- Return only merged gradient items. The caller will attach the target surface identity and type.
- Merge duplicated or overlapping gradients when appropriate.
- Drop clearly empty or redundant gradient items.

Request JSON:
{{ toPrettyJSON . }}
`

func defaultMessageBuilder() MessageBuilder {
	tmpl, err := template.New("aggregator_default_message").Funcs(template.FuncMap{
		"toPrettyJSON": toPrettyJSON,
	}).Parse(defaultMessageTemplateText)
	if err != nil {
		return func(ctx context.Context, request *Request) (*model.Message, error) {
			return nil, fmt.Errorf("parse default aggregation message template: %w", err)
		}
	}
	return func(ctx context.Context, request *Request) (*model.Message, error) {
		var content bytes.Buffer
		if err := tmpl.Execute(&content, newPromptData(request)); err != nil {
			return nil, fmt.Errorf("render aggregation message template: %w", err)
		}
		message := model.NewUserMessage(content.String())
		return &message, nil
	}
}

type promptData struct {
	Surface   promptSurface
	Gradients []promptGradient
}

type promptSurface struct {
	Type astructure.SurfaceType
}

type promptGradient struct {
	Severity promptiter.LossSeverity
	Gradient string
}

func newPromptData(request *Request) promptData {
	if request == nil {
		return promptData{}
	}
	data := promptData{
		Surface: promptSurface{Type: request.Type},
	}
	data.Gradients = make([]promptGradient, 0, len(request.Gradients))
	for _, gradient := range request.Gradients {
		data.Gradients = append(data.Gradients, promptGradient{
			Severity: gradient.Severity,
			Gradient: gradient.Gradient,
		})
	}
	return data
}

// toPrettyJSON renders one value as indented JSON for prompts.
func toPrettyJSON(value any) (string, error) {
	payloadJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal aggregation request: %w", err)
	}
	return string(payloadJSON), nil
}
