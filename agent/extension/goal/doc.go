//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package goal provides an LLMAgent extension for session-scoped goals.
//
// The extension is intentionally agent-scoped. Install it only on the LLMAgent
// that should own the decision "is this goal complete or blocked?".
//
//	ag := llmagent.New("planner",
//	    llmagent.WithModel(m),
//	    llmagent.WithExtensions(goal.New()),
//	)
//
// It contributes three model-callable tools:
//
//   - get_goal
//   - create_goal
//   - update_goal
//
// It also installs BeforeModel / AfterModel callbacks. While the session goal
// is active, streaming progress may still be emitted. A premature final
// response is converted into a control response with Done=false, so llmflow
// continues inside the same Agent invocation. The Runner still emits a single
// runner.completion for the outer Run.
//
// This package does not parse slash commands. Applications that expose a
// "/goal ..." command should parse it in their own entrypoint and call Start.
package goal
