//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolloopwarning provides an opt-in, request-local warning for
// identical consecutive tool rounds.
//
// Register the plugin with runner.WithPlugins(toolloopwarning.New()). The
// plugin is inactive unless registered.
//
// Starting with the second model request in each invocation, the plugin
// examines the two complete tool rounds at the end of the request. Skipping
// the first request prevents an old session-history tail from triggering a
// warning in a new run. Rounds match when their ordered tool names, canonical
// JSON arguments, and model-visible results are identical. Tool-call IDs are
// used only to pair results with calls. A malformed or interrupted transcript,
// an intervening non-tool message, or a changed round breaks adjacency.
//
// On a match, the plugin appends one temporary user-role instruction to that
// model request. It appends the instruction to each eligible request while the
// repeated loop continues, without duplicating it when callbacks re-enter on
// the same request. The instruction is not appended to session events and is
// not restored from session history. A standalone summary built from persisted
// session events does not receive the instruction as source content. When
// cache-safe summary forking reuses a final request containing the instruction,
// it can enter the summarizer input and indirectly affect the persisted derived
// summary. Execution tracing records it as part of the actual model request.
// WithExcludedToolNames can exclude polling or other tools whose repeated
// results are expected.
//
// By default the plugin is warning-only: it makes no additional model or tool
// calls and does not stop or retry the invocation. WithStopAfterWarning arms
// the current invocation after the warning is appended. If the next complete
// model response selects the same ordered tool bundle, the plugin compares the
// final response after custom-response replacement and enabled JSON/text tool
// call repair, then returns an agent.StopError before any of those tools
// execute. The stop diagnostic contains only the bounded action fingerprint,
// not tool arguments or results.
package toolloopwarning
