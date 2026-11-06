//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolfilter pre-filters tools and skills per user turn and routes
// model selection, using a System One decision model (TypeSafe Jev and
// protocol-compatible servers) through the shared internal/systemone client.
//
// The three integrations are concrete implementations over existing
// framework slots; the package adds no generic decision abstraction:
//
//   - NewToolCallback returns a model.BeforeModelCallbackStructured that
//     rewrites the tool list of each model request. Tools the decision model
//     deems irrelevant for the current user message are removed, shrinking
//     the prompt. The default keep set protects framework workflow tools,
//     including skill loading. Use WithKeepAlways("tool_search") to protect
//     tool_search when it is present.
//   - NewSkillFilter returns a skill.VisibilityFilter for
//     llmagent.WithSkillFilter that narrows the skill overview to the skills
//     relevant for the current run. The batch decision is made lazily on the
//     first visibility query of a run and cached for the rest of it.
//   - NewModelRouter returns an agent.ModelSelector that picks the model for
//     a run with one Choice question. The selection is cached per
//     invocation, so a ReAct loop pays for one decision per run.
//
// All three fail open: on any decision-model error the behavior degrades to
// "not configured at all" — the full tool catalog stays visible, all skills
// stay visible, and routing falls back to the configured fallback model.
// This is the opposite direction of the approval reviewer
// (plugin/guardrail/approval), which fails closed because it guards safety.
//
// Constructors follow the integration-package convention: an explicit
// base URL plus options, no environment inference, and no internal types in
// exported signatures. The System One endpoint is {base_url}/v1/systemone
// with Bearer authentication. Inject WithHTTPClient for tests and HTTP
// policy.
package toolfilter
