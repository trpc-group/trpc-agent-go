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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageBuilder encodes one optimization request into one runner message.
type MessageBuilder func(ctx context.Context, request *Request) (*model.Message, error)

const defaultMessageTemplateText = `Optimize one PromptIter surface from the provided current value and aggregated gradients.

You will receive one optimization request for a single surface.
Return exactly one JSON object with Value and Reason fields.

Requirements:
- Keep the response as raw JSON only.
- Do not wrap the response in markdown code fences.
- Return only Value and Reason fields. The caller will attach the target surface identity.
- The patch value must match the request surface type.
- Prefer the smallest high-confidence change that preserves working parts of the current value.
- When the current value is mostly correct, prefer removing unsupported or speculative detail before adding new detail.
- Resolve repeated or consistent gradients first.
- Avoid broad rewrites unless the gradients indicate multiple independent failures.
- Do not trade factual precision for stylistic vividness.
- Add detail only when it is clearly supported by the request and directly required by the gradients.
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
		if err := tmpl.Execute(&content, newPromptData(request)); err != nil {
			return nil, fmt.Errorf("render optimization message template: %w", err)
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
	Type  astructure.SurfaceType
	Value astructure.SurfaceValue
}

type promptGradient struct {
	Severity promptiter.LossSeverity
	Gradient string
}

func newPromptData(request *Request) promptData {
	if request == nil {
		return promptData{}
	}
	data := promptData{}
	if request.Surface != nil {
		data.Surface = promptSurface{
			Type:  request.Surface.Type,
			Value: request.Surface.Value,
		}
	}
	if request.Gradient != nil {
		data.Gradients = make([]promptGradient, 0, len(request.Gradient.Gradients))
		for _, gradient := range request.Gradient.Gradients {
			data.Gradients = append(data.Gradients, promptGradient{
				Severity: gradient.Severity,
				Gradient: gradient.Gradient,
			})
		}
	}
	return data
}

// toPrettyJSON renders one value as indented JSON for prompts.
func toPrettyJSON(value any) (string, error) {
	payloadJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal optimization request: %w", err)
	}
	return string(payloadJSON), nil
}
