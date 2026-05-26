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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// DefaultDeclareBlockerToolDescription is the LLM-facing
// description for todo_declare_blocker. The wording is tuned to
// position this tool as a last-resort *declaration* — not a
// shortcut, not a way to bail out of hard work, and explicitly
// not a "task abandonment". Models that mistake one for the
// other are the primary failure mode this extension is designed to
// prevent.
//
// Notable phrasing choices:
//
//   - "objective external blocker" makes clear the trigger must
//     be something the model cannot resolve on its own.
//   - the enumerated examples are concrete user-facing concepts
//     (permission, credentials, a missing requirement) so the
//     model has anchors that look nothing like "this is hard".
//   - we tell the model what happens AFTER the call ("you may
//     send a final message"), removing the perceived need to
//     also stop generating — the previous wording made some
//     models truncate.
//   - "Do not use this tool to give up on hard but tractable
//     work." is a direct instruction that capable models honour
//     surprisingly well.
const DefaultDeclareBlockerToolDescription = "Declare that an objective external blocker prevents you from " +
	"making any further progress on the remaining todo items. Use this ONLY when the obstacle is something " +
	"YOU CANNOT resolve yourself — for example: missing user permission or credentials, a requirement that is " +
	"ambiguous and needs the user to clarify, a dependency on infrastructure that is not yet provisioned, or " +
	"a sensitive decision that must be made by the user. Calling this tool DOES NOT cancel the todo list; the " +
	"items remain so the user (or a follow-up turn) can pick them up once the missing precondition is supplied. " +
	"After this tool returns, you MAY produce a final message to the user explaining exactly what input is " +
	"missing and what they need to provide. Always supply a concrete, user-facing reason. Do NOT use this " +
	"tool to give up on hard but tractable work, and do NOT use it as a polite way to end the turn."

// declareBlockerInput is the LLM-facing input. The reason is
// required and must be non-empty. Empirically this friction
// eliminates a non-trivial fraction of premature declarations:
// capable models will rather think for one more turn than
// fabricate a reason.
type declareBlockerInput struct {
	Reason string `json:"reason" description:"Concrete, user-facing description of what input is missing and why you cannot continue. Required."`
}

// declareBlockerOutput is the success payload. Compact on
// purpose: the model only needs an ack so it can compose the
// follow-up final message; everything else (state recording,
// observer notification) happens as a side-effect of Call.
type declareBlockerOutput struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// declareBlockerTool implements tool.CallableTool for
// todo_declare_blocker. We hand-roll this rather than going
// through tool/function so we can reach a private *Enforcer for
// observer notification — function.NewFunctionTool would expose
// only the input value to the closure, not enough to thread the
// EnforceEvent through.
type declareBlockerTool struct {
	name        string
	description string
	enforcer    *Enforcer
}

// Compile-time interface assertion.
var _ tool.CallableTool = (*declareBlockerTool)(nil)

func newDeclareBlockerTool(name, description string, e *Enforcer) *declareBlockerTool {
	if name == "" {
		name = DefaultDeclareBlockerToolName
	}
	if description == "" {
		description = DefaultDeclareBlockerToolDescription
	}
	return &declareBlockerTool{name: name, description: description, enforcer: e}
}

// Declaration exposes the tool to the model. The reason field is
// declared `required` at the schema level so providers that
// enforce JSON Schema's `required` (OpenAI, Gemini) reject
// malformed calls before they even reach our Go code.
func (t *declareBlockerTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.description,
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"reason": {
					Type:        "string",
					Description: "Concrete, user-facing description of what input is missing and why you cannot continue.",
				},
			},
			Required: []string{"reason"},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"ok":     {Type: "boolean"},
				"reason": {Type: "string"},
			},
			Required: []string{"ok", "reason"},
		},
	}
}

// Call records the declaration and returns success. Two
// behaviours worth pinning down because they are the heart of
// the v2 redesign:
//
//  1. We deliberately return a NORMAL (success) result, NOT an
//     agent.StopError. The whole point of "declare blocker" is to
//     LET the model emit its final message immediately afterwards;
//     terminating the invocation here would suppress that message
//     and break the contract advertised in Description. The
//     enforcer's AfterModel sees the latched flag on the next
//     pass and stops blocking — that is the only behavioural
//     change.
//
//  2. State is set BEFORE this function returns, and the
//     observer fires before we hand control back. If the LLMAgent
//     is somehow torn down between the tool call and the next
//     model turn (timeout, ctx cancel, panic in another tool),
//     the declaration is still durably recorded on the
//     invocation for trace exporters / metrics.
//
// JSON unmarshalling errors and empty-reason rejections are
// returned WITHOUT marking the invocation as having declared a
// blocker, so a malformed call surfaces back to the model as a
// normal tool error and the model gets a chance to retry with a
// real reason.
func (t *declareBlockerTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	in, err := decodeDeclareBlockerInput(jsonArgs)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, errors.New("todo_declare_blocker: reason is required and must be non-empty")
	}

	inv, _ := agent.InvocationFromContext(ctx)
	markBlockerDeclared(inv, reason)
	if t.enforcer != nil {
		t.enforcer.notifyBlockerDeclared(inv, reason)
	}
	return declareBlockerOutput{OK: true, Reason: reason}, nil
}

// decodeDeclareBlockerInput is split out so tests can reuse the
// parser without going through Call.
func decodeDeclareBlockerInput(raw []byte) (declareBlockerInput, error) {
	var in declareBlockerInput
	if len(raw) == 0 {
		return in, errors.New("todo_declare_blocker: empty arguments")
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("todo_declare_blocker: decode arguments: %w", err)
	}
	return in, nil
}
