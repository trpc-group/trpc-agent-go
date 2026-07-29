//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package governance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicyAllow(t *testing.T) {
	p := NewPolicy([]string{"go", "checkrunner"}, []string{}, false)
	d := p.Check("go")
	assert.True(t, d.Allowed)
	assert.Equal(t, "allow", d.Action)
}

func TestPolicyDeny(t *testing.T) {
	p := NewPolicy([]string{"go"}, []string{"rm", "curl"}, false)
	d := p.Check("rm")
	assert.False(t, d.Allowed)
	assert.Equal(t, "deny", d.Action)
}

func TestPolicyNotInAllowList(t *testing.T) {
	p := NewPolicy([]string{"go"}, []string{}, false)
	d := p.Check("curl")
	assert.False(t, d.Allowed)
}

func TestPolicyDryRun(t *testing.T) {
	p := NewPolicy([]string{"go"}, []string{"rm"}, true)
	d := p.Check("rm")
	assert.True(t, d.Allowed, "dry-run should allow all")
	assert.Equal(t, "allow", d.Action)
}

func TestPolicyNoLists(t *testing.T) {
	p := NewPolicy(nil, nil, false)
	d := p.Check("anything")
	assert.True(t, d.Allowed)
}

func TestDefaultCommands(t *testing.T) {
	allowed := DefaultAllowedCommands()
	assert.Contains(t, allowed, "go")
	assert.Contains(t, allowed, "checkrunner")

	denied := DefaultDeniedCommands()
	assert.Contains(t, denied, "rm")
	assert.Contains(t, denied, "sudo")
}
