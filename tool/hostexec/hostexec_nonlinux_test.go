//go:build !linux && !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyParentDeathSignalNonLinuxIsNoop(
	t *testing.T,
) {
	applyParentDeathSignal(nil)

	attr := &syscall.SysProcAttr{}
	before := *attr

	applyParentDeathSignal(attr)

	require.Equal(t, before, *attr)
}
