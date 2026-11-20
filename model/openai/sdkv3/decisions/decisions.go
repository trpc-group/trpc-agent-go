//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package decisions implements decision.Decider using the OpenAI Decisions API.
package decisions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"trpc.group/trpc-go/trpc-agent-go/decision"
)

// Client evaluates decisions using the OpenAI Go SDK. Use New to construct it.
// It supports concurrent calls if the supplied SDK options and transport do.
type Client struct {
	model   string
	service openai.DecisionService
}

var _ decision.Decider = (*Client)(nil)

// New creates a Decisions client for a nonblank model. The SDK reads its
// environment defaults, including OPENAI_API_KEY and OPENAI_BASE_URL. The
// default is a 30-second request timeout and no retries. SDK options can
// override those settings; they must not replace the request input, model,
// or questions. Nil options are ignored.
func New(model string, opts ...option.RequestOption) (*Client, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("openai decisions: model is required")
	}
	clientOpts := []option.RequestOption{
		option.WithRequestTimeout(30 * time.Second),
		option.WithMaxRetries(0),
	}
	for _, opt := range opts {
		if opt != nil {
			clientOpts = append(clientOpts, opt)
		}
	}
	sdk := openai.NewClient(clientOpts...)
	return &Client{model: model, service: sdk.Decisions}, nil
}

// Decide implements decision.Decider. Answers are matched by the names sent
// in the request. Per-question refusals leave other answers usable.
func (c *Client) Decide(ctx context.Context, req decision.Request) (*decision.Response, error) {
	if c == nil || c.model == "" {
		return nil, fmt.Errorf("openai decisions: client is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("openai decisions: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	params := openai.DecisionNewParams{
		Model:     c.model,
		Input:     nativeInput(req.Input),
		Questions: make([]openai.DecisionNewParamsQuestionUnion, 0, len(req.Questions)),
	}
	for _, q := range req.Questions {
		params.Questions = append(params.Questions, nativeQuestion(q))
	}
	var httpResp *http.Response
	resp, err := c.service.New(ctx, params, option.WithResponseInto(&httpResp))
	if err != nil {
		return nil, fmt.Errorf("openai decisions: %w", err)
	}
	var requestID string
	if httpResp != nil {
		requestID = httpResp.Header.Get("X-Request-ID")
	}
	return projectResponse(req, resp, requestID)
}

type jsonPart struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func nativeInput(in decision.Input) openai.DecisionNewParamsInputUnion {
	if in.JSON == nil {
		return openai.DecisionNewParamsInputUnion{OfString: openai.String(in.Text)}
	}
	// The API supports input_json, but the pinned SDK's input union does not
	// yet represent it. Override this field only; the SDK sends the request.
	return param.Override[openai.DecisionNewParamsInputUnion]([]struct {
		Type    string     `json:"type"`
		Role    string     `json:"role"`
		Content []jsonPart `json:"content"`
	}{{Type: "message", Role: "user", Content: []jsonPart{{Type: "input_json", Data: in.JSON}}}})
}

func nativeQuestion(q decision.Question) openai.DecisionNewParamsQuestionUnion {
	name := openai.String(q.ID)
	switch q.Kind {
	case decision.Predicate:
		return openai.DecisionNewParamsQuestionUnion{OfPredicate: &openai.DecisionNewParamsQuestionPredicate{
			Name: name, Instructions: q.Instructions,
		}}
	case decision.Choice:
		choices := make([]openai.DecisionNewParamsQuestionChoiceChoice, 0, len(q.Options))
		for _, opt := range q.Options {
			choices = append(choices, openai.DecisionNewParamsQuestionChoiceChoice{
				Value:       openai.DecisionNewParamsQuestionChoiceChoiceValueUnion{OfString: openai.String(opt.ID)},
				Description: optionalString(opt.Description),
			})
		}
		return openai.DecisionNewParamsQuestionUnion{OfChoice: &openai.DecisionNewParamsQuestionChoice{
			Name: name, Instructions: q.Instructions, Choices: choices,
		}}
	case decision.Score:
		levels := make([]openai.DecisionNewParamsQuestionScoreLevel, 0, len(q.Options))
		for _, opt := range q.Options {
			levels = append(levels, openai.DecisionNewParamsQuestionScoreLevel{
				Label: opt.ID, Description: optionalString(opt.Description),
			})
		}
		return openai.DecisionNewParamsQuestionUnion{OfScore: &openai.DecisionNewParamsQuestionScore{
			Name: name, Instructions: q.Instructions, Levels: levels,
		}}
	default:
		return openai.DecisionNewParamsQuestionUnion{} // Request.Validate has rejected unsupported kinds.
	}
}

func optionalString(s string) param.Opt[string] {
	if s == "" {
		return param.Opt[string]{}
	}
	return openai.String(s)
}
