//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package team provides a small, Go-idiomatic way to run multiple agents
// together.
//
// The core idea is simple:
//   - A Team is itself an agent.Agent, so it can be passed to
//     runner.NewRunner.
//   - In "coordinator" mode, a coordinator agent calls member agents as
//     tools.
//   - In "swarm" mode, members hand off to each other via transfer_to_agent.
//
// This package focuses on clear composition and safe defaults rather than a
// large surface area.
package team
