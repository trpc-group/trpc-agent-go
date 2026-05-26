//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package todoenforcer turns the soft, advisory tool/todo workflow
// into a hard contract: an LLMAgent that has open todo items
// cannot declare itself "done" without either finishing them or
// formally declaring an external blocker via the
// todo_declare_blocker tool that this extension contributes.
//
// # Why this exists
//
// tool/todo is intentionally low-friction. The model writes a list,
// the framework persists it to session.State, and a NudgeHook may
// append text to the next tool result. Nothing prevents the model
// from emitting a "final" answer while pending or in-progress
// items remain — the empirical failure mode is the model deciding
// it has done enough and exiting early. todoenforcer closes that
// gap at the LLM-agent layer:
//
//  1. AfterModel inspects every final response. If the current
//     branch's todo list still contains pending or in-progress
//     items, the response's `Done` flag is flipped to false so
//     llmflow keeps looping; a "reminder pending" marker is
//     stored on the invocation.
//  2. BeforeModel drains that marker on the next turn and
//     appends a nudge user message to the request, listing the
//     items that remain and the model's two valid next actions
//     (continue or declare a blocker).
//  3. todo_declare_blocker is the model's escape route for the
//     case "I cannot make further progress because of an
//     objective external blocker" (missing user permission, an
//     ambiguous requirement that needs human clarification,
//     infrastructure that is not yet provisioned, a sensitive
//     decision that must be made by the user, …). Calling it
//     records the model's stated reason on the invocation and
//     latches a "declared" flag. From that point on, AfterModel
//     stops blocking final responses for the rest of the
//     invocation — the model is FREE to compose and send its
//     final message explaining what input is missing.
//  4. A bounded retry budget (WithMaxRetries) caps the loop. Once
//     exhausted, the response passes through unchanged so that an
//     undisciplined model cannot trap the runner forever; an
//     OnEnforce observer surfaces the exhaustion event for
//     metrics.
//
// # Usage
//
// Install the enforcer at the LLMAgent level via WithExtensions. The
// extension contributes both todo_write (tool/todo's existing entry
// point) and todo_declare_blocker, so the user does not need to
// register a separate tool/todo instance:
//
//	enforcer := todoenforcer.New()
//	ag := llmagent.New("planner",
//	    llmagent.WithModel(myModel),
//	    llmagent.WithExtensions(enforcer),
//	)
//
// To customise the underlying todo tool (state-key prefix, default
// nudge string, …) construct it explicitly and pass it via
// WithTodoTool — the enforcer will reuse it instead of building a
// default:
//
//	td := todo.New(todo.WithStateKeyPrefix("temp:plan"))
//	ag := llmagent.New("planner",
//	    llmagent.WithExtensions(todoenforcer.New(
//	        todoenforcer.WithTodoTool(td),
//	        todoenforcer.WithMaxRetries(3),
//	    )),
//	)
//
// # Scope
//
// todoenforcer is an agent-scoped extension. Its hooks fire only for
// the LLMAgent it is installed on, never for sub-agents created via
// chain / parallel / cycle / graph containers. Install it on each
// agent that should be subject to the contract. Sharing a single
// Enforcer instance across multiple agents IS supported —
// per-invocation state (retry counter, reminder flag, declared
// flag) is keyed off the invocation, not the extension — and is the
// recommended deployment for cross-agent metric consistency.
//
// # Blocker declaration semantics
//
// Calling todo_declare_blocker:
//
//   - records the model's stated reason on the invocation
//     (state key todoenforcer:blocker_reason) and emits an
//     EnforceEvent with Reason = ReasonBlockerDeclared so
//     observers can count / log it;
//   - permanently allows future final responses on this
//     invocation to pass enforcement, so the model can speak to
//     the user immediately after the call;
//   - does NOT terminate the invocation. The runner remains in
//     control and may, as a separate concern, route the next
//     user turn back to this agent (for example via the framework
//     await_user_reply tool — entirely orthogonal and optional);
//   - does NOT clear the todo list. The list stays in session
//     state so a follow-up user turn (which arrives as a fresh
//     invocation, with all per-invocation state reset) can pick
//     up where the model left off once the missing precondition
//     has been supplied.
package todoenforcer
