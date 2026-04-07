//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type ctxKey string

const userTokenKey ctxKey = "user-token"

var (
	mode  = flag.String("mode", "named", "Broker target mode: named or adhoc")
	token = flag.String("token", "demo-user-token", "User token injected through context for the successful path")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "auth hook example failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	server := startProtectedDemoServer("Bearer " + *token)
	defer server.Close()

	broker := mcpbroker.New(
		mcpbroker.WithServers(map[string]mcpcfg.ConnectionConfig{
			"secure_http": {
				ServerURL: server.URL,
				Transport: "streamable_http",
			},
		}),
		mcpbroker.WithAllowAdHocHTTP(true),
		mcpbroker.WithHTTPHeaderInjector(func(ctx context.Context, req *mcpbroker.HeaderInjectRequest) (map[string]string, error) {
			value, _ := ctx.Value(userTokenKey).(string)
			if value == "" {
				return nil, nil
			}
			return map[string]string{
				"Authorization": "Bearer " + value,
			}, nil
		}),
		mcpbroker.WithErrorInterceptor(func(ctx context.Context, req *mcpbroker.BrokerErrorRequest) (*mcpbroker.BrokerErrorDecision, error) {
			if req.Err == nil {
				return nil, nil
			}
			errText := strings.ToLower(req.Err.Error())
			if !strings.Contains(errText, "unauthorized") &&
				!strings.Contains(errText, "authorization") {
				return nil, nil
			}
			return &mcpbroker.BrokerErrorDecision{
				Handled:   true,
				WrapError: fmt.Errorf("authorization required for %s (%s)", req.BaseURL, req.Phase),
			}, nil
		}),
	)

	fmt.Println("== MCP Broker Auth Hook Example ==")
	fmt.Printf("Mode: %s\n", *mode)
	fmt.Printf("Protected URL: %s\n\n", server.URL)

	serversResult, err := callBrokerTool(context.Background(), broker.Tools(), "mcp_list_servers", map[string]any{})
	if err != nil {
		return err
	}
	printJSON("mcp_list_servers", serversResult)

	selector := "secure_http"
	callSelector := "secure_http.whoami"
	if *mode == "adhoc" {
		selector = server.URL
		callSelector = server.URL + ".whoami"
	}

	fmt.Println("== Without token ==")
	_, err = callBrokerTool(context.Background(), broker.Tools(), "mcp_list_tools", map[string]any{
		"selector": selector,
	})
	if err != nil {
		fmt.Printf("mcp_list_tools error: %v\n\n", err)
	}

	ctx := context.WithValue(context.Background(), userTokenKey, *token)

	fmt.Println("== With token ==")
	listToolsResult, err := callBrokerTool(ctx, broker.Tools(), "mcp_list_tools", map[string]any{
		"selector": selector,
	})
	if err != nil {
		return err
	}
	printJSON("mcp_list_tools", listToolsResult)

	inspectResult, err := callBrokerTool(ctx, broker.Tools(), "mcp_inspect_tools", map[string]any{
		"selector": selector,
		"tools":    []string{"whoami"},
	})
	if err != nil {
		return err
	}
	printJSON("mcp_inspect_tools", inspectResult)

	callResult, err := callBrokerTool(ctx, broker.Tools(), "mcp_call", map[string]any{
		"selector":  callSelector,
		"arguments": map[string]any{},
	})
	if err != nil {
		return err
	}
	printJSON("mcp_call", callResult)

	return nil
}

func callBrokerTool(ctx context.Context, tools []tool.Tool, name string, args map[string]any) (any, error) {
	var callable tool.CallableTool
	for _, tl := range tools {
		if tl.Declaration().Name != name {
			continue
		}
		ct, ok := tl.(tool.CallableTool)
		if !ok {
			return nil, fmt.Errorf("tool %q is not callable", name)
		}
		callable = ct
		break
	}
	if callable == nil {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	data, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}
	return callable.Call(ctx, data)
}

func printJSON(title string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Printf("%s: %+v\n\n", title, value)
		return
	}
	fmt.Printf("%s:\n%s\n\n", title, string(data))
}

type protectedDemoServer struct {
	server *httptest.Server
	URL    string
}

func startProtectedDemoServer(requiredAuth string) *protectedDemoServer {
	mcpServer := tmcp.NewServer(
		"mcpbroker-authhooks-demo",
		"1.0.0",
		tmcp.WithServerPath("/mcp"),
	)

	whoami := tmcp.NewTool(
		"whoami",
		tmcp.WithDescription("Return the current caller identity derived from the bearer token."),
	)
	mcpServer.RegisterTool(whoami, func(ctx context.Context, req *tmcp.CallToolRequest) (*tmcp.CallToolResult, error) {
		_ = ctx
		_ = req
		return tmcp.NewTextResult("current caller: demo-user"), nil
	})

	baseHandler := mcpServer.HTTPHandler()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != requiredAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		baseHandler.ServeHTTP(w, r)
	}))

	return &protectedDemoServer{
		server: httpServer,
		URL:    httpServer.URL + "/mcp",
	}
}

func (s *protectedDemoServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	s.server.Close()
}
