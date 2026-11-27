//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package systemone

import (
	"net/http"
	"time"
)

// Option configures a Reviewer at construction time.
type Option func(*options)

type options struct {
	apiKey        string
	model         string
	httpClient    *http.Client
	timeout       time.Duration
	riskThreshold int
	minConfidence float64
}

// WithAPIKey sets the bearer credential. An empty key omits Authorization.
// The reviewer never reads credentials from the environment.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithModel sets the model name. The default is empty, which lets compatible
// servers select a model. Jev callers must supply a model name.
func WithModel(name string) Option {
	return func(o *options) { o.model = name }
}

// WithHTTPClient sets the HTTP client. New copies its configuration, disables
// redirects on the copy, and shares its transport without closing it. A nil
// client uses the standard transport. Do not mutate the client during New.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// WithTimeout sets the per-review HTTP deadline, including response reading.
// The default is 30 seconds. Zero disables this deadline; negative values are
// invalid. An earlier context deadline or HTTP client timeout still applies.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) { o.timeout = timeout }
}

// WithRiskThreshold sets the exclusive upper bound on normalized risk in
// [0, 100]. The default is 50; only ordinal levels 0 and 1 count toward the
// required below-threshold probability at this setting.
// Approval checks the unrounded score and the probability of levels strictly
// below this threshold. Zero disables automatic approval. These scores are
// specific to this reviewer's rubric, not the guardian reviewer's scale.
func WithRiskThreshold(threshold int) Option {
	return func(o *options) { o.riskThreshold = threshold }
}

// WithMinConfidence sets the inclusive lower bound in [0, 1] for both answer
// confidences, the probability of supported authorization, and the probability
// of risk levels below the risk threshold. The default is 0.8; zero disables
// these uncertainty checks. Confidence is provider-specific, not a guarantee
// of safety or correctness. Calibrate this value for each model and workload.
func WithMinConfidence(confidence float64) Option {
	return func(o *options) { o.minConfidence = confidence }
}
