//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package extension provides agent-scoped capability bundles.
//
// An Extension is a composable unit of agent behaviour. A single
// Extension may contribute any combination of:
//
//   - tools (model-callable functions)
//   - agent callbacks (BeforeAgent / AfterAgent)
//   - model callbacks (BeforeModel / AfterModel)
//   - tool callbacks (BeforeTool / AfterTool)
//
// All contributions are declared inside Extension.Register, which
// receives a *Registry that accumulates them. Which surfaces an
// individual agent implementation actually consumes is
// implementation-specific. LLMAgent (the only consumer at the time
// of writing) consumes tools, agent callbacks, model callbacks and
// tool callbacks. Other agent types (chainagent, cycleagent, ...)
// may consume only a subset; see their constructor documentation.
//
// # Scope: agent, not runner
//
// Extensions are intentionally agent-scoped. They are installed via
// each agent's own constructor option (e.g. llmagent.WithExtensions)
// rather than a global runner-level register. Multi-agent setups
// can therefore wire different extensions onto different agents
// without cross-talk.
//
// For runner-scoped cross-cutting behaviour (a single observer that
// must see every event from every agent on a Runner), use
// plugin.Plugin instead. The two concepts are complementary, not
// overlapping: plugin is "runner-scope callback bundle"; extension
// is "agent-scope capability bundle (which can carry tools)".
//
// # Composing extensions
//
// Multiple extensions installed on the same agent are processed in
// the order the user passed them to the consuming option. Within a
// single agent's hot path the merged execution order is:
//
//	user-registered callbacks   (from agent constructor options)
//	  → extension callbacks in install order
//
// This "user first, extension second" rule lets user-level
// callbacks short-circuit before extension code runs. It is
// intentionally different from runner-scoped plugins: runner
// plugins run before agent-local callbacks because they are
// cross-cutting hooks applied by the runner; extensions are
// folded into one LLMAgent, so LLMAgent keeps user-supplied
// callbacks first and extension callbacks second. See
// llmflow.runBeforeModelCallbacks for the runner ordering.
//
// Tool-name collisions across extensions, or between an extension
// tool and a user tool, are resolved by the consuming agent. The
// recommended rule (and what LLMAgent implements) is earlier-wins:
// user tools are preferred over extension tools, and extensions
// installed earlier are preferred over those installed later.
//
// # Implementing an extension
//
//	type MyExtension struct{}
//
//	func (e *MyExtension) Name() string { return "my-extension" }
//
//	func (e *MyExtension) Register(r *extension.Registry) {
//	    r.Tools(myTool)
//	    r.BeforeModel(e.beforeModel)
//	    r.AfterModel(e.afterModel)
//	}
//
// Extensions should be safe to install on multiple agents in the
// same process. The Register call is the only mutation point the
// framework guarantees; any state an extension keeps between
// callbacks must live in the invocation (via agent.SetState /
// agent.GetStateValue) rather than on the extension instance.
package extension
