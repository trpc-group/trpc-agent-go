//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCRPermissionPolicy_AllowCommand(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	result := policy.CheckCommand(context.Background(), "go vet ./...")
	assert.Equal(t, PermissionAllow, result.Action)
}

func TestCRPermissionPolicy_DenyCommand(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	result := policy.CheckCommand(context.Background(), "rm -rf /")
	assert.Equal(t, PermissionDeny, result.Action)
	assert.Contains(t, result.Reason, "rm")
}

func TestCRPermissionPolicy_NilSandbox(t *testing.T) {
	policy := NewCRPermissionPolicy(nil)

	result := policy.CheckCommand(context.Background(), "any command")
	assert.Equal(t, PermissionAllow, result.Action)
	assert.Contains(t, result.Reason, "no sandbox manager")
}

func TestCRPermissionPolicy_CheckCommandWithRisk_HighRisk(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	result := policy.CheckCommandWithRisk(context.Background(), "rm -rf /", true)
	assert.Equal(t, PermissionDeny, result.Action)
	assert.Contains(t, result.Reason, "high-risk")
}

func TestCRPermissionPolicy_CheckCommandWithRisk_UnknownNotHighRisk(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	result := policy.CheckCommandWithRisk(context.Background(), "some-unknown-cmd", false)
	assert.Equal(t, PermissionAsk, result.Action)
	assert.Contains(t, result.Reason, "needs review")
}

func TestCRPermissionPolicy_CheckCommandWithRisk_AllowedLowRisk(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	result := policy.CheckCommandWithRisk(context.Background(), "go version", false)
	assert.Equal(t, PermissionAllow, result.Action)
}

func TestCRPermissionPolicy_AsToolPermissionPolicy(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)

	toolPolicy := policy.AsToolPermissionPolicy()
	assert.NotNil(t, toolPolicy)
	_ = toolPolicy
}

func TestCRPermissionPolicy_AsToolPermissionPolicy_Deny(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	policy := NewCRPermissionPolicy(mgr)
	toolPolicy := policy.AsToolPermissionPolicy()

	decision, err := toolPolicy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec",
		Arguments: []byte(`rm -rf /`),
	})
	assert.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}
