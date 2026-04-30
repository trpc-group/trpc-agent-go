//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todo

import "context"

// NudgeHook is an optional policy callback invoked after the state
// has been persisted but before the tool returns. Any non-empty string
// it returns is appended to the final tool message, giving the model
// additional structured guidance for the next step.
//
// Arguments:
//
//   - oldTodos is the list as it was before this write, decoded from
//     the session. Empty on the very first write of a session.
//   - submitted is the list the LLM just wrote, exactly as produced
//     (pre-normalisation). In particular, when WithClearOnAllDone is
//     enabled and every item is completed, the tool will persist an
//     empty list and expose Output.Todos as empty, but the hook still
//     receives the all-completed list here so it can react to that
//     exact transition (the default demo uses this to ask the model
//     to summarise when all tasks are done).
//
// Typical uses:
//
//   - verification reminders (e.g. "You just closed 3+ tasks without a
//     verification step; call the verifier before summarising.")
//   - loop detection ("The last 5 updates only flipped the same task
//     back and forth; consider replanning.")
//   - token budget warnings.
//
// Hooks MUST be side-effect free with respect to the checklist itself.
// Mutating oldTodos or submitted is not supported.
type NudgeHook func(ctx context.Context, oldTodos, submitted []Item) string

// options holds the configurable knobs of the Tool.
type options struct {
	toolName       string
	description    string
	defaultNudge   string
	stateKeyPrefix string
	clearOnAllDone bool
	nudgeHooks     []NudgeHook
}

func defaultOptions() options {
	return options{
		toolName:       DefaultToolName,
		description:    DefaultToolDescription,
		defaultNudge:   DefaultNudgeMessage,
		stateKeyPrefix: DefaultStateKeyPrefix,
		clearOnAllDone: true,
	}
}

// Option configures a Tool.
type Option func(*options)

// WithToolName overrides the registered tool name.
//
// Must match ^[a-zA-Z0-9_-]+$ for maximum LLM-provider compatibility.
func WithToolName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.toolName = name
		}
	}
}

// WithDescription overrides the short description exposed to the model.
// For the long usage prompt, inject it via the agent's system instruction
// (see todo.DefaultToolPrompt for a ready-to-use text).
func WithDescription(desc string) Option {
	return func(o *options) {
		if desc != "" {
			o.description = desc
		}
	}
}

// WithNudgeMessage overrides the default reminder appended to every tool
// result. Set to "" to disable the default nudge (hooks still run).
func WithNudgeMessage(msg string) Option {
	return func(o *options) {
		o.defaultNudge = msg
	}
}

// WithStateKeyPrefix overrides the session.State key prefix used for
// persistence. The final key is "<prefix>[:<branch>]".
func WithStateKeyPrefix(prefix string) Option {
	return func(o *options) {
		if prefix != "" {
			o.stateKeyPrefix = prefix
		}
	}
}

// WithClearOnAllDone controls whether the list is cleared once every
// item reaches "completed". Enabled by default to avoid unbounded
// context growth. Disable it if you want completed items to persist
// (e.g. for UI history).
func WithClearOnAllDone(clear bool) Option {
	return func(o *options) {
		o.clearOnAllDone = clear
	}
}

// WithNudgeHook registers an additional policy hook. Hooks run in the
// order they are registered, after the default nudge message.
func WithNudgeHook(hook NudgeHook) Option {
	return func(o *options) {
		if hook != nil {
			o.nudgeHooks = append(o.nudgeHooks, hook)
		}
	}
}
