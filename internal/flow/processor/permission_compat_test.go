//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type permissionSchemaTool struct{}

func (*permissionSchemaTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "connect", InputSchema: &tool.Schema{
		Type: "string", Default: netip.MustParseAddr("127.0.0.1"),
	}}
}

func TestToolPermissionPreservesCustomSchemaValues(t *testing.T) {
	called := false
	inv := agent.NewInvocation()
	inv.RunOptions = agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
		func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
			called = true
			require.Equal(t, netip.MustParseAddr("127.0.0.1"), req.Declaration.InputSchema.Default)
			return tool.AllowPermission(), nil
		},
	))
	p := &FunctionCallResponseProcessor{}
	candidate := &permissionSchemaTool{}
	result, err := p.checkToolPermission(context.Background(), inv, model.ToolCall{
		Function: model.FunctionDefinitionParam{Name: "connect", Arguments: []byte(`"127.0.0.1"`)},
	}, candidate, candidate.Declaration())
	require.NoError(t, err)
	require.Nil(t, result)
	require.True(t, called, "existing policies must work without schema serialization")
}
