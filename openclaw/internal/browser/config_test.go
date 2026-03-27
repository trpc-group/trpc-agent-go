//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

func TestResolveConfig_RequiresProfiles(t *testing.T) {
	t.Parallel()

	_, err := resolveConfig(Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one profile")
}

func TestResolveConfig_DefaultsFirstProfile(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Profiles: []ProfileConfig{{
			Transport: transportStdio,
			Command:   "npx",
		}},
	}

	got, err := resolveConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, defaultProfileName, got.DefaultProfile)
	require.Len(t, got.Profiles, 1)
	require.Equal(t, defaultProfileName, got.Profiles[0].Name)
}

func TestResolveConfig_DefaultProfileMustExist(t *testing.T) {
	t.Parallel()

	cfg := Config{
		DefaultProfile: "chrome",
		Profiles: []ProfileConfig{{
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "npx",
		}},
	}

	_, err := resolveConfig(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "default_profile")
}

func TestResolveConfig_EvaluateEnabledAndDuplicateProfile(t *testing.T) {
	t.Parallel()

	evaluateEnabled := true
	got, err := resolveConfig(Config{
		EvaluateEnabled: &evaluateEnabled,
		Profiles: []ProfileConfig{{
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "npx",
		}},
	})
	require.NoError(t, err)
	require.True(t, got.EvaluateEnabled)

	_, err = resolveConfig(Config{
		Profiles: []ProfileConfig{{
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "npx",
		}, {
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "node",
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicated")
}

func TestResolveConfig_ProfileErrorsPropagate(t *testing.T) {
	t.Parallel()

	_, err := resolveConfig(Config{
		Profiles: []ProfileConfig{{
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "npx",
		}, {
			Transport: transportStdio,
			Command:   "node",
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing name")
}

func TestResolveConfig_NavigationPolicyApplied(t *testing.T) {
	t.Parallel()

	allow := true
	cfg := Config{
		AllowedDomains:  []string{"example.com"},
		BlockedDomains:  []string{"bad.example.com"},
		AllowLoopback:   &allow,
		AllowPrivateNet: &allow,
		AllowFileURLs:   &allow,
		Profiles: []ProfileConfig{{
			Transport: transportStdio,
			Command:   "npx",
		}},
	}

	got, err := resolveConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"example.com"}, got.Navigation.AllowedDomains)
	require.Equal(
		t,
		[]string{"bad.example.com"},
		got.Navigation.BlockedDomains,
	)
	require.True(t, got.Navigation.AllowLoopback)
	require.True(t, got.Navigation.AllowPrivateNet)
	require.True(t, got.Navigation.AllowFileURLs)
}

func TestResolveConfig_ServerTargetsAllowEmptyProfileTransport(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ServerURL:        "http://127.0.0.1:9223",
		AuthToken:        "host-token",
		SandboxServerURL: "http://127.0.0.1:9333",
		SandboxAuthToken: "sandbox-token",
		Nodes: []NodeConfig{{
			ID:        "edge",
			ServerURL: "http://node.example:9444",
			AuthToken: "node-token",
		}},
		Profiles: []ProfileConfig{{
			Name: "openclaw",
		}},
	}

	got, err := resolveConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, got.HostServer)
	require.Equal(t, "http://127.0.0.1:9223", got.HostServer.ServerURL)
	require.Equal(t, "host-token", got.HostServer.AuthToken)
	require.NotNil(t, got.SandboxServer)
	require.Equal(
		t,
		"http://127.0.0.1:9333",
		got.SandboxServer.ServerURL,
	)
	require.Equal(t, "sandbox-token", got.SandboxServer.AuthToken)
	require.Contains(t, got.NodeTargets, "edge")
	require.Equal(t, "http://node.example:9444", got.NodeTargets["edge"].ServerURL)
	require.Empty(t, got.Profiles[0].Connection.Transport)
}

func TestResolveConfig_NodeTargetsIgnoreIncompleteEntries(t *testing.T) {
	t.Parallel()

	got := resolveNodeTargets([]NodeConfig{
		{
			ID:        "   ",
			Name:      "edge",
			ServerURL: "http://node.example:9444",
		},
		{
			ID:        "missing-url",
			ServerURL: "   ",
		},
	})

	require.Len(t, got, 1)
	require.Contains(t, got, "edge")
	require.Equal(t, "http://node.example:9444", got["edge"].ServerURL)
}

func TestResolveProfile_BrowserServerURLDoesNotShadowMCPServerURL(
	t *testing.T,
) {
	t.Parallel()

	got, err := resolveProfile(ProfileConfig{
		Name:      defaultProfileName,
		Transport: transportSSE,
		ServerURL: "https://mcp.example/sse",
	}, 0, false)
	require.NoError(t, err)
	require.Empty(t, got.BrowserServerURL)
	require.Equal(t, transportSSE, got.Connection.Transport)
	require.Equal(t, "https://mcp.example/sse", got.Connection.ServerURL)
}

func TestResolveProfile_RejectsMixedBrowserServerAndTransport(t *testing.T) {
	t.Parallel()

	_, err := resolveProfile(ProfileConfig{
		Name:             defaultProfileName,
		BrowserServerURL: "http://127.0.0.1:9223",
		Transport:        transportStdio,
		Command:          "npx",
	}, 0, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot mix")
}

func TestResolveProfile_BrowserServerURLKeepsAuthToken(t *testing.T) {
	t.Parallel()

	got, err := resolveProfile(ProfileConfig{
		Name:             defaultProfileName,
		Description:      "chrome relay",
		BrowserServerURL: "http://127.0.0.1:9223",
		AuthToken:        "secret",
	}, 0, false)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:9223", got.BrowserServerURL)
	require.Equal(t, "secret", got.AuthToken)
	require.Equal(t, "chrome relay", got.Description)
}

func TestResolveProfile_RequiresNameAfterFirstProfile(t *testing.T) {
	t.Parallel()

	_, err := resolveProfile(ProfileConfig{
		Transport: transportStdio,
		Command:   "npx",
	}, 1, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing name")
}

func TestResolveProfile_ValidationErrorsAreWrapped(t *testing.T) {
	t.Parallel()

	_, err := resolveProfile(ProfileConfig{
		Name:      defaultProfileName,
		Transport: transportSSE,
	}, 0, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), defaultProfileName)
	require.Contains(t, err.Error(), "requires server_url")
}

func TestValidateConnection_RejectsUnsupportedTransport(t *testing.T) {
	t.Parallel()

	err := validateConnection(connectionConfig("bad", "", ""))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported transport")
}

func TestValidateConnection_RequiresCommandForStdio(t *testing.T) {
	t.Parallel()

	err := validateConnection(connectionConfig(transportStdio, "", ""))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires command")
}

func TestResolveNodeTargets_ReturnsNilWhenEmpty(t *testing.T) {
	t.Parallel()

	got := resolveNodeTargets([]NodeConfig{{
		ID:        "missing-url",
		ServerURL: " ",
	}, {
		ID:        " ",
		ServerURL: "http://node.example:9444",
	}})
	require.Nil(t, got)
}

func TestValidateConnection_RequiresServerURLForNetwork(t *testing.T) {
	t.Parallel()

	err := validateConnection(connectionConfig(transportSSE, "", ""))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires server_url")
}

func connectionConfig(
	transport string,
	serverURL string,
	command string,
) mcptool.ConnectionConfig {
	return mcptool.ConnectionConfig{
		Transport: transport,
		ServerURL: serverURL,
		Command:   command,
	}
}
