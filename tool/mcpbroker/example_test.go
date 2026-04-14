//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

func ExampleNew() {
	broker := New(
		WithServers(map[string]mcpcfg.ConnectionConfig{
			"local_stdio": {
				Command: "go",
				Args:    []string{"run", "./mcpserver"},
			},
		}),
		WithAllowAdHocHTTP(true),
	)

	agent := llmagent.New(
		"assistant",
		llmagent.WithTools(broker.Tools()),
	)

	names := make([]string, 0, len(agent.Tools()))
	for _, tl := range agent.Tools() {
		names = append(names, tl.Declaration().Name)
	}
	sort.Strings(names)
	fmt.Println(names)
	// Output: [mcp_call mcp_inspect_tools mcp_list_servers mcp_list_tools]
}
