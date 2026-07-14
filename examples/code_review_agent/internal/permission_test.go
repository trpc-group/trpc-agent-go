//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPermissionPolicy_AllowGo(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, reason := p.Decide("go test ./...")
	require.Equal(t, DecisionAllow, d)
	require.Empty(t, reason)
}

func TestPermissionPolicy_AllowGoVet(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("go vet ./...")
	require.Equal(t, DecisionAllow, d)
}

func TestPermissionPolicy_DenyRm(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, reason := p.Decide("rm -rf /")
	require.Equal(t, DecisionDeny, d)
	require.Contains(t, reason, "denied")
}

func TestPermissionPolicy_DenyCurl(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("curl http://example.com")
	require.Equal(t, DecisionDeny, d)
}

func TestPermissionPolicy_DenyWget(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("wget http://example.com/file")
	require.Equal(t, DecisionDeny, d)
}

func TestPermissionPolicy_ReviewDocker(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, reason := p.Decide("docker build .")
	require.Equal(t, DecisionNeedsHumanReview, d)
	require.Contains(t, reason, "review")
}

func TestPermissionPolicy_ReviewGitPush(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, reason := p.Decide("git push origin main")
	require.Equal(t, DecisionNeedsHumanReview, d)
	require.Contains(t, reason, "review")
}

func TestPermissionPolicy_ReviewGitReset(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("git reset --hard HEAD~1")
	require.Equal(t, DecisionNeedsHumanReview, d)
}

func TestPermissionPolicy_AllowGitStatus(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("git status")
	require.Equal(t, DecisionAllow, d)
}

func TestPermissionPolicy_AskShellPipe(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("cat file | grep secret")
	require.Equal(t, DecisionAsk, d)
}

func TestPermissionPolicy_AskUnknownCmd(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, reason := p.Decide("unknown-tool --flag")
	require.Equal(t, DecisionAsk, d)
	require.Contains(t, reason, "not in the allowed list")
}

func TestPermissionPolicy_ReviewShellCommandString(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	decision, _ := p.Decide("bash -c rm -rf /workspace")
	require.Equal(t, DecisionNeedsHumanReview, decision)
}

func TestPermissionPolicy_EmptyCommand(t *testing.T) {
	p := NewDefaultPermissionPolicy()
	d, _ := p.Decide("")
	require.Equal(t, DecisionDeny, d)
}

func TestIsBlocked(t *testing.T) {
	require.True(t, IsBlocked(DecisionDeny))
	require.True(t, IsBlocked(DecisionAsk))
	require.True(t, IsBlocked(DecisionNeedsHumanReview))
	require.False(t, IsBlocked(DecisionAllow))
}
