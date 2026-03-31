//go:build linux

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import "syscall"

func applyParentDeathSignal(
	attr *syscall.SysProcAttr,
) {
	if attr == nil {
		return
	}
	attr.Pdeathsig = syscall.SIGTERM
}
