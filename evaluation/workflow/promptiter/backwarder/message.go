//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package backwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageBuilder encodes one backward request into one runner message.
type MessageBuilder func(ctx context.Context, request *Request) (*model.Message, error)

const defaultMessageTemplateText = `Compute PromptIter backward attribution for one step.

You will receive one backward request for a single executed step.
Return exactly one JSON object that can be unmarshaled into backwarder.Result.

Requirements:
- Keep the response as raw JSON only.
- Do not wrap the response in markdown code fences.
- Attribute gradients only to surfaces that affected the current step.
- Route upstream gradients only to explicit predecessor steps.
- Do not broadcast the same gradient packet to every predecessor.

Request JSON:
{{ toPrettyJSON . }}
`

func defaultMessageBuilder() MessageBuilder {
	tmpl, err := template.New("backwarder_default_message").Funcs(template.FuncMap{
		"toPrettyJSON": toPrettyJSON,
	}).Parse(defaultMessageTemplateText)
	if err != nil {
		return func(ctx context.Context, request *Request) (*model.Message, error) {
			return nil, fmt.Errorf("parse default backward message template: %w", err)
		}
	}
	return func(ctx context.Context, request *Request) (*model.Message, error) {
		if request == nil {
			return nil, errors.New("render backward message template: request is nil")
		}
		var content bytes.Buffer
		if err := tmpl.Execute(&content, request); err != nil {
			return nil, fmt.Errorf("render backward message template: %w", err)
		}
		message := model.NewUserMessage(content.String())
		return &message, nil
	}
}

// toPrettyJSON renders one value as indented JSON for prompts.
func toPrettyJSON(value any) (string, error) {
	payloadJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal backward request: %w", err)
	}
	return string(payloadJSON), nil
}
