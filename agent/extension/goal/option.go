//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package goal

// Public defaults exposed for embedders and tests.
const (
	// DefaultExtensionName is the extension.Extension name.
	DefaultExtensionName = "goal"

	// DefaultStateKey is the session.State key used for the current goal.
	DefaultStateKey = "temp:goal"

	// DefaultGetGoalToolName is the registered read tool.
	DefaultGetGoalToolName = "get_goal"

	// DefaultCreateGoalToolName is the registered create tool.
	DefaultCreateGoalToolName = "create_goal"

	// DefaultUpdateGoalToolName is the registered update tool.
	DefaultUpdateGoalToolName = "update_goal"

	// DefaultMaxRetries bounds the AfterModel block-retry loop.
	DefaultMaxRetries = 3
)

// EnforceReason classifies OnEnforce events.
type EnforceReason string

const (
	// ReasonBlocked indicates a premature final response was blocked.
	ReasonBlocked EnforceReason = "blocked"

	// ReasonExhausted indicates the retry budget ran out and the final response
	// was allowed through.
	ReasonExhausted EnforceReason = "exhausted"
)

// EnforceEvent is sent to OnEnforce observers.
type EnforceEvent struct {
	Reason        EnforceReason
	AgentName     string
	Goal          *Goal
	AttemptNumber int
	MaxRetries    int
}

// EnforceCallback observes enforcement decisions.
type EnforceCallback func(event EnforceEvent)

// Options controls the Goal extension.
type Options struct {
	Name string

	StateKey string

	GetGoalToolName    string
	CreateGoalToolName string
	UpdateGoalToolName string

	InjectGuidance bool
	MaxRetries     int

	NudgeFormatter NudgeFormatter

	OnEnforce EnforceCallback
}

// Option mutates Options.
type Option func(*Options)

// WithName overrides the extension name.
func WithName(name string) Option {
	return func(o *Options) {
		if name != "" {
			o.Name = name
		}
	}
}

// WithStateKey overrides the session state key.
func WithStateKey(key string) Option {
	return func(o *Options) {
		if key != "" {
			o.StateKey = key
		}
	}
}

// WithToolNames overrides the three model-visible tool names.
func WithToolNames(get, create, update string) Option {
	return func(o *Options) {
		if get != "" {
			o.GetGoalToolName = get
		}
		if create != "" {
			o.CreateGoalToolName = create
		}
		if update != "" {
			o.UpdateGoalToolName = update
		}
	}
}

// WithGuidance controls whether the extension injects non-persisted guidance.
func WithGuidance(enabled bool) Option {
	return func(o *Options) {
		o.InjectGuidance = enabled
	}
}

// WithMaxRetries sets the retry budget for blocking premature final responses.
func WithMaxRetries(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.MaxRetries = n
		}
	}
}

// WithNudgeFormatter overrides the continuation nudge.
func WithNudgeFormatter(f NudgeFormatter) Option {
	return func(o *Options) {
		if f != nil {
			o.NudgeFormatter = f
		}
	}
}

// WithOnEnforce installs an observer for enforcement events.
func WithOnEnforce(cb EnforceCallback) Option {
	return func(o *Options) {
		o.OnEnforce = cb
	}
}
