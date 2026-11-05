//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/model"

	systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// Option configures the constructors of this package. Options are shared
// across NewToolCallback, NewSkillFilter and NewModelRouter; options that do
// not apply to a constructor are ignored by it.
type Option func(*options)

// Default tuning values, calibrated against the Octop production
// implementation (see docs/design/decision-model.md, appendix A).
const (
	defaultThreshold      = 0.6  // keep when P(relevant) >= threshold
	defaultSlack          = 0.15 // fallback margin when no scored item meets the threshold
	defaultMinToolCount   = 8    // catalogs below this size are not filtered
	defaultShortlistLimit = 16   // lexical recall candidates
	defaultMaxQuestions   = 20   // questions per decision request
	defaultTimeout        = 30 * time.Second

	// defaultCacheSize bounds each per-run LRU cache (Octop uses 128).
	defaultCacheSize = 128
)

type options struct {
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client

	threshold      float64
	slack          float64
	minToolCount   int
	shortlistLimit int
	maxQuestions   int
	keepAlways     []string
	keepSkills     []string
	embedder       embedder.Embedder
	observer       func(*Report)

	candidates []modelCandidate
	fallback   model.Model
}

// modelCandidate is one routing target of NewModelRouter.
type modelCandidate struct {
	name     string
	m        model.Model
	criteria string
}

func defaultOptions() options {
	return options{
		threshold:      defaultThreshold,
		slack:          defaultSlack,
		minToolCount:   defaultMinToolCount,
		shortlistLimit: defaultShortlistLimit,
		maxQuestions:   defaultMaxQuestions,
		timeout:        defaultTimeout,
	}
}

func applyOptions(opts []Option) options {
	o := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// WithAPIKey sets the bearer credential for the System One endpoint. There
// is no environment fallback: pass os.Getenv results explicitly.
func WithAPIKey(key string) Option {
	return func(o *options) {
		if key != "" {
			o.apiKey = key
		}
	}
}

// WithDefaultModel pins the decision model version, for example
// "jev-2025-10-10", for reproducibility. When empty the server default is
// used.
func WithDefaultModel(model string) Option {
	return func(o *options) {
		if model != "" {
			o.model = model
		}
	}
}

// WithTimeout sets the per-request timeout. It must be positive or the
// constructor returns an error; the default is 30 seconds.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.timeout = timeout
	}
}

// WithHTTPClient sets the http.Client used for decision requests. The
// client is shallow-copied. It is intended for tests and HTTP policy.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		if client != nil {
			o.httpClient = client
		}
	}
}

// WithThreshold sets the keep threshold for tool and skill filtering:
// a scored candidate is kept when P(relevant) >= threshold. When none meet
// the threshold, WithSlack determines whether near-threshold candidates are
// kept. It must be in [0, 1]. The default is 0.6.
func WithThreshold(threshold float64) Option {
	return func(o *options) {
		o.threshold = threshold
	}
}

// WithSlack sets the fallback margin for tool and skill filtering. If no
// scored candidate meets the threshold, candidates scoring at least threshold
// minus slack are kept. Always-kept and unasked candidates do not affect this
// decision. Slack must be in [0, 1]; the default is 0.15.
func WithSlack(slack float64) Option {
	return func(o *options) {
		o.slack = slack
	}
}

// WithMinToolCount skips filtering when the request carries fewer tools
// than count. The default is 8.
func WithMinToolCount(count int) Option {
	return func(o *options) {
		if count >= 0 {
			o.minToolCount = count
		}
	}
}

// WithShortlistLimit bounds tool and skill recall. Catalogs at or below the
// limit are sent directly to the decision model. Larger catalogs use lexical
// recall, optionally combined with embeddings; if recall finds no candidates,
// the catalog remains visible. Embeddings can also rank queries without lexical
// tokens. The limit must be at least 1; the default is 16.
func WithShortlistLimit(limit int) Option {
	return func(o *options) {
		if limit >= 1 {
			o.shortlistLimit = limit
		}
	}
}

