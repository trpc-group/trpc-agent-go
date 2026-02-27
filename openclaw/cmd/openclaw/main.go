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
	"os"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return app.Main(args)
}
