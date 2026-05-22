//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// Public defaults exposed for embedders and tests.
const (
	// DefaultExtensionName is the extension.Extension Name
	// registered into the host agent. Override via WithName when
	// stacking more than one Enforcer with different policies on
	// the same agent (rare but supported).
	DefaultExtensionName = "todoenforcer"

	// DefaultDeclareBlockerToolName is the registered name of the
	// escape-hatch tool. Override via WithDeclareBlockerToolName
	// to avoid collisions with another tool the agent already
	// exposes.
	DefaultDeclareBlockerToolName = "todo_declare_blocker"

	// DefaultMaxRetries bounds the AfterModel block-retry loop.
	// 3 is empirically a comfortable budget: it gives the model
	// two follow-up turns to recover after the initial premature
	// "done", which covers the typical "I forgot one step"
	// failure mode without trapping a confused agent in a long
	// loop.
	DefaultMaxRetries = 3
)

// EnforceReason classifies the OnEnforce events surfaced by the
// extension. Stringly typed (rather than int constants) so that
// they stay readable in trace exports and metric labels.
type EnforceReason string

const (
	// ReasonBlocked indicates AfterModel flipped a final response
	// to non-final and queued a nudge.
	ReasonBlocked EnforceReason = "blocked"

	// ReasonExhausted indicates the retry budget ran out and the
	// final response was allowed through unmodified (fail-open).
	ReasonExhausted EnforceReason = "exhausted"

	// ReasonBlockerDeclared indicates the model called
	// todo_declare_blocker. The model is then free to emit its
	// final message; the invocation is NOT terminated by this
	// event.
	ReasonBlockerDeclared EnforceReason = "blocker_declared"
)

// EnforceEvent is the payload passed to OnEnforce observers. The
// fields chosen here are the ones that an operator dashboard or a
// metrics pipeline typically wants — agent name (label), reason
// (counter dimension), retry/budget (gauge), pending/in-progress
// counts (histogram), and the blocker reason (free-form).
type EnforceEvent struct {
	Reason          EnforceReason
	AgentName       string
	AttemptNumber   int
	MaxRetries      int
	PendingCount    int
	InProgressCount int
	// BlockerReason is populated only on ReasonBlockerDeclared
	// events; it carries the free-form reason the model supplied
	// to todo_declare_blocker.
	BlockerReason string
}

// EnforceCallback is invoked synchronously for every enforcement
// event. It MUST be cheap (the call is on the model-callback hot
// path) and MUST NOT block. Return values are intentionally not
// part of the contract: the extension owns the enforcement
// decision, callbacks are observers only. Panics are recovered by
// the enforcer so a misbehaving observer cannot crash the run.
type EnforceCallback func(event EnforceEvent)

// Options aggregates every knob of an Enforcer. Made public so
// tests and embedders can introspect a constructed Enforcer's
// configuration; in normal code prefer the With* functional
// options.
type Options struct {
	// Name is the extension.Extension Name. Defaults to
	// DefaultExtensionName when zero.
	Name string

	// MaxRetries bounds the AfterModel block-retry loop. Values
	// <= 0 fall back to DefaultMaxRetries — callers cannot
	// accidentally disable the safety net by passing 0; pass a
	// very large number for "effectively unlimited".
	MaxRetries int

	// TodoTool is an optional pre-configured todo.Tool. When nil
	// the enforcer constructs one with default options. The tool
	// is contributed via extension.Registry.Tools during Register,
	// so users do NOT need to install it separately via WithTools.
	TodoTool *todo.Tool

	// DeclareBlockerToolName / DeclareBlockerToolDescription
	// override the registered name / description of
	// todo_declare_blocker. Empty inputs fall back to defaults;
	// the description default is tuned to position the tool as a
	// last-resort declaration, never a shortcut.
	DeclareBlockerToolName        string
	DeclareBlockerToolDescription string

	// NudgeFormatter renders the user message injected before the
	// next turn. Defaults to DefaultNudgeFormatter.
	NudgeFormatter NudgeFormatter

	// BypassAgents is an opt-out list of agent names. When the
	// invocation's AgentName is in this list, no enforcement
	// happens. Useful when one Enforcer instance is shared across
	// multiple agents and a subset should be exempt.
	BypassAgents []string

	// ScopedAgents is an opt-in list of agent names. When non-
	// empty, only invocations whose AgentName appears here are
	// enforced; everything else passes through. BypassAgents is
	// checked AFTER ScopedAgents (an agent in both lists is
	// bypassed).
	ScopedAgents []string

	// OnEnforce is an optional observer; see EnforceCallback for
	// the contract.
	OnEnforce EnforceCallback
}

