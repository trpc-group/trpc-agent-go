//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides the OpenClaw-like demo binary entrypoint.
package main

import (
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"

	_ "trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/stdin"
	_ "trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/telegram"
)

var (
	runAppFunc            = app.Main
	runUpgradeCommandFunc = runUpgradeCommand
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if isTopLevelVersionRequest(args) {
		_, _ = fmt.Fprintln(os.Stdout, currentVersion())
		return 0
	}
	if isTopLevelUpgradeRequest(args) {
		return runUpgradeCommandFunc(args[1:])
	}
	return runAppFunc(args)
}
