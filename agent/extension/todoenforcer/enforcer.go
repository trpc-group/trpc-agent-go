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
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// Enforcer is the agent-scoped extension that turns tool/todo's
// advisory checklist into a hard contract. See doc.go for the
// high-level rationale and lifecycle.
//
// An Enforcer implements extension.Extension. Its Register call
// registers BeforeModel + AfterModel callbacks and contributes the
// todo_write + todo_declare_blocker tools to the agent's tool list,
// so callers do not need to install tool/todo separately.
//
// All enforcement state is invocation-scoped (see state.go).
// Sharing a single Enforcer across multiple agents is supported
// and is the recommended deployment for cross-agent metric
// consistency.
type Enforcer struct {
	opts               Options
	todoTool           *todo.Tool
	declareBlockerTool *declareBlockerTool
}

// Compile-time interface assertion.
var _ extension.Extension = (*Enforcer)(nil)

// New builds an Enforcer with the supplied options applied on
// top of the defaults. The returned value is ready to install via
// llmagent.WithExtensions.
func New(opts ...Option) *Enforcer {
	o := Options{
		Name:                   DefaultExtensionName,
		MaxRetries:             DefaultMaxRetries,
		DeclareBlockerToolName: DefaultDeclareBlockerToolName,
		NudgeFormatter:         DefaultNudgeFormatter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = DefaultMaxRetries
	}
	if o.NudgeFormatter == nil {
		o.NudgeFormatter = DefaultNudgeFormatter
	}

	e := &Enforcer{opts: o}
	if o.TodoTool != nil {
		e.todoTool = o.TodoTool
	} else {
		e.todoTool = todo.New()
	}
	e.declareBlockerTool = newDeclareBlockerTool(
		o.DeclareBlockerToolName, o.DeclareBlockerToolDescription, e,
	)
	return e
}

// Name implements extension.Extension.
func (e *Enforcer) Name() string {
	if e.opts.Name == "" {
		return DefaultExtensionName
	}
	return e.opts.Name
}

// Register implements extension.Extension.
//
// Wires three things onto the agent:
//
//   - todo_write (the workhorse) and todo_declare_blocker
//     (the escape hatch) tools, contributed in that fixed order so
//     they appear in the agent declaration the same way users learn
//     about them in the docs;
//   - a BeforeModel callback that injects a nudge message when the
//     previous turn flagged a reminder pending;
//   - an AfterModel callback that flips Done=false when the model
//     tries to finalise with open todo items.
//
// We deliberately do not register BeforeAgent / AfterAgent / tool
// callbacks: enforcement decisions are about the model's structured
// output (Done flag + tool calls), not the agent lifecycle or
// per-tool dispatch.
func (e *Enforcer) Register(r *extension.Registry) {
	if r == nil {
		return
	}
	if e.todoTool != nil {
		r.Tools(e.todoTool)
	}
	if e.declareBlockerTool != nil {
		r.Tools(e.declareBlockerTool)
	}
	r.BeforeModel(e.beforeModel)
	r.AfterModel(e.afterModel)
}