// Option mutates Options. Implementations must be safe to apply
// in any order on a partially constructed Options value.
type Option func(*Options)

// WithName overrides the extension.Extension Name. Empty input
// ignored.
func WithName(name string) Option {
	return func(o *Options) {
		if name != "" {
			o.Name = name
		}
	}
}

// WithMaxRetries sets the block-retry budget. Non-positive inputs
// fall back to DefaultMaxRetries.
func WithMaxRetries(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.MaxRetries = n
		}
	}
}

// WithTodoTool injects a pre-configured todo.Tool to reuse
// instead of constructing a default. Useful when the agent needs
// a custom state-key prefix or NudgeHook shared across multiple
// todo_write callsites.
func WithTodoTool(t *todo.Tool) Option {
	return func(o *Options) {
		if t != nil {
			o.TodoTool = t
		}
	}
}

// WithDeclareBlockerToolName overrides the registered name of
// the escape-hatch tool. Empty input ignored.
func WithDeclareBlockerToolName(name string) Option {
	return func(o *Options) {
		if name != "" {
			o.DeclareBlockerToolName = name
		}
	}
}

// WithDeclareBlockerToolDescription overrides the LLM-facing
// description of the escape-hatch tool. Empty input ignored.
func WithDeclareBlockerToolDescription(desc string) Option {
	return func(o *Options) {
		if desc != "" {
			o.DeclareBlockerToolDescription = desc
		}
	}
}

// WithNudgeFormatter overrides the message renderer. Pass nil to
// keep the default.
func WithNudgeFormatter(f NudgeFormatter) Option {
	return func(o *Options) {
		if f != nil {
			o.NudgeFormatter = f
		}
	}
}

// WithScopedAgents restricts enforcement to invocations whose
// AgentName is in the supplied list.
func WithScopedAgents(names ...string) Option {
	return func(o *Options) {
		o.ScopedAgents = append(o.ScopedAgents, names...)
	}
}

// WithBypassAgents exempts invocations whose AgentName is in the
// supplied list. Bypass takes precedence over Scope.
func WithBypassAgents(names ...string) Option {
	return func(o *Options) {
		o.BypassAgents = append(o.BypassAgents, names...)
	}
}

// WithOnEnforce installs an observer for enforcement events.
func WithOnEnforce(cb EnforceCallback) Option {
	return func(o *Options) {
		o.OnEnforce = cb
	}
}

// inScope decides whether enforcement applies to a given
// invocation. Order of operations:
//
//   - Bypass beats Scope: an explicit exemption always wins.
//   - When Scope is empty, every non-bypassed invocation is in
//     scope. This is the common case since agent-level extensions
//     are already per-agent.
//   - When both lists are empty, every invocation is in scope.
func (o *Options) inScope(inv *agent.Invocation) bool {
	name := ""
	if inv != nil {
		name = inv.AgentName
	}
	for _, n := range o.BypassAgents {
		if n == name {
			return false
		}
	}
	if len(o.ScopedAgents) == 0 {
		return true
	}
	for _, n := range o.ScopedAgents {
		if n == name {
			return true
		}
	}
	return false
}
