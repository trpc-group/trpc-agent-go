//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package systemone implements decision.Decider for Jev and compatible Laya
// servers using the System One protocol.
package systemone

import (
	"context"
	"fmt"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/decision"
	wire "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// Config configures a System One decision client.
type Config struct {
	// BaseURL is the required HTTP(S) API root. The client appends /v1/systemone.
	// The root may include a gateway prefix.
	BaseURL string
	// APIKey is the bearer key. Leave it empty for an unauthenticated server.
	APIKey string
	// Model is the model to request. Leave it empty for Laya server-side routing.
	Model string
	// HTTPClient optionally supplies a transport and timeout settings. New copies
	// its configuration and shares its transport. Redirects are not followed.
	HTTPClient *http.Client
}

// Client evaluates decisions using Jev or a compatible Laya server. Use New
// to construct it. It supports concurrent calls with a concurrent transport.
type Client struct {
	native *wire.Client
}

var _ decision.Decider = (*Client)(nil)

// New creates a System One decision client. The client does not retry
// requests and adds a 30-second deadline; earlier caller deadlines apply.
func New(config Config) (*Client, error) {
	native, err := wire.NewClient(config.BaseURL,
		wire.WithAPIKey(config.APIKey),
		wire.WithDefaultModel(config.Model),
		wire.WithHTTPClient(config.HTTPClient),
	)
	if err != nil {
		return nil, err
	}
	return &Client{native: native}, nil
}

// Decide implements decision.Decider using the common System One answer fields.
func (c *Client) Decide(ctx context.Context, req decision.Request) (*decision.Response, error) {
	if c == nil || c.native == nil {
		return nil, fmt.Errorf("systemone: decision client is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("systemone: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	nativeReq := &wire.Request{
		State:     req.Input.Text,
		Questions: make(map[string]wire.Question, len(req.Questions)),
	}
	if req.Input.JSON != nil {
		nativeReq.State = req.Input.JSON
	}
	for _, q := range req.Questions {
		nativeReq.Questions[q.ID] = nativeQuestion(q)
	}
	resp, err := c.native.SystemOne(ctx, nativeReq)
	if err != nil {
		return nil, err
	}

	result := &decision.Response{
		Model:     resp.Model,
		RequestID: resp.RequestID,
		Usage: decision.Usage{
			InputTokens:  tokenCount(resp.Usage.InputTokens),
			OutputTokens: tokenCount(resp.Usage.OutputTokens),
			Truncated:    resp.Usage.Truncated,
		},
		Answers: make(map[string]decision.Answer, len(req.Questions)),
	}
	// The native client has already validated every ID, type and distribution.
	for _, q := range req.Questions {
		answer, err := projectAnswer(q, resp.Answers[q.ID])
		if err != nil {
			return nil, err
		}
		result.Answers[q.ID] = answer
	}
	return result, nil
}

func nativeQuestion(q decision.Question) wire.Question {
	switch q.Kind {
	case decision.Predicate:
		return wire.BinaryQuestion{Instructions: q.Instructions}
	case decision.Choice:
		options := make([]wire.ChoiceOption, 0, len(q.Options))
		for _, opt := range q.Options {
			options = append(options, wire.ChoiceOption{Name: opt.ID, Description: opt.Description})
		}
		return wire.ChoiceQuestion{Instructions: q.Instructions, Options: options}
	case decision.Score:
		criteria := make([]string, 0, len(q.Options))
		for _, opt := range q.Options {
			text := opt.ID
			if opt.Description != "" {
				text += ": " + opt.Description
			}
			criteria = append(criteria, text)
		}
		return wire.ScoreQuestion{Instructions: q.Instructions, Criteria: criteria}
	default:
		return nil // Request.Validate has rejected unsupported kinds.
	}
}

func projectAnswer(q decision.Question, native wire.Answer) (decision.Answer, error) {
	switch a := native.(type) {
	case *wire.BinaryAnswer:
		return decision.Answer{Status: decision.Answered, Probability: &a.Probability}, nil
	case *wire.ChoiceAnswer:
		return decision.Answer{
			Status: decision.Answered, Choice: a.Choice,
			Confidence: &a.Confidence, Probabilities: a.Probabilities,
		}, nil
	case *wire.ScoreAnswer:
		weights := make(map[string]float64, len(a.Levels))
		for i, level := range a.Levels {
			weights[q.Options[i].ID] = level.Probability
		}
		return decision.Answer{
			Status: decision.Answered, Score: &a.Score,
			Confidence: &a.Confidence, Probabilities: weights,
		}, nil
	default:
		return decision.Answer{}, fmt.Errorf("systemone: unsupported answer type %T for %q", native, q.ID)
	}
}

func tokenCount(n *int) *int64 {
	if n == nil {
		return nil
	}
	value := int64(*n)
	return &value
}
