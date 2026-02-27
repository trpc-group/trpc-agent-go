//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main shows how to build a custom OpenClaw distribution by
// importing `openclaw/app` and enabling plugins via anonymous imports.
package main

import (
	"os"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"

	_ "trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/echotool"
	_ "trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/stdin"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
