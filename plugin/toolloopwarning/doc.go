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
// model request. It warns once per unchanged streak and rearms after the round
// changes or adjacency breaks. The instruction is not appended to session
// events and is not restored from session history. Consumers that deliberately
// reuse the final model request, such as cache-safe summary forking and
// execution tracing, can still observe it. WithExcludedToolNames can exclude
// polling or other tools whose repeated results are expected.
//
// The plugin makes no additional model or tool calls. It does not stop or
// retry the invocation.
package toolloopwarning
