//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	serverName    = "browser-mcp-test"
	serverVersion = "1.0.0"
	toolTabs      = "browser_tabs"
	tabsText      = "> 1 Example - https://example.com"
)

func main() {
	server := mcp.NewStdioServer(serverName, serverVersion)
	server.RegisterTool(
		mcp.NewTool(toolTabs),
		func(ctx context.Context, req *mcp.CallToolRequest) (
			*mcp.CallToolResult,
			error,
		) {
			return mcp.NewTextResult(tabsText), nil
		},
	)

	if err := server.Start(); err != nil {
		panic(err)
	}
}
