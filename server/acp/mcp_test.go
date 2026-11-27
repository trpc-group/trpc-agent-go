//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"errors"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	jsonRPCInvalidParamsCode = -32602
	jsonRPCInternalErrorCode = -32603
)

func TestNormalizeMCPServerName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "mixed case", input: "Demo Server", want: "demo_server"},
		{name: "punctuation", input: "GitHub-API.v2", want: "github_api_v2"},
		{name: "already normalized", input: "server_1", want: "server_1"},
		{name: "empty", input: " ", wantErr: true},
		{name: "no alphanumeric", input: "---", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeMCPServerName(test.input)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestValidateMCPServer(t *testing.T) {
	usedNames := make(map[string]struct{})
	stdio, normalized, err := validateMCPServer(acpsdk.McpServer{
		Stdio: &acpsdk.McpServerStdio{
			Name:    "Demo Server",
			Command: "server",
			Env: []acpsdk.EnvVariable{{
				Name:  "TOKEN",
				Value: "secret",
			}},
		},
	}, usedNames)
	require.NoError(t, err)
	assert.Equal(t, "Demo Server", stdio.Name)
	assert.Equal(t, "demo_server", normalized)
	assert.Equal(t, map[string]string{"TOKEN": "secret"}, mcpEnv(stdio.Env))

	tests := []struct {
		name           string
		server         acpsdk.McpServer
		usedNames      map[string]struct{}
		wantCode       int
		wantErrorMatch string
	}{
		{
			name:           "missing transport",
			server:         acpsdk.McpServer{},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "only stdio MCP servers are supported",
		},
		{
			name: "multiple transports",
			server: acpsdk.McpServer{
				Stdio: &acpsdk.McpServerStdio{Name: "server", Command: "server"},
				Http:  &acpsdk.McpServerHttpInline{Name: "server"},
			},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "exactly one MCP transport must be specified",
		},
		{
			name: "missing name",
			server: acpsdk.McpServer{
				Stdio: &acpsdk.McpServerStdio{Command: "server"},
			},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "MCP server name is required",
		},
		{
			name: "missing command",
			server: acpsdk.McpServer{
				Stdio: &acpsdk.McpServerStdio{Name: "server"},
			},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "MCP server command is required",
		},
		{
			name: "missing env name",
			server: acpsdk.McpServer{
				Stdio: &acpsdk.McpServerStdio{
					Name:    "server",
					Command: "server",
					Env:     []acpsdk.EnvVariable{{Value: "secret"}},
				},
			},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "environment variable name is required",
		},
		{
			name: "normalized name conflict",
			server: acpsdk.McpServer{
				Stdio: &acpsdk.McpServerStdio{
					Name:    "DEMO_SERVER",
					Command: "server",
				},
			},
			usedNames:      map[string]struct{}{"demo_server": {}},
			wantCode:       jsonRPCInvalidParamsCode,
			wantErrorMatch: "conflicts after normalization",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateMCPServer(test.server, test.usedNames)
			require.Error(t, err)
			var requestError *acpsdk.RequestError
			require.True(t, errors.As(err, &requestError))
			assert.Equal(t, test.wantCode, requestError.Code)
			assert.Contains(t, err.Error(), test.wantErrorMatch)
		})
	}
}

func TestMCPEnv(t *testing.T) {
	assert.Nil(t, mcpEnv(nil))
	assert.Equal(t, map[string]string{
		"FIRST":  "one",
		"SECOND": "two",
	}, mcpEnv([]acpsdk.EnvVariable{
		{Name: "FIRST", Value: "one"},
		{Name: "SECOND", Value: "two"},
	}))
}

func TestValidateMCPToolNamesDetectsCrossServerCollisions(t *testing.T) {
	used := make(map[string]string)
	require.NoError(t, validateMCPToolNames(
		"foo_bar",
		[]tool.Tool{namedDeclarationTool{name: "foo_bar_baz"}},
		used,
	))

	err := validateMCPToolNames(
		"foo",
		[]tool.Tool{namedDeclarationTool{name: "foo_bar_baz"}},
		used,
	)
	require.Error(t, err)
	var requestError *acpsdk.RequestError
	require.ErrorAs(t, err, &requestError)
	assert.Equal(t, jsonRPCInvalidParamsCode, requestError.Code)
	assert.Contains(t, err.Error(), "conflict after server prefixing")
}

type namedDeclarationTool struct {
	name string
}

func (t namedDeclarationTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}
