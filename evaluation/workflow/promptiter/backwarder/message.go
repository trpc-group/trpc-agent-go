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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageBuilder encodes one backward request into one runner message.
type MessageBuilder func(ctx context.Context, request *Request) (*model.Message, error)

const defaultMessageTemplateText = `Compute PromptIter backward attribution for one step.

You will receive one backward request for a single executed step.
Return exactly one JSON object with Gradients and Upstream fields.

Requirements:
- Keep the response as raw JSON only.
- Do not wrap the response in markdown code fences.
- Attribute gradients only to listed gradient surfaces.
- Route upstream gradients only to listed predecessor steps.
- If no gradient surfaces are listed and predecessor steps are listed, route non-empty upstream gradients to the relevant predecessor steps.
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
		if err := tmpl.Execute(&content, newPromptData(request)); err != nil {
			return nil, fmt.Errorf("render backward message template: %w", err)
		}
		message := model.NewUserMessage(content.String())
		return &message, nil
	}
}

type promptData struct {
	Node             promptNode
	Input            *atrace.Snapshot         `json:",omitempty"`
	Output           *atrace.Snapshot         `json:",omitempty"`
	Error            string                   `json:",omitempty"`
	GradientSurfaces []promptSurface          `json:",omitempty"`
	OtherSurfaces    []promptContextSurface   `json:",omitempty"`
	Predecessors     []promptPredecessor      `json:",omitempty"`
	Incoming         []promptIncomingGradient `json:",omitempty"`
}

type promptNode struct {
	Kind astructure.NodeKind `json:",omitempty"`
	Name string              `json:",omitempty"`
}

type promptSurface struct {
	SurfaceID string
	Type      astructure.SurfaceType
	Value     astructure.SurfaceValue
}

type promptContextSurface struct {
	Type  astructure.SurfaceType
	Value astructure.SurfaceValue
}

type promptPredecessor struct {
	StepID string
	Output *atrace.Snapshot `json:",omitempty"`
	Error  string           `json:",omitempty"`
}

type promptIncomingGradient struct {
	Severity promptiter.LossSeverity
	Gradient string
}

func newPromptData(request *Request) promptData {
	if request == nil {
		return promptData{}
	}
	data := promptData{
		Input:  request.Input,
		Output: request.Output,
		Error:  request.Error,
	}
	if request.Node != nil {
		data.Node = promptNode{
			Kind: request.Node.Kind,
			Name: request.Node.Name,
		}
	}
	allowedSurfaceIDList := requestAllowedGradientSurfaceIDs(request)
	allowedSurfaceIDs := make(map[string]struct{}, len(allowedSurfaceIDList))
	for _, surfaceID := range allowedSurfaceIDList {
		allowedSurfaceIDs[surfaceID] = struct{}{}
	}
	data.GradientSurfaces = make([]promptSurface, 0, len(request.Surfaces))
	data.OtherSurfaces = make([]promptContextSurface, 0, len(request.Surfaces))
	for _, surface := range request.Surfaces {
		if _, ok := allowedSurfaceIDs[surface.SurfaceID]; ok {
			data.GradientSurfaces = append(data.GradientSurfaces, promptSurface{
				SurfaceID: surface.SurfaceID,
				Type:      surface.Type,
				Value:     surface.Value,
			})
			continue
		}
		data.OtherSurfaces = append(data.OtherSurfaces, promptContextSurface{
			Type:  surface.Type,
			Value: surface.Value,
		})
	}
	data.Predecessors = make([]promptPredecessor, 0, len(request.Predecessors))
	for _, predecessor := range request.Predecessors {
		data.Predecessors = append(data.Predecessors, promptPredecessor{
			StepID: predecessor.StepID,
			Output: predecessor.Output,
			Error:  predecessor.Error,
		})
	}
	data.Incoming = make([]promptIncomingGradient, 0, len(request.Incoming))
	for _, incoming := range request.Incoming {
		data.Incoming = append(data.Incoming, promptIncomingGradient{
			Severity: incoming.Severity,
			Gradient: incoming.Gradient,
		})
	}
	return data
}

// toPrettyJSON renders one value as indented JSON for prompts.
func toPrettyJSON(value any) (string, error) {
	payloadJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal backward request: %w", err)
	}
	return string(payloadJSON), nil
}
