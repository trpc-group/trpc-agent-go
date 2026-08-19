//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolloopwarning provides an opt-in, session-recorded warning for
// identical consecutive tool rounds.
//
// Register the plugin with runner.WithPlugins(toolloopwarning.New()). The
// plugin is inactive unless registered.
//
// Two adjacent complete tool rounds match when their tool names, canonical
// JSON arguments, and final model-visible results are identical. On a match,
// the plugin queues one synthetic user-role message for the next model turn
// and records it in session history with the queued-message source
// "plugin/toolloopwarning". The detector resets after each match, so four
// identical rounds produce warnings after the second and fourth rounds.
// Tool results are fingerprinted only after every Runner OnEvent hook has
// finished, so the comparison uses the event that is about to be persisted.
// A queued user message consumed between rounds breaks their adjacency.
//
// The plugin makes no additional model or tool calls. It does not stop or
// retry the invocation.
package toolloopwarning
