//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package systemone provides a tool approval reviewer backed by the System One
// decision API, including Jev and protocol-compatible servers.
package systemone

import (
	"context"
	"fmt"
	"math"
	"time"

	systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
)

const (
	riskLevelUnknown  = "unknown"
	riskLevelLow      = "low"
	riskLevelModerate = "moderate"
	riskLevelHigh     = "high"
	riskLevelCritical = "critical"
)

// Reviewer evaluates authorization and risk with fixed System One questions
// and applies local approval rules. Construct it with New; its zero value is
// unusable. It supports concurrent reviews if the supplied HTTP transport does.
// It owns no background workers or transport resources and needs no Close.
type Reviewer struct {
	client        *systemoneclient.Client
	riskThreshold int
	minConfidence float64
}

var _ review.Reviewer = (*Reviewer)(nil)

// New creates a reviewer for an explicit HTTP(S) API root. A gateway path prefix
// is allowed; /v1/systemone is appended. Invalid URLs, credentials, deadlines,
// and thresholds return errors. Nil options are ignored; the last option wins.
// No model, credential, or endpoint is inferred from the environment.
func New(baseURL string, opts ...Option) (*Reviewer, error) {
	o := options{timeout: 30 * time.Second, riskThreshold: 50, minConfidence: 0.8}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.riskThreshold < 0 || o.riskThreshold > 100 {
		return nil, fmt.Errorf("systemone reviewer: risk threshold must be in [0, 100]")
	}
	if math.IsNaN(o.minConfidence) || o.minConfidence < 0 || o.minConfidence > 1 {
		return nil, fmt.Errorf("systemone reviewer: minimum confidence must be in [0, 1]")
	}
	client, err := systemoneclient.NewClient(baseURL,
		systemoneclient.WithAPIKey(o.apiKey),
		systemoneclient.WithDefaultModel(o.model),
		systemoneclient.WithHTTPClient(o.httpClient),
		systemoneclient.WithTimeout(o.timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	return &Reviewer{client: client, riskThreshold: o.riskThreshold, minConfidence: o.minConfidence}, nil
}

// Review evaluates the supplied action and transcript without mutating them.
// Callers must not mutate the request while Review runs. Nil contexts/requests,
// blank tool names, invalid UTF-8/JSON, and HTTP/protocol failures return an error
// and no decision. Cancellation is propagated through errors.Is.
//
// With Context, user entries must have OriginUserInput; unknown and delegated
// input cannot establish authorization. Without a nonblank user entry, Review
// returns a denial with risk 100 and level
// "unknown" without contacting the server. Otherwise it makes one request with
// authorization and risk questions, with no retries or redirects. Reported input
// truncation, unsupported authorization, or insufficient confidence denies
// approval. Absence of truncation metadata does not establish completeness.
//
// RiskScore is round(25 * ordinal score), in [0, 100]. RiskLevel uses the
// unrounded value: low through 25, moderate through 50, high through 75, and
// critical above 75. Reason is a local policy explanation, not generated text.
// Approval requires supported authorization and all configured risk/confidence
// checks. It applies only to this snapshot; callers must preserve reviewed
// arguments and enforce their final execution permission policy separately.
func (r *Reviewer) Review(ctx context.Context, req *review.Request) (*review.Decision, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("systemone reviewer: reviewer is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("systemone reviewer: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	state, hasUser, err := buildState(req)
	if err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	if !hasUser {
		return &review.Decision{RiskScore: 100, RiskLevel: riskLevelUnknown,
			Reason: "Authorization is insufficient: no user intent is present in the transcript."}, nil
	}
	response, err := r.client.SystemOne(ctx, &systemoneclient.Request{
		State: state, Questions: questions(),
	})
	if err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	authorization, err := response.Choice(authorizationQuestion)
	if err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	risk, err := response.Score(riskQuestion)
	if err != nil {
		return nil, fmt.Errorf("systemone reviewer: %w", err)
	}
	return r.decide(response, authorization, risk), nil
}

func (r *Reviewer) decide(response *systemoneclient.Response, authorization *systemoneclient.ChoiceAnswer,
	risk *systemoneclient.ScoreAnswer) *review.Decision {
	normalized := risk.Score * riskScale
	decision := &review.Decision{RiskScore: int(math.Round(normalized)), RiskLevel: riskLevel(normalized)}
	var belowThreshold float64
	for i, level := range risk.Levels {
		if float64(i)*riskScale < float64(r.riskThreshold) {
			belowThreshold += level.Probability
		}
	}
	switch {
	case response.Usage.Truncated != nil && *response.Usage.Truncated:
		decision.Reason = "Review input was truncated by the server; automatic approval is unavailable."
	case authorization.Choice != supported:
		decision.Reason = fmt.Sprintf("Authorization is %s; automatic approval requires supported authorization.",
			authorization.Choice)
	case authorization.Confidence < r.minConfidence || authorization.Probabilities[supported] < r.minConfidence:
		decision.Reason = "Authorization evidence is uncertain; confidence or supported probability is below the minimum."
	case risk.Confidence < r.minConfidence:
		decision.Reason = "Risk assessment confidence is below the minimum."
	case normalized >= float64(r.riskThreshold):
		decision.Reason = fmt.Sprintf("Assessed risk %.2f is not below the automatic approval threshold %d.",
			normalized, r.riskThreshold)
	// Summing level probabilities can put an inclusive boundary a few ULPs low.
	case belowThreshold+1e-12 < r.minConfidence:
		decision.Reason = "The probability of risk below the automatic approval threshold is insufficient."
	default:
		decision.Approved = true
		decision.Reason = "Authorization is supported and the configured risk and confidence checks passed."
	}
	return decision
}

func riskLevel(score float64) string {
	switch {
	case score <= 25:
		return riskLevelLow
	case score <= 50:
		return riskLevelModerate
	case score <= 75:
		return riskLevelHigh
	default:
		return riskLevelCritical
	}
}
