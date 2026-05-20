//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newAGUIServer(run runner.Runner, sessionService session.Service) (*agui.Server, error) {
	return agui.New(
		run,
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithPath(*path),
		agui.WithMessagesSnapshotEnabled(true),
	)
}