// WithMaxQuestions bounds the number of questions in one decision request.
// Shortlisted candidates beyond the budget are not asked and are kept. It
// must be at least 1. The default is 20.
func WithMaxQuestions(limit int) Option {
	return func(o *options) {
		if limit >= 1 {
			o.maxQuestions = limit
		}
	}
}

// WithKeepAlways adds model-facing tool names that are never scored or
// filtered, in addition to the non-removable framework workflow tool set.
// Use the exact names in the model request, including any ToolSet prefix,
// to protect application-critical tools. Names absent from a request have
// no effect. The default set also matches by name, so application tools
// with the same names as framework workflow tools are kept.
func WithKeepAlways(names ...string) Option {
	return func(o *options) {
		o.keepAlways = append(o.keepAlways, names...)
	}
}

// WithKeepSkills adds skill names that are always visible without being
// scored. Use it to protect core skills.
func WithKeepSkills(names ...string) Option {
	return func(o *options) {
		o.keepSkills = append(o.keepSkills, names...)
	}
}

// WithShortlistEmbedder adds an embedding recall path for tool and skill
// catalogs above the shortlist limit, fused with the
// keyword path by reciprocal rank fusion. When the embedder fails, the
// keyword path alone decides (fail-open). Concurrent requests for the same
// text share one embedding operation per filter. Callers stop waiting after
// 30 seconds or when their context is canceled. An embedder that ignores its
// context retains its slot until it returns. When all 8
// embedding slots are occupied, new distinct texts skip embedding; keyword
// recall remains available. Successful embeddings are cached per filter in
// separate least-recently-used caches for
// 128 user texts and 1024 candidate descriptions. Evicted entries are
// recomputed when needed. Each filter performs at most 8 embedding calls
// concurrently, including calls for user texts.
func WithShortlistEmbedder(e embedder.Embedder) Option {
	return func(o *options) {
		if e != nil {
			o.embedder = e
		}
	}
}

// WithObserver registers a callback invoked once per turn with the
// filtering outcome, including per-tool scores and estimated token savings.
// It runs as part of the shared decision and must be fast and safe for
// concurrent use. If the initiating caller stops waiting, the callback may
// finish after that caller returns. Logging the report is the intended use.
func WithObserver(observer func(*Report)) Option {
	return func(o *options) {
		if observer != nil {
			o.observer = observer
		}
	}
}

// WithModelCandidate registers a routing target for NewModelRouter: name
// identifies the candidate on the wire, m is the model instance returned
// when selected, and criteria describes the tasks suited to the model. At
// least one candidate is required.
func WithModelCandidate(name string, m model.Model, criteria string) Option {
	return func(o *options) {
		o.candidates = append(o.candidates, modelCandidate{name: name, m: m, criteria: criteria})
	}
}

// WithFallbackModel sets the model returned when routing fails (decision
// error, unknown choice, blank input). When not set, routing failures keep
// the invocation's base model, matching the agent.ModelSelector contract.
func WithFallbackModel(m model.Model) Option {
	return func(o *options) {
		if m != nil {
			o.fallback = m
		}
	}
}

// newSystemOneClient builds the shared decision client from resolved
// options. Internal types never leak into exported signatures.
func newSystemOneClient(baseURL string, o options) (*systemoneclient.Client, error) {
	if o.timeout <= 0 {
		return nil, fmt.Errorf("toolfilter: timeout must be positive, got %v", o.timeout)
	}
	client, err := systemoneclient.NewClient(baseURL,
		systemoneclient.WithAPIKey(o.apiKey),
		systemoneclient.WithDefaultModel(o.model),
		systemoneclient.WithTimeout(o.timeout),
		systemoneclient.WithHTTPClient(o.httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("toolfilter: %w", err)
	}
	return client, nil
}

// validateProbability checks a threshold-like option value.
func validateProbability(name string, value float64) error {
	if math.IsNaN(value) || value < 0 || value > 1 {
		return fmt.Errorf("toolfilter: %s must be in [0, 1], got %v", name, value)
	}
	return nil
}
