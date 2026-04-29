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
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/plugin/identity"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// --- HTTP tool ----------------------------------------------------------
//
// simulatedHTTPTool models any tool that ultimately talks to an external
// HTTP service (MCP SSE/Streamable, webhooks, custom gateway clients). It
// never actually dials the network; instead it builds an *http.Request and
// prints whatever headers the request would carry, so the effect of
// identity.HeadersFromContext is observable.

type httpToolArgs struct {
	Path string `json:"path"`
}

func newHTTPTool() tool.CallableTool {
	return function.NewFunctionTool(
		httpToolImpl,
		function.WithName("http_tool"),
		function.WithDescription(
			"Simulates an outbound HTTP call and prints the headers "+
				"it would send.",
		),
	)
}

// httpToolImpl reads identity headers from context and attaches them to a
// fake request, mimicking what a real mcp.WithHTTPBeforeRequest hook would
// do before sending the request over the wire.
func httpToolImpl(ctx context.Context, args httpToolArgs) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com"+args.Path, nil)
	if err != nil {
		return "", err
	}
	headers, err := identity.HeadersFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("read identity headers: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return formatRequest(req), nil
}

func formatRequest(req *http.Request) string {
	names := make([]string, 0, len(req.Header))
	for k := range req.Header {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := []string{fmt.Sprintf("%s %s", req.Method, req.URL)}
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%s", k, req.Header.Get(k)))
	}
	return joinParts(parts)
}

// --- Command tool --------------------------------------------------------
//
// simulatedCommandTool models any tool that spawns a child process
// (skill_run, workspace_exec, or a custom bin wrapper). It does not
// actually fork; it just prints the env slice it would hand to exec.Cmd,
// so callers can see how identity.EnvVarsFromContext flows through.

type commandToolArgs struct {
	Command string `json:"command"`
}

func newCommandTool() tool.CallableTool {
	return function.NewFunctionTool(
		commandToolImpl,
		function.WithName("command_tool"),
		function.WithDescription(
			"Simulates a child-process command and prints the env "+
				"vars that would be passed to exec.Cmd.",
		),
	)
}

// commandToolImpl reads identity env vars from context and merges them
// with a small static base. A real tool would hand `env` to exec.Cmd.Env,
// which is exactly what codeexecutor.NewEnvInjectingCodeExecutor does for
// skill_run / workspace_exec so each tool implementation does not have to.
func commandToolImpl(ctx context.Context, args commandToolArgs) (string, error) {
	baseEnv := map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin"}
	maps.Copy(baseEnv, identity.EnvVarsFromContext(ctx))

	keys := make([]string, 0, len(baseEnv))
	for k := range baseEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{fmt.Sprintf("exec %q", args.Command)}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, baseEnv[k]))
	}
	return joinParts(parts), nil
}

// joinParts renders a tool result as a single compact string. Tools would
// normally return structured data here; we keep it as a string so the demo
// output prints cleanly.
func joinParts(parts []string) string {
	// Drop zero-length entries defensively.
	filtered := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		filtered = append(filtered, p)
	}
	b, _ := json.Marshal(filtered)
	return string(b)
}
