//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package responses adapts OpenAI Responses to model.Model using client-managed
// history and local function tools. Complete native output items are retained
// as an indivisible Message.ProviderData record; text and tool calls are projections.
// Tool messages with image content parts use native function output arrays;
// text-only tool messages retain their string output format.
// A response is committed only after a valid completed response, never on EOF.
// An empty completed stream is an error when earlier events already showed
// output; those events stay previews and are not turned into native items or
// tool calls. Additional include names are accepted through the existing
// extra-field maps and are unioned with required encrypted reasoning.
// WithHostedToolSearch and WithClientToolSearch opt in to tool search for the
// tools that the agent defers. A client search handler returns ToolSearchEntry
// values to select candidates, substitute executable tools, or load tools from
// an external catalog, even without deferred candidates. The responses/toolsearch package
// supplies a local keyword handler for the request's deferred candidates.
// Each Run borrows returned tool objects. The application owns their
// resources and any bindings it needs to recover across Runs.
// Other hosted tools, background execution, and server-managed continuation
// require additional runtime capabilities and are outside this adapter's contract.
package responses