// beforeModel prepares a model request while enforcement is active.
//
// It disables streaming whenever open todo items exist. The
// enforcer can only make a hard allow/block decision after seeing
// a complete model response; streaming deltas would otherwise
// reach clients before AfterModel has a chance to reject the final
// answer.
//
// It also injects a nudge user message when the previous
// AfterModel turn flagged a pending reminder.
//
// Notes worth pinning down:
//
//   - We append directly to args.Request.Messages, mirroring how
//     toolsearch / errormessage manipulate Request in BeforeModel.
//     We do NOT use internal/state/steer.Queue: that queue is for
//     runner-level injected user messages and persists into
//     session history, which we want to avoid so retries don't
//     pollute downstream transcripts.
//   - The pending flag is consumed unconditionally — even when
//     the formatter returns "" (silent block) — otherwise the
//     next turn would try to inject again indefinitely.
//   - Internal failures (todo decode error, missing invocation)
//     are logged and skipped rather than propagated. Aborting a
//     run because of a corrupted state entry would be worse than
//     a missed nudge.
func (e *Enforcer) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	if !e.opts.inScope(inv) {
		return nil, nil
	}
	pendingReminder := reminderPending(inv)
	if pendingReminder {
		setReminderPending(inv, false)
	}

	// Read through the prefix the configured todo tool actually
	// writes with. todo.GetTodos hard-codes DefaultStateKeyPrefix,
	// so a user that supplied WithTodoTool(todo.New(
	// todo.WithStateKeyPrefix("custom"))) would otherwise see the
	// enforcer silently miss every write — open items would
	// linger in "custom:<branch>" while the enforcer looked at
	// "temp:todos:<branch>" and concluded the list was empty.
	items, err := todo.GetTodosWithPrefix(
		invocationSession(inv),
		e.todoStateKeyPrefix(),
		invocationBranch(inv),
	)
	if err != nil {
		log.WarnfContext(ctx, "todoenforcer: read todos failed: %v", err)
		return nil, nil
	}
	inProgress, pending := splitByStatus(items)
	if len(inProgress) == 0 && len(pending) == 0 {
		return nil, nil
	}
	args.Request.GenerationConfig.Stream = false

	if !pendingReminder {
		return nil, nil
	}

	msg := e.opts.NudgeFormatter(NudgeContext{
		AgentName:              invocationAgentName(inv),
		Pending:                pending,
		InProgress:             inProgress,
		AttemptNumber:          retryCount(inv),
		MaxRetries:             e.opts.MaxRetries,
		TodoToolName:           e.todoToolName(),
		DeclareBlockerToolName: e.declareBlockerToolName(),
	})
	if msg == "" {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.NewUserMessage(msg))
	return nil, nil
}

func (e *Enforcer) todoToolName() string {
	if e == nil || e.todoTool == nil {
		return todo.DefaultToolName
	}
	decl := e.todoTool.Declaration()
	if decl == nil || decl.Name == "" {
		return todo.DefaultToolName
	}
	return decl.Name
}

func (e *Enforcer) todoStateKeyPrefix() string {
	if e == nil || e.todoTool == nil {
		return todo.DefaultStateKeyPrefix
	}
	return e.todoTool.StateKeyPrefix()
}

func (e *Enforcer) declareBlockerToolName() string {
	if e == nil {
		return DefaultDeclareBlockerToolName
	}
	if e.declareBlockerTool != nil && e.declareBlockerTool.name != "" {
		return e.declareBlockerTool.name
	}
	if e.opts.DeclareBlockerToolName != "" {
		return e.opts.DeclareBlockerToolName
	}
	return DefaultDeclareBlockerToolName
}

