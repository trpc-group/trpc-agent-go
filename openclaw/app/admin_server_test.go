//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
)

func TestOpenAdminBinding_AutoPortFallback(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	binding, err := openAdminBinding(
		busy.Addr().String(),
		true,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = binding.listener.Close()
	})

	require.NotNil(t, binding.listener)
	require.NotEqual(t, busy.Addr().String(), binding.addr)
	require.True(t, binding.relocated)
	require.NotEmpty(t, binding.url)
}

func TestOpenAdminBinding_ExactPortFailure(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	_, err = openAdminBinding(
		busy.Addr().String(),
		false,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen on")
}

func TestBuildAdminConfig_IncludesBrowserProviders(t *testing.T) {
	t.Parallel()

	cfg := buildAdminConfig(
		runOptions{
			AppName: "openclaw",
			ToolProviders: []pluginSpec{{
				Type: toolProviderBrowser,
				Name: "primary-browser",
				Config: yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:19790"
sandbox_server_url: "http://127.0.0.1:20790"
allow_loopback: true
profiles:
  - name: "openclaw"
    transport: "stdio"
    command: "npx"
  - name: "chrome"
    browser_server_url: "http://127.0.0.1:19790"
nodes:
  - id: "edge"
    server_url: "http://node.example:7777"
`),
			}},
		},
		agentTypeLLM,
		"instance-1",
		admin.LangfuseStatus{},
		"/tmp/state",
		"/tmp/debug",
		time.Unix(0, 0),
		nil,
		admin.Routes{},
		nil,
		nil,
		nil,
		nil,
		"127.0.0.1:8081",
		"http://127.0.0.1:8081",
		nil,
		nil,
		nil,
	)

	require.Len(t, cfg.Browser.Providers, 1)
	require.NotNil(t, cfg.Skills)
	provider := cfg.Browser.Providers[0]
	require.Equal(t, "primary-browser", provider.Name)
	require.Equal(t, "openclaw", provider.DefaultProfile)
	require.Equal(t, "http://127.0.0.1:19790", provider.HostServerURL)
	require.Equal(
		t,
		"http://127.0.0.1:20790",
		provider.SandboxServerURL,
	)
	require.True(t, provider.AllowLoopback)
	require.Len(t, provider.Profiles, 2)
	require.Equal(
		t,
		"http://127.0.0.1:19790",
		provider.Profiles[1].BrowserServerURL,
	)
	require.Len(t, provider.Nodes, 1)
	require.Equal(t, "edge", provider.Nodes[0].ID)
}

func TestBuildBrowserAdminConfig_SkipsInvalidSpecs(t *testing.T) {
	t.Parallel()

	evaluateEnabled := true
	allowLoopback := true
	allowPrivateNet := true
	allowFileURLs := true

	cfg := buildBrowserAdminConfig(
		[]pluginSpec{
			{Type: "search", Name: "web"},
			{
				Type: toolProviderBrowser,
				Name: "broken",
				Config: yamlNode(t, `
unknown_field: true
`),
			},
			{
				Type: toolProviderBrowser,
				Name: "primary-browser",
				Config: yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:19790"
evaluate_enabled: true
allow_loopback: true
allow_private_networks: true
allow_file_urls: true
profiles:
  - name: "openclaw"
    transport: "stdio"
    command: "npx"
nodes:
  - id: "edge"
    server_url: "http://node.example:7777"
`),
			},
		},
		nil,
	)

	require.Len(t, cfg.Providers, 1)
	provider := cfg.Providers[0]
	require.Equal(t, "primary-browser", provider.Name)
	require.Equal(t, "openclaw", provider.DefaultProfile)
	require.Equal(t, "http://127.0.0.1:19790", provider.HostServerURL)
	require.Equal(t, evaluateEnabled, provider.EvaluateEnabled)
	require.Equal(t, allowLoopback, provider.AllowLoopback)
	require.Equal(t, allowPrivateNet, provider.AllowPrivateNet)
	require.Equal(t, allowFileURLs, provider.AllowFileURLs)
	require.Len(t, provider.Profiles, 1)
	require.Equal(t, "openclaw", provider.Profiles[0].Name)
	require.Len(t, provider.Nodes, 1)
	require.Equal(t, "edge", provider.Nodes[0].ID)
}