// afterModel decides whether the response is allowed to be final.
//
// Decision tree, ordered for fast no-op on the common path:
//
//  1. Missing invocation, no response, or out of scope → no-op.
//  2. Response is an error, still streaming, or carries tool
//     calls → no-op. Error responses must surface unchanged, and
//     tool-call responses are passed through because tool calls
//     are how the model continues doing the work (for example
//     `model -> todo_write(completed) -> model -> final`).
//  3. Blocker already declared on this invocation → pass through.
//     Per the v2 contract, once the model has formally signalled
//     "I cannot proceed without input you have to give me", we
//     never block its final messages again until the next
//     user-initiated invocation arrives. The whole point of the
//     escape hatch is to LET the model talk to the user.
//  4. Read todos. If none are open, pass through.
//  5. Retry budget exhausted → emit an exhausted event and pass
//     through (fail-open). The counter is reset so any
//     observability code that re-reads it sees a clean state;
//     correctness does not depend on it because the Invocation
//     is about to be discarded anyway.
//  6. Otherwise: return a non-content control response with
//     Done=false, set reminder pending, bump the retry counter,
//     and emit a blocked event.
//
// Returning a separate CustomResponse is intentional: the original
// model text was a premature final answer. Letting it pass through
// with Done=false would continue the loop, but still leak the false
// answer to clients and session history.
func (e *Enforcer) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	if args == nil || args.Response == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	if !e.opts.inScope(inv) {
		return nil, nil
	}
	if args.Error != nil || args.Response.Error != nil {
		return nil, nil
	}
	if !e.shouldConsiderResponse(args.Response) {
		return nil, nil
	}

	if blockerDeclared(inv) {
		return nil, nil
	}

	// Read through the prefix the configured todo tool actually
	// writes with. todo.GetTodos hard-codes DefaultStateKeyPrefix,
	// so a user that supplied WithTodoTool(todo.New(
	// todo.WithStateKeyPrefix("custom"))) would otherwise see the
	// enforcer silently miss every write — open items would
	// linger in "custom:<branch>" while the enforcer looked at
	// "temp:todos:<branch>" and concluded the list was empty.
	items, err := todo.GetTodosWithPrefix(
		invocationSession(inv),
		e.todoStateKeyPrefix(),
		invocationBranch(inv),
	)
	if err != nil {
		log.WarnfContext(ctx, "todoenforcer: read todos failed: %v", err)
		return nil, nil
	}
	if !hasOpenItems(items) {
		return nil, nil
	}

	inProgress, pending := splitByStatus(items)

	if retryCount(inv) >= e.opts.MaxRetries {
		// Budget exhausted. Surface for metrics, then let the
		// response through — the model has "won" the loop, and we
		// prefer letting the user see a possibly-wrong final
		// answer over keeping the runner stuck.
		e.notify(EnforceEvent{
			Reason:          ReasonExhausted,
			AgentName:       invocationAgentName(inv),
			AttemptNumber:   retryCount(inv),
			MaxRetries:      e.opts.MaxRetries,
			PendingCount:    len(pending),
			InProgressCount: len(inProgress),
		})
		resetRetryCount(inv)
		return nil, nil
	}

	setReminderPending(inv, true)
	attempt := incRetryCount(inv)
	e.notify(EnforceEvent{
		Reason:          ReasonBlocked,
		AgentName:       invocationAgentName(inv),
		AttemptNumber:   attempt,
		MaxRetries:      e.opts.MaxRetries,
		PendingCount:    len(pending),
		InProgressCount: len(inProgress),
	})
	return &model.AfterModelResult{
		CustomResponse: blockedControlResponse(args.Response),
	}, nil
}

func blockedControlResponse(src *model.Response) *model.Response {
	if src == nil {
		return &model.Response{Done: false}
	}
	rsp := src.Clone()
	rsp.Done = false
	rsp.IsPartial = false
	rsp.Choices = nil
	rsp.Error = nil
	return rsp
}

// shouldConsiderResponse mirrors llmflow's loop-termination
// predicate but narrows it to successful final text responses.
// Tool-call responses are a continuation signal rather than an exit
// signal, and error responses must surface without todo enforcement.
func (e *Enforcer) shouldConsiderResponse(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	if rsp.IsPartial || rsp.Error != nil {
		return false
	}
	if rsp.IsToolCallResponse() {
		return false
	}
	return rsp.IsFinalResponse()
}

// notify is a thin wrapper around the user-supplied callback. We
// recover panics so a misbehaving observer cannot crash the
// model-callback hot path; the runtime cost is one extra deferred
// call when an OnEnforce is configured.
func (e *Enforcer) notify(evt EnforceEvent) {
	if e.opts.OnEnforce == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("todoenforcer: OnEnforce panic: %v", r)
		}
	}()
	e.opts.OnEnforce(evt)
}

// notifyBlockerDeclared is the declare-blocker side of notify.
// Kept as a separate method so the escape-hatch tool does not
// need to know the EnforceEvent layout.
func (e *Enforcer) notifyBlockerDeclared(inv *agent.Invocation, reason string) {
	if e == nil {
		return
	}
	e.notify(EnforceEvent{
		Reason:        ReasonBlockerDeclared,
		AgentName:     invocationAgentName(inv),
		MaxRetries:    e.opts.MaxRetries,
		BlockerReason: strings.TrimSpace(reason),
	})
}

// invocationSession / invocationBranch / invocationAgentName are
// nil-safe shims around agent.Invocation field access. They exist
// because BeforeModel / AfterModel can in principle be called
// with a nil invocation in pure unit tests, and we prefer to
// no-op gracefully rather than panic.
func invocationSession(inv *agent.Invocation) *session.Session {
	if inv == nil {
		return nil
	}
	return inv.Session
}

func invocationBranch(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.Branch
}

func invocationAgentName(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.AgentName
}
